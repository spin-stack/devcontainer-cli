package doctor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// StatePath returns the location of the persisted check report. It follows the
// XDG Base Directory spec for state ($XDG_STATE_HOME, else ~/.local/state),
// namespaced under devcontainer/.
func StatePath() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "devcontainer", "check.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "devcontainer", "check.json"), nil
}

// Save writes the report to the default StatePath, creating parent directories.
func Save(report Report) error {
	path, err := StatePath()
	if err != nil {
		return err
	}
	return SaveTo(path, report)
}

// SaveTo writes the report to path (pretty-printed JSON), creating parents.
func SaveTo(path string, report Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Load reads the report from the default StatePath. The bool is false (with a
// nil error) when no report has been persisted yet — a never-checked system is
// an expected state, not an error.
func Load() (Report, bool, error) {
	path, err := StatePath()
	if err != nil {
		return Report{}, false, err
	}
	return LoadFrom(path)
}

// LoadFrom reads and decodes the report at path.
func LoadFrom(path string) (Report, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Report{}, false, nil
	}
	if err != nil {
		return Report{}, false, err
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return Report{}, false, err
	}
	return report, true, nil
}
