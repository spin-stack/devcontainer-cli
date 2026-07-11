package cli

import (
	"reflect"
	"testing"
)

func TestRegistryHostFromImageRef(t *testing.T) {
	cases := map[string]string{
		"ghcr.io/org/img:tag":                           "ghcr.io",
		"ghcr.io/org/img@sha256:abc":                    "ghcr.io",
		"myreg.example.com:5000/team/app":               "myreg.example.com:5000",
		"localhost:5000/app":                            "localhost:5000",
		"123456.dkr.ecr.us-east-1.amazonaws.com/repo:1": "123456.dkr.ecr.us-east-1.amazonaws.com",
		// Docker Hub forms → no explicit registry → "" (ambient auth applies).
		"ubuntu:22.04":   "",
		"library/ubuntu": "",
		"org/img:tag":    "",
		"mcr":            "",
		"":               "",
	}
	for ref, want := range cases {
		if got := registryHostFromImageRef(ref); got != want {
			t.Errorf("registryHostFromImageRef(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestRegistryCacheRef(t *testing.T) {
	cases := map[string]string{
		"type=registry,ref=ghcr.io/org/cache:main": "ghcr.io/org/cache:main",
		"type=registry,ref=myreg.com/c,mode=max":   "myreg.com/c",
		"type=local,dest=/tmp/c":                   "", // not a registry cache
		"type=inline":                              "",
		"ghcr.io/org/cache:main":                   "ghcr.io/org/cache:main", // bare ref
		"":                                         "",
	}
	for spec, want := range cases {
		if got := registryCacheRef(spec); got != want {
			t.Errorf("registryCacheRef(%q) = %q, want %q", spec, got, want)
		}
	}
}

func TestCollectBuildRegistries(t *testing.T) {
	got := collectBuildRegistries(
		"ghcr.io/base/img:1", // base
		[]string{"ghcr.io/base/img:1", "reg.example.com/app:2", "ubuntu"},            // tags (dup base, hub)
		[]string{"type=registry,ref=cache.example.com/c:main", "type=local,dest=/x"}, // cacheFrom
		"type=registry,ref=cache.example.com/c:main",                                 // cacheTo (dup of cacheFrom)
	)
	want := []string{"ghcr.io", "reg.example.com", "cache.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collectBuildRegistries = %v, want %v", got, want)
	}
}

func TestCollectBuildRegistriesEmpty(t *testing.T) {
	// Only Docker Hub / local caches → nothing to bridge.
	if got := collectBuildRegistries("ubuntu:22.04", []string{"org/app"}, []string{"type=inline"}, ""); len(got) != 0 {
		t.Fatalf("expected no registries, got %v", got)
	}
}
