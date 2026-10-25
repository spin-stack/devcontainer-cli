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

// Only ${templateOption:NAME} with a single name is a template option: TS
// replaceTemplatedValues matches /\${templateOption:\s*(\w+?)\s*}/g, which
// neither a second argument nor another variable fits, so both stay as written.
func TestResolveTemplateOptionTakesOneName(t *testing.T) {
	ctx := SubstitutionContext{TemplateOptions: map[string]string{"a": "x"}}
	got, err := NewVariableResolver().Resolve(ctx, PhaseTemplate, "${templateOption:a}|${templateOption:a:b}|${other:a}")
	if err != nil {
		t.Fatal(err)
	}
	if want := "x|${templateOption:a:b}|${other:a}"; got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}
