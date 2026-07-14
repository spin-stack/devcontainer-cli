package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestParitySetupUsesLaneBinary guards the root cause of a red daily parity run:
// the runtime matrix runs in two lanes with DIFFERENT binaries — the push/PR lane
// (`task parity:runtime`, deps build → ./devcontainer) and the daily coverage lane
// (`task coverage:parity-runtime`, which builds ONLY the instrumented binary at
// artifacts/coverage/bin and points CLI_GO at it, never building ./devcontainer).
// A setup_cmd/cleanup_cmd/verify_cmd that hard-codes ${PARITY_REPO_ROOT}/devcontainer
// (or node .../reference/devcontainer.js) therefore invokes a missing binary in the
// coverage lane: setup's `up` fails, no container is created, and the measured
// command reports "Dev container not found". It passes in the plain lane and
// locally, so the divergence only ever shows up in the daily.
//
// The fix routes every setup-side CLI call through ${PARITY_CLI_GO} / ${PARITY_CLI_TS},
// which parityEnv binds to the same binary the measured command uses in each lane.
// This test asserts both halves so a re-hardcoded path can't regress the daily green.
func TestParitySetupUsesLaneBinary(t *testing.T) {
	// Half 1: parityEnv must export the CLI vars the matrix now depends on.
	env := parityEnv("some.case", "go", "/repo", false)
	for _, key := range []string{"PARITY_CLI_GO", "PARITY_CLI_TS"} {
		if env[key] == "" {
			t.Errorf("parityEnv missing %s — setup_cmd references would not expand", key)
		}
	}
	if got := env["PARITY_CLI_GO"]; got != filepath.Join("/repo", "devcontainer") {
		t.Errorf("PARITY_CLI_GO default = %q, want the repo-root binary", got)
	}
	if got, want := env["PARITY_CLI_TS"], "node "+filepath.Join("/repo", "reference", "devcontainer.js"); got != want {
		t.Errorf("PARITY_CLI_TS default = %q, want %q", got, want)
	}

	// Half 2: no matrix command may hard-code the binary path — it must go through
	// the lane-aware vars, or the coverage lane breaks again.
	path := filepath.Join("docs", "parity", "parity-matrix.yaml")
	raw, err := os.ReadFile(filepath.Join("..", "..", path))
	if err != nil {
		t.Fatalf("read matrix: %v", err)
	}
	var matrix parityMatrix
	if err := yaml.Unmarshal(raw, &matrix); err != nil {
		t.Fatalf("parse matrix: %v", err)
	}

	const (
		hardGo = "${PARITY_REPO_ROOT}/devcontainer"
		hardTS = "reference/devcontainer.js"
	)
	for _, tc := range matrix.InitialCases {
		for _, f := range []struct{ name, cmd string }{
			{"setup_cmd", tc.SetupCmd},
			{"cleanup_cmd", tc.CleanupCmd},
			{"verify_cmd", tc.VerifyCmd},
		} {
			if strings.Contains(f.cmd, hardGo) || strings.Contains(f.cmd, hardTS) {
				t.Errorf("case %q %s hard-codes the CLI path; use ${PARITY_CLI_GO}/${PARITY_CLI_TS} so the coverage lane's instrumented binary is invoked:\n  %s",
					tc.ID, f.name, f.cmd)
			}
		}
	}
}
