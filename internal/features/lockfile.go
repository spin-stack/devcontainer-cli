package features

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// Lockfile represents a devcontainer-lock.json.
type Lockfile struct {
	Features map[string]LockfileEntry `json:"features"`
}

// featureIDCollator is the collator of TS's `a.id.localeCompare(b.id)`, which
// orders a generated lockfile's features: Node's default, en-US.
var featureIDCollator = collate.New(language.AmericanEnglish)

// MarshalJSON writes the features in the order TS generateLockfile sorts them
// in, String.prototype.localeCompare of their ids; ids that collate as equal
// keep byte order.
func (l Lockfile) MarshalJSON() ([]byte, error) {
	ids := slices.SortedFunc(maps.Keys(l.Features), func(a, b string) int {
		if c := featureIDCollator.CompareString(a, b); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	buf.WriteString(`{"features":{`)
	for i, id := range ids {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := enc.Encode(id); err != nil {
			return nil, err
		}
		buf.WriteByte(':')
		if err := enc.Encode(l.Features[id]); err != nil {
			return nil, err
		}
	}
	buf.WriteString(`}}`)
	return buf.Bytes(), nil
}

// LockfileEntry records a pinned feature version.
type LockfileEntry struct {
	Version   string   `json:"version"`
	Resolved  string   `json:"resolved"`  // registry/path@sha256:...
	Integrity string   `json:"integrity"` // sha256:...
	DependsOn []string `json:"dependsOn,omitempty"`
}

// GenerateLockfile creates a lockfile from resolved features config.
// GenerateLockfile builds the lockfile from resolved feature sets. Features whose
// userFeatureId is in excludeUserFeatureIDs are omitted — the upstream CLI does not
// write features supplied only via --additional-features to the lockfile.
func GenerateLockfile(config *Config, excludeUserFeatureIDs map[string]bool) *Lockfile {
	lf := &Lockfile{Features: map[string]LockfileEntry{}}
	for _, fs := range config.FeatureSets {
		src := fs.SourceInfo
		if src == nil {
			continue
		}
		srcType := src.SourceType()
		if srcType != "oci" && srcType != "direct-tarball" {
			continue
		}
		if excludeUserFeatureIDs[src.UserFeatureID()] {
			continue
		}

		if len(fs.Features) == 0 {
			continue
		}
		feat := fs.Features[0]

		var resolved string
		switch s := src.(type) {
		case *OCISource:
			resolved = fmt.Sprintf("%s/%s/%s@%s", s.Registry, s.Namespace, s.ID, fs.ComputedDigest)
		case *TarballSource:
			resolved = s.TarballURI
		}

		e := LockfileEntry{
			Version:   feat.Version,
			Resolved:  resolved,
			Integrity: fs.ComputedDigest,
		}
		if len(feat.DependsOn) > 0 {
			e.DependsOn = feat.DependsOnKeys()
		}
		lf.Features[src.UserFeatureID()] = e
	}
	return lf
}

// ReadLockfile reads a lockfile from disk, as the reference's readLockfile
// (lockfile.ts) does. It returns (lockfile, initLockfile, error): a missing
// file is no lockfile, and a file that is blank once trimmed is the marker
// that asks for one to be written (initLockfile). Every other failure is an
// error, as readLockfile rethrows everything but ENOENT: a file that cannot be
// read or is not JSON comes back as a *LockfileError carrying the message the
// reference reports. JSON null is no lockfile (TS reads it through `?.`), and
// JSON that is not shaped like a lockfile is an error too.
func ReadLockfile(configPath string) (*Lockfile, bool, error) {
	path := LockfilePath(configPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, nodeFSError(err)
	}

	// readLocalFile's Buffer, decoded by toString() and trimmed by trim().
	content := decodeUTF8Lossy(data)
	if strings.TrimFunc(content, isJSWhitespace) == "" {
		return nil, true, nil // empty marker
	}
	if msg := v8JSONSyntaxError(content); msg != "" {
		return nil, false, &LockfileError{Name: "SyntaxError", Message: msg}
	}

	if !jsonTruthy(data) {
		return nil, false, nil
	}
	var lf Lockfile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, false, fmt.Errorf("parse lockfile: %w", err)
	}
	return &lf, false, nil
}

// The errors a frozen WriteLockfile fails with, TS writeLockfile's own text:
// plain JavaScript errors, so they carry the reference's own message
// (internal/errors.ReferenceError).
var (
	ErrLockfileMissing  error = &frozenLockfileError{"Lockfile does not exist."}
	ErrLockfileMismatch error = &frozenLockfileError{"Lockfile does not match."}
)

// frozenLockfileError is a message TS writeLockfile throws as a plain Error.
type frozenLockfileError struct{ msg string }

func (e *frozenLockfileError) Error() string { return e.msg }

// ReferenceError marks the message as the reference's own.
func (e *frozenLockfileError) ReferenceError() {}

// jsonTruthy is the truthiness of JSON.parse(data), valid JSON: TS reads the
// parsed lockfile through `lockfile?.` and `if (lockfile)`, so null, false, 0
// and "" are no lockfile.
func jsonTruthy(data []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return true // valid JSON V8 parses; encoding/json reports the rest
	}
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		f, _ := v.Float64() // out of range: ±Inf, truthy
		return f != 0
	}
	return true
}

// LockfileError is a lockfile the reference's readLockfile fails on, carrying
// what the reference reports: the class and message of the JavaScript error,
// and for a file system error the properties Node.js gives it.
type LockfileError struct {
	// Name is the error's class: SyntaxError for JSON.parse, Error for the
	// file system.
	Name    string
	Message string
	// Errno, Code and Syscall are a file system error's properties; Path is
	// set only when the open failed, as Node.js sets it.
	Errno   int
	Code    string
	Syscall string
	Path    string
	Err     error
}

func (e *LockfileError) Error() string { return e.Message }

func (e *LockfileError) Unwrap() error { return e.Err }

// ReferenceError marks the message as the reference's own, the message of a
// plain JavaScript error it throws (internal/errors.ReferenceError).
func (e *LockfileError) ReferenceError() {}

// Stack is the error's stack without its frames, as the reference logs it.
func (e *LockfileError) Stack() string { return e.Name + ": " + e.Message }

// Inspect is what console.error prints for the error, frames left out: a
// SyntaxError's stack, and for a file system error, which Node.js raises with
// no frames, util.inspect's "[Error: message]" and its properties.
func (e *LockfileError) Inspect() string {
	if e.Code == "" {
		return e.Stack()
	}
	props := fmt.Sprintf("  errno: %d,\n  code: %s,\n  syscall: %s", e.Errno, jsQuote(e.Code), jsQuote(e.Syscall))
	if e.Path != "" {
		props += ",\n  path: " + jsQuote(e.Path)
	}
	return fmt.Sprintf("[%s] {\n%s\n}", e.Stack(), props)
}

// nodeErrno is the libuv code and description Node.js names a file system
// error with.
var nodeErrno = map[syscall.Errno][2]string{
	syscall.EACCES:       {"EACCES", "permission denied"},
	syscall.EISDIR:       {"EISDIR", "illegal operation on a directory"},
	syscall.ENOTDIR:      {"ENOTDIR", "not a directory"},
	syscall.ELOOP:        {"ELOOP", "too many symbolic links encountered"},
	syscall.ENAMETOOLONG: {"ENAMETOOLONG", "name too long"},
	syscall.EPERM:        {"EPERM", "operation not permitted"},
	syscall.EIO:          {"EIO", "i/o error"},
}

// nodeFSError is the error fs.readFile fails with for err: "<CODE>:
// <description>, <syscall>", with the path quoted when the open failed. An
// error Node.js would name differently keeps its own message.
func nodeFSError(err error) error {
	var pathErr *fs.PathError
	var errno syscall.Errno
	if !errors.As(err, &pathErr) || !errors.As(err, &errno) {
		return err
	}
	name, ok := nodeErrno[errno]
	if !ok {
		return err
	}
	e := &LockfileError{Name: "Error", Errno: -int(errno), Code: name[0], Syscall: pathErr.Op, Err: err}
	e.Message = fmt.Sprintf("%s: %s, %s", name[0], name[1], pathErr.Op)
	if pathErr.Op == "open" {
		e.Path = pathErr.Path
		e.Message += " '" + pathErr.Path + "'"
	}
	return e
}

// jsQuote quotes s as util.inspect does: in single quotes, or in double
// quotes or backticks when s holds a single quote, escaping backslashes and
// the quote.
func jsQuote(s string) string {
	quote := "'"
	if strings.Contains(s, "'") {
		switch {
		case !strings.Contains(s, `"`):
			quote = `"`
		case !strings.Contains(s, "`"):
			quote = "`"
		}
	}
	s = strings.ReplaceAll(s, `\`, `\\`)
	return quote + strings.ReplaceAll(s, quote, `\`+quote) + quote
}

// isJSWhitespace is what String.prototype.trim removes: WhiteSpace (Zs, tab,
// vertical tab, form feed, no-break space, BOM) and LineTerminator.
func isJSWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', '\u2028', '\u2029', '\ufeff':
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

// decodeUTF8Lossy decodes b as Buffer.toString('utf8') does, the WHATWG UTF-8
// decoder: each maximal invalid subpart becomes one U+FFFD.
func decodeUTF8Lossy(b []byte) string {
	var out strings.Builder
	for len(b) > 0 {
		n := sequenceLen(b)
		r, _ := utf8.DecodeRune(b[:n]) // U+FFFD for an invalid subpart
		out.WriteRune(r)
		b = b[n:]
	}
	return out.String()
}

// sequenceLen is the length of the UTF-8 sequence at the start of b, or of
// its maximal subpart when it is invalid: a lead byte and the continuation
// bytes that could still have completed it.
func sequenceLen(b []byte) int {
	lead := b[0]
	var need int
	lo, hi := byte(0x80), byte(0xbf)
	switch {
	case 0xc2 <= lead && lead <= 0xdf:
		need = 1
	case 0xe0 <= lead && lead <= 0xef:
		need = 2
		switch lead {
		case 0xe0:
			lo = 0xa0
		case 0xed:
			hi = 0x9f
		}
	case 0xf0 <= lead && lead <= 0xf4:
		need = 3
		switch lead {
		case 0xf0:
			lo = 0x90
		case 0xf4:
			hi = 0x8f
		}
	}
	n := 1
	for n <= need && n < len(b) && lo <= b[n] && b[n] <= hi {
		n++
		lo, hi = 0x80, 0xbf
	}
	return n
}

// WriteOptions controls how WriteLockfile behaves.
type WriteOptions struct {
	// Frozen fails (rather than writing) if the lockfile is missing or would change.
	Frozen bool
}

// WriteLockfile writes a lockfile to disk as TS writeLockfile (lockfile.ts)
// does: it creates the file when there is none and rewrites it when its
// content would change. With opts.Frozen set, it fails instead of writing when
// the lockfile is missing or would change. A lockfile that exists but cannot be
// read is an error, as writeLockfile rethrows all but ENOENT. A new lockfile
// gets Node's fs.writeFile mode, 0666 less the umask; a rewritten one keeps
// its mode.
func WriteLockfile(configPath string, lockfile *Lockfile, opts WriteOptions) error {
	path := LockfilePath(configPath)

	newData, err := stringifyJSON(lockfile)
	if err != nil {
		return err
	}

	oldData, readErr := os.ReadFile(path)
	missing := errors.Is(readErr, os.ErrNotExist)
	if readErr != nil && !missing {
		return readErr
	}
	if opts.Frozen && missing {
		return ErrLockfileMissing
	}

	// TS compares the old lockfile through JSON.parse -> JSON.stringify, so a
	// cosmetic difference is no change, and one that does not parse always is.
	oldNormalized, parsed := normalizeJSON(oldData)
	unchanged := parsed && oldNormalized == string(newData)

	if opts.Frozen {
		if !unchanged {
			return ErrLockfileMismatch
		}
		return nil
	}

	if unchanged {
		return nil
	}

	return os.WriteFile(path, newData, 0o666)
}

// LockfilePath returns the lockfile path for a given config path.
// If config is .devcontainer.json → .devcontainer-lock.json
// If config is devcontainer.json → devcontainer-lock.json
func LockfilePath(configPath string) string {
	dir := filepath.Dir(configPath)
	base := filepath.Base(configPath)
	if strings.HasPrefix(base, ".") {
		return filepath.Join(dir, ".devcontainer-lock.json")
	}
	return filepath.Join(dir, "devcontainer-lock.json")
}

// stringifyJSON is TS JSON.stringify(v, null, 2) + "\n": two-space indent,
// no HTML escaping, a trailing newline.
func stringifyJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// normalizeJSON is TS JSON.stringify(JSON.parse(data), null, 2) + "\n" for a
// lockfile: keys stay in their order, strings are re-escaped, numbers are
// written as a float64 is (as JS writes them) and whitespace is re-indented. It reports false when data is not exactly one JSON value.
func normalizeJSON(data []byte) (string, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var out bytes.Buffer
	if err := normalizeValue(dec, &out, "\n"); err != nil {
		return "", false
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", false
	}
	out.WriteByte('\n')
	return out.String(), true
}

// normalizeValue writes the next value of dec to out as JSON.stringify(v,
// null, 2) does at the depth whose line break and indent is newline.
func normalizeValue(dec *json.Decoder, out *bytes.Buffer, newline string) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		data, err := stringifyJSON(tok)
		out.Write(bytes.TrimSuffix(data, []byte("\n")))
		return err
	}
	out.WriteByte(byte(delim))
	inner := newline + "  "
	n := 0
	for ; dec.More(); n++ {
		if n > 0 {
			out.WriteByte(',')
		}
		out.WriteString(inner)
		if delim == '{' {
			if err := normalizeValue(dec, out, inner); err != nil {
				return err
			}
			out.WriteString(": ")
		}
		if err := normalizeValue(dec, out, inner); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	if n > 0 {
		out.WriteString(newline)
	}
	out.WriteByte(byte(delim) + 2) // '{'+2 is '}', '['+2 is ']'
	return nil
}
