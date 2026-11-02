//go:build !darwin

package screen

import (
	"net"
	"os"
)

func VerifyApp(*net.UnixConn, Peer) error { return ErrAccess }
func NativeAvailable() bool               { return false }

func bootstrapFile() (*os.File, error) { return nil, ErrAccess }
func monitorPeer(*Client) error        { return ErrAccess }
