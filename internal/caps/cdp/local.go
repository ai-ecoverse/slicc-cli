package cdp

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	client "github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/coder/websocket"
)

var errLocal = errors.New("guarded browser session unavailable")

func LocalEndpoint(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "http" && net.ParseIP(u.Hostname()).IsLoopback() && u.Port() != "" && raw == "http://"+u.Host && u.User == nil
}

func OpenLocal(ctx context.Context, base string, origins []string, observe func(client.Event)) (*client.Client, error) {
	if !LocalEndpoint(base) {
		return nil, errLocal
	}
	info, e := fetchVersion(ctx, base)
	if e != nil {
		return nil, errLocal
	}
	u, e := url.Parse(info.WebSocketURL)
	if e != nil || "http://"+u.Host != base || u.Scheme != "ws" || u.User != nil || !strings.HasPrefix(u.Path, "/devtools/browser/") || !strings.HasPrefix(info.Browser, "Chrome/") && !strings.HasPrefix(info.Browser, "HeadlessChrome/") {
		return nil, errLocal
	}
	r := NewRegistry(nil)
	if e := r.Protect(origins); e != nil {
		return nil, errLocal
	}
	r.entries = []*entry{{ep: browser.Endpoint{WSOnly: true, WebSocketURL: info.WebSocketURL, CDP: base}, info: link.Browser{ID: "local"}, relays: map[*relay]struct{}{}}}
	a, b := net.Pipe()
	s := &localStream{Conn: a, ready: make(chan struct{}), done: make(chan struct{})}
	t := &localTransport{c: b, s: s}
	go func() { defer func() { _ = s.CloseWrite() }(); r.serveStream(s) }()
	select {
	case <-ctx.Done():
		_ = s.CloseWrite()
	case <-s.done:
	case <-s.ready:
		return client.NewClient(t, observe), nil
	}
	_ = b.Close()
	return nil, errLocal
}

type localStream struct {
	net.Conn
	ready, done chan struct{}
	once        sync.Once
	calls       sync.Map
}

func (s *localStream) OpenPayload() json.RawMessage { return json.RawMessage(`{"browser":"local"}`) }
func (s *localStream) Accept(any) error             { close(s.ready); return nil }
func (s *localStream) Done() <-chan struct{}        { return s.done }
func (s *localStream) CloseWrite() error {
	s.once.Do(func() { close(s.done); _ = s.Close() })
	return nil
}
func (s *localStream) Reset(string) error                                { return s.CloseWrite() }
func (s *localStream) ReserveAllocation(n int, _ bool) *link.Reservation { return link.ReserveLocal(n) }
func (s *localStream) WaitWritable(context.Context, int) error           { return errLocal }
func (s *localStream) TryWrite([]byte) (bool, error)                     { return false, errLocal }

type localTransport struct {
	c net.Conn
	s *localStream
}

func (t *localTransport) TrackCall(ctx context.Context, id int64) func() {
	t.s.calls.Store(id, ctx)
	return func() { t.s.calls.Delete(id) }
}

func (t *localTransport) Close(websocket.StatusCode, string) error { return t.c.Close() }
func (t *localTransport) Write(ctx context.Context, _ websocket.MessageType, b []byte) error {
	dl, _ := ctx.Deadline()
	_ = t.c.SetWriteDeadline(dl)
	_, e := t.c.Write(pageFrame(b))
	return e
}
func (t *localTransport) Read(context.Context) (websocket.MessageType, []byte, error) {
	var h [4]byte
	if _, e := io.ReadFull(t.c, h[:]); e != nil {
		return 0, nil, e
	}
	n := binary.BigEndian.Uint32(h[:])
	if n > 1<<20 {
		_ = t.c.Close()
		return 0, nil, errLocal
	}
	_ = t.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	b := make([]byte, n)
	_, e := io.ReadFull(t.c, b)
	_ = t.c.SetReadDeadline(time.Time{})
	return websocket.MessageText, b, e
}
