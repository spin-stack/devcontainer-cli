//go:build linux

package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/devcontainers/cli/internal/doctor"
)

// openPTY opens a pseudo-terminal: what is written to the returned terminal
// can be read back from the returned master.
func openPTY(t *testing.T) (master, terminal *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { master.Close() })
	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		t.Fatalf("unlock pty: %v", errno)
	}
	var n uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); errno != 0 {
		t.Fatalf("pty number: %v", errno)
	}
	terminal, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { terminal.Close() })
	return master, terminal
}

type terminalStderr struct{ terminal *os.File }

func (terminalStderr) Stdout() io.Writer   { return io.Discard }
func (o terminalStderr) Stderr() io.Writer { return o.terminal }

// warnOnTerminal runs warnUncheckedHost with a terminal for stderr and the
// check state under stateHome, and returns what it wrote there.
func warnOnTerminal(t *testing.T, stateHome string) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", stateHome)
	master, terminal := openPTY(t)
	warnUncheckedHost(terminalStderr{terminal})
	const end = "END-OF-OUTPUT"
	fmt.Fprintln(terminal, end)
	var got strings.Builder
	r := bufio.NewReader(master)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read terminal: %v (read %q)", err, got.String())
		}
		line = strings.TrimRight(line, "\r\n")
		if line == end {
			return got.String()
		}
		got.WriteString(line + "\n")
	}
}

func saveCheckState(t *testing.T, overall doctor.Status) string {
	t.Helper()
	home := t.TempDir()
	if err := doctor.SaveTo(filepath.Join(home, "devcontainer", "check.json"), doctor.Report{Overall: overall}); err != nil {
		t.Fatal(err)
	}
	return home
}

// On a terminal, up and build hint at `check` when the host was never checked,
// warn when the last check failed, and say nothing after a passing check or
// when the state cannot be read.
func TestWarnUncheckedHostOnATerminal(t *testing.T) {
	unreadable := t.TempDir()
	if err := os.MkdirAll(filepath.Join(unreadable, "devcontainer", "check.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		state string
		want  string
	}{
		{"never checked", t.TempDir(), "hint: host not yet checked — run `devcontainer check` to verify your Docker setup.\n"},
		{"failing host", saveCheckState(t, doctor.StatusFail), "warning: `devcontainer check` reported a failing host configuration — run `devcontainer check` for details.\n"},
		{"passing host", saveCheckState(t, doctor.StatusOK), ""},
		{"unreadable state", unreadable, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := warnOnTerminal(t, tc.state); got != tc.want {
				t.Errorf("stderr = %q, want %q", got, tc.want)
			}
		})
	}
}
