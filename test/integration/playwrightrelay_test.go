package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/coder/websocket"
)

type playwrightEnvelope struct {
	ID      int    `json:"id"`
	Open    bool   `json:"open,omitempty"`
	Close   bool   `json:"close,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func waitExactPlaywrightTarget(ctx context.Context, read func(context.Context) ([]cdp.TargetInfo, error), url string) (cdp.TargetInfo, error) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		infos, err := read(ctx)
		if err != nil {
			return cdp.TargetInfo{}, err
		}
		for _, info := range infos {
			if info.Type == "page" && info.URL == url {
				return info, nil
			}
		}
		select {
		case <-ctx.Done():
			return cdp.TargetInfo{}, fmt.Errorf("exact ordinary fixture target %s not observed: %w", url, ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestPlaywrightTargetWaitIsBoundedAndExact(t *testing.T) {
	want := "http://localhost:4711/"
	calls := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	read := func(context.Context) ([]cdp.TargetInfo, error) {
		calls++
		if calls == 1 {
			return []cdp.TargetInfo{{TargetID: "wrong", Type: "page", URL: want + "other"}}, nil
		}
		return []cdp.TargetInfo{{TargetID: "exact", Type: "page", URL: want}}, nil
	}
	info, err := waitExactPlaywrightTarget(ctx, read, want)
	if err != nil || calls != 2 || info.TargetID != "exact" {
		t.Fatalf("target selected before exact URL arrived: %+v %v calls=%d", info, err, calls)
	}
	deadline, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	info, err = waitExactPlaywrightTarget(deadline, func(context.Context) ([]cdp.TargetInfo, error) {
		return []cdp.TargetInfo{{TargetID: "wrong", Type: "page", URL: want + "other"}}, nil
	}, want)
	if err == nil || info.TargetID != "" || !strings.Contains(err.Error(), "not observed") {
		t.Fatalf("missing exact target was accepted: %+v %v", info, err)
	}
}

func TestPlaywrightCLIBasicStepsThroughRelayWithLaterRefusals(t *testing.T) {
	env := setupProtect(t)
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Relay smoke</title><h1>Ordinary page</h1><button onclick="document.body.dataset.clicked='yes'">Click me</button><input aria-label="Name">`))
	}))
	t.Cleanup(plain.Close)
	url := strings.Replace(plain.URL, "127.0.0.1", "localhost", 1) + "/"
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
		if r.URL.Path == "/foreground" {
			wait, stop := context.WithTimeout(ctx, 10*time.Second)
			defer stop()
			info, err := waitExactPlaywrightTarget(wait, env.b.client.Targets, url)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			s, err := env.b.client.Attach(wait, info.TargetID)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			defer func() { _ = s.Detach(ctx) }()
			if err := s.Call(wait, "Page.bringToFront", nil, nil); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			var visible struct {
				Visible bool `json:"visible"`
				Focused bool `json:"focused"`
				Frames  int  `json:"frames"`
			}
			err = s.Evaluate(wait, `new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve({visible:document.visibilityState==='visible', focused:document.hasFocus(),frames:2}))))`, cdp.EvaluateOptions{AwaitPromise: true, ReturnByValue: true}, &visible)
			if err != nil || !visible.Visible || !visible.Focused || visible.Frames != 2 {
				http.Error(w, "ordinary fixture did not become focused and rendered", 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(visible)
			return
		}
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
	cmd := exec.CommandContext(ctx, requireNode(t), "testdata/playwright-relay.mjs", strings.Replace(bridge.URL, "http", "ws", 1)+"/cdp", url, bridge.URL+"/foreground")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("playwright-cli basic relay steps: %v\n%s", err, out)
	}
	t.Logf("playwright-cli actual results: %s", out)
}
