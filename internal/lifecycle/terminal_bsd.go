//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package lifecycle

import "golang.org/x/sys/unix"

// ioctl requests for reading/writing terminal attributes on macOS/BSD.
// (Linux uses TCGETS/TCSETS; see terminal_linux.go.)
const (
	ioctlReadTermios  = unix.TIOCGETA
	ioctlWriteTermios = unix.TIOCSETA
)
