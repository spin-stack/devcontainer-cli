package cli

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestWorkflowsDeclarePermissions pins least privilege for GITHUB_TOKEN: every
// workflow must set `permissions`, either at the top level (covering all of its
// jobs) or on each job. Without it the token inherits the repository default,
// which CodeQL reports as "Workflow does not contain permissions" — six jobs of
// go-cli.yml were flagged that way.
//
// A Go test cannot exercise CI, so this asserts the workflow files themselves.
func TestWorkflowsDeclarePermissions(t *testing.T) {
	dir := filepath.Join("..", "..", ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	type workflow struct {
		Permissions yaml.Node `yaml:"permissions"`
		Jobs        map[string]struct {
			Permissions yaml.Node `yaml:"permissions"`
			// A job that only calls a reusable workflow declares `uses`; its
			// permissions belong to the called workflow.
			Uses string `yaml:"uses"`
		} `yaml:"jobs"`
	}

	seen := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (filepath.Ext(name) != ".yml" && filepath.Ext(name) != ".yaml") {
			continue
		}
		seen++
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var wf workflow
		if err := yaml.Unmarshal(data, &wf); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if !wf.Permissions.IsZero() {
			continue // a top-level block covers every job
		}
		if len(wf.Jobs) == 0 {
			t.Errorf("%s: no jobs found (parse problem?)", name)
			continue
		}
		for job, spec := range wf.Jobs {
			if spec.Uses == "" && spec.Permissions.IsZero() {
				t.Errorf("%s: job %q declares no permissions — add a job-level `permissions:` block, or a top-level one for the whole workflow", name, job)
			}
		}
	}
	if seen == 0 {
		t.Fatalf("no workflow files found under %s", dir)
	}
}

// TestGoCLIWorkflowIsReadOnlyByDefault pins the specific grant: the CI workflow's
// default is read-only, so a job added later cannot silently inherit write access.
func TestGoCLIWorkflowIsReadOnlyByDefault(t *testing.T) {
	path := filepath.Join("..", "..", ".github", "workflows", "go-cli.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var wf struct {
		Permissions map[string]string `yaml:"permissions"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	want := map[string]string{"contents": "read"}
	if len(wf.Permissions) != len(want) {
		t.Fatalf("permissions = %v, want %v", wf.Permissions, want)
	}
	for scope, level := range want {
		if wf.Permissions[scope] != level {
			t.Errorf("permissions[%q] = %q, want %q", scope, wf.Permissions[scope], level)
		}
	}
}
