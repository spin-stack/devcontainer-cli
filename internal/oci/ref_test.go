package oci

import (
	"testing"
)

func TestParseRef_TagVersion(t *testing.T) {
	ref, err := ParseRef("ghcr.io/devcontainers/features/go:1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Registry != "ghcr.io" {
		t.Errorf("registry = %q", ref.Registry)
	}
	if ref.Owner != "devcontainers" {
		t.Errorf("owner = %q", ref.Owner)
	}
	if ref.Namespace != "devcontainers/features" {
		t.Errorf("namespace = %q", ref.Namespace)
	}
	if ref.Path != "devcontainers/features/go" {
		t.Errorf("path = %q", ref.Path)
	}
	if ref.Resource != "ghcr.io/devcontainers/features/go" {
		t.Errorf("resource = %q", ref.Resource)
	}
	if ref.ID != "go" {
		t.Errorf("id = %q", ref.ID)
	}
	if ref.Tag != "1.0.0" {
		t.Errorf("tag = %q", ref.Tag)
	}
	if ref.Version != "1.0.0" {
		t.Errorf("version = %q", ref.Version)
	}
	if ref.Digest != "" {
		t.Errorf("digest = %q", ref.Digest)
	}
}

func TestParseRef_DigestVersion(t *testing.T) {
	ref, err := ParseRef("ghcr.io/devcontainers/features/go@sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Tag != "" {
		t.Errorf("tag = %q", ref.Tag)
	}
	if ref.Digest != "sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890" {
		t.Errorf("digest = %q", ref.Digest)
	}
	if ref.Version != ref.Digest {
		t.Errorf("version = %q", ref.Version)
	}
}

func TestParseRef_NoTag(t *testing.T) {
	ref, err := ParseRef("ghcr.io/devcontainers/features/go")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Tag != "latest" {
		t.Errorf("tag = %q, want 'latest'", ref.Tag)
	}
	if ref.Version != "latest" {
		t.Errorf("version = %q", ref.Version)
	}
}

func TestParseRef_UpperCase(t *testing.T) {
	ref, err := ParseRef("GHCR.IO/DevContainers/Features/Go:1")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Registry != "ghcr.io" {
		t.Errorf("registry = %q (should be lowered)", ref.Registry)
	}
	if ref.ID != "go" {
		t.Errorf("id = %q (should be lowered)", ref.ID)
	}
}

func TestParseRef_RegistryWithPort(t *testing.T) {
	ref, err := ParseRef("localhost:5000/myns/myfeature:1.0")
	if err != nil {
		t.Fatal(err)
	}
	// The colon in "localhost:5000" is before the last slash,
	// so it should be treated as part of registry, not as a tag separator.
	if ref.Registry != "localhost:5000" {
		t.Errorf("registry = %q", ref.Registry)
	}
	if ref.ID != "myfeature" {
		t.Errorf("id = %q", ref.ID)
	}
	if ref.Tag != "1.0" {
		t.Errorf("tag = %q", ref.Tag)
	}
}

func TestParseRef_DotPrefix(t *testing.T) {
	_, err := ParseRef(".invalid/path/feature:1")
	if err == nil {
		t.Error("expected error for dot-prefixed input")
	}
}

func TestParseRef_ShortPath(t *testing.T) {
	_, err := ParseRef("ghcr.io/tooShort")
	if err == nil {
		t.Error("expected error for path with <3 segments")
	}
}

func TestParseRef_InvalidDigestAlgo(t *testing.T) {
	_, err := ParseRef("ghcr.io/ns/id/feat@md5:abc")
	if err == nil {
		t.Error("expected error for non-sha256 algorithm")
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
}

func TestParseCollectionRef(t *testing.T) {
	ref, err := ParseCollectionRef("ghcr.io", "devcontainers/features")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Registry != "ghcr.io" {
		t.Errorf("registry = %q", ref.Registry)
	}
	if ref.Path != "devcontainers/features" {
		t.Errorf("path = %q", ref.Path)
	}
	if ref.Tag != "latest" {
		t.Errorf("tag = %q", ref.Tag)
	}
	if ref.Resource != "ghcr.io/devcontainers/features" {
		t.Errorf("resource = %q", ref.Resource)
	}
}

func TestParseRef_MajorVersionTag(t *testing.T) {
	ref, err := ParseRef("ghcr.io/devcontainers/features/node:1")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Tag != "1" {
		t.Errorf("tag = %q, want '1'", ref.Tag)
	}
}

func TestParseRef_LatestTag(t *testing.T) {
	ref, err := ParseRef("ghcr.io/devcontainers/features/node:latest")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Tag != "latest" {
		t.Errorf("tag = %q", ref.Tag)
	}
}
