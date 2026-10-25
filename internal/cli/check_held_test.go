package cli

import (
	"encoding/json"
	"errors"
	"io"
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
