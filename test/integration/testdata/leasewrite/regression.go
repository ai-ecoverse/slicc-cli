package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	client "github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/coder/websocket"
)

type leaseWrite struct {
	ctx  context.Context
	typ  websocket.MessageType
	data []byte
	err  error
	done bool
}

var leaseWrites struct {
	mu      sync.Mutex
	rl      *relay
	writes  []*leaseWrite
	entered chan *leaseWrite
}

type leaseRecorder struct{ rl *relay }

func leaseRecord(rl *relay) leaseRecorder { return leaseRecorder{rl} }

func (r leaseRecorder) Write(ctx context.Context, typ websocket.MessageType, p []byte) error {
	w := &leaseWrite{ctx: ctx, typ: typ, data: append([]byte(nil), p...)}
	leaseWrites.mu.Lock()
	leaseWrites.rl = r.rl
	leaseWrites.writes = append(leaseWrites.writes, w)
	entered := leaseWrites.entered
	leaseWrites.mu.Unlock()
	select {
	case entered <- w:
	default:
	}
	err := r.rl.conn.Write(ctx, typ, p)
	leaseWrites.mu.Lock()
	w.err, w.done = err, true
	leaseWrites.mu.Unlock()
	return err
}

func leaseResetRecorder() {
	leaseWrites.mu.Lock()
	leaseWrites.rl, leaseWrites.writes, leaseWrites.entered = nil, nil, make(chan *leaseWrite, 64)
	leaseWrites.mu.Unlock()
}

func leaseRecorded(id int64) []*leaseWrite {
	leaseWrites.mu.Lock()
	defer leaseWrites.mu.Unlock()
	var out []*leaseWrite
	for _, w := range leaseWrites.writes {
		if leaseID(w.data) == id {
			out = append(out, w)
		}
	}
	return out
}

func leaseEntered(t *testing.T, id int64) *leaseWrite {
	t.Helper()
	leaseWrites.mu.Lock()
	entered := leaseWrites.entered
	leaseWrites.mu.Unlock()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case w := <-entered:
			if id == 0 || leaseID(w.data) == id {
				return w
			}
		case <-deadline:
			t.Fatalf("write of call %d never reached the browser websocket", id)
			return nil
		}
	}
}

func leaseID(data []byte) int64 {
	var head struct{ ID int64 }
	_ = json.Unmarshal(data, &head)
	return head.ID
}

type leaseConn struct {
	net.Conn
	mu      sync.Mutex
	hold    chan struct{}
	fail    error
	entered chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (c *leaseConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	hold, fail := c.hold, c.fail
	c.mu.Unlock()
	if fail != nil {
		return 0, fail
	}
	if hold != nil {
		select {
		case c.entered <- struct{}{}:
		default:
		}
		select {
		case <-hold:
		case <-c.closed:
			return 0, net.ErrClosed
		}
	}
	return c.Conn.Write(p)
}

func (c *leaseConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func (c *leaseConn) block() {
	c.mu.Lock()
	c.hold = make(chan struct{})
	c.mu.Unlock()
}

func (c *leaseConn) unblock() {
	c.mu.Lock()
	hold := c.hold
	c.hold = nil
	c.mu.Unlock()
	if hold != nil {
		close(hold)
	}
}

func (c *leaseConn) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-c.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the frame write never reached the browser socket")
	}
}

type leaseBrowser struct {
	srv   *httptest.Server
	mu    sync.Mutex
	got   [][]byte
	conns []*leaseConn
}

func newLeaseBrowser(t *testing.T) *leaseBrowser {
	t.Helper()
	b := &leaseBrowser{}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"Browser": "Chrome/150.0.0.0", "Protocol-Version": "1.3", "webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/browser/lease"})
	})
	mux.HandleFunc("/devtools/browser/lease", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn.SetReadLimit(MaxMessage)
		defer func() { _ = conn.CloseNow() }()
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			b.mu.Lock()
			b.got = append(b.got, data)
			b.mu.Unlock()
			var p struct {
				ID        int64  `json:"id"`
				Method    string `json:"method"`
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(data, &p) != nil || p.Method == "Lease.holder" {
				continue
			}
			result := map[string]any{}
			switch p.Method {
			case "Browser.getVersion":
				result = map[string]any{"product": "Chrome/150.0.0.0", "protocolVersion": "1.3"}
			case "Target.attachToBrowserTarget":
				result = map[string]any{"sessionId": "R"}
			case "Target.getTargets":
				result = map[string]any{"targetInfos": []any{}}
			}
			reply := map[string]any{"id": p.ID, "result": result}
			if p.SessionID != "" {
				reply["sessionId"] = p.SessionID
			}
			out, _ := json.Marshal(reply)
			if conn.Write(context.Background(), websocket.MessageText, out) != nil {
				return
			}
		}
	})
	b.srv = httptest.NewServer(mux)
	t.Cleanup(b.srv.Close)
	old := httpClient
	httpClient = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		lc := &leaseConn{Conn: c, entered: make(chan struct{}, 1), closed: make(chan struct{})}
		b.mu.Lock()
		b.conns = append(b.conns, lc)
		b.mu.Unlock()
		return lc, nil
	}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(func() { httpClient = old })
	return b
}

func (b *leaseBrowser) ws() *leaseConn {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.conns[len(b.conns)-1]
}

func (b *leaseBrowser) received(id int64) [][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out [][]byte
	for _, data := range b.got {
		if leaseID(data) == id {
			out = append(out, data)
		}
	}
	return out
}

func (b *leaseBrowser) receivedMethod(method string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, data := range b.got {
		if strings.Contains(string(data), `"method":"`+method+`"`) {
			n++
		}
	}
	return n
}

func (b *leaseBrowser) waitReceived(t *testing.T, id int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(b.received(id)) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("browser never received call %d", id)
		}
		time.Sleep(time.Millisecond)
	}
}

type leaseFixture struct {
	b  *leaseBrowser
	c  *client.Client
	rl *relay
	s  *localStream
	ws *leaseConn
}

func leaseOpen(t *testing.T) *leaseFixture {
	t.Helper()
	leaseResetRecorder()
	b := newLeaseBrowser(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	c, err := OpenLocal(ctx, b.srv.URL, nil, nil)
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Call(ctx, "", "Browser.getVersion", nil, nil); err != nil {
		t.Fatalf("warm-up call: %v", err)
	}
	leaseWrites.mu.Lock()
	rl := leaseWrites.rl
	leaseWrites.mu.Unlock()
	if rl == nil {
		t.Fatal("warm-up call did not pass forward")
	}
	leaseWrites.mu.Lock()
	leaseWrites.entered = make(chan *leaseWrite, 64)
	leaseWrites.mu.Unlock()
	s, ok := rl.s.(*localStream)
	if !ok {
		t.Fatal("OpenLocal relay is not on a local stream")
	}
	return &leaseFixture{b: b, c: c, rl: rl, s: s, ws: b.ws()}
}

func (f *leaseFixture) noTeardown(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case <-f.ws.closed:
		t.Fatal("the cancelled call closed the shared browser websocket")
	case <-f.s.Done():
		t.Fatal("the cancelled call ended the local stream")
	case <-time.After(within):
	}
}

func (f *leaseFixture) alive(t *testing.T) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	before := len(leaseRecordedAll())
	if err := f.c.Call(ctx, "", "Browser.getVersion", nil, nil); err != nil {
		t.Fatalf("a later call on the shared session failed: %v", err)
	}
	if f.c.Err() != nil {
		t.Fatalf("client failed: %v", f.c.Err())
	}
	select {
	case <-f.s.Done():
		t.Fatal("local stream ended")
	case <-f.ws.closed:
		t.Fatal("browser websocket closed")
	default:
	}
	all := leaseRecordedAll()
	if len(all) != before+1 {
		t.Fatalf("later call recorded %d writes", len(all)-before)
	}
	return leaseID(all[len(all)-1].data)
}

func leaseRecordedAll() []*leaseWrite {
	leaseWrites.mu.Lock()
	defer leaseWrites.mu.Unlock()
	return append([]*leaseWrite(nil), leaseWrites.writes...)
}

func leaseBackground(t *testing.T, w *leaseWrite) {
	t.Helper()
	leaseWrites.mu.Lock()
	ctx, typ, err, done := w.ctx, w.typ, w.err, w.done
	leaseWrites.mu.Unlock()
	if ctx != context.Background() {
		t.Fatal("the browser write ran under the call context, not context.Background()")
	}
	if typ != websocket.MessageText {
		t.Fatalf("message type %v", typ)
	}
	if !done || err != nil {
		t.Fatalf("browser write: done=%v err=%v", done, err)
	}
}

func (f *leaseFixture) park(t *testing.T, id int64) {
	t.Helper()
	data := []byte(fmt.Sprintf(`{"id":%d,"method":"Browser.getVersion"}`, id))
	c, cid, ok := parseCommand(data)
	if !ok {
		t.Fatal("parseCommand")
	}
	v := f.rl.g.fromCommand(c, cid, ok)
	f.rl.parkVerdict(parkKey{session: "lease-release"}, data, c, cid, v)
}

func (f *leaseFixture) start(t *testing.T, path string, ctx context.Context, id int64) (int64, *leaseWrite, <-chan error) {
	t.Helper()
	done := make(chan error, 1)
	if path == "release" {
		f.s.calls.Store(id, ctx)
		t.Cleanup(func() { f.s.calls.Delete(id) })
		f.park(t, id)
		done <- nil
		return id, leaseEntered(t, id), done
	}
	go func() { done <- f.c.Call(ctx, "", "Browser.getVersion", nil, nil) }()
	w := leaseEntered(t, 0)
	return leaseID(w.data), w, done
}

func TestLeaseWriteCancelDuringLockWait(t *testing.T) {
	for _, path := range []string{"toBrowser", "release"} {
		t.Run(path, func(t *testing.T) {
			f := leaseOpen(t)
			f.ws.block()
			held := make(chan error, 1)
			go func() {
				held <- f.rl.conn.Write(context.Background(), websocket.MessageText, []byte(`{"method":"Lease.holder"}`))
			}()
			f.ws.waitEntered(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			id, w, done := f.start(t, path, ctx, 500001)
			cancel()
			f.noTeardown(t, 300*time.Millisecond)
			f.ws.unblock()
			if err := <-held; err != nil {
				t.Fatalf("holder write: %v", err)
			}
			f.b.waitReceived(t, id)
			if err := <-done; path == "toBrowser" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled call returned %v", err)
			}
			leaseBackground(t, w)
			next := f.alive(t)
			if got := len(f.b.received(id)); got != 1 {
				t.Fatalf("browser received the call %d times", got)
			}
			if f.b.receivedMethod("Lease.holder") != 1 {
				t.Fatal("holder write lost")
			}
			if next == id {
				t.Fatal("call id reused")
			}
		})
	}
}

func TestLeaseWriteCancelDuringFrameWrite(t *testing.T) {
	for _, path := range []string{"toBrowser", "release"} {
		t.Run(path, func(t *testing.T) {
			f := leaseOpen(t)
			f.ws.block()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			id, w, done := f.start(t, path, ctx, 500002)
			f.ws.waitEntered(t)
			cancel()
			f.noTeardown(t, 300*time.Millisecond)
			f.ws.unblock()
			f.b.waitReceived(t, id)
			if err := <-done; path == "toBrowser" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled call returned %v", err)
			}
			leaseBackground(t, w)
			next := f.alive(t)
			if got := len(f.b.received(id)); got != 1 {
				t.Fatalf("browser received the call %d times", got)
			}
			if next == id {
				t.Fatal("call id reused")
			}
		})
	}
}

func TestLeaseWriteDeadCallDropped(t *testing.T) {
	t.Run("toBrowser", func(t *testing.T) {
		f := leaseOpen(t)
		f.ws.block()
		first := make(chan error, 1)
		go func() { first <- f.c.Call(t.Context(), "", "Browser.getVersion", nil, nil) }()
		head := leaseEntered(t, 0)
		f.ws.waitEntered(t)
		ctx, cancel := context.WithCancel(t.Context())
		dead := make(chan error, 1)
		go func() { dead <- f.c.Call(ctx, "", "Browser.getVersion", nil, nil) }()
		var id int64
		deadline := time.Now().Add(5 * time.Second)
		for id == 0 {
			f.s.calls.Range(func(k, _ any) bool {
				if k.(int64) != leaseID(head.data) {
					id = k.(int64)
				}
				return true
			})
			if time.Now().After(deadline) {
				t.Fatal("second call never tracked")
			}
			time.Sleep(time.Millisecond)
		}
		cancel()
		f.ws.unblock()
		if err := <-first; err != nil {
			t.Fatalf("live call: %v", err)
		}
		if err := <-dead; !errors.Is(err, context.Canceled) {
			t.Fatalf("dead call returned %v", err)
		}
		next := f.alive(t)
		if n := len(leaseRecorded(id)); n != 0 {
			t.Fatalf("dead call reached the websocket write %d times", n)
		}
		if n := len(f.b.received(id)); n != 0 {
			t.Fatalf("browser received the dead call %d times", n)
		}
		if next <= id {
			t.Fatalf("call id %d after dead id %d", next, id)
		}
	})
	t.Run("release", func(t *testing.T) {
		f := leaseOpen(t)
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		f.s.calls.Store(int64(500003), cancelled)
		f.s.calls.Store(int64(500005), t.Context())
		t.Cleanup(func() { f.s.calls.Delete(int64(500003)); f.s.calls.Delete(int64(500005)) })
		f.ws.block()
		f.park(t, 500003)
		f.park(t, 500004)
		f.park(t, 500005)
		w := leaseEntered(t, 500005)
		f.noTeardown(t, 100*time.Millisecond)
		f.ws.unblock()
		f.b.waitReceived(t, 500005)
		leaseBackground(t, w)
		f.alive(t)
		for _, id := range []int64{500003, 500004} {
			if n := len(leaseRecorded(id)) + len(f.b.received(id)); n != 0 {
				t.Fatalf("dead call %d was written", id)
			}
		}
		if n := len(f.b.received(500005)); n != 1 {
			t.Fatalf("live call after dead ones received %d times", n)
		}
	})
}

func TestLeaseWriteLiveCallOnce(t *testing.T) {
	f := leaseOpen(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := f.c.Call(ctx, "", "Browser.getVersion", nil, nil); err != nil {
		t.Fatal(err)
	}
	all := leaseRecordedAll()
	w := all[len(all)-1]
	id := leaseID(w.data)
	leaseBackground(t, w)
	got := f.b.received(id)
	if len(got) != 1 || len(leaseRecorded(id)) != 1 || string(got[0]) != string(w.data) {
		t.Fatalf("live call written %d times, recorded %d", len(got), len(leaseRecorded(id)))
	}
}

type leaseLinkStream struct {
	*localStream
	mu      sync.Mutex
	reasons []string
}

func (s *leaseLinkStream) Reset(reason string) error {
	s.mu.Lock()
	s.reasons = append(s.reasons, reason)
	s.mu.Unlock()
	return s.localStream.CloseWrite()
}

func TestLeaseWriteNonLocalUnchanged(t *testing.T) {
	leaseResetRecorder()
	b := newLeaseBrowser(t)
	r := NewRegistry(nil)
	ws := "ws://" + strings.TrimPrefix(b.srv.URL, "http://") + "/devtools/browser/lease"
	r.entries = []*entry{{ep: browser.Endpoint{WSOnly: true, WebSocketURL: ws, CDP: b.srv.URL}, info: link.Browser{ID: "local"}, relays: map[*relay]struct{}{}}}
	a, page := net.Pipe()
	ls := &leaseLinkStream{localStream: &localStream{Conn: a, ready: make(chan struct{}), done: make(chan struct{})}}
	served := make(chan struct{})
	go func() { defer close(served); defer func() { _ = ls.CloseWrite() }(); r.serveStream(ls) }()
	t.Cleanup(func() { _ = page.Close(); <-served })
	select {
	case <-ls.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("non-local stream never opened")
	}
	pt := &localTransport{c: page, s: ls.localStream}
	go func() {
		for {
			if _, _, err := pt.Read(context.Background()); err != nil {
				return
			}
		}
	}()
	send := func(id int64) *leaseWrite {
		t.Helper()
		if err := pt.Write(context.Background(), websocket.MessageText, []byte(fmt.Sprintf(`{"id":%d,"method":"Browser.getVersion"}`, id))); err != nil {
			t.Fatal(err)
		}
		return leaseEntered(t, id)
	}
	for _, id := range []int64{7, 9} {
		w := send(id)
		deadline := time.Now().Add(5 * time.Second)
		for {
			leaseWrites.mu.Lock()
			done := w.done
			leaseWrites.mu.Unlock()
			if done || time.Now().After(deadline) {
				break
			}
			time.Sleep(time.Millisecond)
		}
		leaseBackground(t, w)
		b.waitReceived(t, id)
	}
	for _, id := range []int64{7, 9} {
		got, rec := b.received(id), leaseRecorded(id)
		if len(got) != 1 || len(rec) != 1 || string(got[0]) != string(rec[0].data) {
			t.Fatalf("link-stream command %d written %d times, recorded %d", id, len(got), len(rec))
		}
	}
	injected := errors.New("lease-write injected failure")
	conn := b.ws()
	conn.mu.Lock()
	conn.fail = injected
	conn.mu.Unlock()
	w := send(8)
	select {
	case <-ls.done:
	case <-time.After(5 * time.Second):
		t.Fatal("write failure did not end the link stream")
	}
	leaseWrites.mu.Lock()
	ctx, err := w.ctx, w.err
	leaseWrites.mu.Unlock()
	if ctx != context.Background() || !errors.Is(err, injected) {
		t.Fatalf("link-stream write ctx background=%v err=%v", ctx == context.Background(), err)
	}
	ls.mu.Lock()
	reasons := append([]string(nil), ls.reasons...)
	ls.mu.Unlock()
	if len(reasons) == 0 || reasons[0] != "ECONNRESET: browser socket closed" {
		t.Fatalf("reset reasons %q", reasons)
	}
}
