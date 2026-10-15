//go:build !linux

package udp

import "net"

func recvErrors(*net.UDPConn, int) {}

func (x *session) drainErrors() int { return 0 }
