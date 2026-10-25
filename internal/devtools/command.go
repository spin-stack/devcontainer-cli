package devtools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Main runs the devtool command args names (args[0] is the program, args[1] the
// command) and returns the process's exit code: 2 for a usage error, the code a
// gate or a probe chose, 1 for any other failure. Each command wires stdin,
// stdout, git, `go tool` and docker around the pure logic of this package; the
// children a command starts write to stdout and stderr too.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "usage: devtool <command> [args...]")
		return 2
	}
	t := tool{in: stdin, out: stdout, errw: stderr}
	cmd, rest := args[1], args[2:]
	var err error
	switch cmd {
	case "parity-affected":
		err = t.parityAffected()
	case "parity-affected-commands":
		err = t.parityAffectedCommands(rest)
	case "daily-changed":
		err = t.dailyChanged(rest)
	case "coverage-report":
		err = t.coverageReport(rest)
	case "coverage-gate":
		err = t.coverageGate(rest)
	case "coverage-merge":
		err = t.coverageMerge(rest)
	case "ci-prepare-runner":
		err = t.ciPrepareRunner()
	case "docker-cache-export":
		err = t.dockerCacheExport()
	case "release-resolve":
		err = t.releaseResolve()
	case "metrics":
		err = t.metrics(rest)
	default:
		fmt.Fprintf(stderr, "devtool: unknown command %q\n", cmd)
		return 2
	}
	if err == nil {
		return 0
	}
	// exitError carries a specific code (gate failures, probe failures); other
	// errors are usage/infra problems.
	if ee, ok := err.(exitError); ok {
		if ee.msg != "" {
			fmt.Fprintln(stderr, ee.msg)
		}
		return ee.code
	}
	fmt.Fprintf(stderr, "devtool %s: %v\n", cmd, err)
	return 1
}

type exitError struct {
	code int
	msg  string
}

func (e exitError) Error() string { return e.msg }

// tool is where a command reads its input and writes its output.
type tool struct {
	in        io.Reader
	out, errw io.Writer
}

// --- parity selection ------------------------------------------------------

func (t tool) parityAffected() error {
	files, err := readLines(t.in)
	if err != nil {
		return err
	}
	fmt.Fprintln(t.out, ParityAffected(files))
	return nil
}

func (t tool) parityAffectedCommands(args []string) error {
	base := ""
	if len(args) > 0 {
		base = args[0]
	}
	head := "HEAD"
	if len(args) > 1 {
		head = args[1]
	}

	if base == "" || !gitCommitExists(base) {
		fmt.Fprintln(t.errw, "unknown base → running the full matrix")
		fmt.Fprintln(t.out, "commands=all")
		fmt.Fprintln(t.out, "run=true")
		return nil
	}

	out, err := runOut("git", "diff", "--name-only", base, head)
	if err != nil {
		return fmt.Errorf("git diff: %w", err)
	}
	fmt.Fprintln(t.errw, "changed files:")
	fmt.Fprintln(t.errw, out)

	cmds := ParityAffected(strings.Split(out, "\n"))
	fmt.Fprintf(t.errw, "affected parity commands: %s\n", cmds)

	fmt.Fprintf(t.out, "commands=%s\n", cmds)
	if cmds == "none" {
		fmt.Fprintln(t.out, "run=false")
	} else {
		fmt.Fprintln(t.out, "run=true")
	}
	return nil
}

func (t tool) dailyChanged(args []string) error {
	window := 25
	if len(args) > 0 {
		w, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("window-hours %q: %w", args[0], err)
		}
		window = w
	}

	now, err := nowEpoch()
	if err != nil {
		return err
	}
	lastStr, err := runOut("git", "log", "-1", "--format=%ct")
	if err != nil {
		return fmt.Errorf("git log: %w", err)
	}
	last, err := strconv.Atoi(strings.TrimSpace(lastStr))
	if err != nil {
		return fmt.Errorf("commit timestamp %q: %w", lastStr, err)
	}

	fmt.Fprintf(t.errw, "HEAD commit is %dh old (window %dh)\n", (now-last)/3600, window)
	if DailyChanged(last, now, window) {
		fmt.Fprintln(t.out, "run=true")
	} else {
		fmt.Fprintln(t.out, "run=false")
		fmt.Fprintf(t.errw, "no commits within the last %dh — skipping the full runtime matrix\n", window)
	}
	return nil
}

// nowEpoch honors DAILY_NOW_EPOCH (tests / reproducibility), else wall clock.
func nowEpoch() (int, error) {
	if v := os.Getenv("DAILY_NOW_EPOCH"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("DAILY_NOW_EPOCH %q: %w", v, err)
		}
		return n, nil
	}
	return int(time.Now().Unix()), nil
}

// --- coverage --------------------------------------------------------------

func (t tool) coverageReport(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: coverage-report <profile|covdata-dir> [title]")
	}
	src := args[0]
	title := "coverage"
	if len(args) > 1 {
		title = args[1]
	}
	report, err := t.renderCoverage(src, title)
	if err != nil {
		return err
	}
	fmt.Fprint(t.out, report)
	return nil
}

// renderCoverage renders a profile file or a covdata directory (converted with
// `go tool covdata textfmt`) to a Markdown table.
func (t tool) renderCoverage(src, title string) (string, error) {
	profile := src
	if info, err := os.Stat(src); err == nil && info.IsDir() {
		if empty, _ := dirEmpty(src); empty {
			return fmt.Sprintf("### Coverage — %s\n\n_(no coverage data)_\n", title), nil
		}
		profile = strings.TrimRight(src, "/") + ".out"
		if err := t.run("go", "tool", "covdata", "textfmt", "-i="+src, "-o="+profile); err != nil {
			return "", fmt.Errorf("covdata textfmt: %w", err)
		}
	}
	f, err := os.Open(profile)
	if err != nil {
		return fmt.Sprintf("### Coverage — %s\n\n_(no coverage data)_\n", title), nil
	}
	defer f.Close()
	pkgs, total, err := ParseProfile(f)
	if err != nil {
		return "", err
	}
	return RenderReport(pkgs, total, title), nil
}

func (t tool) coverageGate(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: coverage-gate <profile.out> TOTAL=<min> [pkg=min ...]")
	}
	f, err := os.Open(args[0])
	if err != nil {
		return fmt.Errorf("open profile: %w", err)
	}
	defer f.Close()
	pkgs, total, err := ParseProfile(f)
	if err != nil {
		return err
	}
	floors, err := ParseFloors(args[1:])
	if err != nil {
		return err
	}
	results, ok := CoverageGate(pkgs, total, floors)
	for _, r := range results {
		fmt.Fprintln(t.out, r.Line)
	}
	if !ok {
		return exitError{code: 1}
	}
	return nil
}

func (t tool) coverageMerge(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: coverage-merge <data-root> [out-dir] [title]")
	}
	root := args[0]
	out := strings.TrimRight(root, "/") + "/merged"
	if len(args) > 1 {
		out = args[1]
	}
	title := "merged (all lanes)"
	if len(args) > 2 {
		title = args[2]
	}

	inputs, err := laneDirs(root, out)
	if err != nil {
		return err
	}
	if len(inputs) == 0 {
		fmt.Fprintf(t.out, "### Coverage — %s\n\n_(no lane data to merge)_\n", title)
		return nil
	}
	fmt.Fprintf(t.errw, "merging coverage lanes: %s\n", strings.Join(inputs, ","))

	if err := os.RemoveAll(out); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	if err := t.run("go", "tool", "covdata", "merge", "-i="+strings.Join(inputs, ","), "-o="+out); err != nil {
		return fmt.Errorf("covdata merge: %w", err)
	}
	report, err := t.renderCoverage(out, title)
	if err != nil {
		return err
	}
	fmt.Fprint(t.out, report)
	return nil
}

// laneDirs returns the non-empty per-lane covdata subdirs under root (skipping the
// merge output dir itself).
func laneDirs(root, out string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", root, err)
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		d := filepath.Join(root, e.Name())
		if d == filepath.Clean(out) {
			continue
		}
		if empty, _ := dirEmpty(d); empty {
			continue
		}
		dirs = append(dirs, d)
	}
	return dirs, nil
}

// --- CI runner side effects ------------------------------------------------

func (t tool) ciPrepareRunner() error {
	t.printDisk("disk before:")
	free, err := FreeDisk("/")
	if err != nil {
		return fmt.Errorf("free disk: %w", err)
	}
	return t.prepareRunner(free)
}

// prepareRunner makes a runner with free bytes on / ready for the runtime lanes:
// it deletes the preinstalled SDKs when the runner lacks RunnerDiskRoom, and
// switches docker to the containerd image store.
func (t tool) prepareRunner(free uint64) error {
	if NeedsDiskFreeing(free) {
		// Free ~25-30GB of preinstalled SDKs; ignore failures (|| true in the script).
		bloat := []string{
			"/usr/share/dotnet", "/opt/ghc", "/usr/local/lib/android",
			"/opt/hostedtoolcache/CodeQL", "/usr/share/swift",
			"/usr/local/share/boost", "/usr/lib/jvm",
		}
		_ = t.run("sudo", append([]string{"rm", "-rf"}, bloat...)...)
		_ = t.run("sudo", "docker", "image", "prune", "-af")
		t.printDisk("disk after:")
	} else {
		fmt.Fprintf(t.out, "%d GB free: the runner has room, nothing deleted\n", free>>30)
	}

	// Enable the containerd image store (build-cache export + --platform).
	tee := exec.Command("sudo", "tee", "/etc/docker/daemon.json")
	tee.Stdin = strings.NewReader(`{"features":{"containerd-snapshotter":true}}`)
	tee.Stdout, tee.Stderr = t.out, t.errw
	if err := tee.Run(); err != nil {
		return fmt.Errorf("write daemon.json: %w", err)
	}
	if err := t.run("sudo", "systemctl", "restart", "docker"); err != nil {
		return fmt.Errorf("restart docker: %w", err)
	}
	return t.run("docker", "info", "-f", "driver={{.DriverStatus}}")
}

func (t tool) dockerCacheExport() error {
	d, err := os.MkdirTemp("", "devcontainer-cache-export")
	if err != nil {
		return err
	}
	defer os.RemoveAll(d)
	if err := os.WriteFile(filepath.Join(d, "Dockerfile"), []byte("FROM busybox\n"), 0o644); err != nil { // mutate-exempt: only the build below reads the file, as its owner, and it is removed on return
		return err
	}
	// Exit non-zero (silently) iff the builder can't export cache — the caller uses
	// this as a fast precondition probe.
	cmd := exec.Command("docker", "buildx", "build",
		"--cache-to=type=local,dest="+filepath.Join(d, "c"),
		"-t", "devcontainer-cache-export-probe", d)
	if err := cmd.Run(); err != nil {
		return exitError{code: 1}
	}
	return nil
}

// --- helpers ---------------------------------------------------------------

// readLines splits r into lines. bufio.Scanner's default 64 KiB token limit is
// far beyond any path git prints (PATH_MAX is 4096).
func readLines(r io.Reader) ([]string, error) {
	var lines []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}

func (t tool) run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = t.out, t.errw
	return cmd.Run()
}

func runOut(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

func gitCommitExists(ref string) bool {
	return exec.Command("git", "cat-file", "-e", ref+"^{commit}").Run() == nil
}

func (t tool) printDisk(label string) {
	fmt.Fprintln(t.out, label)
	_ = t.run("sh", "-c", "df -h / | tail -1")
}

func dirEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

// --- release ---------------------------------------------------------------

// releaseResolve writes the version, tag and commit to release to $GITHUB_OUTPUT
// (or stdout), and a line to the job summary. What only the workflow knows comes
// in the environment: GITHUB_EVENT_NAME, VERSION_INPUT, GITHUB_REF_NAME, GITHUB_SHA.
func (t tool) releaseResolve() error {
	in := ReleaseInput{
		Event:        os.Getenv("GITHUB_EVENT_NAME"),
		VersionInput: os.Getenv("VERSION_INPUT"),
		RefName:      os.Getenv("GITHUB_REF_NAME"),
		PushedSHA:    os.Getenv("GITHUB_SHA"),
		Today:        time.Now().UTC().Format("20060102"),
		TagExists: func(tag string) bool {
			return exec.Command("git", "rev-parse", "-q", "--verify", "refs/tags/"+tag).Run() == nil
		},
	}
	if in.Event == "" || in.Event == "workflow_dispatch" {
		tags, err := runOut("git", "tag", "-l", "v"+in.Today+".*")
		if err != nil {
			return fmt.Errorf("list tags: %w", err)
		}
		in.TagsToday = len(strings.Fields(tags))
		if in.HeadSHA, err = runOut("git", "rev-parse", "HEAD"); err != nil {
			return fmt.Errorf("resolve HEAD: %w", err)
		}
	}
	rel, err := ResolveRelease(in)
	if err != nil {
		return exitError{code: 1, msg: "::error::" + err.Error()}
	}
	if err := t.appendTo("GITHUB_OUTPUT", fmt.Sprintf("version=%s\ntag=%s\nsha=%s\n", rel.Version, rel.Tag, rel.SHA)); err != nil {
		return err
	}
	return t.appendTo("GITHUB_STEP_SUMMARY", fmt.Sprintf("Releasing %s (version %s) from %s\n", rel.Tag, rel.Version, rel.SHA))
}

// appendTo appends s to the file the environment variable names, or writes it to
// stdout outside a workflow.
func (t tool) appendTo(env, s string) error {
	path := os.Getenv(env)
	if path == "" {
		_, err := fmt.Fprint(t.out, s)
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", env, err)
	}
	if _, err := f.WriteString(s); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", env, err)
	}
	return f.Close()
}

// --- metrics ---------------------------------------------------------------

// metrics writes artifacts/metrics.json: startup time of the binary against the
// Node oracle (METRICS_RUNS runs, 30 by default) and the size of the binaries.
// Every measurement is best effort: one that cannot be taken is null.
//
//	devtool metrics <version> <binary> <oracle.js> <out.json>
func (t tool) metrics(args []string) error {
	if len(args) != 4 {
		return fmt.Errorf("usage: devtool metrics <version> <binary> <oracle.js> <out.json>")
	}
	version, bin, oracle, out := args[0], args[1], args[2], args[3]
	runs := 30
	if v := os.Getenv("METRICS_RUNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("METRICS_RUNS %q: %w", v, err)
		}
		runs = n
	}
	quiet := func(name string, args ...string) func() {
		return func() { _ = exec.Command(name, args...).Run() }
	}
	fmt.Fprintf(t.out, "metrics: timing tool = go, runs=%d\n", runs)
	m := Metrics{
		Version:     version,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		TimingTool:  "go",
		Runs:        runs,
	}
	m.StartupMs.GoVersion = new(MeanMillis(runs, quiet(bin, "--version")))

	// read-configuration against a minimal image-based workspace: variable
	// substitution only, no Docker involved.
	ws, err := os.MkdirTemp("", "devcontainer-metrics-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(ws)
	cfg := filepath.Join(ws, ".devcontainer", "devcontainer.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil { // mutate-exempt: a scratch workspace only this process's children read, removed on return
		return err
	}
	if err := os.WriteFile(cfg, []byte(`{"image":"mcr.microsoft.com/devcontainers/base:ubuntu"}`+"\n"), 0o644); err != nil { // mutate-exempt: a scratch workspace only this process's children read, removed on return
		return err
	}
	m.StartupMs.GoReadConfiguration = new(MeanMillis(runs, quiet(bin, "read-configuration", "--workspace-folder", ws)))

	if _, err := os.Stat(oracle); err == nil {
		if _, err := exec.LookPath("node"); err == nil {
			m.StartupMs.NodeVersion = new(MeanMillis(runs, quiet("node", oracle, "--version")))
		}
	}
	if m.StartupMs.NodeVersion == nil {
		fmt.Fprintf(t.out, "metrics: node oracle not found at %s (run 'task reference') — skipping Node A/B\n", oracle)
	}

	dist := filepath.Join(filepath.Dir(bin), "dist", filepath.Base(bin))
	m.SizesBytes = SizesBytes{
		LocalBinary:      FileSize(bin),
		LinuxAmd64Binary: FileSize(dist + "-linux-amd64"),
		LinuxAmd64Gzip:   GzipSize(dist + "-linux-amd64"),
		LinuxArm64Binary: FileSize(dist + "-linux-arm64"),
		LinuxArm64Gzip:   GzipSize(dist + "-linux-arm64"),
	}
	if m.SizesBytes.LinuxAmd64Binary == nil {
		fmt.Fprintln(t.out, "metrics: dist binaries absent — run 'task build:cross' for release sizes")
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(t.out, "metrics: wrote %s\n%s\n", out, data)
	return nil
}
