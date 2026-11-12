package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type stringOrSlice []string

func (s *stringOrSlice) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*s = []string{n.Value}
		return nil
	}
	var xs []string
	if err := n.Decode(&xs); err != nil {
		return err
	}
	*s = xs
	return nil
}

func (s stringOrSlice) contains(v string) bool {
	return slices.Contains(s, v)
}

type wfJob struct {
	If       string        `yaml:"if"`
	Needs    stringOrSlice `yaml:"needs"`
	Strategy struct {
		Matrix struct {
			Shard []int `yaml:"shard"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
	Env   map[string]string `yaml:"env"`
	Steps []struct {
		Uses string `yaml:"uses"`
		Run  string `yaml:"run"`
	} `yaml:"steps"`
}

type workflow struct {
	Jobs map[string]wfJob `yaml:"jobs"`
}

func loadWorkflow(t *testing.T) workflow {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "go-cli.yml"))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	var wf workflow
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse workflow: %v", err)
	}
	return wf
}

func TestWorkflowShardMatrixMatchesShardTotal(t *testing.T) {
	wf := loadWorkflow(t)

	sharded := 0
	for name, job := range wf.Jobs {
		total, ok := job.Env["PARITY_SHARD_TOTAL"]
		if !ok {
			continue
		}
		sharded++

		shards := job.Strategy.Matrix.Shard
		if total != strconv.Itoa(len(shards)) {
			t.Errorf("job %q: PARITY_SHARD_TOTAL=%q but matrix.shard has %d entries; a mismatch silently drops cases",
				name, total, len(shards))
		}
		for i, s := range shards {
			if s != i {
				t.Errorf("job %q: matrix.shard must be 0..%d in order, got %v (index %d = %d)",
					name, len(shards)-1, shards, i, s)
			}
		}
		if idx := job.Env["PARITY_SHARD_INDEX"]; idx != "${{ matrix.shard }}" {
			t.Errorf("job %q: PARITY_SHARD_INDEX=%q, want ${{ matrix.shard }} so each runner gets its slice",
				name, idx)
		}
	}

	if sharded < 1 {
		t.Errorf("found %d sharded parity jobs, want at least 1 (parity-runtime)", sharded)
	}
}

func TestWorkflowRuntimeJobsShareCompositeAction(t *testing.T) {
	wf := loadWorkflow(t)
	const action = "./.github/actions/setup-parity-runtime"

	for _, name := range []string{"parity-runtime", "parity-publish-full"} {
		job, ok := wf.Jobs[name]
		if !ok {
			t.Errorf("job %q missing", name)
			continue
		}
		used := false
		for _, s := range job.Steps {
			if s.Uses == action {
				used = true
			}
			if strings.HasPrefix(s.Uses, "actions/setup-node") || strings.HasPrefix(s.Uses, "go-task/setup-task") {
				t.Errorf("job %q re-inlines %q; use the composite action instead", name, s.Uses)
			}
		}
		if !used {
			t.Errorf("job %q does not use %q — runtime setup no longer shared", name, action)
		}
	}
}

func TestWorkflowDailyLanesGatedOnChanges(t *testing.T) {
	wf := loadWorkflow(t)

	if _, ok := wf.Jobs["daily-changes"]; !ok {
		t.Fatal("daily-changes job missing — nothing gates the daily lanes")
	}
	for _, name := range []string{"parity-runtime", "parity-publish-full"} {
		job, ok := wf.Jobs[name]
		if !ok {
			t.Errorf("job %q missing", name)
			continue
		}
		if !job.Needs.contains("daily-changes") {
			t.Errorf("job %q needs %v, missing daily-changes", name, job.Needs)
		}
		if !strings.Contains(job.If, "needs.daily-changes.outputs.run") {
			t.Errorf("job %q if=%q does not gate on daily-changes output", name, job.If)
		}
	}
}
