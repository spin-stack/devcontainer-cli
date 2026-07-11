package lifecycle

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/devcontainers/cli/internal/log"
)

// UserEnvProbeStrategy defines how to probe the container's user environment.
type UserEnvProbeStrategy string

const (
	ProbeNone                  UserEnvProbeStrategy = "none"
	ProbeLoginInteractiveShell UserEnvProbeStrategy = "loginInteractiveShell"
	ProbeInteractiveShell      UserEnvProbeStrategy = "interactiveShell"
	ProbeLoginShell            UserEnvProbeStrategy = "loginShell"
)

// ProbeRemoteEnv runs a shell command in the container to capture environment variables.
// If sessionDataFolder is provided, results are cached in a JSON file inside the container.
func ProbeRemoteEnv(logger log.Log, shellServer *ShellServer, strategy UserEnvProbeStrategy, remoteUser string, sessionDataFolder ...string) (map[string]string, error) {
	if strategy == ProbeNone || strategy == "" {
		return map[string]string{}, nil
	}

	// Try cache first
	cacheDir := ""
	if len(sessionDataFolder) > 0 {
		cacheDir = sessionDataFolder[0]
	}
	if cacheDir != "" {
		if cached := readEnvCache(shellServer, strategy, cacheDir); cached != nil {
			logger.Write(fmt.Sprintf("Using cached env probe (%s)", strategy), log.LevelTrace)
			return cached, nil
		}
	}

	// Use the user's actual login shell (from /etc/passwd) instead of a
	// hardcoded "bash" — on alpine/minimal images bash is absent, and the user
	// may use zsh/fish. Falls back to /bin/sh. Matches the TS CLI, which probes
	// with containerProperties.shell.
	shell := getUserShell(shellServer)

	var probeCmd string
	switch strategy {
	case ProbeLoginInteractiveShell:
		probeCmd = fmt.Sprintf("%s -lic env", shell)
	case ProbeInteractiveShell:
		probeCmd = fmt.Sprintf("%s -ic env", shell)
	case ProbeLoginShell:
		probeCmd = fmt.Sprintf("%s -lc env", shell)
	default:
		probeCmd = "env"
	}

	// Wrap with `timeout 10` so a shell startup script that waits on stdin does
	// not hang `up` forever (the TS CLI enforces a 10s userEnvProbe timeout). If
	// the shell/timeout fails (exit != 0, incl. 124 on timeout), fall back to
	// plain env.
	logger.Write(fmt.Sprintf("Probing remote env with strategy %q (shell %s)...", strategy, shell), log.LevelTrace)

	stdout, exitCode, err := shellServer.Exec("timeout 10 " + probeCmd)
	if err != nil {
		return nil, fmt.Errorf("probe remote env: %w", err)
	}
	if exitCode != 0 {
		if exitCode == 124 {
			logger.Write("userEnvProbe timed out after 10s; avoid waiting for input in shell startup scripts. Falling back to plain 'env'.", log.LevelWarning)
		} else {
			logger.Write("Probe failed, falling back to plain 'env'", log.LevelTrace)
		}
		stdout, _, err = shellServer.Exec("env")
		if err != nil {
			return nil, err
		}
	}

	result := parseEnvOutput(stdout)
	// The probed PWD is the shell's working dir, not meaningful for the merged
	// remote env — drop it, matching the TS CLI.
	delete(result, "PWD")

	// Merge the probed PATH with the container's base PATH so entries the login
	// shell dropped (or that only exist in the image env) are preserved — like
	// the TS CLI mergePaths. The container base PATH is the shell server's own
	// $PATH (docker exec inherits the image env).
	if shellPath := result["PATH"]; shellPath != "" {
		if basePath, _, berr := shellServer.Exec(`printf %s "$PATH"`); berr == nil && strings.TrimSpace(basePath) != "" {
			isRoot := remoteUser == "" || remoteUser == "root" || remoteUser == "0"
			result["PATH"] = mergePaths(shellPath, strings.TrimSpace(basePath), isRoot)
		}
	}

	// Cache if session folder provided
	if cacheDir != "" && len(result) > 0 {
		writeEnvCache(shellServer, result, strategy, cacheDir)
	}

	return result, nil
}

// mergePaths merges the container's base PATH into the shell-probed PATH,
// inserting any missing base entries in their relative order and skipping /sbin
// directories for non-root users — matching the TS CLI mergePaths.
func mergePaths(shellPath, containerPath string, rootUser bool) string {
	result := strings.Split(shellPath, ":")
	insertAt := 0
	for _, entry := range strings.Split(containerPath, ":") {
		idx := -1
		for i, e := range result {
			if e == entry {
				idx = i
				break
			}
		}
		if idx == -1 {
			if rootUser || !isSbinPath(entry) {
				result = append(result[:insertAt], append([]string{entry}, result[insertAt:]...)...)
				insertAt++
			}
		} else {
			insertAt = idx + 1
		}
	}
	return strings.Join(result, ":")
}

// isSbinPath reports whether a PATH entry is (or is under) an sbin directory.
func isSbinPath(entry string) bool {
	return strings.HasSuffix(entry, "/sbin") || strings.Contains(entry, "/sbin/")
}

// getUserShell returns the current user's login shell from /etc/passwd, or
// /bin/sh as a fallback. The shell server already runs as the remoteUser
// (docker exec -u), so no `su` is needed.
func getUserShell(s *ShellServer) string {
	out, code, err := s.Exec(`getent passwd "$(id -un)" 2>/dev/null | cut -d: -f7`)
	if err == nil && code == 0 {
		if sh := strings.TrimSpace(out); sh != "" {
			return sh
		}
	}
	return "/bin/sh"
}

func readEnvCache(s *ShellServer, strategy UserEnvProbeStrategy, sessionDataFolder string) map[string]string {
	cachePath := fmt.Sprintf("%s/env-%s.json", sessionDataFolder, strategy)
	stdout, code, err := s.Exec(fmt.Sprintf("cat '%s' 2>/dev/null", cachePath))
	if err != nil || code != 0 || stdout == "" {
		return nil
	}
	var env map[string]string
	if json.Unmarshal([]byte(stdout), &env) != nil {
		return nil
	}
	return env
}

func writeEnvCache(s *ShellServer, env map[string]string, strategy UserEnvProbeStrategy, sessionDataFolder string) {
	data, err := json.MarshalIndent(env, "", "\t")
	if err != nil {
		return
	}
	cachePath := fmt.Sprintf("%s/env-%s.json", sessionDataFolder, strategy)
	cmd := fmt.Sprintf("mkdir -p '%s' && cat > '%s' << 'ENVJSON'\n%s\nENVJSON", sessionDataFolder, cachePath, string(data))
	s.Exec(cmd)
}

// parseEnvOutput parses `env` command output into a map.
// Handles multi-line values (value continues if next line doesn't contain '=').
func parseEnvOutput(output string) map[string]string {
	env := make(map[string]string)
	lines := strings.Split(output, "\n")

	var currentKey, currentValue string
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}

		// Check if this line starts a new KEY=VALUE
		eqIdx := strings.IndexByte(line, '=')
		if eqIdx > 0 && !strings.ContainsAny(line[:eqIdx], " \t") {
			// Save previous entry
			if currentKey != "" {
				env[currentKey] = currentValue
			}
			currentKey = line[:eqIdx]
			currentValue = line[eqIdx+1:]
		} else if currentKey != "" {
			// Continuation of multi-line value
			currentValue += "\n" + line
		}
	}
	// Save last entry
	if currentKey != "" {
		env[currentKey] = currentValue
	}

	return env
}
