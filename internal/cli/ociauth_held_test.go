package cli

import (
	"context"
	"testing"

	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
)

func TestWithOCIAuthPolicyKeepsTheContext(t *testing.T) {
	policy, err := oci.NewAuthPolicy(true, nil, log.Null)
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(context.Background())
	ctx := withOCIAuthPolicy(parent, policy)
	cancel()
	if ctx.Err() == nil {
		t.Error("cancelling the command context did not reach the context holding the policy")
	}
	if got := ociAuthPolicy(ctx); got != policy {
		t.Errorf("ociAuthPolicy = %p, want the stored policy %p", got, policy)
	}
	//nolint:staticcheck // SA1012: a nil context is the case under test
	if got := ociAuthPolicy(withOCIAuthPolicy(nil, policy)); got != policy {
		t.Errorf("ociAuthPolicy without a parent context = %p, want %p", got, policy)
	}
}

func TestOCIAuthPolicyIgnoresAStoredNil(t *testing.T) {
	policy := ociAuthPolicy(withOCIAuthPolicy(context.Background(), nil))
	if policy == nil {
		t.Fatal("ociAuthPolicy returned nil")
	}
	if policy.Hardening() {
		t.Error("the default policy must not harden")
	}
}
