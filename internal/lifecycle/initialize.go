package lifecycle

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/log"
)

// RunInitializeCommand executes the initializeCommand on the host machine.
// Supports three forms:
//   - string: run via /bin/sh -c
//   - []string: exec directly (first element is command, rest are args)
//   - map[string]interface{}: run each value as a string command (parallel)
func RunInitializeCommand(ctx context.Context, logger log.Logger, cmd *config.LifecycleCommand, workspaceFolder string) error {
	if cmd == nil || cmd.IsEmpty() {
		return nil
	}

	// String form
	if s, ok := cmd.AsString(); ok {
		resolved := substituteLocalVars(s, workspaceFolder)
		logger.Write(fmt.Sprintf("Running initializeCommand: %s", resolved), log.LevelInfo)
		return runHostCommand(ctx, resolved, workspaceFolder)
	}

	// Array form
	if arr, ok := cmd.AsStringSlice(); ok {
		for i, a := range arr {
			arr[i] = substituteLocalVars(a, workspaceFolder)
		}
		logger.Write(fmt.Sprintf("Running initializeCommand: %s", strings.Join(arr, " ")), log.LevelInfo)
		return runHostExec(ctx, arr, workspaceFolder)
	}

	// Object form (parallel commands)
	if m, ok := cmd.AsMap(); ok {
		logger.Write(fmt.Sprintf("Running %d parallel initializeCommand(s)...", len(m)), log.LevelInfo)
		var wg sync.WaitGroup
		var errs []error
		var mu sync.Mutex
		for name, c := range m {
			cmdStr, ok := c.(string)
			if !ok {
				continue
			}
			wg.Add(1)
			go func(name, cmdStr string) {
				defer wg.Done()
				resolved := substituteLocalVars(cmdStr, workspaceFolder)
				logger.Write(fmt.Sprintf("  [%s] %s", name, resolved), log.LevelInfo)
				if err := runHostCommand(ctx, resolved, workspaceFolder); err != nil {
					mu.Lock()
					errs = append(errs, fmt.Errorf("%s: %w", name, err))
					mu.Unlock()
				}
			}(name, cmdStr)
		}
		wg.Wait()
		if len(errs) > 0 {
			return errs[0]
		}
		return nil
	}

	return nil
}

func substituteLocalVars(s string, workspaceFolder string) string {
	s = strings.ReplaceAll(s, "${localWorkspaceFolder}", workspaceFolder)
	s = strings.ReplaceAll(s, "${localWorkspaceFolderBasename}", filepath.Base(workspaceFolder))
	return s
}

func runHostCommand(ctx context.Context, command string, workDir string) error {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runHostExec(ctx context.Context, args []string, workDir string) error {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
