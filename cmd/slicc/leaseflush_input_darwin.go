//go:build darwin

package main

import "golang.org/x/sys/unix"

func flushLeaseInput(fd int) {
	_ = unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, 1)
}
