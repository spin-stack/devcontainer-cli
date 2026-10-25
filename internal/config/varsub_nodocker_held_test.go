package config

import "testing"

// In the host phase ${containerWorkspaceFolder} is the container workspace
// folder with its own host variables resolved first (TS substitute resolves
// context.containerWorkspaceFolder before substituting the value); the other
// phases leave it alone.
func TestResolveHostPhaseResolvesTheContainerWorkspaceFolderFirst(t *testing.T) {
	ctx := SubstitutionContext{HostSubContext: HostSubContext{
		LocalWorkspaceFolder:     "/home/me/proj",
		ContainerWorkspaceFolder: "/workspaces/${localWorkspaceFolderBasename}",
	}}
	got, err := NewVariableResolver().Resolve(ctx, PhaseHost, "${containerWorkspaceFolder}/src")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/workspaces/proj/src" {
		t.Errorf("host phase = %q, want /workspaces/proj/src", got)
	}

	// With no container workspace folder the tag stays as written.
	ctx.ContainerWorkspaceFolder = ""
	if got, err := NewVariableResolver().Resolve(ctx, PhaseHost, "${containerWorkspaceFolder}"); err != nil || got != "${containerWorkspaceFolder}" {
		t.Errorf("no container folder = %q, %v; want the tag unchanged", got, err)
	}
}
