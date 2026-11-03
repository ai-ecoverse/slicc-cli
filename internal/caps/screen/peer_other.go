//go:build !darwin

package screen

import (
	"net"
	"os"
)

func VerifyApp(c *net.UnixConn, expected Peer) error {
	_, e := VerifyAppWithProof(c, expected)
	return e
}
func VerifyAppWithProof(*net.UnixConn, Peer) (ProofChecks, error) { return ProofChecks{}, ErrAccess }
func NativeAvailable() bool                                       { return false }

func bootstrapFile() (*os.File, error) { return nil, ErrAccess }
func monitorPeer(*Client) error        { return ErrAccess }
