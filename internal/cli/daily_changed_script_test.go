package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestDailyChangedScript covers scripts/daily-changed.sh, which gates the daily
// scheduled parity run on "did the repo change today?": it must emit run=true iff
// HEAD's commit is within the look-back window. Getting the boundary wrong either
// wastes billed runner minutes (false positive) or silently skips the daily
// backstop on a day that had commits (false negative).
func TestDailyChangedScript(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "daily-changed.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("script missing: %v", err)
	}

	// Fixed "now" so the test is deterministic regardless of wall clock.
	const now = 2_000_000_000

	cases := []struct {
		name       string
		commitAgeH int
		window     string // arg to the script; "" uses the script default (25h)
		wantRun    bool
	}{
		{name: "fresh commit runs", commitAgeH: 1, wantRun: true},
		{name: "just inside default window", commitAgeH: 24, wantRun: true},
		{name: "just outside default window", commitAgeH: 26, wantRun: false},
		{name: "day-old with no changes skips", commitAgeH: 48, wantRun: false},
		{name: "custom window widens", commitAgeH: 30, window: "72", wantRun: true},
		{name: "custom window narrows", commitAgeH: 2, window: "1", wantRun: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepoWithCommitAt(t, now-tc.commitAgeH*3600)

			args := []string{script}
			if tc.window != "" {
				args = append(args, tc.window)
			}
			cmd := exec.Command("bash", args...)
			cmd.Dir = repo
			cmd.Env = append(os.Environ(), "DAILY_NOW_EPOCH="+strconv.Itoa(now))
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("script failed: %v", err)
			}

			want := "run=false"
			if tc.wantRun {
				want = "run=true"
			}
			if got := strings.TrimSpace(string(out)); got != want {
				t.Errorf("age=%dh window=%q: got %q, want %q", tc.commitAgeH, tc.window, got, want)
			}
		})
	}
}

// newRepoWithCommitAt creates a throwaway git repo whose single HEAD commit has the
// given committer timestamp (what `git log -1 --format=%ct` reads).
func newRepoWithCommitAt(t *testing.T, epoch int) string {
	t.Helper()
	dir := t.TempDir()
	date := "@" + strconv.Itoa(epoch) + " +0000"
	env := append(os.Environ(),
		"GIT_AUTHOR_DATE="+date,
		"GIT_COMMITTER_DATE="+date,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "f")
	run("commit", "-q", "-m", "seed")
	return dir
}
