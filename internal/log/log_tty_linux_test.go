package log

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"syscall"
	"testing"
	"unsafe"
)

// With stdout a terminal the log is TS's terminal log, colored and ending its
// lines in \r\n (createLogFrom picks createTerminalLog when
// process.stdout.isTTY), and a line's [N ms] counts from New when no
// StartTime is given.
func TestNewWritesTheTerminalLogWhenStdoutIsATerminal(t *testing.T) {
	saved := os.Stdout
	os.Stdout = openPTY(t)
	t.Cleanup(func() { os.Stdout = saved })

	var buf bytes.Buffer
	New(Options{Writer: &buf}).Write("x", LevelInfo)
	m := regexp.MustCompile(`^\[\x1b\[1m\x1b\[38;2;99;143;79m(\d+) ms\x1b\[39m\x1b\[22m\] x\r\n$`).FindStringSubmatch(buf.String())
	if m == nil {
		t.Fatalf("output = %q, want the terminal log line", buf.String())
	}
	if ms, _ := strconv.ParseInt(m[1], 10, 64); ms > 60_000 {
		t.Errorf("elapsed = %d ms, want the time since New", ms)
	}
}

// openPTY opens a pseudo-terminal and returns its terminal side.
func openPTY(t *testing.T) *os.File {
	t.Helper()
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { _ = ptmx.Close() })
	var unlock int32
	if err := ioctl(ptmx, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		t.Fatalf("unlock pty: %v", err)
	}
	var n uint32
	if err := ioctl(ptmx, syscall.TIOCGPTN, unsafe.Pointer(&n)); err != nil {
		t.Fatalf("pty number: %v", err)
	}
	pts, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pts.Close() })
	return pts
}

func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}
