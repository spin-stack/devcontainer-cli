package docker

import (
	"slices"
	"testing"
)

// Every KEY=VALUE build secret becomes `--secret id=KEY,env=KEY` (the contract
// on BuildOptions.Secrets), a one-letter key included.
func TestBuildArgsPassesAOneLetterSecret(t *testing.T) {
	args := (&Client{}).buildArgs(BuildOptions{UseBuildx: true, ContextPath: ".", Secrets: []string{"A=1"}})
	i := slices.Index(args, "--secret")
	if i < 0 || i+1 >= len(args) || args[i+1] != "id=A,env=A" {
		t.Errorf("args = %q, want --secret id=A,env=A", args)
	}
}
