package devtools

import "testing"

// TestParityAffected is a golden table: every expected value was captured from the
// former scripts/parity-affected.sh before the port (files fed one-per-line with a
// trailing newline, as `git diff --name-only` emits), so this proves behavior parity
// and guards the conservative mapping (a wrong "none"/narrow result silently skips
// runtime cases the change could break).
func TestParityAffected(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"empty", nil, "none"},
		{"markdown only", []string{"README.md"}, "none"},
		{"license", []string{"LICENSE"}, "none"},
		{"goreleaser", []string{".goreleaser.yml"}, "none"},
		{"release workflow", []string{".github/workflows/release.yml"}, "none"},
		{"command test file", []string{"internal/cli/exec_test.go"}, "none"},

		{"exec leaf", []string{"internal/cli/exec.go"}, "exec"},
		{"up via gpu", []string{"internal/cli/gpu.go"}, "up"},
		{"up via mounts", []string{"internal/cli/mounts.go"}, "up"},
		{"templates leaf", []string{"internal/cli/templates_apply.go"}, "templates"},
		{"features-info leaf", []string{"internal/cli/features_info.go"}, "features-info"},
		{"build multi-file", []string{"internal/cli/build_auth.go", "internal/cli/cache_key.go"}, "build"},
		{"up+build sorted", []string{"internal/cli/up.go", "internal/cli/build.go"}, "build,up"},
		{"setup+outdated sorted", []string{"internal/cli/setup.go", "internal/cli/outdated.go"}, "outdated,set-up"},
		{"read-config+exec sorted", []string{"internal/cli/read_configuration.go", "internal/cli/exec.go"}, "exec,read-configuration"},
		{"collection fans out", []string{"internal/cli/collection_commands.go"}, "features,features-info,features-package,templates"},
		{"leaf + ignorable", []string{"internal/cli/exec.go", "README.md"}, "exec"},

		{"matrix data → all", []string{"docs/parity/parity-matrix.yaml"}, "all"},
		{"parity harness test → all", []string{"internal/cli/parity_matrix_test.go"}, "all"},
		{"go.mod → all", []string{"go.mod"}, "all"},
		{"Taskfile → all", []string{"Taskfile.yml"}, "all"},
		{"go-cli workflow → all", []string{".github/workflows/go-cli.yml"}, "all"},
		{"shared internal → all", []string{"internal/foo.go"}, "all"},
		{"cmd → all", []string{"cmd/devcontainer/main.go"}, "all"},
		{"unknown path → all", []string{"totally-unknown-file"}, "all"},
		{"full wins over leaf", []string{"internal/cli/exec.go", "docs/parity/parity-matrix.yaml"}, "all"},

		// Post-migration: the ported logic lives under internal/ and cmd/, so a
		// change to it conservatively triggers the full matrix (internal/*, cmd/*).
		{"ported logic → all", []string{"internal/devtools/parityaffected.go"}, "all"},
		{"devtool main → all", []string{"cmd/devtool/main.go"}, "all"},

		{"whitespace trimmed", []string{"  internal/cli/exec.go  ", ""}, "exec"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParityAffected(tc.files); got != tc.want {
				t.Errorf("ParityAffected(%v) = %q, want %q", tc.files, got, tc.want)
			}
		})
	}
}
