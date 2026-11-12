package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParitySetupUsesLaneBinary(t *testing.T) {
	env := parityEnv("some.case", "go", "/repo", false)
	for _, key := range []string{"PARITY_CLI_GO", "PARITY_CLI_TS"} {
		if env[key] == "" {
			t.Errorf("parityEnv missing %s — setup_cmd references would not expand", key)
		}
	}
	if got := env["PARITY_CLI_GO"]; got != filepath.Join("/repo", "devcontainer") {
		t.Errorf("PARITY_CLI_GO default = %q, want the repo-root binary", got)
	}
	if got, want := env["PARITY_CLI_TS"], defaultParityTSCLI("/repo"); got != want {
		t.Errorf("PARITY_CLI_TS default = %q, want %q", got, want)
	}

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
