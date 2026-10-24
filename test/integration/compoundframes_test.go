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

type compoundRow struct {
	Leg  string `json:"leg"`
	Info struct {
		Type          string `json:"type"`
		URL           string `json:"url"`
		ParentFrameID string `json:"parentFrameId"`
	} `json:"info"`
	Layout struct {
		Origin string `json:"origin"`
		Child  string `json:"child"`
	} `json:"layout"`
	Painted int `json:"painted"`
	Own     struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	} `json:"own"`
	Screenshot struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Marked int    `json:"marked"`
	} `json:"screenshot"`
	Cast struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Frames int    `json:"frames"`
		Marked int    `json:"marked"`
	} `json:"cast"`
}

func compoundFixture(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, port, _ := strings.Cut(r.Host, ":")
		w.Header().Set("Content-Type", "text/html")
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 3 {
			http.NotFound(w, r)
			return
		}
		switch parts[0] {
		case "top":
			_, _ = fmt.Fprintf(w, `<!doctype html><body style="margin:0;background:#fff"><script>globalThis.linkTest = {}; addEventListener('message', (e) => { if (e.data === 'compound-loaded') globalThis.compoundLoaded = true; });</script><div id="tick" style="width:16px;height:16px"></div><iframe src="http://ordinary.guard.test:%s/middle/%s/%s" style="border:0;width:380px;height:280px"></iframe><script>let n = 0; setInterval(() => { document.getElementById('tick').style.background = n++ %% 2 ? '#000' : '#888'; }, 40);</script>`, port, parts[1], parts[2])
		case "middle":
			_, _ = fmt.Fprintf(w, `<!doctype html><body style="margin:0;background:#fff"><script>addEventListener('message', (e) => { if (e.data === 'compound-loaded') parent.postMessage(e.data, '*'); });</script><iframe name="nest" src="http://%s.guard.test:%s/leaf/%s/%s" style="border:0;width:360px;height:260px"></iframe>`, parts[1], port, parts[1], parts[2])
		case "leaf":
			if parts[2] == "srcdoc" {
				_, _ = fmt.Fprint(w, `<!doctype html><body style="margin:0;background:#fff"><iframe name="child" style="border:0;width:320px;height:220px" srcdoc="<body style='margin:0;background:#f0f'><script>parent.parent.postMessage('compound-loaded', '*')</script>"></iframe>`)
			} else {
				_, _ = fmt.Fprint(w, `<!doctype html><body style="margin:0;background:#fff"><iframe name="child" style="border:0;width:320px;height:220px"></iframe><script>frames.child.document.body.style.cssText = 'margin:0;background:#f0f'; parent.postMessage('compound-loaded', '*');</script>`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCDPCompoundFramesProtectPageAncestors(t *testing.T) {
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("CI Chrome required")
		}
		t.Skip(harnessChromeNotice)
	}
	for _, kind := range []string{"blank", "srcdoc"} {
		for _, leg := range []string{"control", "guarded", "ordinary"} {
			t.Run(kind+"/"+leg, func(t *testing.T) {
				fixture := compoundFixture(t)
				port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
				protected := "http://seven.guard.test:" + port
				middle := "http://ordinary.guard.test:" + port
				nest := "seven"
				if leg == "ordinary" {
					nest = "other"
				}
				srv := pageServer(t)
				b := launchBrowserOn(t, 0, "--site-per-process", "--host-resolver-rules=MAP *.test 127.0.0.1")
				top := b.openPage(t, "http://page.outer.test:"+port+"/top/"+nest+"/"+kind)
				var loaded bool
				top.evalWithin(`new Promise((resolve) => { const check = () => globalThis.compoundLoaded ? resolve(true) : setTimeout(check, 20); check(); })`, &loaded, 15*time.Second)
				if !loaded {
					t.Fatal("compound frames were not loaded before attach")
				}
				gate := newGuardGate(t, b.endpoint, srv.URL)
				env := &linkEnv{t: t, b: b, origin: srv.URL, p: b.openPage(t, srv.URL+"/index.html")}
				env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--origin", protected, "--name", "compound-frames-node")
				env.waitUp("")
				var hello struct {
					Browsers []helloBrowser `json:"browsers"`
				}
				env.p.eval("linkTest.ready()", &hello)
				if len(hello.Browsers) != 1 {
					t.Fatal("compound browser missing")
				}
				opts := map[string]any{"kind": kind, "leg": leg, "browser": hello.Browsers[0].ID, "targetId": top.target, "middle": middle + "/middle/" + nest + "/" + kind, "protected": protected, "ws": strings.Replace(gate.srv.URL, "http", "ws", 1) + "/browser"}
				var raw json.RawMessage
				env.p.evalWithin("linkTest.compoundFrames("+jsArgs(opts)+")", &raw, 2*time.Minute)
				path := filepath.Join("..", "..", "artifacts", "compound-frames", kind)
				if err := os.MkdirAll(path, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, leg+".json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
				t.Logf("%s %s: %.5000s", kind, leg, raw)
				var row compoundRow
				if err := json.Unmarshal(raw, &row); err != nil {
					t.Fatal(err)
				}
				want := protected
				if leg == "ordinary" {
					want = "http://other.guard.test:" + port
				}
				if row.Leg != leg || row.Info.Type != "iframe" || row.Info.URL != opts["middle"] || row.Info.ParentFrameID != top.target || row.Layout.Origin != want || row.Layout.Child != want || row.Painted < paintThreshold {
					t.Fatalf("compound renderer/origin/control not established: %s", raw)
				}
				refused := "slicc: refused: target is on seven's origin"
				if leg == "guarded" {
					if row.Own.OK || !strings.Contains(row.Own.Error, refused) {
						t.Fatalf("guard did not know the iframe renderer held a protected child: %+v", row.Own)
					}
					if row.Screenshot.OK || row.Screenshot.Marked != 0 || !strings.Contains(row.Screenshot.Error, refused) || row.Cast.OK || row.Cast.Frames != 0 || !strings.Contains(row.Cast.Error, refused) {
						t.Errorf("page ancestor still exposed the known protected renderer: screenshot=%+v cast=%+v", row.Screenshot, row.Cast)
					}
				} else if !row.Screenshot.OK || row.Screenshot.Marked < paintThreshold || !row.Cast.OK || row.Cast.Marked == 0 || (leg == "ordinary" && strings.Contains(row.Own.Error, refused)) {
					t.Errorf("positive/ordinary rendering control missing: %s", raw)
				}
			})
		}
	}
}
