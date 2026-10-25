package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A setup_cmd that fails once with a transient build error is re-run and its
// output read; one that keeps failing still fails the case.
func TestParitySetupRetriesATransientBuildFailure(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "attempted")
	setup := `if [ -e "` + marker + `" ]; then echo READY=yes; else touch "` + marker + `"; echo "ERROR: failed to solve: /tmp/build-features/hello_0: not found" >&2; exit 1; fi`
	env, err := runParitySetup(t.Context(), t.TempDir(), setup, nil)
	if err != nil {
		t.Fatalf("setup after one transient failure: %v", err)
	}
	if env["READY"] != "yes" {
		t.Errorf("setup env = %v, want READY=yes from the second attempt", env)
	}

	if _, err := runParitySetup(t.Context(), t.TempDir(), `echo "ERROR: failed to solve: x" >&2; exit 1`, nil); err == nil || !strings.Contains(err.Error(), "exit=1") {
		t.Errorf("a setup that keeps failing: err = %v, want the exit status", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
}
