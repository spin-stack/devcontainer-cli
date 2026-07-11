//go:build !windows

package lifecycle

import (
	"golang.org/x/sys/unix"
)

// makeRaw puts the terminal into raw mode and returns the previous state.
func makeRaw(fd uintptr) (*unix.Termios, error) {
	termios, err := unix.IoctlGetTermios(int(fd), ioctlReadTermios)
	if err != nil {
		return nil, err
	}

	oldState := *termios

	// Set raw mode
	termios.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	termios.Oflag &^= unix.OPOST
	termios.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	termios.Cflag &^= unix.CSIZE | unix.PARENB
	termios.Cflag |= unix.CS8
	termios.Cc[unix.VMIN] = 1
	termios.Cc[unix.VTIME] = 0

	if err := unix.IoctlSetTermios(int(fd), ioctlWriteTermios, termios); err != nil {
		return nil, err
	}

	return &oldState, nil
}

// restoreTerminal restores the terminal to a previous state.
func restoreTerminal(fd uintptr, state *unix.Termios) error {
	return unix.IoctlSetTermios(int(fd), ioctlWriteTermios, state)
}
