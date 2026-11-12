package exec

import (
	"bytes"
	"context"
	"io"
	"os"
	osexec "os/exec"
	"sync"
)

type Runner interface {
	Run(ctx context.Context, stream io.Writer, name string, args ...string) (stdout, stderr []byte, code int, err error)
}

type OSRunner struct {
	Env []string
}

func (r OSRunner) Run(ctx context.Context, stream io.Writer, name string, args ...string) ([]byte, []byte, int, error) {
	cmd := osexec.CommandContext(ctx, name, args...)
	if len(r.Env) > 0 {
		cmd.Env = append(os.Environ(), r.Env...)
	}

	var stdout, stderr bytes.Buffer
	// os/exec copies stdout and stderr on two separate goroutines. Each has its
	// own capture buffer (no sharing there), but both tee to the single `stream`
	// writer, so that shared writer must be serialized or the two goroutines race.
	live := stream
	if stream != nil {
		live = &syncWriter{w: stream}
	}
	cmd.Stdout = tee(&stdout, live)
	cmd.Stderr = tee(&stderr, live)

	err := cmd.Run()
	if err != nil {
		// CommandContext normally reports the signal used to kill the child as an
		// *exec.ExitError. Preserve the more useful cancellation/deadline cause at
		// this process boundary so callers can reliably use errors.Is.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stdout.Bytes(), stderr.Bytes(), -1, ctxErr
		}
		if exitErr, ok := err.(*osexec.ExitError); ok {
			return stdout.Bytes(), stderr.Bytes(), exitErr.ExitCode(), nil
		}
		return stdout.Bytes(), stderr.Bytes(), -1, err
	}
	return stdout.Bytes(), stderr.Bytes(), 0, nil
}

func tee(buf *bytes.Buffer, live io.Writer) io.Writer {
	if live == nil {
		return buf
	}
	return io.MultiWriter(buf, live)
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
