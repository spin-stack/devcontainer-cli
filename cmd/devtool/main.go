// Command devtool is the repo's CI/dev helper, replacing the former scripts/*.sh.
// Each subcommand wires stdin/stdout, git, `go tool`, and docker around the pure,
// unit-tested logic in internal/devtools. Invoked from the Taskfile and the CI
// workflow (e.g. `go run ./cmd/devtool coverage-gate coverage.out TOTAL=48`).
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

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
	out, err := runOut("date", "+%s")
	if err != nil {
		return 0, fmt.Errorf("date: %w", err)
	}
	return strconv.Atoi(strings.TrimSpace(out))
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
	// Free ~25-30GB of preinstalled SDKs; ignore failures (|| true in the script).
	bloat := []string{
		"/usr/share/dotnet", "/opt/ghc", "/usr/local/lib/android",
		"/opt/hostedtoolcache/CodeQL", "/usr/share/swift",
		"/usr/local/share/boost", "/usr/lib/jvm",
	}
	_ = run("sudo", append([]string{"rm", "-rf"}, bloat...)...)
	_ = run("sudo", "docker", "image", "prune", "-af")
	printDisk("disk after:")

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
