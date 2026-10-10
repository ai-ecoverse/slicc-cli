package link

import (
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
)

const CloseGrace = 10 * time.Second

type Addr struct {
	Kind  string
	ID    uint32
	Local bool
}

func (Addr) Network() string { return "slicc-link" }

func (a Addr) String() string {
	side := "page"
	if a.Local {
		side = "node"
	}
	return side + "/" + a.Kind + "/" + strconv.FormatUint(uint64(a.ID), 10)
}

type streamConn struct {
	s      *Stream
	mu     sync.Mutex
	closed bool
}

func (s *Stream) Conn() net.Conn {
	return &streamConn{s: s}
}

func (c *streamConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *streamConn) Read(p []byte) (int, error) {
	if c.isClosed() {
		return 0, net.ErrClosed
	}
	return c.s.Read(p)
}

func (c *streamConn) Write(p []byte) (int, error) {
	if c.isClosed() {
		return 0, net.ErrClosed
	}
	return c.s.Write(p)
}

func (c *streamConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	if errors.Is(c.s.CloseWrite(), ErrNotAccepted) {
		_ = c.s.Reset("ECONNRESET: closed")
		return nil
	}
	go c.linger()
	return nil
}

func (c *streamConn) linger() {
	timer := time.AfterFunc(CloseGrace, func() { _ = c.s.Reset("ECONNRESET: closed") })
	defer timer.Stop()
	_ = c.s.SetReadDeadline(time.Time{})
	buf := make([]byte, 32*1024)
	for {
		if _, err := c.s.Read(buf); err != nil {
			break
		}
	}
	<-c.s.Done()
}

func (c *streamConn) LocalAddr() net.Addr {
	return Addr{Kind: c.s.kind, ID: c.s.id, Local: true}
}

func (c *streamConn) RemoteAddr() net.Addr {
	return Addr{Kind: c.s.kind, ID: c.s.id}
}

func (c *streamConn) SetDeadline(t time.Time) error {
	_ = c.s.SetReadDeadline(t)
	return c.s.SetWriteDeadline(t)
}

func (c *streamConn) SetReadDeadline(t time.Time) error { return c.s.SetReadDeadline(t) }

func (c *streamConn) SetWriteDeadline(t time.Time) error { return c.s.SetWriteDeadline(t) }
