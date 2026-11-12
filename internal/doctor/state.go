package doctor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

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

func Save(report Report) error {
	path, err := StatePath()
	if err != nil {
		return err
	}
	return SaveTo(path, report)
}

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

func Load() (Report, bool, error) {
	path, err := StatePath()
	if err != nil {
		return Report{}, false, err
	}
	return LoadFrom(path)
}

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
