package cli

import (
	"strings"
	"testing"
)

// up and build warn on stderr, before anything else, about the deprecated
// lockfile flags, as TS warnDeprecatedLockfileFlags (devContainersSpecCLI.ts)
// does at the start of both handlers; the lockfile is written by default, so
// --experimental-lockfile has nothing left to turn on.
func TestDeprecatedLockfileFlagsWarn(t *testing.T) {
	const (
		write  = "Warning: --experimental-lockfile is deprecated. Lockfiles are now enabled by default.\n"
		frozen = "Warning: --experimental-frozen-lockfile is deprecated. Use --frozen-lockfile instead.\n"
	)
	for _, tc := range []struct {
		name                 string
		experimental, frozen bool
		want                 string
	}{
		{"neither", false, false, ""},
		{"experimental-lockfile", true, false, write},
		{"experimental-frozen-lockfile", false, true, frozen},
		{"both", true, true, write + frozen},
	} {
		run := map[string]func(*captureOutput) error{
			"build": func(out *captureOutput) error {
				return runBuild(t.Context(), out, &buildOpts{
					workspaceFolder: t.TempDir(), logLevel: "info", logFormat: "text", buildkit: "auto",
					experimentalLockfile: tc.experimental, experimentalFrozenLockfile: tc.frozen,
				})
			},
			"up": func(out *captureOutput) error {
				return runUp(t.Context(), out, &upOpts{
					workspaceFolder: t.TempDir(), logLevel: "info", logFormat: "text", buildkit: "auto", gpuAvailability: "detect", workspaceMountConsistency: "cached", defaultUserEnvProbe: "loginInteractiveShell", updateRemoteUserUIDDefault: "on",
					experimentalLockfile: tc.experimental, experimentalFrozenLockfile: tc.frozen,
				})
			},
		}
		for cmd, fn := range run {
			t.Run(cmd+"/"+tc.name, func(t *testing.T) {
				out := &captureOutput{}
				// An empty workspace fails loading its configuration, before Docker.
				_ = fn(out)
				stderr := out.err.String()
				if !strings.HasPrefix(stderr, tc.want) || strings.Count(stderr, "is deprecated") != strings.Count(tc.want, "is deprecated") {
					t.Errorf("stderr = %q, want it to start with %q and warn nothing more", stderr, tc.want)
				}
			})
		}
	}
}
