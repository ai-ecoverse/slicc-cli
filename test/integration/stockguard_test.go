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

func stockFixture(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>stock guard</title>
<input id="name"><button id="go" onclick="document.getElementById('out').textContent='hello '+document.getElementById('name').value">Go</button>
<span id="out"></span>
<a id="pop" href="/popup" target="_blank">Open</a>`))
	})
	mux.HandleFunc("/popup", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>popup</title><p>popup</p>`))
	})
	mux.HandleFunc("/frame", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>frame</title><iframe id="f" src="/inner"></iframe>`))
	})
	mux.HandleFunc("/inner", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>inner</title><button id="ibtn">Inner</button>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestStockClientsDriveThroughTheLinkGuard(t *testing.T) {
	env := setupProtect(t)
	fixture := stockFixture(t)
	ordinary := strings.Replace(fixture.URL, "127.0.0.1", "localhost", 1) + "/"
	protected := env.seven + "/"
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
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
	upstream := strings.Replace(bridge.URL, "http", "ws", 1) + "/cdp"
	for _, which := range []string{"playwright", "puppeteer"} {
		cmd := exec.CommandContext(ctx, requireNode(t), "testdata/stockguard.mjs", upstream, ordinary, protected, which)
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), which+" ok") {
			t.Fatalf("%s through the link guard: %v\n%s", which, err, out)
		}
	}
}
