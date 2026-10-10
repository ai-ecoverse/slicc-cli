package kernelin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

const (
	Cap         = "kernel-in"
	Kind        = "kernel"
	DefaultPort = 80
	CDPPort     = 9222
	HeadLimit   = 64 * 1024
	HeadTimeout = 30 * time.Second
	OpenTimeout = 10 * time.Second
	BindHost    = "127.0.0.1"
)

type Open struct {
	Kind string `json:"kind"`
	Port int    `json:"port"`
}

type Config struct {
	Port        int
	HeadTimeout time.Duration
	OpenTimeout time.Duration
	Logf        func(format string, args ...any)
	Status      func(format string, args ...any)
}

type Service struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	links  []*link.Link
	ln     net.Listener
	tried  bool
	err    error
	closed bool
	conns  map[net.Conn]struct{}
	wg     sync.WaitGroup
}

func New(cfg Config) *Service {
	if cfg.HeadTimeout <= 0 {
		cfg.HeadTimeout = HeadTimeout
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = OpenTimeout
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Status == nil {
		cfg.Status = func(string, ...any) {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{cfg: cfg, ctx: ctx, cancel: cancel, conns: map[net.Conn]struct{}{}}
}

func (s *Service) Add(l *link.Link) {
	select {
	case <-l.Ready():
	case <-l.Done():
		return
	default:
		go func() {
			select {
			case <-l.Ready():
				s.Add(l)
			case <-l.Done():
			}
		}()
		return
	}
	h, ok := l.PeerHello()
	if !ok || !h.HasCap(Cap) {
		return
	}
	s.mu.Lock()
	if s.closed || slices.Contains(s.links, l) {
		s.mu.Unlock()
		return
	}
	s.links = append(s.links, l)
	s.mu.Unlock()
	go func() {
		<-l.Done()
		s.remove(l)
	}()
	_ = s.Listen()
}

func (s *Service) remove(l *link.Link) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.links {
		if x == l {
			s.links = append(s.links[:i], s.links[i+1:]...)
			return
		}
	}
}

func (s *Service) current() *link.Link {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.links) - 1; i >= 0; i-- {
		select {
		case <-s.links[i].Done():
		default:
			return s.links[i]
		}
	}
	return nil
}

func (s *Service) Links() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.links)
}

func (s *Service) Listen() error {
	s.mu.Lock()
	if s.tried {
		err := s.err
		s.mu.Unlock()
		return err
	}
	s.tried = true
	addr := net.JoinHostPort(BindHost, strconv.Itoa(s.cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.err = err
		s.mu.Unlock()
		s.cfg.Status("kernel services are off: cannot listen on %s (%s); try --kernel-port", addr, bindReason(err))
		return err
	}
	if s.closed {
		s.mu.Unlock()
		_ = ln.Close()
		return net.ErrClosed
	}
	s.ln = ln
	s.mu.Unlock()
	port := ln.Addr().(*net.TCPAddr).Port
	s.cfg.Status("kernel services on %s", URL("<port>", port))
	s.wg.Add(1)
	go s.accept(ln, port)
	return nil
}

func URL(kernelPort string, listenPort int) string {
	if listenPort == 80 {
		return fmt.Sprintf("http://%s.kernel.localhost/", kernelPort)
	}
	return fmt.Sprintf("http://%s.kernel.localhost:%d/", kernelPort, listenPort)
}

func bindReason(err error) string {
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Err != nil {
		return oe.Err.Error()
	}
	return err.Error()
}

func (s *Service) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	ln := s.ln
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.links = nil
	s.mu.Unlock()
	s.cancel()
	var err error
	if ln != nil {
		err = ln.Close()
	}
	for _, c := range conns {
		_ = c.Close()
	}
	s.wg.Wait()
	return err
}

func (s *Service) accept(ln net.Listener, port int) {
	defer s.wg.Done()
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			s.cfg.Logf("kernel: accept: %v", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if !s.track(c) {
			_ = c.Close()
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.untrack(c)
			s.handle(c, port)
		}()
	}
}

func (s *Service) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Service) untrack(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	_ = c.Close()
}

func (s *Service) handle(c net.Conn, listenPort int) {
	_ = c.SetReadDeadline(time.Now().Add(s.cfg.HeadTimeout))
	buf, end, err := readHead(c)
	if errors.Is(err, errHeadTooLarge) {
		answer(c, 431, "request head too large")
		return
	}
	if err != nil {
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	head, ok := ParseHead(buf[:end])
	if !ok {
		answer(c, 400, "malformed request head")
		return
	}
	port, ok := KernelPort(head.Host, listenPort)
	if !ok {
		answer(c, 421, "only <port>.kernel.localhost is served here")
		return
	}
	if port == CDPPort {
		s.cfg.Logf("kernel %s %d ← 502 refused", head.Method, port)
		answer(c, 502, fmt.Sprintf("kernel port %d: EACCES", port))
		return
	}
	l := s.current()
	if l == nil {
		s.cfg.Logf("kernel %s %d ← 502 no page", head.Method, port)
		answer(c, 502, "no seven page connected")
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, s.cfg.OpenTimeout)
	st, err := l.Open(ctx, Open{Kind: Kind, Port: port}, link.OpenOptions{})
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	cancel()
	if err != nil {
		status, msg := dialFailure(err, timedOut, port)
		s.cfg.Logf("kernel %s %d ← %d %s", head.Method, port, status, msg)
		answer(c, status, msg)
		return
	}
	s.cfg.Logf("kernel %s %d", head.Method, port)
	if _, err := st.Write(buf); err != nil {
		_ = st.Reset("ECONNRESET")
		return
	}
	pipe(st, c)
}

func dialFailure(err error, timedOut bool, port int) (int, string) {
	if timedOut {
		return 504, fmt.Sprintf("kernel port %d did not answer", port)
	}
	var re *link.ResetError
	if errors.As(err, &re) && re.Remote {
		if re.Code == "ECONNREFUSED" {
			return 502, fmt.Sprintf("nothing listening on kernel port %d", port)
		}
		return 502, fmt.Sprintf("kernel port %d: %s", port, Reason(re.Reason()))
	}
	return 502, "no seven page connected"
}

func Reason(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s) && len(out) < 200; i++ {
		if s[i] >= 0x20 && s[i] <= 0x7e {
			out = append(out, s[i])
		}
	}
	if len(out) == 0 {
		return "reset"
	}
	return string(out)
}
