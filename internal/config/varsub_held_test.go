package config

import "testing"

// An empty ${} is left as written and the tags after it are still resolved: TS
// matches /\$\{(.*?)\}/g, so "${}" is a match whose empty variable no case
// handles (replaceWithContext returns the match), and the scan goes on.
func TestResolveKeepsAnEmptyTagAndResolvesTheRest(t *testing.T) {
	ctx := SubstitutionContext{HostSubContext: HostSubContext{LocalWorkspaceFolder: "/home/me/proj"}}
	got, err := NewVariableResolver().Resolve(ctx, PhaseHost, "${}-${localWorkspaceFolderBasename}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "${}-proj" {
		t.Errorf("Resolve = %q, want ${}-proj", got)
	}
}

// ${containerEnv:NAME} matches the name case-insensitively only for a Windows
// platform (TS containerSubstitute: normalizeEnv lowercases the keys and
// lookupValue the name when isWindows); elsewhere a name in another case is
// missing and resolves to "".
func TestResolveContainerEnvIgnoresCaseOnlyOnWindows(t *testing.T) {
	for platform, want := range map[string]string{"win32": "/bin", "linux": ""} {
		ctx := SubstitutionContext{
			HostSubContext: HostSubContext{Platform: platform},
			ContainerEnv:   map[string]string{"Path": "/bin"},
		}
		got, err := NewVariableResolver().Resolve(ctx, PhaseContainer, "${containerEnv:PATH}")
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s: Resolve = %q, want %q", platform, got, want)
		}
	}
}
