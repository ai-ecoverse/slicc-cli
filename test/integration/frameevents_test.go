package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type frameEventsRow struct {
	Mode  string `json:"mode"`
	Kind  string `json:"kind"`
	Leg   string `json:"leg"`
	Setup struct {
		Layout struct {
			Nest  string `json:"nest"`
			Child string `json:"child"`
		} `json:"layout"`
	} `json:"setup"`
	Subscription *struct {
		OK    bool   `json:"ok"`
		Shown bool   `json:"shown"`
		Error string `json:"error"`
	} `json:"subscription"`
	Enabled map[string]string   `json:"enabled"`
	Before  map[string][]string `json:"before"`
	Events  int                 `json:"events"`
	Shown   map[string][]string `json:"shown"`
}

func frameEventsFixture(t *testing.T, needle string) *httptest.Server {
	t.Helper()
	type heldDocument struct {
		release chan struct{}
		once    sync.Once
	}
	var documents sync.Map
	document := func(token string) *heldDocument {
		h, _ := documents.LoadOrStore(token, &heldDocument{release: make(chan struct{})})
		return h.(*heldDocument)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, port, _ := strings.Cut(r.Host, ":")
		w.Header().Set("Content-Type", "text/html")
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch {
		case len(parts) == 2 && parts[0] == "release":
			h := document(parts[1])
			h.once.Do(func() { close(h.release) })
			_, _ = fmt.Fprint(w, "released")
		case len(parts) == 3 && parts[0] == "top" && (parts[1] == "seven" || parts[1] == "ordinary"):
			_, _ = fmt.Fprintf(w, `<!doctype html><body><p>top</p><iframe name="nest" src="http://%s.guard.test:%s/nest/%s?hold=%s" style="width:340px;height:240px"></iframe>`, parts[1], port, parts[2], r.URL.Query().Get("hold"))
		case len(parts) == 1 && parts[0] == "plain":
			_, _ = fmt.Fprint(w, `<!doctype html><body><p>plain</p>`)
		case len(parts) == 2 && parts[0] == "style":
			w.Header().Set("Content-Type", "text/css")
			_, _ = fmt.Fprint(w, `h1 { color: rgb(20, 30, 40); }`)
		case len(parts) == 2 && parts[0] == "nest" && (parts[1] == "blank" || parts[1] == "srcdoc"):
			if token := r.URL.Query().Get("hold"); token != "" {
				select {
				case <-document(token).release:
				case <-r.Context().Done():
					return
				}
			}
			frame := `<iframe name="child"></iframe>`
			if parts[1] == "srcdoc" {
				frame = `<iframe name="child" srcdoc="<body><p>start</p>"></iframe>`
			}
			_, _ = fmt.Fprintf(w, `<!doctype html><title>%s</title><body><p>nest</p>%s<script>let n = 0; setInterval(() => { const d = frames.child.document; if (d.body) { if (!d.querySelector('link')) { const l = d.createElement('link'); l.rel = 'stylesheet'; l.href = '/style/%s.css'; d.head.append(l); } let h = d.querySelector('h1'); if (!h) { h = d.createElement('h1'); h.textContent = '%s-' + n++; d.body.append(h); } else { h.firstChild.data = '%s-' + n++; } const p = d.createElement('p'); p.textContent = '%s-insert-' + n; d.body.append(p); if (d.body.children.length > 6) d.body.lastChild.remove(); d.title = h.textContent; document.title = h.textContent; } }, 100);</script>`, needle, frame, needle, needle, needle, needle)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCDPEventsAcrossNestedFrames(t *testing.T) {
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("CI Chrome required")
		}
		t.Skip(harnessChromeNotice)
	}
	needle := "nestedmark" + fmt.Sprint(time.Now().UnixNano())
	fixture := frameEventsFixture(t, needle)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	ordinary := "http://ordinary.guard.test:" + port
	protected := "http://seven.guard.test:" + port
	srv := pageServer(t)
	b := launchBrowserOn(t, 0, "--force-renderer-accessibility", "--host-resolver-rules=MAP *.guard.test 127.0.0.1")
	gate := newGuardGate(t, b.endpoint, srv.URL)
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--origin", protected, "--name", "frame-events-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatal("frame events browser missing")
	}
	for _, mode := range []string{"start", "running"} {
		for _, kind := range []string{"blank", "srcdoc"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				p := *env.p
				p.t = t
				rows := map[string]frameEventsRow{}
				for _, leg := range []string{"control", "guarded", "ordinary"} {
					nest := "seven"
					if leg == "ordinary" {
						nest = "ordinary"
					}
					token := ""
					if mode == "running" {
						token = fmt.Sprint(time.Now().UnixNano()) + kind + leg
					}
					opts := map[string]any{"mode": mode, "kind": kind, "leg": leg, "needle": needle, "browser": hello.Browsers[0].ID, "ws": strings.Replace(gate.srv.URL, "http", "ws", 1) + "/browser", "page": ordinary + "/top/" + nest + "/" + kind + "?hold=" + token, "gate": gate.srv.URL + "/gate", "release": ordinary + "/release/" + token}
					var raw json.RawMessage
					p.evalWithin("linkTest.frameEvents("+jsArgs(opts)+")", &raw, 2*time.Minute)
					path := filepath.Join("..", "..", "artifacts", "frame-events", mode, kind)
					if err := os.MkdirAll(path, 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(path, leg+".json"), raw, 0600); err != nil {
						t.Fatal(err)
					}
					t.Logf("%s %s %s: %.3000s", mode, kind, leg, raw)
					var row frameEventsRow
					if err := json.Unmarshal(raw, &row); err != nil {
						t.Fatalf("%s: %v", leg, err)
					}
					want := protected
					if nest == "ordinary" {
						want = ordinary
					}
					if row.Leg != leg || row.Setup.Layout.Nest != want || row.Setup.Layout.Child != want {
						t.Fatalf("%s: nested frame not on %s: %+v", leg, want, row.Setup)
					}
					if mode == "running" && row.Enabled["DOM.getDocument"] != "" {
						t.Fatalf("%s: document was not obtained before protection: %v", leg, row.Enabled)
					}
					if mode == "running" {
						if row.Subscription == nil {
							t.Fatalf("%s: late AX subscription missing", leg)
						}
						if leg == "guarded" && row.Subscription.Shown {
							t.Errorf("guarded: in-flight AX answer showed protected names or values")
						}
						if leg != "guarded" && !row.Subscription.Shown {
							t.Errorf("%s: in-flight AX answer did not show the protected frame", leg)
						}
					}
					rows[leg] = row
				}
				if len(rows["control"].Shown) == 0 || (mode == "running" && len(rows["control"].Shown["Accessibility"]) == 0) {
					t.Fatalf("control: no Accessibility event showed the nested frame's text")
				}
				if len(rows["ordinary"].Shown) == 0 {
					t.Errorf("ordinary: no event showed the nested frame's text")
				}
				if mode == "start" && len(rows["guarded"].Before) != 0 {
					t.Errorf("guarded: enable events showed protected text: %v", rows["guarded"].Before)
				}
				if len(rows["guarded"].Shown) != 0 {
					t.Errorf("guarded: events showed the nested frame's text: %v", rows["guarded"].Shown)
				}
			})
		}
	}
}
