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
