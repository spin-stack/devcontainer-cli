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

type Lockfile struct {
	Features map[string]LockfileEntry `json:"features"`
}

// featureIDCollator is the collator of TS's `a.id.localeCompare(b.id)`, which
// orders a generated lockfile's features: Node's default, en-US.
var featureIDCollator = collate.New(language.AmericanEnglish)

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

type LockfileEntry struct {
	Version   string   `json:"version"`
	Resolved  string   `json:"resolved"`
	Integrity string   `json:"integrity"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

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

func ReadLockfile(configPath string) (*Lockfile, bool, error) {
	path := LockfilePath(configPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, nodeFSError(err)
	}

	content := decodeUTF8Lossy(data)
	if strings.TrimFunc(content, isJSWhitespace) == "" {
		return nil, true, nil
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

var (
	ErrLockfileMissing  error = &frozenLockfileError{"Lockfile does not exist."}
	ErrLockfileMismatch error = &frozenLockfileError{"Lockfile does not match."}
)

type frozenLockfileError struct{ msg string }

func (e *frozenLockfileError) Error() string { return e.msg }

func (e *frozenLockfileError) ReferenceError() {}

func jsonTruthy(data []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return true
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

type LockfileError struct {
	Name    string
	Message string
	Errno   int
	Code    string
	Syscall string
	Path    string
	Err     error
}

func (e *LockfileError) Error() string { return e.Message }

func (e *LockfileError) Unwrap() error { return e.Err }

func (e *LockfileError) ReferenceError() {}

func (e *LockfileError) Stack() string { return e.Name + ": " + e.Message }

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

var nodeErrno = map[syscall.Errno][2]string{
	syscall.EACCES:       {"EACCES", "permission denied"},
	syscall.EISDIR:       {"EISDIR", "illegal operation on a directory"},
	syscall.ENOTDIR:      {"ENOTDIR", "not a directory"},
	syscall.ELOOP:        {"ELOOP", "too many symbolic links encountered"},
	syscall.ENAMETOOLONG: {"ENAMETOOLONG", "name too long"},
	syscall.EPERM:        {"EPERM", "operation not permitted"},
	syscall.EIO:          {"EIO", "i/o error"},
}

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
		r, _ := utf8.DecodeRune(b[:n])
		out.WriteRune(r)
		b = b[n:]
	}
	return out.String()
}

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

type WriteOptions struct {
	Frozen bool
}

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

func LockfilePath(configPath string) string {
	dir := filepath.Dir(configPath)
	base := filepath.Base(configPath)
	if strings.HasPrefix(base, ".") {
		return filepath.Join(dir, ".devcontainer-lock.json")
	}
	return filepath.Join(dir, "devcontainer-lock.json")
}

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
	out.WriteByte(byte(delim) + 2)
	return nil
}
