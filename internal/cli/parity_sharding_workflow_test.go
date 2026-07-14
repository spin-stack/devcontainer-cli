package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"gopkg.in/yaml.v3"
)

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
	path := filepath.Join("..", "..", ".github", "workflows", "go-cli.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}

	var wf struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix struct {
					Shard []int `yaml:"shard"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
			Env map[string]string `yaml:"env"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse workflow: %v", err)
	}

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
