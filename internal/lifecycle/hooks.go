// Package lifecycle runs dev container lifecycle commands (hooks) and user-env probes.
package lifecycle

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
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

// HookError records which lifecycle command failed - its phase, its name when
// it is one of an object's parallel commands, and the Feature id or
// "devcontainer.json" it comes from - while preserving the underlying command
// error for CLI-specific error envelopes.
type HookError struct {
	Phase  Phase
	Name   string
	Origin string
	Err    error
}

// Description is TS runLifecycleCommand's error description:
// "[<name> of ]<hook> from <devcontainer.json | Feature '<id>'> failed."
func (e *HookError) Description() string {
	return e.label() + " from " + displayOrigin(e.Origin) + " failed."
}

func (e *HookError) label() string {
	if e.Name != "" {
		return e.Name + " of " + string(e.Phase)
	}
	return string(e.Phase)
}

// displayOrigin is how TS runLifecycleCommands names a command's origin.
func displayOrigin(origin string) string {
	switch origin {
	case "":
		return "devcontainer.json"
	case "devcontainer.json":
		return origin
	}
	return "Feature '" + origin + "'"
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
	logger log.Logger,
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
		commands []any
	}{
		{PhaseOnCreate, merged.OnCreateCommands},
		{PhaseUpdateContent, merged.UpdateContentCommands},
		{PhasePostCreate, merged.PostCreateCommands},
		{PhasePostStart, merged.PostStartCommands},
		{PhasePostAttach, merged.PostAttachCommands},
	}

	for _, p := range phases {
		if opts.SkipPostCreate {
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

			for i, cmd := range p.commands {
				var origin string
				if origins := merged.CommandOrigins[string(p.phase)]; i < len(origins) {
					origin = origins[i]
				}
				if err := executeCommand(executor, logger, p.phase, cmd, origin); err != nil {
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
		if opts.Prebuild && p.phase == PhaseUpdateContent {
			logger.Write("Stopping after updateContentCommand (prebuild)", log.LevelInfo)
			return nil
		}
		if opts.SkipNonBlocking && p.phase == waitFor {
			logger.Write(fmt.Sprintf("Stopping after %s (skipNonBlocking)", waitFor), log.LevelInfo)
			return nil
		}
	}

	return nil
}

// executeCommand runs a single lifecycle command entry. If the command is a
// map (object syntax), entries are executed in parallel matching TS behavior
// (Promise.allSettled): all commands run to completion, and the error names the
// first that failed. TS takes the first in the object's key order; the decoded
// map has none, so it is the first in sorted order (docs/DIVERGENCES.md).
func executeCommand(executor CommandExecutor, logger log.Logger, phase Phase, cmd any, origin string) error {
	from := displayOrigin(origin)
	m, isMap := cmd.(map[string]any)
	if !isMap {
		cmdStr := commandToString(cmd)
		if cmdStr == "" {
			return nil
		}
		logger.Write(fmt.Sprintf("Running the %s from %s...", phase, from), log.LevelInfo)
		if err := executor.Exec(cmdStr); err != nil {
			he := &HookError{Phase: phase, Origin: origin, Err: withArgv(err, cmd)}
			logFailure(logger, he)
			return he
		}
		return nil
	}

	logger.Write(fmt.Sprintf("Running the %s from %s...", phase, from), log.LevelInfo)
	names := slices.Sorted(maps.Keys(m))
	errs := make([]error, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		cmdStr := commandToString(m[name])
		if cmdStr == "" {
			continue
		}
		wg.Go(func() {
			// TS prints a parallel command's header with its output once it succeeded.
			if errs[i] = withArgv(executor.Exec(cmdStr), m[name]); errs[i] == nil {
				logger.Write(fmt.Sprintf("Running %s of %s from %s...", name, phase, from), log.LevelInfo)
			}
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			he := &HookError{Phase: phase, Name: names[i], Origin: origin, Err: err}
			logFailure(logger, he)
			return he
		}
	}
	return nil
}

// withArgv records an array command's arguments on its *CommandError, so the
// error names them as TS does; err is returned as it is otherwise.
func withArgv(err error, cmd any) error {
	args, isArray := cmd.([]any)
	if ce, ok := errors.AsType[*CommandError](err); ok && isArray {
		ce.Argv = make([]string, len(args))
		for i, a := range args {
			ce.Argv[i] = fmt.Sprintf("%v", a)
		}
	}
	return err
}

// logFailure writes TS's line for a command that exited non-zero.
func logFailure(logger log.Logger, he *HookError) {
	if ce, ok := errors.AsType[*CommandError](he.Err); ok && ce.ExitCode != 0 {
		logger.Write(fmt.Sprintf("%s from %s failed with exit code %d. Skipping any further user-provided commands.", he.label(), displayOrigin(he.Origin), ce.ExitCode), log.LevelError)
	}
}

// commandToString converts a lifecycle command value to a shell string.
func commandToString(cmd any) string {
	switch v := cmd.(type) {
	case string:
		return v
	case []any:
		// Array form is argv: execute the arguments directly without shell
		// interpretation (no re-tokenization, no variable expansion). Since the
		// executor runs through /bin/sh -c, shell-quote each argument to preserve
		// argv semantics — matching the devcontainer spec and the TS CLI.
		parts := make([]string, len(v))
		for i, p := range v {
			parts[i] = shellQuote(fmt.Sprintf("%v", p))
		}
		return strings.Join(parts, " ")
	case map[string]any:
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
