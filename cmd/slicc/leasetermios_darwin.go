//go:build darwin

package main

import "golang.org/x/sys/unix"

func termiosEcho(fd int) (bool, error) {
	st, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return false, err
	}
	return st.Lflag&unix.ECHO != 0, nil
}
