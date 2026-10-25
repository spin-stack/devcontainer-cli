package lifecycle

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
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
func RunInitializeCommand(ctx context.Context, logger log.Logger, cmd *config.LifecycleCommand, hostSub config.HostSubContext) error {
	if cmd == nil || cmd.IsEmpty() {
		return nil
	}
	workspaceFolder := hostSub.LocalWorkspaceFolder
	resolver := config.NewVariableResolver()
	resolve := func(value string) (string, error) {
		resolved, err := resolver.Resolve(config.SubstitutionContext{HostSubContext: hostSub}, config.PhaseHost, value)
		if err != nil {
			return "", err
		}
		result, ok := resolved.(string)
		if !ok {
			return "", fmt.Errorf("host substitution returned %T for command string", resolved)
		}
		return result, nil
	}

	// String form
	if s, ok := cmd.AsString(); ok {
		resolved, err := resolve(s)
		if err != nil {
			return err
		}
		logger.Write(fmt.Sprintf("Running initializeCommand: %s", resolved), log.LevelInfo)
		return runHostExec(ctx, []string{"/bin/sh", "-c", resolved}, workspaceFolder)
	}

	// Array form
	if arr, ok := cmd.AsStringSlice(); ok {
		for i, a := range arr {
			resolved, err := resolve(a)
			if err != nil {
				return err
			}
			arr[i] = resolved
		}
		logger.Write(fmt.Sprintf("Running initializeCommand: %s", strings.Join(arr, " ")), log.LevelInfo)
		return runHostExec(ctx, arr, workspaceFolder)
	}

	// Object form (parallel commands)
	if m, ok := cmd.AsMap(); ok {
		logger.Write(fmt.Sprintf("Running %d parallel initializeCommand(s)...", len(m)), log.LevelInfo)
		// All commands run to completion; the error is the first failure in name
		// order, as executeCommand reports a parallel hook's.
		names := slices.Sorted(maps.Keys(m))
		errs := make([]error, len(names))
		var wg sync.WaitGroup
		for i, name := range names {
			cmdStr, ok := m[name].(string)
			if !ok {
				continue
			}
			wg.Go(func() {
				resolved, err := resolve(cmdStr)
				if err != nil {
					errs[i] = fmt.Errorf("%s: %w", name, err)
					return
				}
				logger.Write(fmt.Sprintf("  [%s] %s", name, resolved), log.LevelInfo)
				if err := runHostExec(ctx, []string{"/bin/sh", "-c", resolved}, workspaceFolder); err != nil {
					errs[i] = fmt.Errorf("%s: %w", name, err)
				}
			})
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				return err
			}
		}
		return nil
	}

	return nil
}

func runHostExec(ctx context.Context, args []string, workDir string) error {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
