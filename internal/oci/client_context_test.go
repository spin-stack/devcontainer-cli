package oci

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/log"
)

func TestClientOperationsHonorCanceledContext(t *testing.T) {
	client := NewClient(log.Null, nil)
	ref, err := ParseRef("localhost:1/example/features/demo:1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		run  func() error
	}{
		{"manifest", func() error { _, err := client.FetchManifestContext(ctx, ref, ""); return err }},
		{"blob", func() error {
			_, err := client.FetchBlobContext(ctx, ref, "sha256:"+strings.Repeat("0", 64))
			return err
		}},
		{"tags", func() error { _, err := client.GetPublishedTagsContext(ctx, ref); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
		})
	}
}

func TestFetchManifestContextRejectsNonDomainBeforeTransport(t *testing.T) {
	client := NewClient(log.Null, nil)
	ref := &Ref{Registry: "registry", Path: "owner/demo", Resource: "registry/owner/demo", Version: "latest"}
	_, err := client.FetchManifestContext(context.Background(), ref, "")
	if err == nil || !strings.Contains(err.Error(), "does not look like a domain") {
		t.Fatalf("error = %v", err)
	}
}
