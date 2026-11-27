//go:build linux

package main

import "golang.org/x/sys/unix"

func flushLeaseInput(fd int) {
	_ = unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)
}
