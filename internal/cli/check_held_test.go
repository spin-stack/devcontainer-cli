package cli

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/doctor"
)

// brokenStdout is an Output whose stdout refuses every write.
type brokenStdout struct{ captureOutput }

type refusingWriter struct{}

func (refusingWriter) Write([]byte) (int, error) { return 0, errors.New("stdout closed") }

func (b *brokenStdout) Stdout() io.Writer { return refusingWriter{} }

// Setup checks the system again only when it changed something for real: not
// in a dry run, and not when every action was a manual hint.
func TestSetupNeedsRecheck(t *testing.T) {
	applied := []doctor.Action{{Name: "manual"}, {Name: "builder", Applied: true}}
	manual := []doctor.Action{{Name: "manual"}}
	cases := []struct {
		name    string
		dryRun  bool
		actions []doctor.Action
		want    bool
	}{
		{"applied", false, applied, true},
		{"dry run", true, applied, false},
		{"nothing applied", false, manual, false},
		{"no actions", false, nil, false},
	}
	for _, c := range cases {
		if got := setupNeedsRecheck(c.dryRun, c.actions); got != c.want {
			t.Errorf("%s: setupNeedsRecheck = %v, want %v", c.name, got, c.want)
		}
	}
}

// --json prints the dry-run flag, the actions and the report as one document,
// and a failed remediation still exits 1 after printing it.
func TestWriteSetupResultJSON(t *testing.T) {
	out := &captureOutput{}
	actions := []doctor.Action{{Name: "buildx", Err: "boom"}}
	err := writeSetupResult(out, &setupOpts{json: true, dryRun: true}, actions, sampleReport(), nil)
	if exitCode(err) != 1 {
		t.Fatalf("err = %v, want exit code 1 for a failed remediation", err)
	}
	var got struct {
		DryRun  bool            `json:"dry_run"`
		Actions []doctor.Action `json:"actions"`
		Report  doctor.Report   `json:"report"`
	}
	if err := json.Unmarshal(out.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.out.String())
	}
	if !got.DryRun || len(got.Actions) != 1 || got.Actions[0].Err != "boom" || len(got.Report.Results) != 2 {
		t.Errorf("payload = %+v", got)
	}
	if out.err.Len() != 0 {
		t.Errorf("stderr = %q, want nothing in JSON mode", out.err.String())
	}
}

// A stdout that cannot be written fails the command with that error.
func TestWriteSetupResultJSONWriteError(t *testing.T) {
	err := writeSetupResult(&brokenStdout{}, &setupOpts{json: true}, nil, sampleReport(), nil)
	if err == nil || !strings.Contains(err.Error(), "stdout closed") {
		t.Fatalf("err = %v, want the write error", err)
	}
}

// Text mode prints the actions and the table, notes on stderr a state that
// could not be saved, and succeeds when no remediation failed.
func TestWriteSetupResultText(t *testing.T) {
	out := &captureOutput{}
	actions := []doctor.Action{{Name: "build-cache-export", Applied: true, Message: "created builder"}}
	if err := writeSetupResult(out, &setupOpts{}, actions, sampleReport(), errors.New("read-only home")); err != nil {
		t.Fatalf("err = %v, want success", err)
	}
	if s := out.out.String(); !strings.Contains(s, "✔ build-cache-export") || !strings.Contains(s, "overall: ⚠ warn") {
		t.Errorf("stdout = %q", s)
	}
	if got := out.err.String(); got != "note: could not save check state: read-only home\n" {
		t.Errorf("stderr = %q", got)
	}

	saved := &captureOutput{}
	if err := writeSetupResult(saved, &setupOpts{}, actions, sampleReport(), nil); err != nil {
		t.Fatal(err)
	}
	if saved.err.Len() != 0 {
		t.Errorf("stderr = %q, want no note when the state was saved", saved.err.String())
	}
}

// The table pads its columns two spaces past their widest cell.
func TestWriteReportTableAlignsTheColumns(t *testing.T) {
	var buf strings.Builder
	writeReportTable(&buf, sampleReport())
	want := "✔  docker-daemon       Docker daemon reachable (server 27.0)\n" +
		"⚠  build-cache-export  cannot export cache\n" +
		"  → build-cache-export: run `devcontainer setup`\n" +
		"\noverall: ⚠ warn\n"
	if buf.String() != want {
		t.Errorf("table =\n%s\nwant\n%s", buf.String(), want)
	}
}

// runCheckWithoutDocker runs check against a docker CLI that does not exist,
// so the daemon check fails, with the check state under stateHome.
func runCheckWithoutDocker(t *testing.T, out Output, json bool, stateHome string) error {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", stateHome)
	return runCheck(t.Context(), out, &checkOpts{json: json, dockerPath: filepath.Join(t.TempDir(), "docker")})
}

// A failing host prints the table, saves the state and exits 1.
func TestRunCheckFailingHostExitsOne(t *testing.T) {
	state := t.TempDir()
	out := &captureOutput{}
	err := runCheckWithoutDocker(t, out, false, state)
	if exitCode(err) != 1 {
		t.Fatalf("err = %v, want exit code 1 for a failing host", err)
	}
	if s := out.out.String(); !strings.HasSuffix(s, "\noverall: ✖ fail\n") {
		t.Errorf("stdout = %q, want the table of a failing host", s)
	}
	if out.err.Len() != 0 {
		t.Errorf("stderr = %q, want nothing when the state was saved", out.err.String())
	}
	saved, ok, err := doctor.LoadFrom(filepath.Join(state, "devcontainer", "check.json"))
	if err != nil || !ok || saved.Overall != doctor.StatusFail {
		t.Errorf("saved state = %+v, %v, %v; want the failing report", saved, ok, err)
	}
}

// A state that cannot be saved is a note on stderr, not a failure of its own.
func TestRunCheckNotesAStateItCouldNotSave(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out := &captureOutput{}
	err := runCheckWithoutDocker(t, out, false, blocker)
	if exitCode(err) != 1 {
		t.Fatalf("err = %v, want exit code 1 for a failing host", err)
	}
	if s := out.err.String(); !strings.HasPrefix(s, "note: could not save check state: ") {
		t.Errorf("stderr = %q, want the note", s)
	}
}

// --json prints the report as JSON and still exits 1 for a failing host; a
// stdout that cannot be written fails with that error.
func TestRunCheckJSON(t *testing.T) {
	out := &captureOutput{}
	err := runCheckWithoutDocker(t, out, true, t.TempDir())
	if exitCode(err) != 1 {
		t.Fatalf("err = %v, want exit code 1 for a failing host", err)
	}
	var got doctor.Report
	if err := json.Unmarshal(out.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.out.String())
	}
	if got.Overall != doctor.StatusFail || len(got.Results) == 0 {
		t.Errorf("report = %+v, want a failing report", got)
	}

	err = runCheckWithoutDocker(t, &brokenStdout{}, true, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "stdout closed") {
		t.Fatalf("err = %v, want the write error", err)
	}
}
