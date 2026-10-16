//go:build !linux

package udp

import "net"

func recvErrors(*net.UDPConn, int) {}

func drainErrors(*session) int { return 0 }
