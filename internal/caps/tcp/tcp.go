package tcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
)

const (
	Kind           = "tcp"
	Cap            = "net"
	ConnectTimeout = 30 * time.Second
)

type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

type Open struct {
	Kind string `json:"kind"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

type Opened struct {
	LocalAddr  string `json:"localAddr"`
	RemoteAddr string `json:"remoteAddr"`
}

func Register(n *node.Node) {
	n.Handle(Cap, Kind, Handler(n.Dialer(), n.Logf))
}

func Handler(d Dialer, logf func(format string, args ...any)) link.Handler {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return func(s *link.Stream) {
		var req Open
		if err := json.Unmarshal(s.OpenPayload(), &req); err != nil || req.Host == "" || req.Port < 1 || req.Port > 65535 {
			_ = s.Reset("EPROTO: tcp OPEN needs host and port")
			return
		}
		addr := net.JoinHostPort(strings.Trim(req.Host, "[]"), strconv.Itoa(req.Port))
		ctx, cancel := context.WithTimeout(context.Background(), ConnectTimeout)
		go func() {
			select {
			case <-s.Done():
				cancel()
			case <-ctx.Done():
			}
		}()
		conn, err := d.DialContext(ctx, "tcp", addr)
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		cancel()
		if err != nil {
			code := Errno(err)
			if timedOut {
				code = "ETIMEDOUT"
			}
			logf("tcp %s: %s: %v", addr, code, err)
			_ = s.Reset(code + ": " + err.Error())
			return
		}
		if err := s.Accept(Opened{LocalAddr: conn.LocalAddr().String(), RemoteAddr: conn.RemoteAddr().String()}); err != nil {
			_ = conn.Close()
			return
		}
		Pipe(s, conn)
	}
}

type halfCloser interface {
	CloseWrite() error
}

func Pipe(s *link.Stream, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	inDone := make(chan struct{})
	go func() {
		defer close(inDone)
		if _, err := s.WriteTo(conn); err != nil {
			_ = conn.Close()
			var re *link.ResetError
			if !errors.As(err, &re) {
				_ = s.Reset("ECONNRESET: " + err.Error())
			}
			return
		}
		if hc, ok := conn.(halfCloser); ok {
			_ = hc.CloseWrite()
		}
	}()
	buf := make([]byte, link.MaxPayload)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			if _, werr := s.Write(buf[:n]); werr != nil {
				_ = conn.Close()
				break
			}
		}
		if errors.Is(err, io.EOF) {
			_ = s.CloseWrite()
			break
		}
		if err != nil {
			_ = s.Reset("ECONNRESET: " + err.Error())
			break
		}
	}
	select {
	case <-inDone:
	case <-s.Done():
	}
}

func Errno(err error) string {
	var pe *policy.Error
	if errors.As(err, &pe) {
		return policy.Errno
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsTimeout {
			return "ETIMEDOUT"
		}
		return "ENOTFOUND"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return "ETIMEDOUT"
	}
	for _, m := range errnos {
		for _, e := range m.errs {
			if errors.Is(err, e) {
				return m.code
			}
		}
	}
	if errors.Is(err, syscall.EACCES) || errors.Is(err, os.ErrPermission) {
		return "EACCES"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "ETIMEDOUT"
	}
	return "ECONNREFUSED"
}

type errnoMap struct {
	code string
	errs []error
}
