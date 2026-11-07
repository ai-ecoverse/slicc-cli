package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestG0StockClientsThroughLinkGuard(t *testing.T) {
	b := launchBrowserOn(t, 0, "--host-resolver-rules=MAP frame.test 127.0.0.1")
	env := setupProtectOn(t, b)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	var mu sync.Mutex
	var host *websocket.Conn
	clients := map[int]*websocket.Conn{}
	writers := map[int]*sync.Mutex{}
	next := 0
	writeHost := func(e playwrightEnvelope) error {
		mu.Lock()
		defer mu.Unlock()
		body, err := json.Marshal(e)
		if err != nil {
			return err
		}
		return host.Write(ctx, websocket.MessageText, body)
	}
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "test done") }()
		conn.SetReadLimit(256 * 1024 * 1024)
		if r.URL.Path == "/host" {
			mu.Lock()
			host = conn
			mu.Unlock()
			for {
				_, raw, err := conn.Read(ctx)
				if err != nil {
					return
				}
				var e playwrightEnvelope
				if err := json.Unmarshal(raw, &e); err != nil {
					return
				}
				mu.Lock()
				client, writer := clients[e.ID], writers[e.ID]
				mu.Unlock()
				if client == nil {
					continue
				}
				if e.Error != "" {
					_ = client.Close(websocket.StatusInternalError, e.Error)
					continue
				}
				writer.Lock()
				err = client.Write(ctx, websocket.MessageText, []byte(e.Message))
				writer.Unlock()
				if err != nil {
					mu.Lock()
					delete(clients, e.ID)
					delete(writers, e.ID)
					mu.Unlock()
				}
			}
		}
		mu.Lock()
		next++
		id := next
		clients[id] = conn
		writers[id] = &sync.Mutex{}
		mu.Unlock()
		defer func() {
			_ = writeHost(playwrightEnvelope{ID: id, Close: true})
			mu.Lock()
			delete(clients, id)
			delete(writers, id)
			mu.Unlock()
		}()
		if err := writeHost(playwrightEnvelope{ID: id, Open: true}); err != nil {
			return
		}
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if err := writeHost(playwrightEnvelope{ID: id, Message: string(raw)}); err != nil {
				return
			}
		}
	}))
	t.Cleanup(bridge.Close)
	var ready bool
	env.p.eval("linkTest.playwrightRelay("+jsArgs(strings.Replace(bridge.URL, "http", "ws", 1)+"/host", "chrome")+")", &ready)
	if !ready {
		t.Fatal("relay did not open")
	}
	version, err := getVersion(b.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	direct, _ := version["webSocketDebuggerUrl"].(string)
	out := os.Getenv("G0_OUT")
	if out == "" {
		out = filepath.Join(t.TempDir(), "g0.json")
	}
	cmd := exec.CommandContext(ctx, requireNode(t), "testdata/g0.mjs", out, "control="+direct, "link-guard="+strings.Replace(bridge.URL, "http", "ws", 1)+"/cdp")
	cmd.Env = append(os.Environ(), "G0_FRAME_HOST=frame.test")
	got, err := cmd.CombinedOutput()
	t.Logf("g0 measurement (%s):\n%s", out, got)
	if err != nil {
		t.Fatalf("g0 measurement: %v", err)
	}
}
