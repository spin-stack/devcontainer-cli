package devtools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain fixes the umask a CI runner has, so the modes of the files the
// commands leave behind are the ones the tests assert.
func TestMain(m *testing.M) {
	syscall.Umask(0o022)
	os.Exit(m.Run())
}

// runMain runs one devtool command as the binary would and returns its exit code
// and what it wrote.
func runMain(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var o, e strings.Builder
	code = Main(append([]string{"devtool"}, args...), strings.NewReader(stdin), &o, &e)
	return code, o.String(), e.String()
}

// fakeTools puts an executable per scripts key first on PATH, standing in for a
// host tool the command starts. Each logs its name and argv, one line per call,
// to the returned file, then runs its body.
func fakeTools(t *testing.T, scripts map[string]string) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls.log")
	for name, body := range scripts {
		script := "#!/bin/sh\necho \"" + name + " $*\" >> '" + log + "'\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != want {
		t.Errorf("mode of %s = %o, want %o", path, got, want)
	}
}

// gitRepo makes an empty repository in a new directory and enters it, with no
// user or system configuration to sign or hook its commits.
func gitRepo(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	git(t, "init", "-q")
}

func git(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit adds file and commits it at epoch, returning the commit's hash.
func commit(t *testing.T, file string, epoch int64) string {
	t.Helper()
	writeFile(t, file, file)
	git(t, "add", file)
	date := "@" + strconv.FormatInt(epoch, 10) + " +0000"
	t.Setenv("GIT_COMMITTER_DATE", date)
	t.Setenv("GIT_AUTHOR_DATE", date)
	git(t, "commit", "-q", "-m", file)
	return git(t, "rev-parse", "HEAD")
}

func TestMainUsageErrors(t *testing.T) {
	if code, out, errOut := runMain(t, ""); code != 2 || out != "" || errOut != "usage: devtool <command> [args...]\n" {
		t.Errorf("no command: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if code, out, errOut := runMain(t, "", "nope"); code != 2 || out != "" || errOut != "devtool: unknown command \"nope\"\n" {
		t.Errorf("unknown command: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	code, out, errOut := runMain(t, "", "coverage-report")
	if code != 1 || out != "" || errOut != "devtool coverage-report: usage: coverage-report <profile|covdata-dir> [title]\n" {
		t.Errorf("a command's error: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
}

func TestParityAffectedReadsFilesFromStdin(t *testing.T) {
	code, out, errOut := runMain(t, "internal/cli/exec.go\nREADME.md\n", "parity-affected")
	if code != 0 || out != "exec\n" || errOut != "" {
		t.Errorf("code=%d stdout=%q stderr=%q, want 0 \"exec\\n\" \"\"", code, out, errOut)
	}
}

func TestParityAffectedCommands(t *testing.T) {
	gitRepo(t)
	c0 := commit(t, "a.txt", 1_700_000_000)
	c1 := commit(t, "README.md", 1_700_000_100)
	commit(t, "internal/cli/exec.go", 1_700_000_200)

	full := "commands=all\nrun=true\n"
	unknown := "unknown base → running the full matrix\n"
	cases := []struct {
		name           string
		args           []string
		stdout, stderr string
	}{
		{"no base", nil, full, unknown},
		{"base that is no commit", []string{"0123456789abcdef0123456789abcdef01234567"}, full, unknown},
		{"base against HEAD", []string{c0}, "commands=exec\nrun=true\n",
			"changed files:\nREADME.md\ninternal/cli/exec.go\naffected parity commands: exec\n"},
		{"base against head", []string{c0, c1}, "commands=none\nrun=false\n",
			"changed files:\nREADME.md\naffected parity commands: none\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := runMain(t, "", append([]string{"parity-affected-commands"}, tc.args...)...)
			if code != 0 || out != tc.stdout || errOut != tc.stderr {
				t.Errorf("code=%d\nstdout=%q, want %q\nstderr=%q, want %q", code, out, tc.stdout, errOut, tc.stderr)
			}
		})
	}
}

func TestDailyChangedCommand(t *testing.T) {
	gitRepo(t)
	const last = 1_700_000_000
	commit(t, "a.txt", last)

	cases := []struct {
		name           string
		now            string
		args           []string
		stdout, stderr string
	}{
		{"inside the default window", strconv.Itoa(last + 2*3600), nil, "run=true\n",
			"HEAD commit is 2h old (window 25h)\n"},
		{"at the default window", strconv.Itoa(last + 25*3600), nil, "run=false\n",
			"HEAD commit is 25h old (window 25h)\nno commits within the last 25h — skipping the full runtime matrix\n"},
		{"a window of its own", strconv.Itoa(last + 3*3600 + 3599), []string{"4"}, "run=true\n",
			"HEAD commit is 3h old (window 4h)\n"},
		{"past a window of its own", strconv.Itoa(last + 4*3600), []string{"4"}, "run=false\n",
			"HEAD commit is 4h old (window 4h)\nno commits within the last 4h — skipping the full runtime matrix\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DAILY_NOW_EPOCH", tc.now)
			code, out, errOut := runMain(t, "", append([]string{"daily-changed"}, tc.args...)...)
			if code != 0 || out != tc.stdout || errOut != tc.stderr {
				t.Errorf("code=%d\nstdout=%q, want %q\nstderr=%q, want %q", code, out, tc.stdout, errOut, tc.stderr)
			}
		})
	}

	t.Run("a window that is no number", func(t *testing.T) {
		code, out, errOut := runMain(t, "", "daily-changed", "x")
		want := "devtool daily-changed: window-hours \"x\": strconv.Atoi: parsing \"x\": invalid syntax\n"
		if code != 1 || out != "" || errOut != want {
			t.Errorf("code=%d stdout=%q stderr=%q, want 1 \"\" %q", code, out, errOut, want)
		}
	})

	t.Run("wall clock", func(t *testing.T) {
		t.Setenv("DAILY_NOW_EPOCH", "")
		commit(t, "b.txt", time.Now().Unix())
		if code, out, _ := runMain(t, "", "daily-changed"); code != 0 || out != "run=true\n" {
			t.Errorf("a commit made now: code=%d stdout=%q, want run=true", code, out)
		}
	})
}

const sampleReport = "| Package | Stmts | Coverage |\n|---|---:|---:|\n" +
	"| internal/cli | 8 | 62.5% |\n" +
	"| internal/docker | 6 | 100.0% |\n" +
	"| **TOTAL** | **14** | **78.6%** |\n"

// fakeGo stands in for `go tool covdata`: textfmt writes FAKE_PROFILE to its -o
// file, and merge leaves a file in its -o directory.
func fakeGo(t *testing.T) (log string) {
	t.Helper()
	t.Setenv("FAKE_PROFILE", sampleProfile)
	return fakeTools(t, map[string]string{"go": `for a in "$@"; do case "$a" in -o=*) out="${a#-o=}";; esac; done
case "$3" in
textfmt) printf '%s' "$FAKE_PROFILE" > "$out" ;;
merge) echo merged > "$out/covmeta" ;;
esac`})
}

func TestCoverageReport(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := t.TempDir()
	profile := filepath.Join(dir, "coverage.out")
	writeFile(t, profile, sampleProfile)
	empty := filepath.Join(dir, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	covdata := filepath.Join(dir, "e2e")
	writeFile(t, filepath.Join(covdata, "covmeta"), "x")
	log := fakeGo(t)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"profile", []string{profile}, "### Coverage — coverage (total 78.6%)\n\n" + sampleReport},
		{"profile with a title", []string{profile, "unit (hermetic)"}, "### Coverage — unit (hermetic) (total 78.6%)\n\n" + sampleReport},
		{"missing profile", []string{filepath.Join(dir, "none.out"), "e2e"}, "### Coverage — e2e\n\n_(no coverage data)_\n"},
		{"empty covdata", []string{empty, "e2e"}, "### Coverage — e2e\n\n_(no coverage data)_\n"},
		{"covdata", []string{covdata + "/", "e2e"}, "### Coverage — e2e (total 78.6%)\n\n" + sampleReport},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := runMain(t, "", append([]string{"coverage-report"}, tc.args...)...)
			if code != 0 || out != tc.want || errOut != "" {
				t.Errorf("code=%d stderr=%q\nstdout=%q\nwant   %q", code, errOut, out, tc.want)
			}
		})
	}
	want := "go tool covdata textfmt -i=" + covdata + "/ -o=" + covdata + ".out\n"
	if got := readFile(t, log); got != want {
		t.Errorf("go calls = %q, want %q", got, want)
	}
}

func TestCoverageGateCommand(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "coverage.out")
	writeFile(t, profile, sampleProfile)

	code, out, errOut := runMain(t, "", "coverage-gate", profile, "TOTAL=70")
	if code != 0 || out != "OK:   TOTAL coverage 78.6% >= 70% floor\n" || errOut != "" {
		t.Errorf("passing gate: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	code, out, errOut = runMain(t, "", "coverage-gate", profile, "TOTAL=70", "cli=70")
	want := "OK:   TOTAL coverage 78.6% >= 70% floor\nFAIL: cli coverage 62.5% is below the 70% floor\n"
	if code != 1 || out != want || errOut != "" {
		t.Errorf("failing gate: code=%d stdout=%q stderr=%q, want 1 %q \"\"", code, out, errOut, want)
	}
	code, _, errOut = runMain(t, "", "coverage-gate", profile)
	if code != 1 || errOut != "devtool coverage-gate: usage: coverage-gate <profile.out> TOTAL=<min> [pkg=min ...]\n" {
		t.Errorf("no floor: code=%d stderr=%q", code, errOut)
	}
}

func TestCoverageMerge(t *testing.T) {
	// An edit that took the title for a directory would leave it in the package.
	t.Chdir(t.TempDir())
	t.Run("lanes into the default directory", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "a", "covmeta"), "a")
		writeFile(t, filepath.Join(root, "b", "covmeta"), "b")
		writeFile(t, filepath.Join(root, "notes.txt"), "not a lane")
		writeFile(t, filepath.Join(root, "merged", "stale"), "the last merge")
		if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
			t.Fatal(err)
		}
		log := fakeGo(t)

		code, out, errOut := runMain(t, "", "coverage-merge", root+"/")
		merged := filepath.Join(root, "merged")
		lanes := filepath.Join(root, "a") + "," + filepath.Join(root, "b")
		want := "### Coverage — merged (all lanes) (total 78.6%)\n\n" + sampleReport
		if code != 0 || out != want || errOut != "merging coverage lanes: "+lanes+"\n" {
			t.Errorf("code=%d stderr=%q\nstdout=%q\nwant   %q", code, errOut, out, want)
		}
		wantCalls := "go tool covdata merge -i=" + lanes + " -o=" + merged + "\n" +
			"go tool covdata textfmt -i=" + merged + " -o=" + merged + ".out\n"
		if got := readFile(t, log); got != wantCalls {
			t.Errorf("go calls = %q, want %q", got, wantCalls)
		}
		if _, err := os.Stat(filepath.Join(merged, "stale")); !os.IsNotExist(err) {
			t.Errorf("the last merge's file survived: %v", err)
		}
		assertMode(t, merged, 0o755)
	})

	t.Run("into a directory and title of its own", func(t *testing.T) {
		root, out := t.TempDir(), filepath.Join(t.TempDir(), "all")
		writeFile(t, filepath.Join(root, "a", "covmeta"), "a")
		log := fakeGo(t)

		code, stdout, _ := runMain(t, "", "coverage-merge", root, out, "lanes")
		if want := "### Coverage — lanes (total 78.6%)\n\n" + sampleReport; code != 0 || stdout != want {
			t.Errorf("code=%d stdout=%q, want %q", code, stdout, want)
		}
		if got := readFile(t, log); !strings.HasPrefix(got, "go tool covdata merge -i="+filepath.Join(root, "a")+" -o="+out+"\n") {
			t.Errorf("go calls = %q", got)
		}
	})

	t.Run("no lane but the output directory", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "out", "covmeta"), "the last merge")
		code, out, errOut := runMain(t, "", "coverage-merge", root, filepath.Join(root, "out"))
		if want := "### Coverage — merged (all lanes)\n\n_(no lane data to merge)_\n"; code != 0 || out != want || errOut != "" {
			t.Errorf("code=%d stdout=%q stderr=%q, want %q", code, out, errOut, want)
		}
	})

	if code, _, errOut := runMain(t, "", "coverage-merge"); code != 1 || errOut != "devtool coverage-merge: usage: coverage-merge <data-root> [out-dir] [title]\n" {
		t.Errorf("no root: code=%d stderr=%q", code, errOut)
	}
}

// runnerTools fakes what preparing a runner starts: sudo (tee echoes its stdin,
// as the real one does), docker and the shell printing the disk.
func runnerTools(t *testing.T) (log string) {
	t.Helper()
	return fakeTools(t, map[string]string{
		"sudo":   `if [ "$1" = tee ]; then cat; echo tee-stderr >&2; fi`,
		"docker": `echo "driver=[fake]"`,
		"sh":     `echo "/dev/fake 100G 10G 90G 10% /"`,
	})
}

const daemonJSON = `{"features":{"containerd-snapshotter":true}}`

const runnerTail = "sudo tee /etc/docker/daemon.json\n" +
	"sudo systemctl restart docker\n" +
	"docker info -f driver={{.DriverStatus}}\n"

func TestPrepareRunner(t *testing.T) {
	t.Run("short of room", func(t *testing.T) {
		log := runnerTools(t)
		var out, errOut strings.Builder
		if err := (tool{out: &out, errw: &errOut}).prepareRunner(RunnerDiskRoom - 1); err != nil {
			t.Fatal(err)
		}
		wantCalls := "sudo rm -rf /usr/share/dotnet /opt/ghc /usr/local/lib/android /opt/hostedtoolcache/CodeQL /usr/share/swift /usr/local/share/boost /usr/lib/jvm\n" +
			"sudo docker image prune -af\n" +
			"sh -c df -h / | tail -1\n" + runnerTail
		if got := readFile(t, log); got != wantCalls {
			t.Errorf("calls = %q\nwant    %q", got, wantCalls)
		}
		want := "disk after:\n/dev/fake 100G 10G 90G 10% /\n" + daemonJSON + "driver=[fake]\n"
		if out.String() != want || errOut.String() != "tee-stderr\n" {
			t.Errorf("stdout=%q, want %q\nstderr=%q", out.String(), want, errOut.String())
		}
	})

	t.Run("with room", func(t *testing.T) {
		log := runnerTools(t)
		var out, errOut strings.Builder
		if err := (tool{out: &out, errw: &errOut}).prepareRunner(RunnerDiskRoom); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, log); got != runnerTail {
			t.Errorf("calls = %q, want %q", got, runnerTail)
		}
		want := "60 GB free: the runner has room, nothing deleted\n" + daemonJSON + "driver=[fake]\n"
		if out.String() != want {
			t.Errorf("stdout=%q, want %q", out.String(), want)
		}
	})

	t.Run("the command", func(t *testing.T) {
		log := runnerTools(t)
		code, out, _ := runMain(t, "", "ci-prepare-runner")
		if code != 0 || !strings.HasPrefix(out, "disk before:\n/dev/fake 100G 10G 90G 10% /\n") {
			t.Errorf("code=%d stdout=%q", code, out)
		}
		if got := readFile(t, log); !strings.HasPrefix(got, "sh -c df -h / | tail -1\n") || !strings.HasSuffix(got, runnerTail) {
			t.Errorf("calls = %q", got)
		}
	})
}

func TestDockerCacheExport(t *testing.T) {
	log := fakeTools(t, map[string]string{"docker": `cat "$6/Dockerfile" >> "$FAKE_LOG"
[ "$3" = "--cache-to=type=local,dest=$6/c" ] || exit 9
echo noise; echo noise >&2
exit "$FAKE_EXIT"`})
	t.Setenv("FAKE_LOG", filepath.Join(t.TempDir(), "dockerfile"))

	t.Setenv("FAKE_EXIT", "0")
	if code, out, errOut := runMain(t, "", "docker-cache-export"); code != 0 || out != "" || errOut != "" {
		t.Errorf("a builder that exports: code=%d stdout=%q stderr=%q, want 0 and silence", code, out, errOut)
	}
	t.Setenv("FAKE_EXIT", "1")
	if code, out, errOut := runMain(t, "", "docker-cache-export"); code != 1 || out != "" || errOut != "" {
		t.Errorf("a builder that cannot: code=%d stdout=%q stderr=%q, want 1 and silence", code, out, errOut)
	}
	call := readFile(t, log)
	if !strings.HasPrefix(call, "docker buildx build --cache-to=type=local,dest=") || !strings.Contains(call, " -t devcontainer-cache-export-probe ") {
		t.Errorf("docker call = %q", call)
	}
	if got := readFile(t, os.Getenv("FAKE_LOG")); got != "FROM busybox\nFROM busybox\n" {
		t.Errorf("Dockerfiles built = %q", got)
	}
}

func TestReleaseResolve(t *testing.T) {
	today := time.Now().UTC().Format("20060102")
	gitRepo(t)
	head := commit(t, "a.txt", 1_700_000_000)
	git(t, "tag", "v"+today+".01")
	git(t, "tag", "v"+today+".02")
	for _, k := range []string{"GITHUB_EVENT_NAME", "VERSION_INPUT", "GITHUB_REF_NAME", "GITHUB_SHA", "GITHUB_OUTPUT", "GITHUB_STEP_SUMMARY"} {
		t.Setenv(k, "")
	}

	t.Run("outside CI: the day's next CalVer to stdout", func(t *testing.T) {
		code, out, errOut := runMain(t, "", "release-resolve")
		if now := time.Now().UTC().Format("20060102"); now != today {
			t.Skip("the day changed during the test")
		}
		v := today + ".03"
		want := "version=" + v + "\ntag=v" + v + "\nsha=" + head + "\nReleasing v" + v + " (version " + v + ") from " + head + "\n"
		if code != 0 || out != want || errOut != "" {
			t.Errorf("code=%d stderr=%q\nstdout=%q\nwant   %q", code, errOut, out, want)
		}
	})

	t.Run("dispatch: its input to the workflow's files", func(t *testing.T) {
		dir := t.TempDir()
		output, summary := filepath.Join(dir, "output"), filepath.Join(dir, "summary")
		writeFile(t, output, "earlier=step\n")
		t.Setenv("GITHUB_OUTPUT", output)
		t.Setenv("GITHUB_STEP_SUMMARY", summary)
		t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
		t.Setenv("VERSION_INPUT", "v1.2.3")
		if code, out, errOut := runMain(t, "", "release-resolve"); code != 0 || out != "" || errOut != "" {
			t.Errorf("code=%d stdout=%q stderr=%q", code, out, errOut)
		}
		if got, want := readFile(t, output), "earlier=step\nversion=1.2.3\ntag=v1.2.3\nsha="+head+"\n"; got != want {
			t.Errorf("GITHUB_OUTPUT = %q, want %q", got, want)
		}
		if got, want := readFile(t, summary), "Releasing v1.2.3 (version 1.2.3) from "+head+"\n"; got != want {
			t.Errorf("GITHUB_STEP_SUMMARY = %q, want %q", got, want)
		}
		assertMode(t, summary, 0o644)
	})

	t.Run("dispatch of a released tag", func(t *testing.T) {
		t.Setenv("VERSION_INPUT", today+".01")
		code, out, errOut := runMain(t, "", "release-resolve")
		want := "::error::tag already exists: v" + today + ".01 — releases are immutable, bump the build number\n"
		if code != 1 || out != "" || errOut != want {
			t.Errorf("code=%d stdout=%q stderr=%q, want 1 \"\" %q", code, out, errOut, want)
		}
	})

	t.Run("tag push, with no repository to ask", func(t *testing.T) {
		t.Chdir(t.TempDir())
		t.Setenv("GITHUB_EVENT_NAME", "push")
		t.Setenv("GITHUB_REF_NAME", "v9.9.9")
		t.Setenv("GITHUB_SHA", "pushed")
		code, out, errOut := runMain(t, "", "release-resolve")
		want := "version=9.9.9\ntag=v9.9.9\nsha=pushed\nReleasing v9.9.9 (version 9.9.9) from pushed\n"
		if code != 0 || out != want || errOut != "" {
			t.Errorf("code=%d stdout=%q stderr=%q, want %q", code, out, errOut, want)
		}
	})
}

// metricsTools fakes the binary measured, in its own directory, and node; each
// logs its calls, and the binary's read-configuration logs the config it reads.
func metricsTools(t *testing.T) (bin, log string) {
	t.Helper()
	log = fakeTools(t, map[string]string{"node": ""})
	bin = filepath.Join(t.TempDir(), "devcontainer")
	writeFile(t, bin, "#!/bin/sh\necho \"bin $*\" >> '"+log+"'\n"+
		`[ "$1" = read-configuration ] && cat "$3/.devcontainer/devcontainer.json" >> '`+log+"'\nexit 0\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func readMetrics(t *testing.T, path, stdout string) Metrics {
	t.Helper()
	data := readFile(t, path)
	if !strings.HasSuffix(stdout, "metrics: wrote "+path+"\n"+data) {
		t.Errorf("stdout = %q does not end with what it wrote, %q", stdout, data)
	}
	var m Metrics
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMetrics(t *testing.T) {
	// An edit that swapped its arguments would write the version as a file here.
	t.Chdir(t.TempDir())
	t.Run("everything measured", func(t *testing.T) {
		bin, log := metricsTools(t)
		writeFile(t, filepath.Join(filepath.Dir(bin), "dist", "devcontainer-linux-amd64"), strings.Repeat("a", 1000))
		writeFile(t, filepath.Join(filepath.Dir(bin), "dist", "devcontainer-linux-arm64"), strings.Repeat("ab", 1500))
		oracle := filepath.Join(t.TempDir(), "oracle.js")
		writeFile(t, oracle, "")
		out := filepath.Join(t.TempDir(), "artifacts", "metrics.json")
		t.Setenv("METRICS_RUNS", "2")
		before := time.Now().UTC().Truncate(time.Second)

		code, stdout, errOut := runMain(t, "", "metrics", "v1", bin, oracle, out)
		if code != 0 || errOut != "" {
			t.Fatalf("code=%d stderr=%q", code, errOut)
		}
		if !strings.HasPrefix(stdout, "metrics: timing tool = go, runs=2\nmetrics: wrote ") {
			t.Errorf("stdout = %q", stdout)
		}
		m := readMetrics(t, out, stdout)
		at, err := time.Parse(time.RFC3339, m.GeneratedAt)
		if err != nil || at.Before(before) || at.After(time.Now()) {
			t.Errorf("generated_at = %q (%v), want the time of the run", m.GeneratedAt, err)
		}
		if m.Version != "v1" || m.TimingTool != "go" || m.Runs != 2 {
			t.Errorf("version=%q timing_tool=%q runs=%d", m.Version, m.TimingTool, m.Runs)
		}
		if m.StartupMs.GoVersion == nil || m.StartupMs.GoReadConfiguration == nil || m.StartupMs.NodeVersion == nil {
			t.Errorf("startup_ms = %+v, want every command timed", m.StartupMs)
		}
		s := m.SizesBytes
		dist := filepath.Join(filepath.Dir(bin), "dist", "devcontainer")
		for name, got := range map[string]*int64{
			"local": s.LocalBinary, "amd64": s.LinuxAmd64Binary, "amd64 gzip": s.LinuxAmd64Gzip,
			"arm64": s.LinuxArm64Binary, "arm64 gzip": s.LinuxArm64Gzip,
		} {
			want := map[string]*int64{
				"local": FileSize(bin), "amd64": new(int64(1000)), "amd64 gzip": GzipSize(dist + "-linux-amd64"),
				"arm64": new(int64(3000)), "arm64 gzip": GzipSize(dist + "-linux-arm64"),
			}[name]
			if got == nil || *got != *want {
				t.Errorf("%s size = %v, want %d", name, got, *want)
			}
		}
		assertMode(t, out, 0o644)
		assertMode(t, filepath.Dir(out), 0o755)

		calls := readFile(t, log)
		config := `{"image":"mcr.microsoft.com/devcontainers/base:ubuntu"}` + "\n"
		if n := strings.Count(calls, "bin --version\n"); n != 3 {
			t.Errorf("--version ran %d times, want a warm-up and 2 runs", n)
		}
		if n := strings.Count(calls, config); n != 3 || strings.Count(calls, "bin read-configuration --workspace-folder ") != 3 {
			t.Errorf("read-configuration ran %d times with the config, want 3:\n%s", n, calls)
		}
		if n := strings.Count(calls, "node "+oracle+" --version\n"); n != 3 {
			t.Errorf("the oracle ran %d times, want 3", n)
		}
	})

	t.Run("by default, without oracle or release binaries", func(t *testing.T) {
		bin, log := metricsTools(t)
		oracle := filepath.Join(t.TempDir(), "absent.js")
		out := filepath.Join(t.TempDir(), "metrics.json")
		t.Setenv("METRICS_RUNS", "")

		code, stdout, _ := runMain(t, "", "metrics", "dev", bin, oracle, out)
		want := "metrics: timing tool = go, runs=30\n" +
			"metrics: node oracle not found at " + oracle + " (run 'task reference') — skipping Node A/B\n" +
			"metrics: dist binaries absent — run 'task build:cross' for release sizes\n"
		if code != 0 || !strings.HasPrefix(stdout, want) {
			t.Errorf("code=%d stdout=%q, want it to start with %q", code, stdout, want)
		}
		m := readMetrics(t, out, stdout)
		if m.Runs != 30 || m.StartupMs.NodeVersion != nil || m.SizesBytes.LocalBinary == nil || m.SizesBytes.LinuxAmd64Binary != nil {
			t.Errorf("runs=%d startup=%+v sizes=%+v", m.Runs, m.StartupMs, m.SizesBytes)
		}
		if n := strings.Count(readFile(t, log), "bin --version\n"); n != 31 {
			t.Errorf("--version ran %d times, want 31", n)
		}
	})

	if code, _, errOut := runMain(t, "", "metrics", "v1"); code != 1 || errOut != "devtool metrics: usage: devtool metrics <version> <binary> <oracle.js> <out.json>\n" {
		t.Errorf("one argument: code=%d stderr=%q", code, errOut)
	}
}
