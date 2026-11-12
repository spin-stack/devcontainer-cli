package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/devcontainers/cli/internal/log"
)

type UserEnvProbeStrategy string

const (
	ProbeNone                  UserEnvProbeStrategy = "none"
	ProbeLoginInteractiveShell UserEnvProbeStrategy = "loginInteractiveShell"
	ProbeInteractiveShell      UserEnvProbeStrategy = "interactiveShell"
	ProbeLoginShell            UserEnvProbeStrategy = "loginShell"
)

type shellExec interface {
	Exec(cmd string) (string, int, error)
}

func ProbeRemoteEnv(logger log.Logger, shellServer shellExec, strategy UserEnvProbeStrategy, remoteUser, sessionDataFolder string) (map[string]string, error) {
	if strategy == ProbeNone || strategy == "" {
		return map[string]string{}, nil
	}

	cacheDir := sessionDataFolder
	if cacheDir != "" {
		if cached := readEnvCache(shellServer, strategy, cacheDir); cached != nil {
			logger.Write(fmt.Sprintf("Using cached env probe (%s)", strategy), log.LevelTrace)
			return cached, nil
		}
	}

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
	delete(result, "PWD")

	if shellPath := result["PATH"]; shellPath != "" {
		if basePath, _, berr := shellServer.Exec(`printf %s "$PATH"`); berr == nil && strings.TrimSpace(basePath) != "" {
			isRoot := remoteUser == "" || remoteUser == "root" || remoteUser == "0"
			result["PATH"] = mergePaths(shellPath, strings.TrimSpace(basePath), isRoot)
		}
	}

	if cacheDir != "" && len(result) > 0 {
		writeEnvCache(shellServer, result, strategy, cacheDir)
	}

	return result, nil
}

func mergePaths(shellPath, containerPath string, rootUser bool) string {
	result := strings.Split(shellPath, ":")
	insertAt := 0
	for entry := range strings.SplitSeq(containerPath, ":") {
		if idx := slices.Index(result, entry); idx != -1 {
			insertAt = idx + 1
		} else if rootUser || !isSbinPath(entry) {
			result = slices.Insert(result, insertAt, entry)
			insertAt++
		}
	}
	return strings.Join(result, ":")
}

func isSbinPath(entry string) bool {
	return strings.HasSuffix(entry, "/sbin") || strings.Contains(entry, "/sbin/")
}

func getUserShell(s shellExec) string {
	out, code, err := s.Exec(`getent passwd "$(id -un)" 2>/dev/null | cut -d: -f7`)
	if err == nil && code == 0 {
		if sh := strings.TrimSpace(out); sh != "" {
			return sh
		}
	}
	return "/bin/sh"
}

func readEnvCache(s shellExec, strategy UserEnvProbeStrategy, sessionDataFolder string) map[string]string {
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

func writeEnvCache(s shellExec, env map[string]string, strategy UserEnvProbeStrategy, sessionDataFolder string) {
	data, err := json.MarshalIndent(env, "", "\t")
	if err != nil {
		return
	}
	cachePath := fmt.Sprintf("%s/env-%s.json", sessionDataFolder, strategy)
	cmd := fmt.Sprintf("mkdir -p '%s' && cat > '%s' << 'ENVJSON'\n%s\nENVJSON", sessionDataFolder, cachePath, string(data))
	_, _, _ = s.Exec(cmd)
}

func parseEnvOutput(output string) map[string]string {
	env := make(map[string]string)
	lines := strings.Split(output, "\n")

	var currentKey, currentValue string
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}

		eqIdx := strings.IndexByte(line, '=')
		if eqIdx > 0 && !strings.ContainsAny(line[:eqIdx], " \t") {
			if currentKey != "" {
				env[currentKey] = currentValue
			}
			currentKey = line[:eqIdx]
			currentValue = line[eqIdx+1:]
		} else if currentKey != "" {
			currentValue += "\n" + line
		}
	}
	if currentKey != "" {
		env[currentKey] = currentValue
	}

	return env
}

func ProbedRemoteEnv(ctx context.Context, exec ContainerExecutor, logger log.Logger, containerID, remoteUser string, strategy UserEnvProbeStrategy, sessionDataFolder string) map[string]string {
	if strategy == ProbeNone {
		return map[string]string{}
	}
	server := NewShellServer(ctx, exec, containerID, remoteUser)
	defer server.Close()
	env, err := ProbeRemoteEnv(logger, server, strategy, remoteUser, sessionDataFolder)
	if err != nil {
		logger.Write(log.ToErrorText(err.Error()), log.LevelInfo)
	}
	if env == nil {
		env = map[string]string{}
	}
	return env
}
