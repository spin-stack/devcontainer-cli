package lifecycle

import (
	"context"
	"fmt"
	"maps"
	"os/exec"
	"slices"
	"strings"
	"sync"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/log"
)

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

	if s, ok := cmd.AsString(); ok {
		resolved, err := resolve(s)
		if err != nil {
			return err
		}
		logger.Write(fmt.Sprintf("Running initializeCommand: %s", resolved), log.LevelInfo)
		return runHostExec(ctx, logger, []string{"/bin/sh", "-c", resolved}, workspaceFolder)
	}

	if arr, ok := cmd.AsStringSlice(); ok {
		for i, a := range arr {
			resolved, err := resolve(a)
			if err != nil {
				return err
			}
			arr[i] = resolved
		}
		logger.Write(fmt.Sprintf("Running initializeCommand: %s", strings.Join(arr, " ")), log.LevelInfo)
		return runHostExec(ctx, logger, arr, workspaceFolder)
	}

	if m, ok := cmd.AsMap(); ok {
		logger.Write(fmt.Sprintf("Running %d parallel initializeCommand(s)...", len(m)), log.LevelInfo)
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
				if err := runHostExec(ctx, logger, []string{"/bin/sh", "-c", resolved}, workspaceFolder); err != nil {
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

func runHostExec(ctx context.Context, logger log.Logger, args []string, workDir string) error {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = workDir
	out := rawLog{logger}
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

type rawLog struct{ logger log.Logger }

func (w rawLog) Write(p []byte) (int, error) {
	w.logger.Raw(string(p), log.LevelInfo)
	return len(p), nil
}
