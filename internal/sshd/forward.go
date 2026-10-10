package sshd

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"syscall"

	"golang.org/x/crypto/ssh"
)

type forwardRequest struct {
	Host     string
	Port     uint32
	OrigHost string
	OrigPort uint32
}

func (s *Server) forward(ctx context.Context, nc ssh.NewChannel) {
	var req forwardRequest
	if err := ssh.Unmarshal(nc.ExtraData(), &req); err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, "slicc: malformed direct-tcpip request")
		return
	}
	addr := net.JoinHostPort(req.Host, strconv.FormatUint(uint64(req.Port), 10))
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	conn, err := s.dialer.DialContext(dctx, "tcp", addr)
	cancel()
	if err != nil {
		reason := ssh.ConnectionFailed
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EACCES) {
			reason = ssh.Prohibited
		}
		_ = nc.Reject(reason, err.Error())
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		_ = conn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	splice(ch, conn)
}

type closeWriter interface {
	CloseWrite() error
}

func splice(ch ssh.Channel, conn net.Conn) {
	done := make(chan error, 2)
	go func() {
		_, err := io.Copy(conn, ch)
		if cw, ok := conn.(closeWriter); ok {
			_ = cw.CloseWrite()
		}
		done <- err
	}()
	go func() {
		_, err := io.Copy(ch, conn)
		_ = ch.CloseWrite()
		done <- err
	}()
	if err := <-done; err == nil {
		<-done
	}
	_ = ch.Close()
	_ = conn.Close()
}
