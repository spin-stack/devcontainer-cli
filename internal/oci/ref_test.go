package oci

import (
	"testing"
)

func TestParseRef(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		want    Ref
	}{
		{
			name:  "tag version",
			input: "ghcr.io/devcontainers/features/go:1.0.0",
			want: Ref{
				Registry: "ghcr.io", Owner: "devcontainers", Namespace: "devcontainers/features",
				Path: "devcontainers/features/go", Resource: "ghcr.io/devcontainers/features/go",
				ID: "go", Tag: "1.0.0", Version: "1.0.0",
			},
		},
		{
			name:  "digest version",
			input: "ghcr.io/devcontainers/features/go@sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
			want: Ref{
				Registry: "ghcr.io", Owner: "devcontainers", Namespace: "devcontainers/features",
				Path: "devcontainers/features/go", Resource: "ghcr.io/devcontainers/features/go",
				ID:      "go",
				Digest:  "sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
				Version: "sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
			},
		},
		{
			name:  "no tag defaults to latest",
			input: "ghcr.io/devcontainers/features/go",
			want: Ref{
				Registry: "ghcr.io", Owner: "devcontainers", Namespace: "devcontainers/features",
				Path: "devcontainers/features/go", Resource: "ghcr.io/devcontainers/features/go",
				ID: "go", Tag: "latest", Version: "latest",
			},
		},
		{
			name:  "uppercase is lowered",
			input: "GHCR.IO/DevContainers/Features/Go:1",
			want: Ref{
				Registry: "ghcr.io", Owner: "devcontainers", Namespace: "devcontainers/features",
				Path: "devcontainers/features/go", Resource: "ghcr.io/devcontainers/features/go",
				ID: "go", Tag: "1", Version: "1",
			},
		},
		{
			name:  "registry with port",
			input: "localhost:5000/myns/myfeature:1.0",
			want: Ref{
				Registry: "localhost:5000", Owner: "myns", Namespace: "myns",
				Path: "myns/myfeature", Resource: "localhost:5000/myns/myfeature",
				ID: "myfeature", Tag: "1.0", Version: "1.0",
			},
		},
		{
			name:  "major version tag",
			input: "ghcr.io/devcontainers/features/node:1",
			want: Ref{
				Registry: "ghcr.io", Owner: "devcontainers", Namespace: "devcontainers/features",
				Path: "devcontainers/features/node", Resource: "ghcr.io/devcontainers/features/node",
				ID: "node", Tag: "1", Version: "1",
			},
		},
		{
			name:  "latest tag",
			input: "ghcr.io/devcontainers/features/node:latest",
			want: Ref{
				Registry: "ghcr.io", Owner: "devcontainers", Namespace: "devcontainers/features",
				Path: "devcontainers/features/node", Resource: "ghcr.io/devcontainers/features/node",
				ID: "node", Tag: "latest", Version: "latest",
			},
		},
		{
			name:  "nested namespace",
			input: "ghcr.io/org/team/features/go:1.0",
			want: Ref{
				Registry: "ghcr.io", Owner: "org", Namespace: "org/team/features",
				Path: "org/team/features/go", Resource: "ghcr.io/org/team/features/go",
				ID: "go", Tag: "1.0", Version: "1.0",
			},
		},
		{
			name:  "custom registry without port",
			input: "myregistry.example.com/ns/feature:2.0",
			want: Ref{
				Registry: "myregistry.example.com", Owner: "ns", Namespace: "ns",
				Path: "ns/feature", Resource: "myregistry.example.com/ns/feature",
				ID: "feature", Tag: "2.0", Version: "2.0",
			},
		},
		{name: "dot-prefixed input", input: ".invalid/path/feature:1", wantErr: true},
		{name: "path with too few segments", input: "ghcr.io/tooShort", wantErr: true},
		{name: "non-sha256 digest algorithm", input: "ghcr.io/ns/id/feat@md5:abc", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := ParseRef(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRef(%q): %v", tt.input, err)
			}
			for _, f := range []struct {
				name, got, want string
			}{
				{"Registry", ref.Registry, tt.want.Registry},
				{"Owner", ref.Owner, tt.want.Owner},
				{"Namespace", ref.Namespace, tt.want.Namespace},
				{"Path", ref.Path, tt.want.Path},
				{"Resource", ref.Resource, tt.want.Resource},
				{"ID", ref.ID, tt.want.ID},
				{"Tag", ref.Tag, tt.want.Tag},
				{"Version", ref.Version, tt.want.Version},
				{"Digest", ref.Digest, tt.want.Digest},
			} {
				if f.got != f.want {
					t.Errorf("%s = %q, want %q", f.name, f.got, f.want)
				}
			}
		})
	}
}

func TestParseRef_URLs(t *testing.T) {
	ref, err := ParseRef("ghcr.io/devcontainers/features/go:1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if got := ref.ManifestURL(); got != "https://ghcr.io/v2/devcontainers/features/go/manifests/1.2.3" {
		t.Errorf("manifest url = %q", got)
	}
	if got := ref.BlobURL("sha256:abc"); got != "https://ghcr.io/v2/devcontainers/features/go/blobs/sha256:abc" {
		t.Errorf("blob url = %q", got)
	}
	if got := ref.TagsURL(); got != "https://ghcr.io/v2/devcontainers/features/go/tags/list" {
		t.Errorf("tags url = %q", got)
	}
	// A digest ref pins the manifest by digest instead of tag.
	digestRef := &Ref{Registry: "ghcr.io", Path: "devcontainers/features/go", Version: "sha256:abc123", Digest: "sha256:abc123"}
	if got := digestRef.ManifestURL(); got != "https://ghcr.io/v2/devcontainers/features/go/manifests/sha256:abc123" {
		t.Errorf("digest manifest url = %q", got)
	}
}

func TestParseCollectionRef(t *testing.T) {
	tests := []struct {
		name              string
		registry, path    string
		wantErr           bool
		wantTag, wantRsrc string
	}{
		{name: "valid", registry: "ghcr.io", path: "devcontainers/features", wantTag: "latest", wantRsrc: "ghcr.io/devcontainers/features"},
		{name: "invalid path", registry: "ghcr.io", path: "INVALID PATH!!", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := ParseCollectionRef(tt.registry, tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q/%q", tt.registry, tt.path)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ref.Registry != tt.registry || ref.Path != tt.path || ref.Tag != tt.wantTag || ref.Resource != tt.wantRsrc {
				t.Errorf("got {registry:%q path:%q tag:%q resource:%q}", ref.Registry, ref.Path, ref.Tag, ref.Resource)
			}
		})
	}
}
