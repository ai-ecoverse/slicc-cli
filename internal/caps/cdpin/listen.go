package cdpin

import (
	"errors"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"sync"
	"syscall"
)

const (
	bindHost4 = "127.0.0.1"
	bindHost6 = "::1"
	wildHost  = "0.0.0.0"
)

type BindError struct {
	Port int
	Err  error
}

func (e *BindError) Error() string { return fmt.Sprintf("port %d is taken; try --port", e.Port) }

func (e *BindError) Unwrap() error { return e.Err }

func bindAll(port int) ([]net.Listener, int, error) {
	ln, err := bind4(port)
	if err != nil {
		return nil, 0, &BindError{Port: port, Err: err}
	}
	got := ln.Addr().(*net.TCPAddr).Port
	out := []net.Listener{ln}
	if ln6, err := net.Listen("tcp6", net.JoinHostPort(bindHost6, strconv.Itoa(got))); err == nil {
		out = append(out, ln6)
	}
	return out, got, nil
}

func bind4(port int) (net.Listener, error) {
	ln, err := net.Listen("tcp4", net.JoinHostPort(bindHost4, strconv.Itoa(port)))
	if err == nil || runtime.GOOS != "darwin" || !errors.Is(err, syscall.EACCES) {
		return ln, err
	}
	return net.Listen("tcp4", net.JoinHostPort(wildHost, strconv.Itoa(port)))
}

func Loopback(a net.Addr) bool {
	t, ok := a.(*net.TCPAddr)
	return ok && t.IP.IsLoopback()
}

type guarded struct {
	net.Listener
	logf func(string, ...any)
}

func Guard(ln net.Listener, logf func(string, ...any)) net.Listener {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &guarded{Listener: ln, logf: logf}
}

func (g *guarded) Accept() (net.Conn, error) {
	for {
		c, err := g.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if Loopback(c.RemoteAddr()) {
			return c, nil
		}
		g.logf("cdp: refused %s, not loopback", c.RemoteAddr())
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.SetLinger(0)
		}
		_ = c.Close()
	}
}

type multiListener struct {
	lns   []net.Listener
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
	wg    sync.WaitGroup
}

func merge(lns []net.Listener) *multiListener {
	m := &multiListener{lns: lns, conns: make(chan net.Conn), done: make(chan struct{})}
	for _, ln := range lns {
		m.wg.Add(1)
		go m.pump(ln)
	}
	return m
}

func (m *multiListener) pump(ln net.Listener) {
	defer m.wg.Done()
	for {
		c, err := ln.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return
		}
		select {
		case m.conns <- c:
		case <-m.done:
			_ = c.Close()
			return
		}
	}
}

func (m *multiListener) Accept() (net.Conn, error) {
	select {
	case c := <-m.conns:
		return c, nil
	case <-m.done:
		return nil, net.ErrClosed
	}
}

func (m *multiListener) Close() error {
	m.once.Do(func() {
		close(m.done)
		for _, ln := range m.lns {
			_ = ln.Close()
		}
	})
	return nil
}

func (m *multiListener) Addr() net.Addr { return m.lns[0].Addr() }
