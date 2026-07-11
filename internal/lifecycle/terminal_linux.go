//go:build linux

package lifecycle

import "golang.org/x/sys/unix"

// ioctl requests for reading/writing terminal attributes on Linux.
// (macOS/BSD use TIOCGETA/TIOCSETA; see terminal_bsd.go.)
const (
	ioctlReadTermios  = unix.TCGETS
	ioctlWriteTermios = unix.TCSETS
)
