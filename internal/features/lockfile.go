package features

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Lockfile represents a devcontainer-lock.json.
type Lockfile struct {
	Features map[string]LockfileEntry `json:"features"`
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
	type entry struct {
		id    string
		entry LockfileEntry
	}

	var entries []entry
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

		var dependsOn []string
		if feat.DependsOn != nil {
			for k := range feat.DependsOn {
				dependsOn = append(dependsOn, k)
			}
			sort.Strings(dependsOn)
		}

		e := LockfileEntry{
			Version:   feat.Version,
			Resolved:  resolved,
			Integrity: fs.ComputedDigest,
		}
		if len(dependsOn) > 0 {
			e.DependsOn = dependsOn
		}

		entries = append(entries, entry{id: src.UserFeatureID(), entry: e})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].id < entries[j].id
	})

	lf := &Lockfile{Features: make(map[string]LockfileEntry, len(entries))}
	for _, e := range entries {
		lf.Features[e.id] = e.entry
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
		if os.IsNotExist(err) {
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

// WriteLockfile writes a lockfile to disk.
// If frozen is true and the lockfile has changed, returns an error.
func WriteLockfile(configPath string, lockfile *Lockfile, frozen bool, force bool) error {
	path := LockfilePath(configPath)

	newData, err := json.MarshalIndent(lockfile, "", "  ")
	if err != nil {
		return err
	}

	oldData, readErr := os.ReadFile(path)

	if !force && readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}

	// If no old lockfile and not explicitly enabled, skip
	if !force && os.IsNotExist(readErr) {
		return nil
	}

	if frozen {
		if os.IsNotExist(readErr) {
			return fmt.Errorf("lockfile does not exist")
		}
		if string(oldData) != string(newData) {
			return fmt.Errorf("lockfile does not match")
		}
		return nil
	}

	if readErr == nil && string(oldData) == string(newData) {
		return nil // no change
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
