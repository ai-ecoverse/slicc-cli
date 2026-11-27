//go:build darwin

package main

import "golang.org/x/sys/unix"

func termiosForceEcho(fd int) error {
	st, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return err
	}
	st.Lflag |= unix.ECHO
	return unix.IoctlSetTermios(fd, unix.TIOCSETA, st)
}
