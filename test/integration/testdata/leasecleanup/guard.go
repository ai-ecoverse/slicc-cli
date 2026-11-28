package lease

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	guarded "github.com/ai-ecoverse/slicc-cli/internal/caps/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
	"github.com/coder/websocket"
)

const cleanupEndpoint = "http://127.0.0.1:1"

type cleanupWire struct {
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Session string          `json:"sessionId"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   *cdp.Error      `json:"error"`
}

type cleanupTrace struct {
	mu                 sync.Mutex
	commands, replies  []cleanupWire
	repliesBeforeClose int
	firstClose         bool
}

func (tr *cleanupTrace) record(direction string, data []byte) {
	var msg cleanupWire
	if json.Unmarshal(data, &msg) != nil {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if direction == "send" {
		if msg.Method == "Target.closeTarget" && !tr.firstClose {
			tr.repliesBeforeClose = len(tr.replies)
			tr.firstClose = true
		}
		tr.commands = append(tr.commands, msg)
	} else {
		tr.replies = append(tr.replies, msg)
	}
}

func (tr *cleanupTrace) closes() []cleanupWire {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	var out []cleanupWire
	for _, msg := range tr.commands {
		if msg.Method == "Target.closeTarget" {
			out = append(out, msg)
		}
	}
	return out
}

func (tr *cleanupTrace) commandCounts() (int, int) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	n := 0
	for _, msg := range tr.commands {
		if msg.Method == "Page.getFrameTree" && msg.Session == "S1" {
			n++
		}
	}
	return len(tr.commands), n
}

func (tr *cleanupTrace) response(id int64) cleanupWire {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for _, msg := range tr.replies {
		if msg.ID == id {
			return msg
		}
	}
	return cleanupWire{}
}

type cleanupPipeListener struct {
	peers chan net.Conn
	done  chan struct{}
	stop  sync.Once
}

func (l *cleanupPipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.peers:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *cleanupPipeListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
}
func (l *cleanupPipeListener) Close() error { l.stop.Do(func() { close(l.done) }); return nil }
func (l *cleanupPipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	a, b := net.Pipe()
	select {
	case l.peers <- a:
		return b, nil
	case <-ctx.Done():
		a.Close()
		b.Close()
		return nil, ctx.Err()
	case <-l.done:
		a.Close()
		b.Close()
		return nil, net.ErrClosed
	}
}

type cleanupGuardBrowser struct {
	live      atomic.Bool
	ctx       context.Context
	ws        chan *websocket.Conn
	command   chan cleanupWire
	forwarded chan cleanupWire
	errors    chan error
}

func (f *cleanupGuardBrowser) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/json/version" {
		_, _ = io.WriteString(w, `{"Browser":"Chrome/999","webSocketDebuggerUrl":"ws://127.0.0.1:1/devtools/browser/cleanup"}`)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		f.errors <- err
		return
	}
	defer ws.CloseNow()
	f.ws <- ws
	go f.respond(ws)
	for {
		_, data, err := ws.Read(f.ctx)
		if err != nil {
			return
		}
		var msg cleanupWire
		if err := json.Unmarshal(data, &msg); err != nil {
			f.errors <- err
			return
		}
		select {
		case f.command <- msg:
		case <-f.ctx.Done():
			return
		}
	}
}

func cleanupWSWrite(ctx context.Context, ws *websocket.Conn, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return ws.Write(ctx, websocket.MessageText, b)
}

func cleanupGuardFrame(url string) map[string]any {
	origin := "://"
	if url == cleanupAllowed {
		origin = "https://billing.example"
	}
	return map[string]any{"id": "T1", "loaderId": "L1", "url": url, "securityOrigin": origin}
}

func (f *cleanupGuardBrowser) respond(ws *websocket.Conn) {
	loc := "about:blank"
	for {
		select {
		case <-f.ctx.Done():
			return
		case m := <-f.command:
			var result any = map[string]any{}
			switch m.Method {
			case "Browser.getVersion":
				result = map[string]any{"product": "Chrome/999", "protocolVersion": "1.3"}
			case "Target.attachToBrowserTarget":
				result = map[string]any{"sessionId": "B1"}
			case "Target.getTargets":
				result = map[string]any{"targetInfos": []any{}}
			case "Target.createTarget":
				f.live.Store(true)
				result = map[string]any{"targetId": "T1"}
			case "Target.getTargetInfo":
				result = map[string]any{"targetInfo": map[string]any{"targetId": "T1", "type": "page", "url": loc}}
			case "Target.attachToTarget":
				sid := "S1"
				if m.Session == "B1" {
					sid = "N1"
				}
				result = map[string]any{"sessionId": sid}
			case "Page.getNavigationHistory":
				result = map[string]any{"currentIndex": 0, "entries": []any{map[string]any{"id": 1, "url": loc}}}
			case "Page.getFrameTree":
				result = map[string]any{"frameTree": map[string]any{"frame": cleanupGuardFrame(loc)}}
			case "Page.navigate":
				loc = cleanupAllowed
				result = map[string]any{"frameId": "T1", "loaderId": "L1"}
			case "Runtime.evaluate":
				result = map[string]any{"result": map[string]any{"value": "https://billing.example"}}
			case "Target.detachFromTarget", "Target.closeTarget":
				if m.Method == "Target.closeTarget" {
					f.live.Store(false)
				}
				f.forwarded <- m
				result = map[string]any{"success": true}
			}
			reply := map[string]any{"id": m.ID, "result": result}
			if m.Session != "" {
				reply["sessionId"] = m.Session
			}
			if err := cleanupWSWrite(f.ctx, ws, reply); err != nil {
				return
			}
		}
	}
}

func cleanupReceive[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-ctx.Done():
		t.Fatalf("fixture wait: %v", ctx.Err())
	}
	panic("unreachable")
}

func newCleanupGuard(t *testing.T, baseline bool) (cleanupFixture, *cleanupGuardBrowser, *cleanupTrace, *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	l := &cleanupPipeListener{peers: make(chan net.Conn), done: make(chan struct{})}
	browser := &cleanupGuardBrowser{ctx: ctx, ws: make(chan *websocket.Conn, 1), command: make(chan cleanupWire, 64), forwarded: make(chan cleanupWire, 64), errors: make(chan error, 8)}
	server := &http.Server{Handler: http.HandlerFunc(browser.serve)}
	go func() { _ = server.Serve(l) }()
	t.Cleanup(func() { _ = server.Close(); _ = l.Close() })
	transport := &http.Transport{DialContext: l.dial}
	restore := guarded.LeaseCleanupHTTPTransport(transport)
	t.Cleanup(func() { restore(); transport.CloseIdleConnections() })
	tr := &cleanupTrace{}
	t.Cleanup(guarded.LeaseCleanupTrace(tr.record))
	o, err := protect.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	bs := &browserSession{tabs: map[string]*tab{}}
	bs.c, err = guarded.OpenLocal(ctx, cleanupEndpoint, o.Extra(), func(ev cdp.Event) {
		if !baseline || ev.Method != "Target.detachedFromTarget" {
			bs.observe(ev)
		}
	})
	if err != nil {
		select {
		case browserErr := <-browser.errors:
			t.Fatalf("guard fixture: %v (%v)", err, browserErr)
		default:
			t.Fatal(err)
		}
	}
	f := newCleanupBroker(t, o, bs, nil)
	f.prime(t)
	return f, browser, tr, cleanupReceive(t, ctx, browser.ws)
}

func cleanupEvent(t *testing.T, ctx context.Context, ws *websocket.Conn, method, session string, params any) {
	t.Helper()
	meta := map[string]any{"method": method, "params": params}
	if session != "" {
		meta["sessionId"] = session
	}
	if err := cleanupWSWrite(ctx, ws, meta); err != nil {
		t.Fatal(err)
	}
}

func cleanupProtectedCommit(t *testing.T, f cleanupFixture, browser *cleanupGuardBrowser, ws *websocket.Conn, dest string) {
	t.Helper()
	if f.a.tab.target != "T1" || f.a.tab.session != "S1" {
		t.Fatal("fixture failed to create leased tab")
	}
	sub := f.b.bs.c.Subscribe()
	defer sub.Close()
	target := map[string]any{"targetId": "T1", "type": "page", "url": dest}
	cleanupEvent(t, browser.ctx, ws, "Target.targetInfoChanged", "B1", map[string]any{"targetInfo": target})
	for _, sid := range []string{"N1", "S1"} {
		cleanupEvent(t, browser.ctx, ws, "Page.frameNavigated", sid, map[string]any{"frame": cleanupGuardFrame(dest)})
	}
	for {
		cmd := cleanupReceive(t, browser.ctx, browser.forwarded)
		if cmd.Method == "Target.detachFromTarget" && string(cmd.Params) == `{"sessionId":"S1"}` {
			break
		}
	}
	cleanupEvent(t, browser.ctx, ws, "Target.detachedFromTarget", "", map[string]any{"sessionId": "S1", "targetId": "T1"})
	for {
		ev := cleanupReceive(t, browser.ctx, sub.Events())
		if ev.Method == "Target.detachedFromTarget" {
			if ev.SessionID != "" || string(ev.Params) != `{"sessionId":"S1","targetId":"T1"}` {
				t.Fatalf("unexpected detach: %+v", ev)
			}
			break
		}
	}
}

func TestLeaseCleanupRealGuard(t *testing.T) {
	for _, row := range []struct {
		name, url string
		kept      bool
	}{
		{"R9OrdinaryProtected", "https://seven.sliccy.ai/plain", false},
		{"R9KeptSevenOS", "https://seven.sliccy.ai/os/", true},
		{"R9KeptVault", "https://vault.sliccy.ai/", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			var baseline cleanupResult
			t.Run("baseline", func(t *testing.T) {
				f, browser, tr, ws := newCleanupGuard(t, true)
				cleanupProtectedCommit(t, f, browser, ws, row.url)
				calls, reads := tr.commandCounts()
				baseline = f.action(t, "snapshot", "")
				cleanupRefusal(t, baseline, denied)
				if newCalls, newReads := tr.commandCounts(); newCalls != calls+1 || newReads != reads+1 {
					t.Fatal("baseline refusal must make one frame read")
				}
				if len(tr.closes()) != 0 {
					t.Fatal("baseline attempted scope close")
				}
			})
			t.Run("cleanup", func(t *testing.T) {
				f, browser, tr, ws := newCleanupGuard(t, false)
				cleanupPhase(t)
				cleanupProtectedCommit(t, f, browser, ws, row.url)
				f.b.bs.cleanup.Wait()
				calls, _ := tr.commandCounts()
				got := f.action(t, "snapshot", "")
				cleanupRefusal(t, got, denied)
				if newCalls, _ := tr.commandCounts(); newCalls != calls {
					t.Fatal("cut tab made a CDP call before refusing")
				}
				if got.err != baseline.err || !slices.Equal(got.payload, baseline.payload) {
					t.Fatalf("holder changed: baseline=%+v observed=%+v", baseline, got)
				}
				attempts := tr.closes()
				if len(attempts) != 1 || attempts[0].Session != "" || string(attempts[0].Params) != `{"targetId":"T1"}` {
					t.Fatalf("guarded close attempts=%+v", attempts)
				}
				reply := tr.response(attempts[0].ID)
				if row.kept {
					if reply.Error == nil || reply.Error.Code != -32000 || reply.Error.Message != "slicc: refused: target is on seven's origin" {
						t.Fatalf("kept close result: %+v", reply)
					}
					select {
					case cmd := <-browser.forwarded:
						t.Fatalf("kept tab reached Chrome close: %+v", cmd)
					default:
					}
					t.Logf("guard refused close of T1; browser received no close")
				} else {
					cmd := cleanupReceive(t, browser.ctx, browser.forwarded)
					if cmd.Method != "Target.closeTarget" || reply.Error != nil || !strings.Contains(string(reply.Result), `"success":true`) {
						t.Fatalf("ordinary close got %+v %+v", cmd, reply)
					}
				}
				if browser.live.Load() != row.kept {
					t.Fatalf("mock browser target open=%t want %t", browser.live.Load(), row.kept)
				}
				f.revoke(t)
				if len(tr.closes()) != 1 {
					t.Fatalf("duplicate close on revoke: %+v", tr.closes())
				}
			})
		})
	}
}
