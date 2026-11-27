package ctl

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func peerIsUs(c net.Conn) bool {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return false
	}
	var cred *unix.Xucred
	var cerr error
	if err := raw.Control(func(fd uintptr) { cred, cerr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED) }); err != nil || cerr != nil {
		return false
	}
	return int(cred.Uid) == os.Getuid()
}

const PeerPIDs = true

func peerPID(c net.Conn) (int, bool) {
	if !peerIsUs(c) {
		return 0, false
	}
	raw, err := c.(*net.UnixConn).SyscallConn()
	if err != nil {
		return 0, false
	}
	var pid int
	var perr error
	if err := raw.Control(func(fd uintptr) { pid, perr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID) }); err != nil || perr != nil {
		return 0, false
	}
	return pid, pid > 0
}
