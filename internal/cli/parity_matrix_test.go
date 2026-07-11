// Package cli parity matrix.
//
// Runtime cases run in parallel, bounded by `go test -parallel N`. Isolation is
// per case: single-container cases use unique --id-label parity.case values, and
// compose cases get a unique COMPOSE_PROJECT_NAME (see parityEnv); build cache/
// output cases use a unique BUILDX_BUILDER instead of touching the global buildx
// default. A few cases that assert a specific compose project name opt out via
// no_compose_isolation, and cases that can't be isolated set serial: true.
//
// Recommended invocation for the full runtime lane (docker required):
//
//	PARITY_LANE=all go test ./internal/cli -run TestParityMatrix -parallel 6 -timeout 30m
//
// ~5.5 min vs ~26 min sequential. Higher -parallel values increase docker
// contention and transient flakes; 6 is a stable default on an 8-core host.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// --- YAML types ---

type parityMatrix struct {
	InitialCases []parityCase `yaml:"initial_cases"`
}

type parityCase struct {
	ID              string   `yaml:"id"`
	Lane            string   `yaml:"lane"`
	Command         string   `yaml:"command"`
	Priority        string   `yaml:"priority"`
	DockerRequired  bool     `yaml:"docker_required"`
	NetworkRequired bool     `yaml:"network_required"`
	TSCmd           string   `yaml:"ts_cmd"`
	SetupCmd        string   `yaml:"setup_cmd"`
	VerifyCmd       string   `yaml:"verify_cmd"`
	CleanupCmd      string   `yaml:"cleanup_cmd"`
	Asserts         []string `yaml:"asserts"`
	Class           string   `yaml:"class"`
	CurrentStatus   string   `yaml:"current_status"`
	Notes           string   `yaml:"notes"`
	// NoComposeIsolation opts a case out of the injected per-case
	// COMPOSE_PROJECT_NAME (used to isolate parallel runtime cases). Set it for
	// cases that exercise compose project-name resolution, or whose reuse logic
	// is sensitive to the project name.
	NoComposeIsolation bool `yaml:"no_compose_isolation"`
	// Serial forces a case to run sequentially (no t.Parallel). Used for the few
	// runtime cases that can't be isolated for parallel execution.
	Serial bool `yaml:"serial"`
}

type paritySideResult struct {
	Stdout       string
	Stderr       string
	ExitCode     int
	VerifyStdout string
	VerifyStderr string
	VerifyExit   int
}

// --- Main test ---

func TestParityMatrix(t *testing.T) {
	repoRoot := findRepoRoot(t)
	matrixPath := filepath.Join(repoRoot, "docs", "migration", "parity-matrix.yaml")

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

	cliTS := envOr("CLI_TS", "node "+filepath.Join(repoRoot, "reference", "devcontainer.js"))
	cliGO := envOr("CLI_GO", filepath.Join(repoRoot, "devcontainer"))
	defaultTimeout := 60 * time.Second
	runtimeTimeout := 300 * time.Second

	// Check TS CLI exists
	if _, err := os.Stat(filepath.Join(repoRoot, "reference", "dist", "spec-node", "devContainersSpecCLI.js")); err != nil {
		t.Skip("TS reference not compiled (reference/dist not found). Run: git submodule update --init && task ts:compile")
	}
	// Check Go CLI exists
	if _, err := os.Stat(cliGO); err != nil {
		t.Skip("Go CLI not built. Run: task build")
	}

	dockerAvailable := isDockerAvailable()

	for _, tc := range matrix.InitialCases {
		tc := tc
		if !matchesFilter(tc) {
			continue
		}

		t.Run(tc.ID, func(t *testing.T) {
			// Cases run in parallel (bounded by `go test -parallel N`); per-case
			// COMPOSE_PROJECT_NAME + id-labels isolate runtime cases. A few cases
			// that can't be isolated opt into serial execution.
			if !tc.Serial {
				t.Parallel()
			}

			// Runtime tests are opt-in: only run when PARITY_LANE=runtime or PARITY_LANE=all
			if tc.Lane == "runtime" && os.Getenv("PARITY_LANE") == "" {
				t.Skip("runtime tests require PARITY_LANE=runtime or PARITY_LANE=all")
			}
			if tc.DockerRequired && (os.Getenv("PARITY_SKIP_DOCKER") == "true" || !dockerAvailable) {
				t.Skip("docker required")
			}
			if tc.NetworkRequired && os.Getenv("PARITY_SKIP_NETWORK") == "true" {
				t.Skip("network required")
			}
			perCaseTimeout := defaultTimeout
			if tc.Lane == "runtime" {
				perCaseTimeout = runtimeTimeout
			}
			tsCtx, tsCancel := context.WithTimeout(t.Context(), perCaseTimeout)
			tsRes := runParitySide(t, tsCtx, repoRoot, cliTS, tc, "ts")
			tsStatus := classifyParitySide(tsCtx.Err(), tsRes.Stdout, tsRes.Stderr, tsRes.ExitCode)
			tsCancel()
			if tsStatus.Skip {
				t.Skipf("TS %s", tsStatus.Reason)
				return
			}

			goCtx, goCancel := context.WithTimeout(t.Context(), perCaseTimeout)
			goRes := runParitySide(t, goCtx, repoRoot, cliGO, tc, "go")
			goStatus := classifyParitySide(goCtx.Err(), goRes.Stdout, goRes.Stderr, goRes.ExitCode)
			goCancel()
			if goStatus.Timeout {
				t.Fatalf("Go timed out")
			}
			if goStatus.Infra && tsRes.ExitCode == 0 {
				t.Fatalf("Go failed with infra error (exit %d)", goRes.ExitCode)
			}

			asserts := setFrom(tc.Asserts)

			// Skip when TS fails due to infra but Go succeeds; this is not a product mismatch.
			if tsRes.ExitCode != 0 && goRes.ExitCode == 0 && tsStatus.Infra {
				t.Skipf("TS infra error (exit %d)", tsRes.ExitCode)
				return
			}

			// Exit code
			if asserts["exit_code"] && tsRes.ExitCode != goRes.ExitCode {
				t.Errorf("exit code: TS=%d Go=%d", tsRes.ExitCode, goRes.ExitCode)
			}

			// Non-zero exit path
			if tsRes.ExitCode != 0 && goRes.ExitCode != 0 {
				if asserts["stdout_normalized"] {
					tsNorm := normalizeOutput(tsRes.Stdout)
					goNorm := normalizeOutput(goRes.Stdout)
					if tsNorm != goNorm {
						t.Errorf("stdout differs:\n--- TS\n%s\n--- Go\n%s", tsNorm, goNorm)
					}
				}
				if asserts["stderr_normalized"] {
					tsReason := extractErrorReason(tsRes.Stdout, tsRes.Stderr)
					goReason := extractErrorReason(goRes.Stdout, goRes.Stderr)
					if tsReason != goReason {
						t.Errorf("error reason differs:\n  TS: %s\n  Go: %s", tsReason, goReason)
					}
				}
				return
			}

			// Success path
			if asserts["stdout_normalized"] {
				tsNorm := normalizeOutput(tsRes.Stdout)
				goNorm := normalizeOutput(goRes.Stdout)
				if tsNorm != goNorm {
					t.Errorf("stdout differs:\n--- TS\n%s\n--- Go\n%s", tsNorm, goNorm)
				}
			}

			if asserts["stderr_normalized"] {
				tsNorm := normalizeText(tsRes.Stderr)
				goNorm := normalizeText(goRes.Stderr)
				if tsNorm != goNorm {
					t.Errorf("stderr differs:\n--- TS\n%s\n--- Go\n%s", tsNorm, goNorm)
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
				tsVerifyOut := normalizeText(tsRes.VerifyStdout)
				goVerifyOut := normalizeText(goRes.VerifyStdout)
				if tsVerifyOut != goVerifyOut {
					t.Errorf("verify stdout differs:\n--- TS\n%s\n--- Go\n%s", tsVerifyOut, goVerifyOut)
				}
				tsVerifyErr := normalizeText(tsRes.VerifyStderr)
				goVerifyErr := normalizeText(goRes.VerifyStderr)
				if tsVerifyErr != goVerifyErr {
					t.Errorf("verify stderr differs:\n--- TS\n%s\n--- Go\n%s", tsVerifyErr, goVerifyErr)
				}
			}
		})
	}
}

// --- CLI execution ---

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

func runParityCLI(ctx context.Context, repoRoot, cli, cmdArgs string, env map[string]string) (stdout, stderr string, exitCode int) {
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
	stdout, stderr, exitCode, err := runShellCommand(ctx, repoRoot, setupCmd, env)
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("exit=%d stdout=%q stderr=%q", exitCode, strings.TrimSpace(stdout), strings.TrimSpace(stderr))
	}

	setupEnv := map[string]string{}
	for _, line := range strings.Split(stdout, "\n") {
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

// --- Normalization ---

var (
	reTimestampGo      = regexp.MustCompile(`\[\d+ ms\]`)
	reTimestampTS      = regexp.MustCompile(`\[\d{4}-\d{2}-\d{2}T[\d:.]+Z\]`)
	reHostPath         = regexp.MustCompile(`/Users/[^\s"'\n]+`)
	reHomePath         = regexp.MustCompile(`/home/[^\s"'\n]+`)
	reTmpPath          = regexp.MustCompile(`/tmp/[^\s"'\n]+`)
	reVarFolders       = regexp.MustCompile(`/var/folders/[^\s"'\n]+`)
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

func normalizeString(s string) string {
	s = reHostPath.ReplaceAllString(s, "<HOST_PATH>")
	s = reHomePath.ReplaceAllString(s, "<HOST_PATH>")
	s = reTmpPath.ReplaceAllString(s, "<TMP_PATH>")
	s = reVarFolders.ReplaceAllString(s, "<TMP_PATH>")
	s = reSHA256.ReplaceAllString(s, "sha256:<HASH>")
	s = reHexID.ReplaceAllString(s, "<ID>")
	s = reMermaidNode.ReplaceAllString(s, "<NODE>[")
	// Normalize parity side identifiers in image/container names
	s = reParitySideSuffix.ReplaceAllString(s, "-<SIDE>$2")
	return s
}

func normalizeOutput(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// Try JSON parse
	var parsed interface{}
	if json.Unmarshal([]byte(raw), &parsed) == nil {
		normalized := normalizeValue(parsed)
		out, _ := json.Marshal(normalized)
		return string(out)
	}

	// TS occasionally prefixes JSON with banner/noise on stderr/stdout; recover the payload.
	if candidate := extractJSONCandidate(raw); candidate != "" && candidate != raw {
		if json.Unmarshal([]byte(candidate), &parsed) == nil {
			normalized := normalizeValue(parsed)
			out, _ := json.Marshal(normalized)
			return string(out)
		}
	}

	// Fallback: normalize as text
	return normalizeString(raw)
}

func normalizeValue(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for k, v := range val {
			if v == nil {
				continue
			}
			if k == "devcontainer.metadata" {
				if s, ok := v.(string); ok {
					result[k] = normalizeEmbeddedJSON(s)
					continue
				}
			}
			result[k] = normalizeValue(v)
		}
		return sortedMap(result)
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, item := range val {
			out[i] = normalizeValue(item)
		}
		return out
	case string:
		if embedded, ok := parseEmbeddedJSON(val); ok {
			return normalizeValue(embedded)
		}
		return normalizeString(val)
	default:
		return val
	}
}

func normalizeEmbeddedJSON(raw string) interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if parsed, ok := parseEmbeddedJSON(raw); ok {
		return normalizeValue(parsed)
	}
	return normalizeString(raw)
}

func parseEmbeddedJSON(raw string) (interface{}, bool) {
	var parsed interface{}
	if json.Unmarshal([]byte(raw), &parsed) == nil {
		return parsed, true
	}
	return nil, false
}

// sortedMap returns a json.RawMessage with sorted keys.
func sortedMap(m map[string]interface{}) map[string]interface{} {
	// json.Marshal already sorts keys in Go 1.12+
	return m
}

func normalizeText(raw string) string {
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")

		// Strip noise lines
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

		// Normalize timestamps
		line = reTimestampGo.ReplaceAllString(line, "[X ms]")
		line = reTimestampTS.ReplaceAllString(line, "[X ms]")

		// Normalize paths and IDs
		line = normalizeString(line)

		// Normalize dockerfile size
		line = reDockerfileB.ReplaceAllString(line, "transferring dockerfile: <N>B")

		// Skip blank lines
		if strings.TrimSpace(line) == "" {
			continue
		}

		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// --- Error reason extraction ---

func extractErrorReason(stdout, stderr string) string {
	text := stderr

	// Try JSON stdout for error outcome
	var parsed map[string]interface{}
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

	// Invalid choice patterns
	if m := matchChoiceYargs(text); m != "" {
		return m
	}
	if m := matchChoiceGo(text); m != "" {
		return m
	}
	if m := matchInvalidMode(text); m != "" {
		return m
	}

	// Missing required
	re := regexp.MustCompile(`(?m)Missing required argument:\s*(.+)$`)
	if match := re.FindStringSubmatch(text); len(match) > 1 {
		return "missing-required|" + normalizeRequired(strings.Split(match[1], "\n")[0])
	}

	// Unmatched format
	re = regexp.MustCompile(`(?m)Unmatched argument format:\s*(.+)$`)
	if match := re.FindStringSubmatch(text); len(match) > 1 {
		return "invalid-format|" + strings.TrimSpace(match[1])
	}

	// Parse error — match both Go and TS variants
	if strings.Contains(text, "Failed to parse Feature identifier") ||
		strings.Contains(text, "failed validation") {
		return "parse-error|feature-identifier"
	}

	// Implications
	re = regexp.MustCompile(`Implications failed:\s*\n?\s*(.+)`)
	if match := re.FindStringSubmatch(text); len(match) > 1 {
		return "implications|" + strings.TrimSpace(match[1])
	}

	// Fallback: last meaningful line, ignoring the version banner (only TS emits
	// it) and Node.js stack frames (0.88 lets a few error paths throw with a
	// trace). This lets a TS "Error: X" + trace line up with a Go "X" message,
	// and a banner-only silent-exit line up with Go's empty stderr.
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || isBannerLine(line) || isStackFrame(line) {
			continue
		}
		// Strip the TS log timestamp prefix ("[2026-...Z] ") and a leading
		// "Error: " so a TS-thrown error lines up with Go's plain message.
		line = reLogTimestamp.ReplaceAllString(line, "")
		return strings.TrimPrefix(line, "Error: ")
	}
	return ""
}

var reLogTimestamp = regexp.MustCompile(`^\[[^\]]*\]\s*`)

// isBannerLine reports whether a line is the CLI version banner
// ("[ts] @devcontainers/cli X. Node.js ...") that only the TS CLI prints.
func isBannerLine(line string) bool {
	return strings.Contains(line, "@devcontainers/cli") && strings.Contains(line, "Node.js")
}

// isStackFrame reports whether a line is a Node.js stack frame ("at ...").
func isStackFrame(line string) bool {
	return strings.HasPrefix(line, "at ")
}

var reChoiceYargs = regexp.MustCompile(`(?m)Argument:\s*([^,]+),\s*Given:\s*"([^"]+)",\s*Choices:\s*(.+)$`)
var reChoiceGo = regexp.MustCompile(`(?m)Invalid value "([^"]+)" for --([^.\s]+)\.\s*Choose from:\s*(.+)$`)
var reInvalidMode = regexp.MustCompile(`(?m)Invalid mode "([^"]+)".*Choose from:\s*(.+)$`)
var reSetupEnv = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

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
	raw = regexp.MustCompile(`(?i)^One of\s+`).ReplaceAllString(raw, "")
	raw = regexp.MustCompile(`(?i)\s+is required\.?$`).ReplaceAllString(raw, "")
	parts := regexp.MustCompile(`\s+or\s+|,\s*`).Split(raw, -1)
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

// --- Helpers ---

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

	var payload map[string]interface{}
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
	case []interface{}:
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
		// Newer BuildKit attaches provenance/SBOM attestations by default, wrapping
		// the feature-content image in an attestation manifest list. That breaks the
		// TS CLI's `COPY --from=dev_containers_feature_content_source ...` feature
		// build ("/tmp/build-features/<feat>: not found"). Disable default
		// attestations for BOTH sides so the oracle builds the same way it did when
		// these cases were validated — an environment artifact, not a product diff.
		"BUILDX_NO_DEFAULT_ATTESTATIONS": "1",
	}
	if isolateCompose {
		// Give each case its own compose project so parallel runtime cases that
		// share a fixture don't manage each other's containers. The name is the
		// SAME for both sides: ts and go run sequentially within a case (ts's
		// deferred cleanup runs before go starts), so they never overlap, and a
		// side-independent name keeps the emitted composeProjectName identical
		// across sides for output comparison. Cases that assert a specific
		// project name (compose `name:`, env-var, derived) opt out via
		// no_compose_isolation.
		env["COMPOSE_PROJECT_NAME"] = composeProjectName(caseID)
	}
	return env
}

// composeProjectName builds a valid, unique-per-case compose project name
// ([a-z0-9_-]).
func composeProjectName(caseID string) string {
	var b strings.Builder
	b.WriteString("dc")
	for _, r := range strings.ToLower(caseID) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func sanitizeEnvValue(value string) string {
	replacer := strings.NewReplacer("/", "-", " ", "-", ":", "-", "\t", "-", "\n", "-")
	return replacer.Replace(value)
}

func mergeEnv(base, extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func isDockerAvailable() bool {
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

func matchesFilter(tc parityCase) bool {
	return selected(tc.Lane, os.Getenv("PARITY_LANE")) &&
		selected(tc.Priority, os.Getenv("PARITY_PRIORITY")) &&
		selected(tc.Command, os.Getenv("PARITY_COMMAND")) &&
		selectedSubstring(tc.ID, os.Getenv("PARITY_CASE")) &&
		selected(tc.CurrentStatus, os.Getenv("PARITY_STATUS"))
}

func selected(value, filter string) bool {
	return filter == "" || filter == "all" || value == filter
}

func selectedSubstring(value, filter string) bool {
	return filter == "" || filter == "all" || strings.Contains(value, filter)
}

func isInfraError(stdout, stderr string) bool {
	combined := stdout + stderr
	infraPatterns := []string{
		"docker buildx",
		"no match for platform",
		"failed to solve",
		"ERROR: error getting credentials",
		"permission denied",
	}
	for _, p := range infraPatterns {
		if strings.Contains(strings.ToLower(combined), strings.ToLower(p)) {
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
