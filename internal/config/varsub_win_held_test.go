package config

import "testing"

// On win32 both '\' and '/' separate the workspace folder's basename, and a
// folder without one is its own basename.
func TestLocalWorkspaceFolderBasenameOnWindows(t *testing.T) {
	for folder, want := range map[string]string{
		`C:\Users\me\project`: "project",
		`C:/Users/me/project`: "project",
		`C:\Users/me\mixed`:   "mixed",
		`project`:             "project",
	} {
		ctx := HostSubContext{Platform: "win32", LocalWorkspaceFolder: folder, Env: map[string]string{}}
		got, err := NewVariableResolver().Resolve(SubstitutionContext{HostSubContext: ctx}, PhaseHost, "${localWorkspaceFolderBasename}")
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("basename of %q = %v, want %q", folder, got, want)
		}
	}
}
