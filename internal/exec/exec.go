// Package exec provides a small seam over running external processes so callers
// can inject a fake in tests instead of shelling out to a real binary.
package exec

import (
	"bytes"
	"context"
	"io"
	"os"
	osexec "os/exec"
)

// Runner runs an external command to completion, capturing its stdout and
// stderr. When stream is non-nil, the child's combined output is also written
// there as it is produced, so callers can show live progress for long-running
// commands. It returns the process exit code and an error ONLY when the command
// could not be run at all (binary not found, context cancelled, ...). A command
// that runs and exits non-zero returns that exit code with a nil error — the
// caller inspects code to decide success, matching the existing docker CLI
// wrappers.
type Runner interface {
	Run(ctx context.Context, stream io.Writer, name string, args ...string) (stdout, stderr []byte, code int, err error)
}

// OSRunner is the default Runner, backed by os/exec. Env, when non-empty, is
// appended to the current process environment for the child (matching the
// existing docker.Client/ComposeClient behavior).
type OSRunner struct {
	Env []string
}

// Run implements Runner using os/exec.
func (r OSRunner) Run(ctx context.Context, stream io.Writer, name string, args ...string) ([]byte, []byte, int, error) {
	cmd := osexec.CommandContext(ctx, name, args...)
	if len(r.Env) > 0 {
		cmd.Env = append(os.Environ(), r.Env...)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = tee(&stdout, stream)
	cmd.Stderr = tee(&stderr, stream)

	err := cmd.Run()
	if err != nil {
		// CommandContext normally reports the signal used to kill the child as an
		// *exec.ExitError. Preserve the more useful cancellation/deadline cause at
		// this process boundary so callers can reliably use errors.Is.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stdout.Bytes(), stderr.Bytes(), -1, ctxErr
		}
		if exitErr, ok := err.(*osexec.ExitError); ok {
			// Ran and exited non-zero: report the code, not an error.
			return stdout.Bytes(), stderr.Bytes(), exitErr.ExitCode(), nil
		}
		// Could not run (not found, cancelled, ...).
		return stdout.Bytes(), stderr.Bytes(), -1, err
	}
	return stdout.Bytes(), stderr.Bytes(), 0, nil
}

// tee returns a writer that always fills buf (so the output is captured for the
// return value) and, when live is non-nil, also forwards to live for streaming.
func tee(buf *bytes.Buffer, live io.Writer) io.Writer {
	if live == nil {
		return buf
	}
	return io.MultiWriter(buf, live)
}
