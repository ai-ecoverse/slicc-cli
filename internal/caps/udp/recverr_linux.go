//go:build linux

package udp

import (
	"encoding/binary"
	"net"
	"net/netip"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	originICMP  = 2
	originICMP6 = 3
)

func recvErrors(c *net.UDPConn, family int) {
	rc, err := c.SyscallConn()
	if err != nil {
		return
	}
	_ = rc.Control(func(fd uintptr) {
		if family == 6 {
			_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_RECVERR, 1)
			return
		}
		_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_RECVERR, 1)
	})
}

type queued struct {
	to     netip.AddrPort
	reason string
}

func (x *session) drainErrors() int {
	rc, err := x.c.SyscallConn()
	if err != nil {
		return 0
	}
	var got []queued
	count := 0
	buf, oob := make([]byte, 64), make([]byte, 512)
	_ = rc.Control(func(fd uintptr) {
		for {
			_, oobn, _, from, err := unix.Recvmsg(int(fd), buf, oob, unix.MSG_ERRQUEUE|unix.MSG_DONTWAIT)
			if err != nil {
				return
			}
			count++
			if q, ok := parseQueued(oob[:oobn], from); ok {
				got = append(got, q)
			}
		}
	})
	for _, q := range got {
		if x.known(q.to) {
			x.o.Logf("udp %s: %s: %s", x.name, q.to, q.reason)
			x.unreachable(q.to, q.reason)
		}
	}
	return count
}

func parseQueued(oob []byte, from unix.Sockaddr) (queued, bool) {
	var to netip.AddrPort
	switch sa := from.(type) {
	case *unix.SockaddrInet4:
		to = netip.AddrPortFrom(netip.AddrFrom4(sa.Addr), uint16(sa.Port))
	case *unix.SockaddrInet6:
		to = netip.AddrPortFrom(netip.AddrFrom16(sa.Addr).Unmap(), uint16(sa.Port))
	default:
		return queued{}, false
	}
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return queued{}, false
	}
	for _, m := range msgs {
		v4 := m.Header.Level == unix.IPPROTO_IP && m.Header.Type == unix.IP_RECVERR
		v6 := m.Header.Level == unix.IPPROTO_IPV6 && m.Header.Type == unix.IPV6_RECVERR
		if !v4 && !v6 || len(m.Data) < 8 {
			continue
		}
		if origin := m.Data[4]; origin != originICMP && origin != originICMP6 {
			continue
		}
		errno := syscall.Errno(binary.NativeEndian.Uint32(m.Data))
		return queued{to: to, reason: sendErrno(errno) + ": " + errno.Error()}, true
	}
	return queued{}, false
}
