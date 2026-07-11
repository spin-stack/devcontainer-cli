package oci

import (
	"testing"
)

func TestParseRef_NestedNamespace(t *testing.T) {
	ref, err := ParseRef("ghcr.io/org/team/features/go:1.0")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Registry != "ghcr.io" {
		t.Errorf("registry = %q", ref.Registry)
	}
	if ref.Namespace != "org/team/features" {
		t.Errorf("namespace = %q", ref.Namespace)
	}
	if ref.ID != "go" {
		t.Errorf("id = %q", ref.ID)
	}
}

func TestParseRef_OnlyDigest(t *testing.T) {
	ref, err := ParseRef("ghcr.io/devcontainers/features/go@sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Tag != "" {
		t.Errorf("tag should be empty for digest ref, got %q", ref.Tag)
	}
	if ref.Version != ref.Digest {
		t.Error("version should equal digest")
	}
}

func TestParseRef_RegistryNoPort(t *testing.T) {
	ref, err := ParseRef("myregistry.example.com/ns/feature:2.0")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Registry != "myregistry.example.com" {
		t.Errorf("registry = %q", ref.Registry)
	}
	if ref.Tag != "2.0" {
		t.Errorf("tag = %q", ref.Tag)
	}
}

func TestParseCollectionRef_Invalid(t *testing.T) {
	_, err := ParseCollectionRef("ghcr.io", "INVALID PATH!!")
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestRef_ManifestURL_Digest(t *testing.T) {
	ref := &Ref{
		Registry: "ghcr.io",
		Path:     "devcontainers/features/go",
		Version:  "sha256:abc123",
		Digest:   "sha256:abc123",
	}
	url := ref.ManifestURL()
	if url != "https://ghcr.io/v2/devcontainers/features/go/manifests/sha256:abc123" {
		t.Errorf("url = %q", url)
	}
}

func TestComputeTags_SingleDigit(t *testing.T) {
	tags := computeTags("1")
	// "1" has 1 part → no minor tag, just [1, latest]
	if len(tags) != 2 {
		t.Errorf("tags = %v, want [1, latest]", tags)
	}
	if tags[0] != "1" || tags[1] != "latest" {
		t.Errorf("tags = %v", tags)
	}
}

func TestComputeTags_FourParts(t *testing.T) {
	tags := computeTags("1.2.3.4")
	// Unusual but should still work: [1.2.3.4, 1.2, 1, latest]
	if tags[0] != "1.2.3.4" {
		t.Errorf("tags[0] = %q", tags[0])
	}
}
