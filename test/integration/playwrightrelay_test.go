package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type playwrightEnvelope struct {
	ID      int    `json:"id"`
	Open    bool   `json:"open,omitempty"`
	Close   bool   `json:"close,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func TestPlaywrightCLIBasicStepsThroughRelayWithLaterRefusals(t *testing.T) {
	env := setupProtect(t)
	var mu sync.Mutex
	var host *websocket.Conn
	clients := map[int]*websocket.Conn{}
	writers := map[int]*sync.Mutex{}
	next := 0
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
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
		conn.SetReadLimit(64 * 1024 * 1024)
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
					return
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
		t.Fatal("raw relay adapter did not open")
	}
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Relay smoke</title><h1>Ordinary page</h1><button onclick="document.body.dataset.clicked='yes'">Click me</button><input aria-label="Name">`))
	}))
	t.Cleanup(plain.Close)
	url := strings.Replace(plain.URL, "127.0.0.1", "localhost", 1)
	cmd := exec.CommandContext(ctx, requireNode(t), "testdata/playwright-relay.mjs", strings.Replace(bridge.URL, "http", "ws", 1)+"/cdp", url)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("playwright-cli basic relay steps: %v\n%s", err, out)
	}
	t.Logf("playwright-cli actual results: %s", out)
}
