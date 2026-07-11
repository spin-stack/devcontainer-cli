package lifecycle

import (
	"fmt"
	"strings"
	"sync"

	"github.com/devcontainers/cli/internal/core/log"
	"github.com/devcontainers/cli/internal/imagemeta"
)

// Phase represents a lifecycle command phase.
type Phase string

const (
	PhaseInitialize    Phase = "initializeCommand"
	PhaseOnCreate      Phase = "onCreateCommand"
	PhaseUpdateContent Phase = "updateContentCommand"
	PhasePostCreate    Phase = "postCreateCommand"
	PhasePostStart     Phase = "postStartCommand"
	PhasePostAttach    Phase = "postAttachCommand"
)

// AllPhases returns lifecycle phases in execution order.
func AllPhases() []Phase {
	return []Phase{
		PhaseInitialize,
		PhaseOnCreate,
		PhaseUpdateContent,
		PhasePostCreate,
		PhasePostStart,
		PhasePostAttach,
	}
}

// RunOptions controls which lifecycle phases to execute.
type RunOptions struct {
	SkipPostCreate  bool
	SkipNonBlocking bool
	SkipPostAttach  bool
	Prebuild        bool
	WaitFor         Phase // stop after this phase; default: updateContentCommand
	// AfterPostCreate, if set, runs once after the postCreateCommand phase and
	// before postStartCommand — matching where the TS CLI installs dotfiles.
	// Dotfiles failures are non-fatal, so it returns no error.
	AfterPostCreate func()
}

// CommandExecutor runs a shell command inside a container.
type CommandExecutor interface {
	Exec(command string) error
}

// HookError records which lifecycle phase failed while preserving the
// underlying command error for CLI-specific error envelopes.
type HookError struct {
	Phase Phase
	Name  string
	Err   error
}

func (e *HookError) Error() string {
	if e.Name != "" {
		return fmt.Sprintf("%s failed: %s: %v", e.Phase, e.Name, e.Err)
	}
	return fmt.Sprintf("%s failed: %v", e.Phase, e.Err)
}

func (e *HookError) Unwrap() error {
	return e.Err
}

// HookRunner runs lifecycle hooks against a merged config.
func RunHooks(
	logger log.Log,
	executor CommandExecutor,
	merged *imagemeta.MergedConfig,
	opts RunOptions,
) error {
	waitFor := opts.WaitFor
	if waitFor == "" {
		if merged.WaitFor != "" {
			waitFor = Phase(merged.WaitFor)
		} else {
			waitFor = PhaseUpdateContent
		}
	}

	phases := []struct {
		phase    Phase
		commands []interface{}
	}{
		{PhaseOnCreate, merged.OnCreateCommands},
		{PhaseUpdateContent, merged.UpdateContentCommands},
		{PhasePostCreate, merged.PostCreateCommands},
		{PhasePostStart, merged.PostStartCommands},
		{PhasePostAttach, merged.PostAttachCommands},
	}

	for _, p := range phases {
		if opts.SkipPostCreate && isPostCreatePhase(p.phase) {
			logger.Write(fmt.Sprintf("Skipping %s (skipPostCreate)", p.phase), log.LevelInfo)
			continue
		}
		if opts.SkipPostAttach && p.phase == PhasePostAttach {
			logger.Write(fmt.Sprintf("Skipping %s (skipPostAttach)", p.phase), log.LevelInfo)
			continue
		}

		if len(p.commands) > 0 {
			logger.Event(log.Event{
				Type:   "progress",
				Name:   "Running " + string(p.phase) + "...",
				Status: "running",
			})

			for _, cmd := range p.commands {
				if err := executeCommand(executor, logger, p.phase, cmd); err != nil {
					logger.Event(log.Event{
						Type:   "progress",
						Name:   "Running " + string(p.phase) + "...",
						Status: "failed",
					})
					return err
				}
			}

			logger.Event(log.Event{
				Type:   "progress",
				Name:   "Running " + string(p.phase) + "...",
				Status: "succeeded",
			})
		}

		// Install dotfiles after postCreateCommand, before postStartCommand.
		if p.phase == PhasePostCreate && opts.AfterPostCreate != nil {
			opts.AfterPostCreate()
		}

		// Check stop conditions
		if opts.Prebuild && (p.phase == PhaseOnCreate || p.phase == PhaseUpdateContent) {
			if p.phase == PhaseUpdateContent {
				logger.Write("Stopping after updateContentCommand (prebuild)", log.LevelInfo)
				return nil
			}
		}
		if opts.SkipNonBlocking && p.phase == waitFor {
			logger.Write(fmt.Sprintf("Stopping after %s (skipNonBlocking)", waitFor), log.LevelInfo)
			return nil
		}
	}

	return nil
}

func isPostCreatePhase(p Phase) bool {
	return p == PhaseOnCreate || p == PhaseUpdateContent ||
		p == PhasePostCreate || p == PhasePostStart || p == PhasePostAttach
}

// executeCommand runs a single lifecycle command entry. If the command is a
// map (object syntax), entries are executed in parallel matching TS behavior
// (Promise.allSettled). All commands run to completion; errors are collected.
func executeCommand(executor CommandExecutor, logger log.Log, phase Phase, cmd interface{}) error {
	m, isMap := cmd.(map[string]interface{})
	if !isMap {
		cmdStr := commandToString(cmd)
		if cmdStr == "" {
			return nil
		}
		logger.Write(fmt.Sprintf("Running %s: %s", phase, cmdStr), log.LevelInfo)
		if err := executor.Exec(cmdStr); err != nil {
			return &HookError{Phase: phase, Err: err}
		}
		return nil
	}

	// Parallel execution for object syntax, matching TS Promise.allSettled.
	type result struct {
		name string
		err  error
	}
	var wg sync.WaitGroup
	results := make(chan result, len(m))

	for name, val := range m {
		cmdStr := commandToString(val)
		if cmdStr == "" {
			continue
		}
		wg.Add(1)
		go func(name, cmdStr string) {
			defer wg.Done()
			logger.Write(fmt.Sprintf("Running %s (%s): %s", phase, name, cmdStr), log.LevelInfo)
			results <- result{name: name, err: executor.Exec(cmdStr)}
		}(name, cmdStr)
	}

	wg.Wait()
	close(results)

	var errs []string
	for r := range results {
		if r.err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", r.name, r.err))
		}
	}
	if len(errs) > 0 {
		return &HookError{Phase: phase, Err: fmt.Errorf("%s", strings.Join(errs, "; "))}
	}
	return nil
}

// commandToString converts a lifecycle command value to a shell string.
func commandToString(cmd interface{}) string {
	switch v := cmd.(type) {
	case string:
		return v
	case []interface{}:
		// Array form is argv: execute the arguments directly without shell
		// interpretation (no re-tokenization, no variable expansion). Since the
		// executor runs through /bin/sh -c, shell-quote each argument to preserve
		// argv semantics — matching the devcontainer spec and the TS CLI.
		parts := make([]string, len(v))
		for i, p := range v {
			parts[i] = shellQuote(fmt.Sprintf("%v", p))
		}
		return strings.Join(parts, " ")
	case map[string]interface{}:
		// Maps are handled by executeCommand for parallel execution.
		// This case only fires if commandToString is called recursively
		// from within a map value.
		var result string
		for _, val := range v {
			s := commandToString(val)
			if s != "" {
				if result != "" {
					result += " && "
				}
				result += s
			}
		}
		return result
	default:
		if cmd == nil {
			return ""
		}
		return fmt.Sprintf("%v", cmd)
	}
}

// shellQuote wraps a string in single quotes for POSIX sh, escaping any
// embedded single quotes as '\” so the value passes through /bin/sh -c as a
// single literal argument (no expansion, no word splitting).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
