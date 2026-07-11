//go:build !windows

package lifecycle

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
)

// ExecWithPTY runs a command with a pseudo-terminal, forwarding stdin/stdout
// and handling window resize signals. Used by `devcontainer exec` for
// interactive sessions.
func ExecWithPTY(dockerPath string, args []string) (exitCode int, err error) {
	cmd := exec.Command(dockerPath, args...)

	// Start the command with a pty
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return -1, fmt.Errorf("start pty: %w", err)
	}
	defer ptmx.Close()

	// Handle window resize
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	defer func() {
		signal.Stop(ch)
		close(ch)
	}()
	go func() {
		for range ch {
			if err := pty.InheritSize(os.Stdin, ptmx); err != nil {
				// ignore resize errors
			}
		}
	}()
	ch <- syscall.SIGWINCH // Initial resize

	// Set raw mode on stdin
	oldState, err := makeRaw(os.Stdin.Fd())
	if err == nil {
		defer restoreTerminal(os.Stdin.Fd(), oldState)
	}

	// Copy stdin → ptmx
	go func() {
		io.Copy(ptmx, os.Stdin)
	}()

	// Copy ptmx → stdout
	io.Copy(os.Stdout, ptmx)

	// Wait for command to finish
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}

	return 0, nil
}
