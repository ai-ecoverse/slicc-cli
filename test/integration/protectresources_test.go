package integration

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

var resourceKinds = []string{"document", "fetch", "xhr", "image", "script", "stylesheet", "font", "media", "track", "eventsource", "beacon", "websocket", "preflight", "manifest", "prefetch"}

func resourceRequestFixture(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	counts := map[string]map[string]int{}
	armed := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "X-Guard-Probe, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
		w.Header().Set("Cache-Control", "no-store")
		key := r.URL.Query().Get("key")
		if r.URL.Path == "/arm" {
			mu.Lock()
			armed[key] = r.URL.Query().Get("protected")
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/counts" {
			mu.Lock()
			value := map[string]int{}
			for k, v := range counts[key] {
				value[k] = v
			}
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(value)
			return
		}
		if r.URL.Path == "/resource" || r.URL.Path == "/redirect" || r.URL.Path == "/replay" {
			role := "ordinary"
			if strings.HasPrefix(r.Host, "seven.guard.test:") || strings.HasPrefix(r.Host, "vault.sliccy.ai:") {
				role = "protected"
			}
			if r.URL.Path == "/redirect" {
				role = "redirect"
			}
			mu.Lock()
			if counts[key] == nil {
				counts[key] = map[string]int{}
			}
			counts[key][role]++
			mu.Unlock()
			if r.URL.Path == "/replay" {
				mu.Lock()
				to := armed[key]
				mu.Unlock()
				if to != "" {
					http.Redirect(w, r, to+"/resource?kind=xhr&key="+key, http.StatusFound)
					return
				}
			}
			if r.URL.Path == "/redirect" {
				to := r.URL.Query().Get("protected")
				if to == "" {
					to = "http://seven.guard.test:" + r.Host[strings.LastIndexByte(r.Host, ':')+1:]
				}
				http.Redirect(w, r, to+"/resource?kind="+r.URL.Query().Get("kind")+"&key="+key, http.StatusFound)
				return
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			switch r.URL.Query().Get("kind") {
			case "websocket":
				c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
				if err == nil {
					_ = c.Close(websocket.StatusNormalClosure, "fixture")
				}
				return
			case "image":
				w.Header().Set("Content-Type", "image/png")
				data, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
				_, _ = w.Write(data)
			case "script":
				w.Header().Set("Content-Type", "text/javascript")
				_, _ = w.Write([]byte("globalThis.resourceFixture = true;"))
			case "stylesheet":
				w.Header().Set("Content-Type", "text/css")
				_, _ = w.Write([]byte("body { color: black; }"))
			case "font":
				w.Header().Set("Content-Type", "font/woff2")
				_, _ = w.Write([]byte("wOF2fixture"))
			case "media":
				w.Header().Set("Content-Type", "audio/wav")
				data, _ := base64.StdEncoding.DecodeString("UklGRiQAAABXQVZFZm10IBAAAAABAAEARKwAAIhYAQACABAAZGF0YQAAAAA=")
				_, _ = w.Write(data)
			case "track":
				w.Header().Set("Content-Type", "text/vtt")
				_, _ = w.Write([]byte("WEBVTT\n\n00:00.000 --> 00:01.000\nfixture\n"))
			case "eventsource":
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: fixture\n\n"))
			case "manifest":
				w.Header().Set("Content-Type", "application/manifest+json")
				_, _ = fmt.Fprint(w, `{"name":"fixture","start_url":"/probe"}`)
			default:
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write([]byte("resource fixture"))
			}
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>ordinary resource probe</title><body>probe"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCDPPageResourceTrafficIsExplicitlyAllowedByC5(t *testing.T) {
	fixture := resourceRequestFixture(t)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	ordinary := "http://ordinary.guard.test:" + port
	protected := "http://seven.guard.test:" + port
	srv := pageServer(t)
	b := launchBrowserOn(t, 0, "--host-resolver-rules=MAP *.guard.test 127.0.0.1,MAP vault.sliccy.ai 127.0.0.1")
	gate := newGuardGate(t, b.endpoint, srv.URL)
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--origin", protected, "--name", "resource-probe-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatal("resource probe browser missing")
	}
	for _, host := range []string{"seven.guard.test", "vault.sliccy.ai"} {
		protected := "http://" + host + ":" + port
		for _, kind := range resourceKinds {
			for _, redirect := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/redirect=%t", host, kind, redirect), func(t *testing.T) {
					p := *env.p
					p.t = t
					for _, leg := range []string{"control", "guarded", "guarded-intercept"} {
						key := strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + leg
						actor := leg
						if leg == "guarded-intercept" {
							actor = "guarded"
						}
						opts := map[string]any{"kind": kind, "key": key, "leg": actor, "redirect": redirect, "intercept": leg == "guarded-intercept", "browser": hello.Browsers[0].ID, "ordinary": ordinary, "protected": protected, "ws": strings.Replace(gate.srv.URL, "http", "ws", 1) + "/browser", "counts": fixture.URL + "/counts"}
						var raw json.RawMessage
						p.evalWithin("linkTest.resourceBlock("+jsArgs(opts)+")", &raw, 30*time.Second)
						var row struct {
							Kind            string         `json:"kind"`
							Key             string         `json:"key"`
							Counts          map[string]int `json:"counts"`
							ProtectedPauses int            `json:"protectedPausesShown"`
						}
						if err := json.Unmarshal(raw, &row); err != nil {
							t.Fatal(err)
						}
						path := filepath.Join("..", "..", "artifacts", "protected-resources", host, kind, fmt.Sprintf("redirect-%t", redirect))
						if err := os.MkdirAll(path, 0755); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(path, leg+".json"), raw, 0600); err != nil {
							t.Fatal(err)
						}
						t.Logf("resource %s redirect=%t %s: protected server hits=%d, redirects=%d, ordinary=%d", kind, redirect, leg, row.Counts["protected"], row.Counts["redirect"], row.Counts["ordinary"])
						if row.Kind != kind || row.Key != key {
							t.Fatal("resource observation identity missing")
						}
						if kind == "websocket" && redirect {
							if row.Counts["protected"] != 0 || row.Counts["redirect"] != 1 || !strings.Contains(string(raw), "Unexpected response code: 302") {
								t.Fatalf("native WebSocket redirect outcome not independently proven: %s", raw)
							}
							t.Log("ALLOWED-BY-C5: WebSocket initial request allowed; Chrome itself rejects 302, no protected follow-up")
							continue
						}
						if leg == "control" {
							if row.Counts["protected"] == 0 {
								t.Fatalf("native positive control did not reach protected fixture: %s", raw)
							}
							if redirect && row.Counts["redirect"] == 0 {
								t.Fatal("redirect control did not exercise ordinary hop")
							}
							continue
						}
						if kind == "document" {
							if row.Counts["protected"] != 0 {
								t.Errorf("existing document navigation protection failed: %s", raw)
							}
						} else if row.Counts["protected"] == 0 {
							t.Errorf("allowed-by-C5 page request did not reach protected fixture: %s redirect=%t %s", kind, redirect, leg)
						} else {
							t.Log("ALLOWED-BY-C5: page-initiated resource request, not a client fetch path")
						}
						if row.ProtectedPauses != 0 {
							t.Errorf("protected pause handed to client: %s", raw)
						}
						if redirect && row.Counts["redirect"] == 0 {
							t.Error("ordinary redirect request was not allowed")
						}
					}
				})
			}
		}
	}
}
