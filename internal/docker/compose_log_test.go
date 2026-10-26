package docker

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

// jsonLog is a --log-format json logger and the events it writes, decoded,
// each as "<type> <level>: <text>".
func jsonLog() (log.Logger, func() []string) {
	var buf bytes.Buffer
	lgr := log.New(log.Options{Format: "json", Writer: &buf})
	return lgr, func() []string {
		var lines []string
		for line := range strings.Lines(buf.String()) {
			var e log.Event
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				return []string{err.Error()}
			}
			lines = append(lines, e.Type+" "+e.Level.String()+": "+e.Text)
		}
		return lines
	}
}

// fakeDocker is an executable docker that answers `compose version --short`
// with version, or fails when version is empty.
func fakeDocker(t *testing.T, version string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docker")
	script := "#!/bin/sh\nexit 1\n"
	if version != "" {
		script = "#!/bin/sh\n[ \"$*\" = 'compose version --short' ] && echo '" + version + "' && exit 0\nexit 1\n"
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// Detecting Compose logs the command it runs and the version it found, as TS
// dockerComposeCLIConfig does (spec-node/dockerCompose.ts).
func TestNewComposeClientLogsTheComposeVersion(t *testing.T) {
	docker := fakeDocker(t, "2.24.5")
	lgr, events := jsonLog()
	c, err := NewComposeClient(docker, "", nil, lgr)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Version, []int{2, 24, 5}) {
		t.Errorf("Version = %v", c.Version)
	}
	run := "Run: " + docker + " compose version --short"
	want := []string{"start debug: " + run, "stop debug: " + run, "text debug: Docker Compose version: 2.24.5"}
	if got := events(); !slices.Equal(got, want) {
		t.Errorf("log = %q, want %q", got, want)
	}
}

// When Compose v2 is not there, the command is still logged, but no version.
func TestNewComposeClientLogsAFailedVersionCommand(t *testing.T) {
	docker := fakeDocker(t, "")
	lgr, events := jsonLog()
	if _, err := NewComposeClient(docker, "", nil, lgr); err == nil {
		t.Fatal("NewComposeClient succeeded without Compose")
	}
	run := "Run: " + docker + " compose version --short"
	want := []string{"start debug: " + run, "stop debug: " + run}
	if got := events(); !slices.Equal(got, want) {
		t.Errorf("log = %q, want %q", got, want)
	}
}
