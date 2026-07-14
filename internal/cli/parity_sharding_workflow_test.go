package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// stringOrSlice decodes a GitHub Actions field that may be a scalar ("needs: x")
// or a sequence ("needs: [x, y]").
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
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
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

// TestWorkflowShardMatrixMatchesShardTotal guards the CI wiring that
// TestInShardPartitions cannot see: the round-robin partition is only a true
// cover-everything-once split when the runner matrix and PARITY_SHARD_TOTAL agree.
// If a job declares matrix.shard: [0,1,2,3] but PARITY_SHARD_TOTAL: "2", shards 2
// and 3 select an empty slice while cases at index 2,3 (mod 4) run in no shard at
// all — the daily backstop goes green having silently skipped a quarter of the
// matrix. This asserts, for every sharded parity job, that:
//   - PARITY_SHARD_TOTAL == len(matrix.shard),
//   - matrix.shard is exactly 0..total-1, and
//   - PARITY_SHARD_INDEX threads the matrix value through.
func TestWorkflowShardMatrixMatchesShardTotal(t *testing.T) {
	wf := loadWorkflow(t)

	// Every job that sets PARITY_SHARD_TOTAL is a sharded lane and must be checked;
	// discovering them from the env (rather than a hardcoded name list) means a new
	// sharded job is covered automatically.
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

	// Both the push/PR (parity-runtime-shard) and the daily (parity-runtime-full)
	// lanes are sharded; if neither is found the parsing (or the jobs) regressed.
	if sharded < 2 {
		t.Errorf("found %d sharded parity jobs, want at least 2 (push/PR + daily full)", sharded)
	}
}

// TestWorkflowRuntimeJobsShareCompositeAction locks in the reuse between the two
// runtime lanes: both must set up their runner through the shared composite action
// (toolchain + ci:prepare-runner + reference) rather than re-inlining the steps, so
// the push/PR and daily environments can't drift apart.
func TestWorkflowRuntimeJobsShareCompositeAction(t *testing.T) {
	wf := loadWorkflow(t)
	const action = "./.github/actions/setup-parity-runtime"

	for _, name := range []string{"parity-runtime-shard", "parity-runtime-full"} {
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
			// The inlined toolchain setup must be gone (that's the point of the reuse).
			if strings.HasPrefix(s.Uses, "actions/setup-go") || strings.HasPrefix(s.Uses, "actions/setup-node") {
				t.Errorf("job %q re-inlines %q; use the composite action instead", name, s.Uses)
			}
		}
		if !used {
			t.Errorf("job %q does not use %q — runtime setup no longer shared", name, action)
		}
	}
}

// TestWorkflowDailyLanesGatedOnChanges asserts the expensive daily lanes only run
// when the repo changed that day: each must `needs: daily-changes` and gate on its
// output. Dropping either turns the change-detection into a no-op and the matrix
// runs (and bills) every day regardless.
func TestWorkflowDailyLanesGatedOnChanges(t *testing.T) {
	wf := loadWorkflow(t)

	if _, ok := wf.Jobs["daily-changes"]; !ok {
		t.Fatal("daily-changes job missing — nothing gates the daily lanes")
	}
	for _, name := range []string{"parity-runtime-full", "parity-publish-full"} {
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
