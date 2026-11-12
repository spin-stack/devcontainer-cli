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

type Phase string

const (
	PhaseInitialize    Phase = "initializeCommand"
	PhaseOnCreate      Phase = "onCreateCommand"
	PhaseUpdateContent Phase = "updateContentCommand"
	PhasePostCreate    Phase = "postCreateCommand"
	PhasePostStart     Phase = "postStartCommand"
	PhasePostAttach    Phase = "postAttachCommand"
)

type RunOptions struct {
	SkipPostCreate  bool
	SkipNonBlocking bool
	SkipPostAttach  bool
	Prebuild        bool
	WaitFor         Phase
	AfterPostCreate func()
}

type CommandExecutor interface {
	Exec(command string) error
}

type HookError struct {
	Phase  Phase
	Name   string
	Origin string
	Err    error
}

func (e *HookError) Description() string {
	return e.label() + " from " + displayOrigin(e.Origin) + " failed."
}

func (e *HookError) label() string {
	if e.Name != "" {
		return e.Name + " of " + string(e.Phase)
	}
	return string(e.Phase)
}

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

		if p.phase == PhasePostCreate && opts.AfterPostCreate != nil {
			opts.AfterPostCreate()
		}

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

func logFailure(logger log.Logger, he *HookError) {
	if ce, ok := errors.AsType[*CommandError](he.Err); ok && ce.ExitCode != 0 {
		logger.Write(fmt.Sprintf("%s from %s failed with exit code %d. Skipping any further user-provided commands.", he.label(), displayOrigin(he.Origin), ce.ExitCode), log.LevelError)
	}
}

func commandToString(cmd any) string {
	switch v := cmd.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, len(v))
		for i, p := range v {
			parts[i] = shellQuote(fmt.Sprintf("%v", p))
		}
		return strings.Join(parts, " ")
	case map[string]any:
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

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
