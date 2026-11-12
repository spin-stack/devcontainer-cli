package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type parityMatrix struct {
	InitialCases []parityCase `yaml:"initial_cases"`
}

type parityCase struct {
	ID                 string   `yaml:"id"`
	Lane               string   `yaml:"lane"`
	Command            string   `yaml:"command"`
	Priority           string   `yaml:"priority"`
	DockerRequired     bool     `yaml:"docker_required"`
	NetworkRequired    bool     `yaml:"network_required"`
	TSCmd              string   `yaml:"ts_cmd"`
	SetupCmd           string   `yaml:"setup_cmd"`
	VerifyCmd          string   `yaml:"verify_cmd"`
	CleanupCmd         string   `yaml:"cleanup_cmd"`
	Asserts            []string `yaml:"asserts"`
	Class              string   `yaml:"class"`
	CurrentStatus      string   `yaml:"current_status"`
	Notes              string   `yaml:"notes"`
	NoComposeIsolation bool     `yaml:"no_compose_isolation"`
	Serial             bool     `yaml:"serial"`
	Lock               string   `yaml:"lock"`
	CompareHashes      bool     `yaml:"compare_hashes"`
	CompareNulls       bool     `yaml:"compare_nulls"`
	Arm64Required      bool     `yaml:"arm64_required"`
	ExpectInfraFailure bool     `yaml:"expect_infra_failure"`
	KeyOrder           []string `yaml:"key_order"`
}

type paritySideResult struct {
	Stdout       string
	Stderr       string
	ExitCode     int
	VerifyStdout string
	VerifyStderr string
	VerifyExit   int
}

type parityOutcome string

const (
	parityMatched        parityOutcome = "matched"
	parityFailed         parityOutcome = "failed"
	paritySkippedDocker  parityOutcome = "skipped-docker"
	paritySkippedNetwork parityOutcome = "skipped-network"
	paritySkippedArm64   parityOutcome = "skipped-arm64"
	parityInconclusive   parityOutcome = "inconclusive"
	parityNotSelected    parityOutcome = "not-selected"
)

type parityReport struct {
	mu       sync.Mutex
	outcomes map[string]parityOutcome
}

func newParityReport(cases []parityCase) *parityReport {
	r := &parityReport{outcomes: make(map[string]parityOutcome, len(cases))}
	for _, tc := range cases {
		r.outcomes[tc.ID] = parityNotSelected
	}
	return r
}

func (r *parityReport) record(id string, outcome parityOutcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outcomes[id] = outcome
}

func (r *parityReport) snapshot() map[parityOutcome][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := map[parityOutcome][]string{
		parityMatched: {}, parityFailed: {}, paritySkippedDocker: {},
		paritySkippedNetwork: {}, paritySkippedArm64: {}, parityInconclusive: {}, parityNotSelected: {},
	}
	for id, outcome := range r.outcomes {
		result[outcome] = append(result[outcome], id)
	}
	for outcome := range result {
		sort.Strings(result[outcome])
	}
	return result
}

func formatParityReport(snapshot map[parityOutcome][]string) string {
	order := []parityOutcome{parityMatched, parityFailed, paritySkippedDocker, paritySkippedNetwork, paritySkippedArm64, parityInconclusive, parityNotSelected}
	var b strings.Builder
	b.WriteString("\n=== PARITY MATRIX REPORT ===\n")
	for _, outcome := range order {
		ids := snapshot[outcome]
		fmt.Fprintf(&b, "%-16s %d\n", outcome+":", len(ids))
		if len(ids) > 0 && outcome != parityMatched && outcome != parityNotSelected {
			fmt.Fprintf(&b, "  %s\n", strings.Join(ids, ", "))
		}
	}
	return b.String()
}

func strictParityError(snapshot map[parityOutcome][]string) error {
	ids := snapshot[parityInconclusive]
	if len(ids) == 0 {
		return nil
	}
	return fmt.Errorf("strict parity gate: %d inconclusive case(s): %s", len(ids), strings.Join(ids, ", "))
}

func writeParityReport(path string, snapshot map[parityOutcome][]string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parity report directory: %w", err)
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal parity report: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write parity report: %w", err)
	}
	return nil
}

var parityLocks = map[string]*sync.Mutex{
	"feature-content-image": {},
}

func TestParityMatrix(t *testing.T) {
	repoRoot := findRepoRoot(t)
	matrixPath := filepath.Join(repoRoot, "docs", "parity", "parity-matrix.yaml")

	data, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatalf("read matrix: %v", err)
	}

	var matrix parityMatrix
	if err := yaml.Unmarshal(data, &matrix); err != nil {
		t.Fatalf("parse matrix: %v", err)
	}

	if len(matrix.InitialCases) == 0 {
		t.Fatal("no cases found in matrix")
	}
	report := newParityReport(matrix.InitialCases)
	t.Cleanup(func() {
		snapshot := report.snapshot()
		fmt.Fprint(os.Stdout, formatParityReport(snapshot))
		if err := writeParityReport(os.Getenv("PARITY_REPORT_FILE"), snapshot); err != nil {
			t.Error(err)
		}
		if os.Getenv("PARITY_STRICT") == "true" {
			if err := strictParityError(snapshot); err != nil {
				t.Error(err)
			}
		}
	})

	cliTS := envOr("CLI_TS", defaultParityTSCLI(repoRoot))
	cliGO := envOr("CLI_GO", filepath.Join(repoRoot, "devcontainer"))
	defaultTimeout := 60 * time.Second
	runtimeTimeout := 300 * time.Second
	if v := os.Getenv("PARITY_RUNTIME_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			runtimeTimeout = d
		}
	}

	if _, err := os.Stat(filepath.Join(repoRoot, "reference", "dist", "spec-node", "devContainersSpecCLI.js")); err != nil {
		t.Skip("TS reference not compiled (reference/dist not found). Run: git submodule update --init && task ts:compile")
	}
	if _, err := os.Stat(cliGO); err != nil {
		t.Skip("Go CLI not built. Run: task build")
	}

	dockerAvailable := isDockerAvailable()

	runtimeLane := os.Getenv("PARITY_LANE") != "" && os.Getenv("PARITY_SKIP_DOCKER") != "true"
	if runtimeLane && dockerAvailable && !hasContainerdImageStore() {
		t.Fatal("parity runtime lane requires the containerd image store (docker build --platform against local images). " +
			"Enable it with `task ci:prepare-runner`, or add {\"features\":{\"containerd-snapshotter\":true}} to /etc/docker/daemon.json and restart Docker.")
	}

	shardIndex, shardTotal := parityShard(t)
	selected := 0

	for _, tc := range matrix.InitialCases {
		if !matchesFilter(tc) {
			continue
		}
		if tc.Lane == "runtime" && os.Getenv("PARITY_LANE") == "" {
			continue
		}
		caseIndex := selected
		selected++
		if !inShard(caseIndex, shardIndex, shardTotal) {
			continue
		}

		t.Run(tc.ID, func(t *testing.T) {
			outcome := parityMatched
			defer func() {
				if t.Failed() {
					outcome = parityFailed
				}
				report.record(tc.ID, outcome)
			}()
			if !tc.Serial {
				t.Parallel()
			}

			if tc.DockerRequired && (os.Getenv("PARITY_SKIP_DOCKER") == "true" || !dockerAvailable) {
				outcome = paritySkippedDocker
				t.Skip("docker required")
			}
			if tc.NetworkRequired && os.Getenv("PARITY_SKIP_NETWORK") == "true" {
				outcome = paritySkippedNetwork
				t.Skip("network required")
			}
			if tc.Arm64Required && os.Getenv("PARITY_ARM64") != "true" {
				outcome = paritySkippedArm64
				t.Skip("arm64 experimental (set PARITY_ARM64=true to run; requires arm64 emulation)")
			}
			if tc.Lock != "" {
				mu, ok := parityLocks[tc.Lock]
				if !ok {
					t.Fatalf("unknown lock %q", tc.Lock)
				}
				mu.Lock()
				defer mu.Unlock()
			}
			perCaseTimeout := defaultTimeout
			if tc.Lane == "runtime" {
				perCaseTimeout = runtimeTimeout
			}
			tsCtx, tsCancel := context.WithTimeout(t.Context(), perCaseTimeout)
			tsRes := runParitySide(t, tsCtx, repoRoot, cliTS, tc, "ts")
			tsStatus := classifyParitySide(tsCtx.Err(), tsRes.Stdout, tsRes.Stderr, tsRes.ExitCode)
			tsCancel()

			goCtx, goCancel := context.WithTimeout(t.Context(), perCaseTimeout)
			goRes := runParitySide(t, goCtx, repoRoot, cliGO, tc, "go")
			goStatus := classifyParitySide(goCtx.Err(), goRes.Stdout, goRes.Stderr, goRes.ExitCode)
			goCancel()
			if goStatus.Timeout {
				t.Fatalf("Go timed out")
			}

			if tsStatus.Skip {
				if !tc.ExpectInfraFailure || !tsStatus.Infra || goRes.ExitCode == 0 {
					outcome = parityInconclusive
					t.Skipf("TS %s (Go exit %d) [case=%s]", tsStatus.Reason, goRes.ExitCode, tc.ID)
					return
				}
			}
			if goStatus.Infra && tsRes.ExitCode == 0 {
				t.Fatalf("Go failed with infra error (exit %d) while TS succeeded", goRes.ExitCode)
			}

			asserts := setFrom(tc.Asserts)

			if tsRes.ExitCode != 0 && goRes.ExitCode == 0 && tsStatus.Infra {
				outcome = parityInconclusive
				t.Skipf("TS infra error (exit %d) [case=%s]", tsRes.ExitCode, tc.ID)
				return
			}

			if expectSuccess, known := caseExpectsOutcome(tc.ID); known {
				if expectSuccess && goRes.ExitCode != 0 {
					if tsRes.ExitCode == 0 {
						t.Errorf("regression: success-intended case, TS exit 0 but Go exited %d\n--- Go stdout\n%s\n--- Go stderr\n%s",
							goRes.ExitCode, strings.TrimSpace(goRes.Stdout), strings.TrimSpace(goRes.Stderr))
					} else {
						outcome = parityInconclusive
						t.Skipf("inconclusive: success-intended case but both sides failed (TS=%d Go=%d) [case=%s]", tsRes.ExitCode, goRes.ExitCode, tc.ID)
						return
					}
				}
				if !expectSuccess && tsRes.ExitCode != 0 && goRes.ExitCode == 0 {
					t.Errorf("regression: failure-intended case, TS exited %d but Go exited 0\n--- Go stdout\n%s", tsRes.ExitCode, strings.TrimSpace(goRes.Stdout))
				}
			}

			if asserts["exit_code"] && tsRes.ExitCode != goRes.ExitCode {
				t.Errorf("exit code: TS=%d Go=%d", tsRes.ExitCode, goRes.ExitCode)
			}

			scrubHashes := !tc.CompareHashes

			if tsRes.ExitCode != 0 && goRes.ExitCode != 0 {
				if asserts["stdout_normalized"] {
					tsNorm := normalizeOutputOpts(tsRes.Stdout, scrubHashes, tc.CompareNulls)
					goNorm := normalizeOutputOpts(goRes.Stdout, scrubHashes, tc.CompareNulls)
					if tsNorm != goNorm {
						t.Errorf("stdout differs:\n--- TS\n%s\n--- Go\n%s", tsNorm, goNorm)
					}
				}
				if asserts["stderr_normalized"] {
					tsReason := normalizeStringOpts(extractErrorReason(tsRes.Stdout, tsRes.Stderr), scrubHashes)
					goReason := normalizeStringOpts(extractErrorReason(goRes.Stdout, goRes.Stderr), scrubHashes)
					if tsReason != goReason {
						t.Errorf("error reason differs:\n  TS: %s\n  Go: %s", tsReason, goReason)
					}
				}
				return
			}

			if asserts["stdout_normalized"] {
				tsNorm := normalizeOutputOpts(tsRes.Stdout, scrubHashes, tc.CompareNulls)
				goNorm := normalizeOutputOpts(goRes.Stdout, scrubHashes, tc.CompareNulls)
				if tsNorm != goNorm {
					t.Errorf("stdout differs:\n--- TS\n%s\n--- Go\n%s", tsNorm, goNorm)
				}
			}

			if asserts["stdout_text"] {
				tsText := normalizeStringOpts(tsRes.Stdout, scrubHashes)
				goText := normalizeStringOpts(goRes.Stdout, scrubHashes)
				if tsText != goText {
					t.Errorf("stdout text differs:\n--- TS\n%s\n--- Go\n%s", tsText, goText)
				}
			}

			if asserts["stderr_normalized"] {
				tsNorm := normalizeTextOpts(tsRes.Stderr, scrubHashes)
				goNorm := normalizeTextOpts(goRes.Stderr, scrubHashes)
				if tsNorm != goNorm {
					t.Errorf("stderr differs:\n--- TS\n%s\n--- Go\n%s", tsNorm, goNorm)
				}
			}

			for _, path := range tc.KeyOrder {
				tsKeys, err := jsonKeyOrder(extractJSONCandidate(tsRes.Stdout), path)
				if err != nil {
					t.Errorf("key_order %q: TS: %v", path, err)
					continue
				}
				goKeys, err := jsonKeyOrder(extractJSONCandidate(goRes.Stdout), path)
				if err != nil {
					t.Errorf("key_order %q: Go: %v", path, err)
					continue
				}
				if !slices.Equal(tsKeys, goKeys) {
					t.Errorf("key order of %q differs:\n  TS: %v\n  Go: %v", path, tsKeys, goKeys)
				}
			}

			if tc.VerifyCmd != "" {
				if tsRes.VerifyExit != goRes.VerifyExit {
					t.Errorf("verify exit code: TS=%d Go=%d", tsRes.VerifyExit, goRes.VerifyExit)
				}
				if tsRes.VerifyExit != 0 || goRes.VerifyExit != 0 {
					t.Errorf("verify failed:\n--- TS stdout\n%s\n--- TS stderr\n%s\n--- Go stdout\n%s\n--- Go stderr\n%s",
						strings.TrimSpace(tsRes.VerifyStdout),
						strings.TrimSpace(tsRes.VerifyStderr),
						strings.TrimSpace(goRes.VerifyStdout),
						strings.TrimSpace(goRes.VerifyStderr),
					)
					return
				}
				tsVerifyOut := normalizeTextOpts(tsRes.VerifyStdout, scrubHashes)
				goVerifyOut := normalizeTextOpts(goRes.VerifyStdout, scrubHashes)
				if tsVerifyOut != goVerifyOut {
					t.Errorf("verify stdout differs:\n--- TS\n%s\n--- Go\n%s", tsVerifyOut, goVerifyOut)
				}
				tsVerifyErr := normalizeTextOpts(tsRes.VerifyStderr, scrubHashes)
				goVerifyErr := normalizeTextOpts(goRes.VerifyStderr, scrubHashes)
				if tsVerifyErr != goVerifyErr {
					t.Errorf("verify stderr differs:\n--- TS\n%s\n--- Go\n%s", tsVerifyErr, goVerifyErr)
				}
			}
		})
	}
}

func caseExpectsOutcome(id string) (expectSuccess bool, known bool) {
	switch {
	case strings.Contains(id, "-success"):
		return true, true
	case strings.Contains(id, "-failure"),
		strings.Contains(id, "invalid"),
		strings.Contains(id, "missing"),
		strings.Contains(id, "not-found"),
		strings.Contains(id, "unsupported"),
		strings.Contains(id, "required"),
		strings.Contains(id, "disallowed"):
		return false, true
	}
	return false, false
}

func runParitySide(t *testing.T, ctx context.Context, repoRoot, cli string, tc parityCase, side string) paritySideResult {
	t.Helper()

	env := parityEnv(tc.ID, side, repoRoot, !tc.NoComposeIsolation)

	if tc.SetupCmd != "" {
		setupEnv, err := runParitySetup(ctx, repoRoot, tc.SetupCmd, env)
		if err != nil {
			t.Fatalf("setup failed for %s/%s: %v", tc.ID, side, err)
		}
		env = mergeEnv(env, setupEnv)
	}

	if tc.CleanupCmd != "" {
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			if err := runParityCleanup(cleanupCtx, repoRoot, tc.CleanupCmd, env); err != nil {
				t.Errorf("cleanup failed for %s/%s: %v", tc.ID, side, err)
			}
		}()
	}

	stdout, stderr, exitCode := runParityCLI(ctx, repoRoot, cli, tc.TSCmd, env)
	result := paritySideResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
	}
	if exitCode == 0 {
		env = mergeEnv(env, extractCLIResultEnv(stdout))
		if tc.VerifyCmd != "" {
			verifyOut, verifyErr, verifyExit, err := runShellCommand(ctx, repoRoot, tc.VerifyCmd, env)
			if err != nil {
				t.Fatalf("verify failed for %s/%s: %v", tc.ID, side, err)
			}
			result.VerifyStdout = verifyOut
			result.VerifyStderr = verifyErr
			result.VerifyExit = verifyExit
		}
	}
	return result
}

const parityInfraRetries = 2

func runParityCLI(ctx context.Context, repoRoot, cli, cmdArgs string, env map[string]string) (stdout, stderr string, exitCode int) {
	return runWithInfraRetry(ctx, parityInfraRetries, func() (string, string, int) {
		return runParityCLIOnce(ctx, repoRoot, cli, cmdArgs, env)
	})
}

func runWithInfraRetry(ctx context.Context, maxAttempts int, run func() (stdout, stderr string, exitCode int)) (stdout, stderr string, exitCode int) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	for attempt := 1; ; attempt++ {
		if attempt > 1 && ctx.Err() != nil {
			return
		}
		stdout, stderr, exitCode = run()
		if exitCode == 0 || attempt >= maxAttempts || !isRetryableFailure(stdout, stderr) || ctx.Err() != nil {
			return
		}
	}
}

func runParityCLIOnce(ctx context.Context, repoRoot, cli, cmdArgs string, env map[string]string) (stdout, stderr string, exitCode int) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-lc", cli+" "+cmdArgs)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), envList(env)...)

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	return outBuf.String(), errBuf.String(), exitCode
}

func runParitySetup(ctx context.Context, repoRoot, setupCmd string, env map[string]string) (map[string]string, error) {
	var err error
	stdout, stderr, exitCode := runWithInfraRetry(ctx, parityInfraRetries, func() (string, string, int) {
		out, errOut, code, runErr := runShellCommand(ctx, repoRoot, setupCmd, env)
		err = runErr
		return out, errOut, code
	})
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("exit=%d stdout=%q stderr=%q", exitCode, strings.TrimSpace(stdout), strings.TrimSpace(stderr))
	}

	setupEnv := map[string]string{}
	for line := range strings.SplitSeq(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if match := reSetupEnv.FindStringSubmatch(line); len(match) == 3 {
			setupEnv[match[1]] = match[2]
		}
	}
	return setupEnv, nil
}

func runParityCleanup(ctx context.Context, repoRoot, cleanupCmd string, env map[string]string) error {
	stdout, stderr, exitCode, err := runShellCommand(ctx, repoRoot, cleanupCmd, env)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("exit=%d stdout=%q stderr=%q", exitCode, strings.TrimSpace(stdout), strings.TrimSpace(stderr))
	}
	return nil
}

func runShellCommand(ctx context.Context, repoRoot, shellCmd string, env map[string]string) (stdout, stderr string, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-lc", shellCmd)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), envList(env)...)

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	exitCode = 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
			err = runErr
		}
	}

	return outBuf.String(), errBuf.String(), exitCode, err
}

var (
	reTimestampTS      = regexp.MustCompile(`\[\d{4}-\d{2}-\d{2}T[\d:.]+Z\]`)
	reHostPath         = regexp.MustCompile(`/Users/[^\s"'\n,;=)]+`)
	reHomePath         = regexp.MustCompile(`/home/[^\s"'\n,;=)]+`)
	reTmpPath          = regexp.MustCompile(`/tmp/[^\s"'\n,;=)]+`)
	reVarFolders       = regexp.MustCompile(`/var/folders/[^\s"'\n,;=)]+`)
	reSHA256           = regexp.MustCompile(`sha256:[a-f0-9]{64}`)
	reHexID            = regexp.MustCompile(`\b[a-f0-9]{12,64}\b`)
	reMermaidNode      = regexp.MustCompile(`[a-f0-9]{6}\[`)
	reDockerfileB      = regexp.MustCompile(`transferring dockerfile: \d+B`)
	reParitySideSuffix = regexp.MustCompile(`-(ts|go)(-\d+)?\b`)

	stripLines = []string{
		"@devcontainers/cli",
		"manifest url:",
		"[httpOci]",
		"Credential helper",
		"Found auths entry",
		"Resolving Feature dependencies",
		"(node:",
		"(Use `node",
	}
)

func normalizeStringOpts(s string, scrubHashes bool) string {
	s = reHostPath.ReplaceAllString(s, "<HOST_PATH>")
	s = reHomePath.ReplaceAllString(s, "<HOST_PATH>")
	s = reTmpPath.ReplaceAllString(s, "<TMP_PATH>")
	s = reVarFolders.ReplaceAllString(s, "<TMP_PATH>")
	if scrubHashes {
		s = reSHA256.ReplaceAllString(s, "sha256:<HASH>")
		s = reHexID.ReplaceAllStringFunc(s, func(m string) string {
			if strings.ContainsAny(m, "abcdef") {
				return "<ID>"
			}
			return m
		})
	}
	s = reMermaidNode.ReplaceAllString(s, "<NODE>[")
	s = reParitySideSuffix.ReplaceAllString(s, "-<SIDE>$2")
	return s
}

func normalizeOutput(raw string) string { return normalizeOutputOpts(raw, true, false) }

func normalizeOutputOpts(raw string, scrubHashes, keepNulls bool) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if parsed, ok := decodeJSONNumber(raw); ok {
		normalized := normalizeValueOpts(parsed, scrubHashes, keepNulls)
		out, _ := json.Marshal(normalized)
		return string(out)
	}

	if candidate := extractJSONCandidate(raw); candidate != "" && candidate != raw {
		if parsed, ok := decodeJSONNumber(candidate); ok {
			normalized := normalizeValueOpts(parsed, scrubHashes, keepNulls)
			out, _ := json.Marshal(normalized)
			return string(out)
		}
	}

	return normalizeStringOpts(raw, scrubHashes)
}

func normalizeValueOpts(v any, scrubHashes, keepNulls bool) any {
	switch val := v.(type) {
	case map[string]any:
		result := make(map[string]any)
		for k, v := range val {
			if v == nil {
				if keepNulls {
					result[k] = nil
				}
				continue
			}
			if k == "devcontainer.metadata" {
				if s, ok := v.(string); ok {
					result[k] = normalizeEmbeddedJSONOpts(s, scrubHashes, keepNulls)
					continue
				}
			}
			result[k] = normalizeValueOpts(v, scrubHashes, keepNulls)
		}
		return sortedMap(result)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = normalizeValueOpts(item, scrubHashes, keepNulls)
		}
		return out
	case string:
		if embedded, ok := parseEmbeddedJSON(val); ok {
			return normalizeValueOpts(embedded, scrubHashes, keepNulls)
		}
		return normalizeStringOpts(val, scrubHashes)
	default:
		return val
	}
}

func normalizeEmbeddedJSONOpts(raw string, scrubHashes, keepNulls bool) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if parsed, ok := parseEmbeddedJSON(raw); ok {
		return normalizeValueOpts(parsed, scrubHashes, keepNulls)
	}
	return normalizeStringOpts(raw, scrubHashes)
}

func parseEmbeddedJSON(raw string) (any, bool) {
	t := strings.TrimSpace(raw)
	if !strings.HasPrefix(t, "{") && !strings.HasPrefix(t, "[") {
		return nil, false
	}
	return decodeJSONNumber(t)
}

func decodeJSONNumber(raw string) (any, bool) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var parsed any
	if dec.Decode(&parsed) == nil {
		return parsed, true
	}
	return nil, false
}

func sortedMap(m map[string]any) map[string]any {
	return m
}

func normalizeTextOpts(raw string, scrubHashes bool) string {
	var lines []string
	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimRight(line, "\r")

		skip := false
		for _, pattern := range stripLines {
			if strings.Contains(line, pattern) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		line = reTimestampTS.ReplaceAllString(line, "[X ms]")

		line = normalizeStringOpts(line, scrubHashes)

		line = reDockerfileB.ReplaceAllString(line, "transferring dockerfile: <N>B")

		if strings.TrimSpace(line) == "" {
			continue
		}

		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func extractErrorReason(stdout, stderr string) string {
	text := stderr

	var parsed map[string]any
	jsonStdout := strings.TrimSpace(stdout)
	if candidate := extractJSONCandidate(jsonStdout); candidate != "" {
		jsonStdout = candidate
	}
	if json.Unmarshal([]byte(jsonStdout), &parsed) == nil {
		if outcome, _ := parsed["outcome"].(string); outcome == "error" {
			if msg, _ := parsed["message"].(string); msg != "" {
				text = msg
			} else if desc, _ := parsed["description"].(string); desc != "" {
				text = desc
			}
		}
	}

	text = strings.ReplaceAll(text, "\r\n", "\n")

	if m := matchChoiceYargs(text); m != "" {
		return m
	}
	if m := matchChoiceGo(text); m != "" {
		return m
	}
	if m := matchInvalidMode(text); m != "" {
		return m
	}

	re := regexp.MustCompile(`(?m)Missing required argument:\s*(.+)$`)
	if match := re.FindStringSubmatch(text); len(match) > 1 {
		return "missing-required|" + normalizeRequired(strings.Split(match[1], "\n")[0])
	}

	re = regexp.MustCompile(`(?m)Unmatched argument format:\s*(.+)$`)
	if match := re.FindStringSubmatch(text); len(match) > 1 {
		return "invalid-format|" + strings.TrimSpace(match[1])
	}

	if strings.Contains(text, "Failed to parse Feature identifier") ||
		strings.Contains(text, "failed validation") {
		return "parse-error|feature-identifier"
	}

	re = regexp.MustCompile(`Implications failed:\s*\n?\s*(.+)`)
	if match := re.FindStringSubmatch(text); len(match) > 1 {
		return "implications|" + strings.TrimSpace(match[1])
	}

	lines := strings.Split(strings.TrimSpace(text), "\n")
	for _, line := range slices.Backward(lines) {
		line := strings.TrimSpace(line)
		if line == "" || isBannerLine(line) || isStackFrame(line) {
			continue
		}
		line = reLogTimestamp.ReplaceAllString(line, "")
		return strings.TrimPrefix(line, "Error: ")
	}
	return ""
}

var reLogTimestamp = regexp.MustCompile(`^\[[^\]]*\]\s*`)

func isBannerLine(line string) bool {
	return strings.Contains(line, "@devcontainers/cli") && strings.Contains(line, "Node.js")
}

func isStackFrame(line string) bool {
	return strings.HasPrefix(line, "at ")
}

var reChoiceYargs = regexp.MustCompile(`(?m)Argument:\s*([^,]+),\s*Given:\s*"([^"]+)",\s*Choices:\s*(.+)$`)
var reChoiceGo = regexp.MustCompile(`(?m)Invalid value "([^"]+)" for --([^.\s]+)\.\s*Choose from:\s*(.+)$`)
var reInvalidMode = regexp.MustCompile(`(?m)Invalid mode "([^"]+)".*Choose from:\s*(.+)$`)
var reSetupEnv = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
var reRequiredPrefix = regexp.MustCompile(`(?i)^One of\s+`)
var reRequiredSuffix = regexp.MustCompile(`(?i)\s+is required\.?$`)
var reRequiredSplit = regexp.MustCompile(`\s+or\s+|,\s*`)

func matchChoiceYargs(text string) string {
	match := reChoiceYargs.FindStringSubmatch(text)
	if len(match) < 4 {
		return ""
	}
	flag := strings.TrimSpace(strings.TrimPrefix(match[1], "--"))
	value := strings.TrimSpace(match[2])
	choices := normalizeChoices(match[3])
	return fmt.Sprintf("invalid-choice|flag=%s|value=%s|choices=%s", flag, value, choices)
}

func matchChoiceGo(text string) string {
	match := reChoiceGo.FindStringSubmatch(text)
	if len(match) < 4 {
		return ""
	}
	value := strings.TrimSpace(match[1])
	flag := strings.TrimSpace(match[2])
	choices := normalizeChoices(match[3])
	return fmt.Sprintf("invalid-choice|flag=%s|value=%s|choices=%s", flag, value, choices)
}

func matchInvalidMode(text string) string {
	match := reInvalidMode.FindStringSubmatch(text)
	if len(match) < 3 {
		return ""
	}
	value := strings.TrimSpace(match[1])
	choices := normalizeChoices(match[2])
	return fmt.Sprintf("invalid-choice|flag=mode|value=%s|choices=%s", value, choices)
}

func normalizeChoices(raw string) string {
	parts := strings.Split(raw, ",")
	var clean []string
	for _, p := range parts {
		p = strings.Trim(strings.TrimSpace(p), `"'`)
		if p != "" {
			clean = append(clean, p)
		}
	}
	sort.Strings(clean)
	return strings.Join(clean, ",")
}

func normalizeRequired(raw string) string {
	raw = reRequiredPrefix.ReplaceAllString(raw, "")
	raw = reRequiredSuffix.ReplaceAllString(raw, "")
	parts := reRequiredSplit.Split(raw, -1)
	var clean []string
	for _, p := range parts {
		p = strings.TrimSpace(strings.TrimPrefix(p, "--"))
		if p != "" {
			clean = append(clean, p)
		}
	}
	sort.Strings(clean)
	return strings.Join(clean, ",")
}

func jsonKeyOrder(data, path string) ([]string, error) {
	raw := json.RawMessage(data)
	if path != "" {
		for name := range strings.SplitSeq(path, ".") {
			if i, err := strconv.Atoi(name); err == nil {
				var arr []json.RawMessage
				if err := json.Unmarshal(raw, &arr); err != nil {
					return nil, fmt.Errorf("%s: %w", path, err)
				}
				if i < 0 || i >= len(arr) {
					return nil, fmt.Errorf("%s: no element %d", path, i)
				}
				raw = arr[i]
				continue
			}
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(raw, &obj); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			var ok bool
			if raw, ok = obj[name]; !ok {
				return nil, fmt.Errorf("%s: no property %q", path, name)
			}
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("%s: not an object", path)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return keys, nil
}

func extractJSONCandidate(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			return raw[start : end+1]
		}
	}

	if start := strings.Index(raw, "["); start >= 0 {
		if end := strings.LastIndex(raw, "]"); end > start {
			return raw[start : end+1]
		}
	}
	return raw
}

func extractCLIResultEnv(stdout string) map[string]string {
	candidate := extractJSONCandidate(stdout)
	if candidate == "" {
		return nil
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(candidate), &payload); err != nil {
		return nil
	}

	env := map[string]string{}
	if containerID, ok := payload["containerId"].(string); ok && containerID != "" {
		env["CONTAINER_ID"] = containerID
	}
	if composeProjectName, ok := payload["composeProjectName"].(string); ok && composeProjectName != "" {
		env["COMPOSE_PROJECT_NAME"] = composeProjectName
	}
	switch imageName := payload["imageName"].(type) {
	case string:
		if imageName != "" {
			env["IMAGE_NAME"] = imageName
		}
	case []any:
		var names []string
		for i, item := range imageName {
			name, ok := item.(string)
			if !ok || name == "" {
				continue
			}
			names = append(names, name)
			env[fmt.Sprintf("IMAGE_NAME_%d", i+1)] = name
		}
		if len(names) > 0 {
			env["IMAGE_NAME"] = names[0]
			env["IMAGE_NAMES"] = strings.Join(names, ",")
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func parityEnv(caseID, side, repoRoot string, isolateCompose bool) map[string]string {
	env := map[string]string{
		"PARITY_CASE_ID":   sanitizeEnvValue(caseID),
		"PARITY_SIDE":      side,
		"PARITY_REPO_ROOT": repoRoot,
		"PARITY_CLI_GO":    envOr("CLI_GO", filepath.Join(repoRoot, "devcontainer")),
		"PARITY_CLI_TS":    envOr("CLI_TS", defaultParityTSCLI(repoRoot)),
		// Newer BuildKit attaches provenance/SBOM attestations by default, wrapping
		// the feature-content image in an attestation manifest list. That breaks the
		// TS CLI's `COPY --from=dev_containers_feature_content_source ...` feature
		// build ("/tmp/build-features/<feat>: not found"). Disable default
		// attestations for BOTH sides so the oracle builds the same way it did when
		// these cases were validated — an environment artifact, not a product diff.
		"BUILDX_NO_DEFAULT_ATTESTATIONS": "1",
	}
	if isolateCompose {
		env["COMPOSE_PROJECT_NAME"] = composeProjectName(caseID)
	}
	return env
}

func composeProjectName(caseID string) string {
	var b strings.Builder
	b.WriteString("dc")
	for _, r := range strings.ToLower(caseID) {
		if isComposeProjectNameRune(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isComposeProjectNameRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
}

func sanitizeEnvValue(value string) string {
	replacer := strings.NewReplacer("/", "-", " ", "-", ":", "-", "\t", "-", "\n", "-")
	return replacer.Replace(value)
}

func mergeEnv(base, extra map[string]string) map[string]string {
	out := map[string]string{}
	maps.Copy(out, base)
	maps.Copy(out, extra)
	return out
}

func envList(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (go.mod)")
		}
		dir = parent
	}
}

// defaultParityTSCLI is the oracle's command line: the node on this process's
// PATH, by its absolute path, running reference/devcontainer.js. The cases run
// through `/bin/sh -lc`, and a login shell rebuilds PATH from the system
// profile: on a runner whose setup-node node is not the system one (the docker8
// fleet's image has node 22 in /usr/local/bin) a bare `node` would then load the
// reference's native node-pty, built for the setup-node version, and fail.
func defaultParityTSCLI(repoRoot string) string {
	node, err := exec.LookPath("node")
	if err != nil {
		node = "node"
	}
	return node + " " + filepath.Join(repoRoot, "reference", "devcontainer.js")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func inShard(caseIndex, index, total int) bool {
	return total <= 1 || caseIndex%total == index
}

func parityShard(t *testing.T) (index, total int) {
	total = 1
	if v := os.Getenv("PARITY_SHARD_TOTAL"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("PARITY_SHARD_TOTAL=%q: must be a positive integer", v)
		}
		total = n
	}
	if v := os.Getenv("PARITY_SHARD_INDEX"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n >= total {
			t.Fatalf("PARITY_SHARD_INDEX=%q: must be in [0,%d)", v, total)
		}
		index = n
	}
	return index, total
}

func isDockerAvailable() bool {
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

func hasContainerdImageStore() bool {
	out, err := exec.Command("docker", "info", "--format", "{{json .DriverStatus}}").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "io.containerd.snapshotter")
}

func matchesFilter(tc parityCase) bool {
	if os.Getenv("PARITY_NETWORK_ONLY") == "true" && !tc.NetworkRequired {
		return false
	}
	return selected(tc.Lane, os.Getenv("PARITY_LANE")) &&
		selected(tc.Priority, os.Getenv("PARITY_PRIORITY")) &&
		selected(tc.Command, os.Getenv("PARITY_COMMAND")) &&
		selectedSubstring(tc.ID, os.Getenv("PARITY_CASE")) &&
		selected(tc.CurrentStatus, os.Getenv("PARITY_STATUS"))
}

func selected(value, filter string) bool {
	if filter == "" || filter == "all" {
		return true
	}
	for f := range strings.SplitSeq(filter, ",") {
		if strings.TrimSpace(f) == value {
			return true
		}
	}
	return false
}

func selectedSubstring(value, filter string) bool {
	return filter == "" || filter == "all" || strings.Contains(value, filter)
}

func isInfraError(stdout, stderr string) bool {
	combined := stdout + stderr
	infraPatterns := []string{
		"no match for platform",
		"failed to solve",
		"ERROR: error getting credentials",
		"Cannot connect to the Docker daemon",
	}
	for _, p := range infraPatterns {
		if strings.Contains(strings.ToLower(combined), strings.ToLower(p)) {
			return true
		}
	}
	return false
}

func isRetryableFailure(stdout, stderr string) bool {
	if isInfraError(stdout, stderr) {
		return true
	}
	combined := strings.ToLower(stdout + stderr)
	retryablePatterns := []string{
		"an error occurred setting up the container",
		"command failed: docker build",
	}
	for _, p := range retryablePatterns {
		if strings.Contains(combined, p) {
			return true
		}
	}
	return false
}

type paritySideStatus struct {
	Skip    bool
	Timeout bool
	Infra   bool
	Reason  string
}

func classifyParitySide(ctxErr error, stdout, stderr string, exitCode int) paritySideStatus {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return paritySideStatus{Skip: true, Timeout: true, Reason: "timed out"}
	}
	if exitCode != 0 && isInfraError(stdout, stderr) {
		return paritySideStatus{Skip: true, Infra: true, Reason: fmt.Sprintf("failed with infra error (exit %d)", exitCode)}
	}
	return paritySideStatus{}
}

func setFrom(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, item := range items {
		m[item] = true
	}
	return m
}
