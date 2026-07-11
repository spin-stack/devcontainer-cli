package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

func TestRunAutoTests_ReportsStagingError(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "test", "missing"), 0o755); err != nil {
		t.Fatal(err)
	}

	results := runAutoTests(log.Null, base, []string{"missing"}, "alpine", "", true)
	if len(results) != 1 || results[0].Status != testError {
		t.Fatalf("results = %#v, want one setup error", results)
	}
}

func TestRunScenarioTests_ReportsInvalidScenarios(t *testing.T) {
	base := t.TempDir()
	testDir := filepath.Join(base, "test", "sample")
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "scenarios.json"), []byte(`{"broken":`), 0o644); err != nil {
		t.Fatal(err)
	}

	results := runScenarioTests(log.Null, base, testDir, "sample", "", "alpine", "", true)
	if len(results) != 1 || results[0].Status != testError {
		t.Fatalf("results = %#v, want one parse error", results)
	}
}

func TestRunScenarioTests_ReportsMissingScriptAsSkipped(t *testing.T) {
	base := t.TempDir()
	testDir := filepath.Join(base, "test", "sample")
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "scenarios.json"), []byte(`{"no-script":{"image":"alpine"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	results := runScenarioTests(log.Null, base, testDir, "sample", "", "alpine", "", true)
	if len(results) != 1 || results[0].Status != testSkipped {
		t.Fatalf("results = %#v, want one skipped scenario", results)
	}
	if exit := reportTestResults(results); exit != 0 {
		t.Fatalf("skipped scenario exit = %d, want 0", exit)
	}
}

func TestReportTestResults_ErrorsFailCommand(t *testing.T) {
	if exit := reportTestResults([]testResult{{Name: "setup", Status: testError, Detail: "boom"}}); exit != 1 {
		t.Fatalf("exit = %d, want 1", exit)
	}
}
