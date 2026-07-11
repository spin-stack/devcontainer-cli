package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/doctor"
)

func sampleReport() doctor.Report {
	return doctor.Report{
		Overall: doctor.StatusWarn,
		Results: []doctor.Result{
			{Name: "docker-daemon", Status: doctor.StatusOK, Summary: "Docker daemon reachable (server 27.0)"},
			{Name: "build-cache-export", Status: doctor.StatusWarn, Summary: "cannot export cache", Remediation: "run `devcontainer setup`", Fixable: true},
		},
	}
}

func TestWriteReportTable(t *testing.T) {
	var buf bytes.Buffer
	writeReportTable(&buf, sampleReport())
	out := buf.String()

	for _, want := range []string{"✔", "docker-daemon", "⚠", "build-cache-export", "→ build-cache-export: run `devcontainer setup`", "overall: ⚠ warn"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q\n---\n%s", want, out)
		}
	}
}

func TestWriteSetupActions(t *testing.T) {
	var buf bytes.Buffer
	actions := []doctor.Action{
		{Name: "build-cache-export", Applied: true, Message: "created builder"},
		{Name: "compose-v2", Applied: false, Message: "manual action required: install compose"},
		{Name: "buildx", Err: "boom", Message: "tried"},
	}
	writeSetupActions(&buf, actions, false)
	out := buf.String()
	if !strings.Contains(out, "Applied:") {
		t.Errorf("missing header: %s", out)
	}
	if !strings.Contains(out, "✔ build-cache-export") || !strings.Contains(out, "✖ buildx") || !strings.Contains(out, "boom") {
		t.Errorf("action rendering wrong:\n%s", out)
	}

	var dry bytes.Buffer
	writeSetupActions(&dry, actions, true)
	if !strings.Contains(dry.String(), "Would apply (dry-run):") {
		t.Errorf("dry-run header missing: %s", dry.String())
	}

	var empty bytes.Buffer
	writeSetupActions(&empty, nil, false)
	if !strings.Contains(empty.String(), "Nothing to do") {
		t.Errorf("empty rendering missing: %s", empty.String())
	}
}

// TestWarnUncheckedHostSilentOnNonTerminal proves the up/build hint never writes
// to a captured (non-TTY) stderr — the invariant that keeps it out of the parity
// harness / scripted output.
func TestWarnUncheckedHostSilentOnNonTerminal(t *testing.T) {
	var stdout, stderr bytes.Buffer
	out := &bufOutput{stdout: &stdout, stderr: &stderr}
	warnUncheckedHost(out)
	if stderr.Len() != 0 || stdout.Len() != 0 {
		t.Fatalf("warnUncheckedHost wrote to non-terminal: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestIsTerminalWriterFalseForBuffer(t *testing.T) {
	if isTerminalWriter(&bytes.Buffer{}) {
		t.Fatal("bytes.Buffer must not be reported as a terminal")
	}
}

// bufOutput adapts two buffers to the Output seam.
type bufOutput struct{ stdout, stderr *bytes.Buffer }

func (b *bufOutput) Stdout() io.Writer { return b.stdout }
func (b *bufOutput) Stderr() io.Writer { return b.stderr }
