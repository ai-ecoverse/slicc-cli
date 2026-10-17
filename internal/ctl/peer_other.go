//go:build unix && !linux && !darwin

package ctl

import "net"

func peerIsUs(net.Conn) bool { return false }
