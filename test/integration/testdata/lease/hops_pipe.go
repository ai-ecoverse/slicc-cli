package lease

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
	"github.com/coder/websocket"
)

const (
	hopRedirect  = "https://billing.ordinary.test/redirect"
	hopProtected = "https://seven.sliccy.ai/secret?n=pipe"
	hopOrdinary  = "https://billing.ordinary.test/app"
)

type hopFake struct {
	mu            sync.Mutex
	fetchArmed    bool
	createURL     string
	navigateURL   string
	protectedLoad int
	failRequest   int
	continueReq   int
	url           string
	loader        string
}

func (f *hopFake) serve(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = c.CloseNow() }()
	c.SetReadLimit(-1)
	send := func(m map[string]any) bool {
		b, _ := json.Marshal(m)
		return c.Write(context.Background(), websocket.MessageText, b) == nil
	}
	f.mu.Lock()
	f.url, f.loader = "about:blank", "L0"
	f.mu.Unlock()
	info := func() map[string]any {
		f.mu.Lock()
		defer f.mu.Unlock()
		return map[string]any{"targetId": "T", "type": "page", "url": f.url, "title": "hop", "browserContextId": "CTX", "attached": true}
	}
	originOf := func(u string) string {
		if u == "about:blank" || u == "" {
			return "://"
		}
		if strings.HasPrefix(u, "chrome-error:") {
			return "://"
		}
		if i := strings.Index(u[8:], "/"); i >= 0 {
			return u[:8+i]
		}
		return u
	}
	for {
		_, data, err := c.Read(r.Context())
		if err != nil {
			return
		}
		var m struct {
			ID        int64           `json:"id"`
			Method    string          `json:"method"`
			SessionID string          `json:"sessionId"`
			Params    json.RawMessage `json:"params"`
		}
		if json.Unmarshal(data, &m) != nil {
			return
		}
		result := any(map[string]any{})
		switch m.Method {
		case "Target.attachToBrowserTarget":
			result = map[string]any{"sessionId": "R"}
		case "Target.setDiscoverTargets":
		case "Target.getTargets":
			result = map[string]any{"targetInfos": []any{}}
		case "Target.createTarget":
			var p struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal(m.Params, &p)
			f.mu.Lock()
			f.createURL = p.URL
			armed := f.fetchArmed
			if p.URL == "" {
				p.URL = "about:blank"
			}
			f.url = p.URL
			f.loader = "L-create"
			f.mu.Unlock()
			result = map[string]any{"targetId": "T"}
			_ = send(map[string]any{"method": "Target.targetCreated", "sessionId": "R", "params": map[string]any{"targetInfo": info()}})
			if p.URL != "about:blank" && strings.Contains(p.URL, "redirect") && !armed {
				f.mu.Lock()
				f.protectedLoad++
				f.url = hopProtected
				f.mu.Unlock()
				_ = send(map[string]any{"method": "Target.targetInfoChanged", "sessionId": "R", "params": map[string]any{"targetInfo": info()}})
			}
		case "Target.attachToTarget":
			session := "S"
			if m.ID >= 1879048192 {
				session = "N"
			}
			result = map[string]any{"sessionId": session}
			_ = send(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": session, "waitingForDebugger": false, "targetInfo": info()}})
		case "Target.getTargetInfo":
			result = map[string]any{"targetInfo": info()}
		case "Fetch.enable":
			f.mu.Lock()
			f.fetchArmed = true
			f.mu.Unlock()
		case "Page.navigate":
			var p struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal(m.Params, &p)
			f.mu.Lock()
			f.navigateURL = p.URL
			armed := f.fetchArmed
			f.mu.Unlock()
			dest := p.URL
			if strings.Contains(dest, "redirect") || strings.Contains(dest, "meta") || strings.Contains(dest, "script") {
				dest = hopProtected
			}
			if strings.Contains(dest, "seven.sliccy.ai") {
				if armed {
					_ = send(map[string]any{"method": "Fetch.requestPaused", "sessionId": "N", "params": map[string]any{
						"requestId": "P1", "networkId": "N1", "frameId": "T", "resourceType": "Document",
						"request": map[string]any{"url": dest, "method": "GET", "headers": map[string]any{}},
					}})
					f.mu.Lock()
					f.url = "chrome-error://chromewebdata/"
					f.loader = "L-blocked"
					f.mu.Unlock()
				} else {
					f.mu.Lock()
					f.protectedLoad++
					f.url = dest
					f.mu.Unlock()
				}
			} else {
				f.mu.Lock()
				f.url = dest
				f.loader = "L-nav"
				f.mu.Unlock()
			}
		case "Fetch.failRequest":
			f.mu.Lock()
			f.failRequest++
			f.mu.Unlock()
		case "Fetch.continueRequest":
			f.mu.Lock()
			f.continueReq++
			f.mu.Unlock()
		case "Page.getFrameTree":
			f.mu.Lock()
			u, loader := f.url, f.loader
			f.mu.Unlock()
			frame := map[string]any{"id": "T", "loaderId": loader, "url": u, "securityOrigin": originOf(u), "mimeType": "text/html"}
			if strings.HasPrefix(u, "chrome-error:") {
				frame["unreachableUrl"] = hopProtected
			}
			result = map[string]any{"frameTree": map[string]any{"frame": frame}}
		case "Page.getNavigationHistory":
			f.mu.Lock()
			u := f.url
			f.mu.Unlock()
			result = map[string]any{"currentIndex": 0, "entries": []any{map[string]any{"id": 1, "url": u, "userTypedURL": u, "title": "hop", "transitionType": "typed"}}}
		case "Page.enable", "Runtime.enable", "Network.enable", "Network.setBypassServiceWorker":
		case "Runtime.evaluate":
			result = map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"ok": true}}}
		case "Page.createIsolatedWorld":
			result = map[string]any{"executionContextId": 2}
			_ = send(map[string]any{"method": "Runtime.executionContextCreated", "sessionId": m.SessionID, "params": map[string]any{"context": map[string]any{
				"id": 2, "origin": originOf(hopOrdinary), "name": "slicc-world", "uniqueId": "u1",
				"auxData": map[string]any{"isDefault": false, "type": "isolated", "frameId": "T"},
			}}})
		}
		out := map[string]any{"id": m.ID, "result": result}
		if m.SessionID != "" {
			out["sessionId"] = m.SessionID
		}
		if !send(out) {
			return
		}
	}
}

func hopServer(t *testing.T) (*hopFake, string) {
	t.Helper()
	f := &hopFake{}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"Browser": "Chrome/120.0.0.0", "Protocol-Version": "1.3", "webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/browser/hop"})
	})
	mux.HandleFunc("/json/list", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("/devtools/browser/hop", f.serve)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func TestLeasePipeCreateArmsBeforeProtectedHop(t *testing.T) {
	f, base := hopServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	bs, err := openBrowser(ctx, base, o)
	if err != nil {
		t.Fatal(err)
	}
	tab := &tab{}
	_ = bs.create(ctx, tab, hopRedirect)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		u := f.createURL
		f.mu.Unlock()
		if u != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createURL != "about:blank" {
		t.Fatalf("createTarget url=%q, want about:blank so Fetch can arm before the first dest hop", f.createURL)
	}
	if f.protectedLoad != 0 {
		t.Fatalf("protected document loaded without fail (%d)", f.protectedLoad)
	}
	if f.navigateURL != hopRedirect {
		t.Fatalf("Page.navigate url=%q, want %s", f.navigateURL, hopRedirect)
	}
}

func TestLeaseChromeCreateRedirectHop(t *testing.T) {
	endpoint := os.Getenv("SLICC_LEASE_HOP_ENDPOINT")
	dest := os.Getenv("SLICC_LEASE_HOP_DEST")
	if endpoint == "" || dest == "" {
		t.Skip("real-Chrome lease hop harness")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	bs, err := openBrowser(ctx, endpoint, o)
	if err != nil {
		t.Fatal(err)
	}
	tab := &tab{}
	if err := bs.create(ctx, tab, dest); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bs.discard(tab) })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, e := bs.stamp(ctx, tab); e != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("stamp stayed admitted after lease create to a 302→protected dest (snapshot would be readable)")
}
