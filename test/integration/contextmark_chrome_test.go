package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type contextMarkFresh struct {
	Label       string `json:"label"`
	Attached    bool   `json:"attached"`
	AttachError string `json:"attachError"`
	Evaluated   bool   `json:"evaluated"`
	Href        string `json:"href"`
}

func TestCDPDefaultContextMarkClearsOnRealNavigation(t *testing.T) {
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("step launch: CI Chrome required for the default-context mark row")
		}
		t.Skip(harnessChromeNotice)
	}
	var mu sync.Mutex
	armedSession, armedTarget := "", ""
	rewritten := 0
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/arm" {
			mu.Lock()
			if r.URL.Query().Get("read") == "" {
				armedSession, armedTarget = r.URL.Query().Get("session"), r.URL.Query().Get("target")
			}
			count := rewritten
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(count)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(w, "<!doctype html><title>context mark %s</title><body>%s", r.URL.Path, r.URL.Path)
	}))
	t.Cleanup(fixture.Close)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	ordinary := "http://page.ordinary.test:" + port
	protected := "http://seven.protected.test:" + port
	srv := pageServer(t)
	b := launchBrowserOn(t, 0, "--host-resolver-rules=MAP *.test 127.0.0.1")
	gate := newGuardGate(t, b.endpoint, srv.URL)
	gate.transform = func(data []byte) []byte {
		if !strings.Contains(string(data), `"Runtime.executionContextCreated"`) {
			return data
		}
		var m map[string]any
		if json.Unmarshal(data, &m) != nil {
			return data
		}
		session, _ := m["sessionId"].(string)
		params, _ := m["params"].(map[string]any)
		context, _ := params["context"].(map[string]any)
		aux, _ := context["auxData"].(map[string]any)
		isDefault, _ := aux["isDefault"].(bool)
		frame, _ := aux["frameId"].(string)
		mu.Lock()
		defer mu.Unlock()
		if armedSession == "" || session != armedSession || !isDefault || frame != armedTarget {
			return data
		}
		context["origin"] = protected
		armedSession = ""
		rewritten++
		out, err := json.Marshal(m)
		if err != nil {
			return data
		}
		return out
	}
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--origin", protected, "--name", "context-mark-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatalf("step hello: browsers %+v", hello.Browsers)
	}
	opts := map[string]any{"browser": hello.Browsers[0].ID, "ws": strings.Replace(gate.srv.URL, "http", "ws", 1) + "/browser", "first": ordinary + "/first", "second": ordinary + "/second", "arm": fixture.URL + "/arm"}
	var raw json.RawMessage
	env.p.evalWithin("linkTest.contextMark("+jsArgs(opts)+")", &raw, 120*time.Second)
	var row struct {
		Steps []struct {
			Name string `json:"name"`
			MS   int64  `json:"ms"`
		} `json:"steps"`
		Control   contextMarkFresh  `json:"control"`
		Marked    contextMarkFresh  `json:"marked"`
		Titled    contextMarkFresh  `json:"titled"`
		Cleared   *contextMarkFresh `json:"cleared"`
		Rewritten int               `json:"rewritten"`
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatalf("step result: %v\n%s", err, raw)
	}
	for _, s := range row.Steps {
		t.Logf("step %s at %d ms", s.Name, s.MS)
	}
	lines := guardLines(env.a)
	for _, line := range strings.Split(lines, "\n") {
		if strings.Contains(line, "default-context mark") || strings.Contains(line, "default context marks") {
			t.Logf("guard: %s", line)
		}
	}
	switch {
	case !row.Control.Attached || !row.Control.Evaluated:
		t.Fatalf("step control: an ordinary page was not admitted before the mark: %s\nguard:\n%s", raw, lines)
	case row.Rewritten != 1:
		t.Fatalf("step rewrite: the client's default context was rewritten %d times: %s\nguard:\n%s", row.Rewritten, raw, lines)
	case !strings.Contains(lines, "default context marks it protected"):
		t.Fatalf("step mark: the guard did not mark the page from its default context\n%s\nguard:\n%s", raw, lines)
	case row.Marked.Attached && row.Marked.Evaluated:
		t.Fatalf("step marked: a fresh session on the marked page was admitted: %s\nguard:\n%s", raw, lines)
	case row.Titled.Attached && row.Titled.Evaluated:
		t.Fatalf("step title: a title change unprotected the marked page: %s\nguard:\n%s", raw, lines)
	case row.Cleared == nil:
		t.Fatalf("step clear: after the tab navigated to an ordinary page, no fresh session was admitted within the bound: %s\nguard:\n%s", raw, lines)
	case !strings.Contains(lines, "cleared a target's default-context mark"):
		t.Fatalf("step clear-log: the guard admitted the page without clearing its mark\n%s\nguard:\n%s", raw, lines)
	}
}
