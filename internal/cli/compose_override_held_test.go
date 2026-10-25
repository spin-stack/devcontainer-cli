package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devcontainers/cli/internal/imagemeta"
)

// TS parseMount keeps every key=value pair; the boolean ones are true only for
// the spellings TS accepts.
func TestParseComposeMountStringBooleans(t *testing.T) {
	tests := []struct {
		spec string
		want composeMount
	}{
		{"type=volume,source=v,target=/v,external=true", composeMount{Type: "volume", Source: "v", Target: "/v", External: true}},
		{"type=volume,source=v,target=/v,external=false", composeMount{Type: "volume", Source: "v", Target: "/v"}},
		{"source=/h,destination=/c,ro", composeMount{Source: "/h", Target: "/c", ReadOnly: true}},
		{"source=/h,target=/c,readonly=true", composeMount{Source: "/h", Target: "/c", ReadOnly: true}},
		{"source=/h,target=/c,ro=1", composeMount{Source: "/h", Target: "/c", ReadOnly: true}},
		{"source=/h,target=/c,readonly=false", composeMount{Source: "/h", Target: "/c"}},
	}
	for _, tt := range tests {
		if got := parseComposeMountString(tt.spec); got != tt.want {
			t.Errorf("parseComposeMountString(%q) = %+v, want %+v", tt.spec, got, tt.want)
		}
	}
}

// A project with a single compose file still carries its version line.
func TestReadComposeVersionPrefixSingleFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "docker-compose.yml")
	if err := os.WriteFile(f, []byte("version: '3'\nservices: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readComposeVersionPrefix([]string{f})
	if err != nil || got != "version: '3'\n\n" {
		t.Errorf("readComposeVersionPrefix = %q, %v; want %q", got, err, "version: '3'\n\n")
	}
	if got, err := readComposeVersionPrefix(nil); err != nil || got != "" {
		t.Errorf("no compose files: %q, %v", got, err)
	}
	if _, err := readComposeVersionPrefix([]string{filepath.Join(t.TempDir(), "missing.yml")}); err == nil {
		t.Error("missing compose file: want an error")
	}
}

// One entry is enough for each list section of the override to be written.
func TestGenerateComposeFeaturesOverrideSingleEntries(t *testing.T) {
	got := generateComposeFeaturesOverride(composeFeaturesOverride{
		Service:           "app",
		UpdatedImageName:  "img",
		OriginalImageName: "img",
		Merged: &imagemeta.MergedConfig{
			ContainerEnv: map[string]string{"A": "1"},
			CapAdd:       []string{"SYS_PTRACE"},
			SecurityOpt:  []string{"seccomp=unconfined"},
		},
		Labels: []string{"l=1"},
		Mounts: []composeMount{{Type: "volume", Source: "vol", Target: "/v"}},
	})
	want := "services:\n  'app':\n    entrypoint: [\"/bin/sh\", \"-c\", \"echo Container started\\n\ntrap \\\"exit 0\\\" 15\\n\n\\n\nexec \\\"$$@\\\"\\n\nwhile sleep 1 & wait $$!; do :; done\", \"-\"]\n    command: []\n    environment:\n      - 'A=1'\n    cap_add:\n      - SYS_PTRACE\n    security_opt:\n      - seccomp=unconfined\n    labels:\n      - 'l=1'\n    volumes:\n      - vol:/v\nvolumes:\n  \n  vol:\n"
	if got != want {
		t.Errorf("override mismatch\ngot:  %q\nwant: %q", got, want)
	}
}

// A single --cache-from still gets its cache_from section, even with no
// Features build.
func TestGenerateComposeBuildOverrideSingleCacheFrom(t *testing.T) {
	got := generateComposeBuildOverride(composeBuildOverride{Service: "app", CacheFrom: []string{"reg/img:cache"}})
	if want := "services:\n  app:\n\n      cache_from:\n        - reg/img:cache\n\n"; got != want {
		t.Errorf("build override = %q, want %q", got, want)
	}
}
