package features

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

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

// ReadLockfile reads a lockfile from disk.
// Returns (lockfile, initLockfile, error).
// If the file is empty, initLockfile=true (marker to initialize on next build).
func ReadLockfile(configPath string) (*Lockfile, bool, error) {
	path := LockfilePath(configPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil, true, nil // empty marker
	}

	var lf Lockfile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, false, fmt.Errorf("parse lockfile: %w", err)
	}
	return &lf, false, nil
}

// WriteOptions controls how WriteLockfile behaves.
type WriteOptions struct {
	// Frozen fails (rather than writing) if the lockfile is missing or would change.
	Frozen bool
	// Force writes even when no prior lockfile exists.
	Force bool
}

// WriteLockfile writes a lockfile to disk. With opts.Frozen set, it fails
// instead of writing when the lockfile is missing or would change.
func WriteLockfile(configPath string, lockfile *Lockfile, opts WriteOptions) error {
	path := LockfilePath(configPath)

	newData, err := stringifyJSON(lockfile)
	if err != nil {
		return err
	}

	oldData, readErr := os.ReadFile(path)

	if !opts.Force && readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}

	// If no old lockfile and not explicitly enabled, skip
	if !opts.Force && errors.Is(readErr, os.ErrNotExist) {
		return nil
	}

	// TS compares the old lockfile through JSON.parse -> JSON.stringify, so a
	// cosmetic difference is no change, and one that does not parse always is.
	oldNormalized, parsed := normalizeJSON(oldData)
	unchanged := parsed && oldNormalized == string(newData)

	if opts.Frozen {
		if errors.Is(readErr, os.ErrNotExist) {
			return fmt.Errorf("lockfile does not exist")
		}
		if !unchanged {
			return fmt.Errorf("lockfile does not match")
		}
		return nil
	}

	if unchanged {
		return nil
	}

	return os.WriteFile(path, newData, 0644)
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
