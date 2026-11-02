//go:build unix

package screen

import (
	"golang.org/x/sys/unix"
	"io"
	"net"
	"syscall"
)

type privateConn struct{ *net.UnixConn }

func (c privateConn) Read(p []byte) (int, error) {
	raw, e := c.SyscallConn()
	if e != nil {
		return 0, e
	}
	var n, on, flags int
	var readErr error
	var oob [256]byte
	e = raw.Read(func(fd uintptr) bool {
		syscall.ForkLock.RLock()
		defer syscall.ForkLock.RUnlock()
		n, on, flags, _, readErr = unix.Recvmsg(int(fd), p, oob[:], unix.MSG_DONTWAIT)
		if readErr == unix.EAGAIN || readErr == unix.EWOULDBLOCK {
			return false
		}
		if on > 0 {
			if messages, pe := unix.ParseSocketControlMessage(oob[:on]); pe == nil {
				for _, m := range messages {
					if fds, fe := unix.ParseUnixRights(&m); fe == nil {
						for _, received := range fds {
							_ = unix.Close(received)
						}
					}
				}
			}
			readErr = ErrAccess
			n = 0
		}
		if flags&unix.MSG_CTRUNC != 0 {
			readErr = ErrAccess
			n = 0
		}
		return true
	})
	if e != nil {
		return 0, e
	}
	if n == 0 && readErr == nil {
		return 0, io.EOF
	}
	return n, readErr
}
