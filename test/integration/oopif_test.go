package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type oopifAnswer struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error"`
	Marked int    `json:"marked"`
}

type oopifRow struct {
	Leg  string `json:"leg"`
	Info struct {
		Type          string `json:"type"`
		URL           string `json:"url"`
		ParentFrameID string `json:"parentFrameId"`
	} `json:"info"`
	Painted    int                    `json:"painted"`
	Screenshot oopifAnswer            `json:"screenshot"`
	Restored   oopifAnswer            `json:"restored"`
	Removed    oopifAnswer            `json:"removed"`
	Probes     map[string]oopifAnswer `json:"probes"`
	Cast       struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Frames int    `json:"frames"`
		Marked int    `json:"marked"`
	} `json:"cast"`
	Live struct {
		Result struct {
			Value int `json:"value"`
		} `json:"result"`
	} `json:"live"`
}

func oopifFixture(t *testing.T) *httptest.Server {
	t.Helper()
	files := http.FileServer(http.Dir(filepath.Join("testdata", "link")))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, port, _ := strings.Cut(r.Host, ":")
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/protected-top", "/protected-nest":
			child := "http://vault.secret.test:" + port + "/child"
			if r.URL.Path == "/protected-top" && r.URL.Query().Get("mode") == "nested" {
				child = "http://seven.protected.test:" + port + "/protected-nest"
			}
			module := `<script>globalThis.linkTest = {};</script>`
			if r.URL.Path == "/protected-top" {
				module = `<script type="module" src="/page.js"></script>`
			}
			_, _ = fmt.Fprintf(w, `<!doctype html><body>%s<script>addEventListener('message', (e) => { if (e.data === 'loaded') { globalThis.frameLoaded = true; if (parent !== self) parent.postMessage('loaded', '*'); } });</script><iframe src="%s"></iframe>`, module, child)
		case "/daily":
			_, _ = fmt.Fprint(w, `<!doctype html><body style="margin:0;background:#f0f"><h1>daily-marker</h1><input autofocus><div id="tick" style="width:16px;height:16px"></div><script>globalThis.linkTest = {}; globalThis.keys = 0; addEventListener('keydown', () => globalThis.keys++); let n = 0; setInterval(() => { document.getElementById('tick').style.background = n++ % 2 ? '#000' : '#888'; }, 40);</script>`)
		case "/child":
			_, _ = fmt.Fprint(w, `<!doctype html><body style="margin:0;background:#f0f"><p>frame</p><script>parent.postMessage('loaded', '*');</script>`)
		case "/top":
			_, _ = fmt.Fprintf(w, `<!doctype html><body style="margin:0;background:#fff"><script>globalThis.linkTest = {}; addEventListener('message', (e) => { if (e.data === 'loaded') globalThis.frameLoaded = true; });</script><div id="tick" style="width:16px;height:16px"></div><iframe src="http://%s:%s/child" style="border:0;width:340px;height:240px"></iframe><script>let n = 0; setInterval(() => { document.getElementById('tick').style.background = n++ %% 2 ? '#000' : '#888'; }, 40);</script>`, r.URL.Query().Get("host"), port)
		case "/plain":
			_, _ = fmt.Fprint(w, `<!doctype html><body style="background:#fff"><p>plain</p>`)
		default:
			w.Header().Del("Content-Type")
			files.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCDPPreAttachedProtectedOOPIFParent(t *testing.T) {
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("CI Chrome required")
		}
		t.Skip(harnessChromeNotice)
	}
	for _, leg := range []string{"control", "guarded", "ordinary"} {
		t.Run(leg, func(t *testing.T) {
			fixture := oopifFixture(t)
			port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
			protected := "http://seven.protected.test:" + port
			host := "seven.protected.test"
			if leg == "ordinary" {
				host = "other.unprotected.test"
			}
			ordinary := "http://page.ordinary.test:" + port
			srv := pageServer(t)
			b := launchBrowserOn(t, 0, "--site-per-process", "--isolate-origins="+protected+",http://other.unprotected.test:"+port, "--host-resolver-rules=MAP *.test 127.0.0.1")
			top := b.openPage(t, ordinary+"/top?host="+host)
			var loaded bool
			top.evalWithin(`new Promise((resolve) => { const check = () => globalThis.frameLoaded ? resolve(true) : setTimeout(check, 20); check(); })`, &loaded, 15*time.Second)
			if !loaded {
				t.Fatal("frame was not loaded before attach")
			}
			gate := newGuardGate(t, b.endpoint, srv.URL)
			env := &linkEnv{t: t, b: b, origin: srv.URL}
			env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--origin", protected, "--name", "oopif-node")
			env.p = b.openPage(t, srv.URL+"/index.html")
			env.waitUp("")
			var hello struct {
				Browsers []helloBrowser `json:"browsers"`
			}
			env.p.eval("linkTest.ready()", &hello)
			if len(hello.Browsers) != 1 {
				t.Fatal("OOPIF browser missing")
			}
			opts := map[string]any{"leg": leg, "browser": hello.Browsers[0].ID, "targetId": top.target, "ws": strings.Replace(gate.srv.URL, "http", "ws", 1) + "/browser", "child": "http://" + host + ":" + port + "/child", "plain": ordinary + "/plain"}
			var raw json.RawMessage
			env.p.evalWithin("linkTest.oopif("+jsArgs(opts)+")", &raw, 2*time.Minute)
			path := filepath.Join("..", "..", "artifacts", "oopif")
			if err := os.MkdirAll(path, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, leg+".json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: %.6000s", leg, raw)
			var row oopifRow
			if err := json.Unmarshal(raw, &row); err != nil {
				t.Fatal(err)
			}
			if row.Leg != leg || row.Info.Type != "iframe" || row.Info.URL != opts["child"] || row.Painted < 1000 {
				t.Fatalf("preloaded cross-process frame not established: %+v", row)
			}
			for name, answer := range map[string]oopifAnswer{"screenshot": row.Screenshot, "restored": row.Restored} {
				if leg == "guarded" {
					if answer.OK || !strings.Contains(answer.Error, "slicc: refused: target is on seven's origin") {
						t.Errorf("%s: protected parent not refused: %+v", name, answer)
					}
				} else if !answer.OK || answer.Marked < 1000 {
					t.Errorf("%s: positive paint control missing: %+v", name, answer)
				}
			}
			if leg == "guarded" {
				if row.Cast.OK || row.Cast.Frames != 0 || !strings.Contains(row.Cast.Error, "slicc: refused: target is on seven's origin") {
					t.Errorf("cast not refused: %+v", row.Cast)
				}
				for method, answer := range row.Probes {
					if answer.OK || !strings.Contains(answer.Error, "slicc: refused: target is on seven's origin") {
						t.Errorf("%s not refused: %+v", method, answer)
					}
				}
			} else if !row.Cast.OK || row.Cast.Marked == 0 {
				t.Errorf("cast positive control missing: %+v", row.Cast)
			}
			if !row.Removed.OK || row.Removed.Marked >= 1000 || row.Live.Result.Value != 42 {
				t.Errorf("parent not drivable after iframe removal: removed=%+v live=%+v", row.Removed, row.Live)
			}
		})
	}
}
