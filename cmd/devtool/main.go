// Command devtool is the repo's CI/dev helper, replacing the former scripts/*.sh.
// Each subcommand wires stdin/stdout, git, `go tool`, and docker around the pure,
// unit-tested logic in internal/devtools. Invoked from the Taskfile and the CI
// workflow (e.g. `go run ./cmd/devtool coverage-gate coverage.out TOTAL=48`).
package main

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

	"github.com/devcontainers/cli/internal/devtools"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: devtool <command> [args...]")
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "parity-affected":
		err = parityAffected()
	case "parity-affected-commands":
		err = parityAffectedCommands(args)
	case "daily-changed":
		err = dailyChanged(args)
	case "coverage-report":
		err = coverageReport(args)
	case "coverage-gate":
		err = coverageGate(args)
	case "coverage-merge":
		err = coverageMerge(args)
	case "ci-prepare-runner":
		err = ciPrepareRunner()
	case "docker-cache-export":
		err = dockerCacheExport()
	case "release-resolve":
		err = releaseResolve()
	case "metrics":
		err = metrics(args)
	default:
		fmt.Fprintf(os.Stderr, "devtool: unknown command %q\n", cmd)
		os.Exit(2)
	}
	if err != nil {
		// exitError carries a specific code (gate failures, probe failures); other
		// errors are usage/infra problems.
		if ee, ok := err.(exitError); ok {
			if ee.msg != "" {
				fmt.Fprintln(os.Stderr, ee.msg)
			}
			os.Exit(ee.code)
		}
		fmt.Fprintf(os.Stderr, "devtool %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

type exitError struct {
	code int
	msg  string
}

func (e exitError) Error() string { return e.msg }

// --- parity selection ------------------------------------------------------

func parityAffected() error {
	files, err := readLines(os.Stdin)
	if err != nil {
		return err
	}
	fmt.Println(devtools.ParityAffected(files))
	return nil
}

func parityAffectedCommands(args []string) error {
	base := ""
	if len(args) > 0 {
		base = args[0]
	}
	head := "HEAD"
	if len(args) > 1 {
		head = args[1]
	}

	if base == "" || !gitCommitExists(base) {
		fmt.Fprintln(os.Stderr, "unknown base → running the full matrix")
		fmt.Println("commands=all")
		fmt.Println("run=true")
		return nil
	}

	out, err := runOut("git", "diff", "--name-only", base, head)
	if err != nil {
		return fmt.Errorf("git diff: %w", err)
	}
	fmt.Fprintln(os.Stderr, "changed files:")
	fmt.Fprintln(os.Stderr, out)

	cmds := devtools.ParityAffected(strings.Split(out, "\n"))
	fmt.Fprintf(os.Stderr, "affected parity commands: %s\n", cmds)

	fmt.Printf("commands=%s\n", cmds)
	if cmds == "none" {
		fmt.Println("run=false")
	} else {
		fmt.Println("run=true")
	}
	return nil
}

func dailyChanged(args []string) error {
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

	ageHours := (now - last) / 3600
	if devtools.DailyChanged(last, now, window) {
		fmt.Fprintf(os.Stderr, "HEAD commit is %dh old (window %dh)\n", ageHours, window)
		fmt.Println("run=true")
	} else {
		fmt.Fprintf(os.Stderr, "HEAD commit is %dh old (window %dh)\n", ageHours, window)
		fmt.Println("run=false")
		fmt.Fprintf(os.Stderr, "no commits within the last %dh — skipping the full runtime matrix\n", window)
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

func coverageReport(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: coverage-report <profile|covdata-dir> [title]")
	}
	src := args[0]
	title := "coverage"
	if len(args) > 1 {
		title = args[1]
	}
	report, err := renderCoverage(src, title)
	if err != nil {
		return err
	}
	fmt.Print(report)
	return nil
}

// renderCoverage renders a profile file or a covdata directory (converted with
// `go tool covdata textfmt`) to a Markdown table.
func renderCoverage(src, title string) (string, error) {
	profile := src
	if info, err := os.Stat(src); err == nil && info.IsDir() {
		if empty, _ := dirEmpty(src); empty {
			return fmt.Sprintf("### Coverage — %s\n\n_(no coverage data)_\n", title), nil
		}
		profile = strings.TrimRight(src, "/") + ".out"
		if err := run("go", "tool", "covdata", "textfmt", "-i="+src, "-o="+profile); err != nil {
			return "", fmt.Errorf("covdata textfmt: %w", err)
		}
	}
	f, err := os.Open(profile)
	if err != nil {
		return fmt.Sprintf("### Coverage — %s\n\n_(no coverage data)_\n", title), nil
	}
	defer f.Close()
	pkgs, total, err := devtools.ParseProfile(f)
	if err != nil {
		return "", err
	}
	return devtools.RenderReport(pkgs, total, title), nil
}

func coverageGate(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: coverage-gate <profile.out> TOTAL=<min> [pkg=min ...]")
	}
	f, err := os.Open(args[0])
	if err != nil {
		return fmt.Errorf("open profile: %w", err)
	}
	defer f.Close()
	pkgs, total, err := devtools.ParseProfile(f)
	if err != nil {
		return err
	}
	floors, err := devtools.ParseFloors(args[1:])
	if err != nil {
		return err
	}
	results, ok := devtools.CoverageGate(pkgs, total, floors)
	for _, r := range results {
		fmt.Println(r.Line)
	}
	if !ok {
		return exitError{code: 1}
	}
	return nil
}

func coverageMerge(args []string) error {
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
		fmt.Printf("### Coverage — %s\n\n_(no lane data to merge)_\n", title)
		return nil
	}
	fmt.Fprintf(os.Stderr, "merging coverage lanes: %s\n", strings.Join(inputs, ","))

	if err := os.RemoveAll(out); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	if err := run("go", "tool", "covdata", "merge", "-i="+strings.Join(inputs, ","), "-o="+out); err != nil {
		return fmt.Errorf("covdata merge: %w", err)
	}
	report, err := renderCoverage(out, title)
	if err != nil {
		return err
	}
	fmt.Print(report)
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

func ciPrepareRunner() error {
	printDisk("disk before:")
	free, err := devtools.FreeDisk("/")
	if err != nil {
		return fmt.Errorf("free disk: %w", err)
	}
	if devtools.NeedsDiskFreeing(free) {
		// Free ~25-30GB of preinstalled SDKs; ignore failures (|| true in the script).
		bloat := []string{
			"/usr/share/dotnet", "/opt/ghc", "/usr/local/lib/android",
			"/opt/hostedtoolcache/CodeQL", "/usr/share/swift",
			"/usr/local/share/boost", "/usr/lib/jvm",
		}
		_ = run("sudo", append([]string{"rm", "-rf"}, bloat...)...)
		_ = run("sudo", "docker", "image", "prune", "-af")
		printDisk("disk after:")
	} else {
		fmt.Printf("%d GB free: the runner has room, nothing deleted\n", free>>30)
	}

	// Enable the containerd image store (build-cache export + --platform).
	tee := exec.Command("sudo", "tee", "/etc/docker/daemon.json")
	tee.Stdin = strings.NewReader(`{"features":{"containerd-snapshotter":true}}`)
	tee.Stdout, tee.Stderr = os.Stdout, os.Stderr
	if err := tee.Run(); err != nil {
		return fmt.Errorf("write daemon.json: %w", err)
	}
	if err := run("sudo", "systemctl", "restart", "docker"); err != nil {
		return fmt.Errorf("restart docker: %w", err)
	}
	return run("docker", "info", "-f", "driver={{.DriverStatus}}")
}

func dockerCacheExport() error {
	d, err := os.MkdirTemp("", "devcontainer-cache-export")
	if err != nil {
		return err
	}
	defer os.RemoveAll(d)
	if err := os.WriteFile(filepath.Join(d, "Dockerfile"), []byte("FROM busybox\n"), 0o644); err != nil {
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

func readLines(r io.Reader) ([]string, error) {
	var lines []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func runOut(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

func gitCommitExists(ref string) bool {
	return exec.Command("git", "cat-file", "-e", ref+"^{commit}").Run() == nil
}

func printDisk(label string) {
	fmt.Println(label)
	_ = run("sh", "-c", "df -h / | tail -1")
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
func releaseResolve() error {
	in := devtools.ReleaseInput{
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
	rel, err := devtools.ResolveRelease(in)
	if err != nil {
		return exitError{code: 1, msg: "::error::" + err.Error()}
	}
	if err := appendTo("GITHUB_OUTPUT", fmt.Sprintf("version=%s\ntag=%s\nsha=%s\n", rel.Version, rel.Tag, rel.SHA)); err != nil {
		return err
	}
	return appendTo("GITHUB_STEP_SUMMARY", fmt.Sprintf("Releasing %s (version %s) from %s\n", rel.Tag, rel.Version, rel.SHA))
}

// appendTo appends s to the file the environment variable names, or writes it to
// stdout outside a workflow.
func appendTo(env, s string) error {
	path := os.Getenv(env)
	if path == "" {
		_, err := fmt.Print(s)
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
func metrics(args []string) error {
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
	fmt.Printf("metrics: timing tool = go, runs=%d\n", runs)
	m := devtools.Metrics{
		Version:     version,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		TimingTool:  "go",
		Runs:        runs,
	}
	m.StartupMs.GoVersion = new(devtools.MeanMillis(runs, quiet(bin, "--version")))

	// read-configuration against a minimal image-based workspace: variable
	// substitution only, no Docker involved.
	ws, err := os.MkdirTemp("", "devcontainer-metrics-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(ws)
	cfg := filepath.Join(ws, ".devcontainer", "devcontainer.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(cfg, []byte(`{"image":"mcr.microsoft.com/devcontainers/base:ubuntu"}`+"\n"), 0o644); err != nil {
		return err
	}
	m.StartupMs.GoReadConfiguration = new(devtools.MeanMillis(runs, quiet(bin, "read-configuration", "--workspace-folder", ws)))

	if _, err := os.Stat(oracle); err == nil {
		if _, err := exec.LookPath("node"); err == nil {
			m.StartupMs.NodeVersion = new(devtools.MeanMillis(runs, quiet("node", oracle, "--version")))
		}
	}
	if m.StartupMs.NodeVersion == nil {
		fmt.Printf("metrics: node oracle not found at %s (run 'task reference') — skipping Node A/B\n", oracle)
	}

	dist := filepath.Join(filepath.Dir(bin), "dist", filepath.Base(bin))
	m.SizesBytes = devtools.SizesBytes{
		LocalBinary:      devtools.FileSize(bin),
		LinuxAmd64Binary: devtools.FileSize(dist + "-linux-amd64"),
		LinuxAmd64Gzip:   devtools.GzipSize(dist + "-linux-amd64"),
		LinuxArm64Binary: devtools.FileSize(dist + "-linux-arm64"),
		LinuxArm64Gzip:   devtools.GzipSize(dist + "-linux-arm64"),
	}
	if m.SizesBytes.LinuxAmd64Binary == nil {
		fmt.Println("metrics: dist binaries absent — run 'task build:cross' for release sizes")
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
	fmt.Printf("metrics: wrote %s\n%s\n", out, data)
	return nil
}
