//go:build !windows

package fs

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func sysErrnoName(e syscall.Errno) string { return unix.ErrnoName(e) }
