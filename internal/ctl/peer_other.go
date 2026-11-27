//go:build unix && !linux && !darwin

package ctl

import "net"

func peerIsUs(net.Conn) bool { return false }

const PeerPIDs = false

func peerPID(net.Conn) (int, bool) { return 0, false }
