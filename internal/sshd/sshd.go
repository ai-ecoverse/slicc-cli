package sshd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	ServerVersion    = "SSH-2.0-slicc"
	EvalSubsystem    = "slicc-eval"
	SFTPSubsystem    = "sftp"
	DefaultEvalQuiet = 500 * time.Millisecond
	handshakeTimeout = 30 * time.Second
	dialTimeout      = 30 * time.Second
	drainGrace       = 2 * time.Second
)

var (
	ErrNoRunner = errors.New("slicc runs no commands on this machine: it was started without a runner")
	ErrRefused  = fmt.Errorf("slicc: forwarding is not allowed from this machine: %w", os.ErrPermission)
)

type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

type RefuseAll struct{}

func (RefuseAll) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, ErrRefused
}

type Config struct {
	HostKey   ssh.Signer
	Runner    []string
	Shell     []string
	Eval      []string
	EvalQuiet time.Duration
	Dialer    Dialer
	Dir       string
	NoSFTP    bool
}

type Server struct {
	cfg    Config
	shell  []string
	dialer Dialer
	ssh    *ssh.ServerConfig
	eval   *evalHost
	mu     sync.Mutex
	closed bool
}

func New(cfg Config) (*Server, error) {
	if cfg.HostKey == nil {
		return nil, errors.New("sshd: a host key is required")
	}
	s := &Server{cfg: cfg, dialer: cfg.Dialer, shell: cfg.Shell}
	if s.dialer == nil {
		s.dialer = RefuseAll{}
	}
	if s.shell == nil {
		s.shell = ShellFor(cfg.Runner)
	}
	if s.cfg.Dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			s.cfg.Dir = home
		}
	}
	if len(cfg.Eval) > 0 {
		quiet := cfg.EvalQuiet
		if quiet <= 0 {
			quiet = DefaultEvalQuiet
		}
		s.eval = &evalHost{argv: cfg.Eval, quiet: quiet, dir: s.cfg.Dir}
	}
	s.ssh = &ssh.ServerConfig{NoClientAuth: true, ServerVersion: ServerVersion}
	s.ssh.AddHostKey(cfg.HostKey)
	return s, nil
}

func (s *Server) HostKey() ssh.PublicKey {
	return s.cfg.HostKey.PublicKey()
}

func (s *Server) ServeConn(c net.Conn) error {
	defer func() { _ = c.Close() }()
	if s.isClosed() {
		return errors.New("sshd: server closed")
	}
	_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
	conn, chans, reqs, err := ssh.NewServerConn(c, s.ssh)
	if err != nil {
		return err
	}
	_ = c.SetDeadline(time.Time{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		go s.handleChannel(ctx, nc)
	}
	if err := conn.Wait(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	if s.eval != nil {
		s.eval.close()
	}
	return nil
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Server) handleChannel(ctx context.Context, nc ssh.NewChannel) {
	switch nc.ChannelType() {
	case "session":
		if len(s.cfg.Runner) == 0 {
			_ = nc.Reject(ssh.Prohibited, ErrNoRunner.Error())
			return
		}
		ch, reqs, err := nc.Accept()
		if err != nil {
			return
		}
		newSession(ctx, s, ch).serve(reqs)
	case "direct-tcpip":
		s.forward(ctx, nc)
	default:
		_ = nc.Reject(ssh.UnknownChannelType, "slicc: unsupported channel type "+nc.ChannelType())
	}
}
