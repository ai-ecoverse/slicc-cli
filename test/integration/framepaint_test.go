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

const paintThreshold = 1000

type paintTally struct {
	Frames int `json:"frames"`
	Marked int `json:"marked"`
	Most   int `json:"most"`
}

type paintCast struct {
	Start struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	} `json:"start"`
	paintTally
	Before       int        `json:"before"`
	BeforeMarked int        `json:"beforeMarked"`
	Since        paintTally `json:"since"`
	After        paintTally `json:"after"`
}

type paintLayers struct {
	Layers   int      `json:"layers"`
	Made     int      `json:"made"`
	Replayed int      `json:"replayed"`
	Marked   int      `json:"marked"`
	Most     int      `json:"most"`
	Profiled int      `json:"profiled"`
	Logged   int      `json:"logged"`
	LogShown bool     `json:"logShown"`
	Errors   []string `json:"errors"`
}

type paintRow struct {
	Mode  string `json:"mode"`
	Kind  string `json:"kind"`
	Leg   string `json:"leg"`
	Setup struct {
		Layout struct {
			Nest  string `json:"nest"`
			Child string `json:"child"`
			URL   string `json:"url"`
		} `json:"layout"`
		Painted int  `json:"painted"`
		Away    *int `json:"away"`
	} `json:"setup"`
	Cast          *paintCast   `json:"cast"`
	Layers        *paintLayers `json:"layers"`
	RestoreType   string       `json:"restoreType"`
	RestoredPaint int          `json:"restoredPaint"`
}

func paintFixture(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, port, _ := strings.Cut(r.Host, ":")
		w.Header().Set("Content-Type", "text/html")
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		tick := `<div id="tick" style="width:16px;height:16px;background:#000"></div><script>let n = 0; setInterval(() => { document.getElementById('tick').style.background = n++ % 2 ? '#000' : '#888'; }, 40);</script>`
		switch {
		case len(parts) == 3 && parts[0] == "top" && (parts[1] == "seven" || parts[1] == "ordinary"):
			nest := fmt.Sprintf("http://%s.guard.test:%s/nest/%s", parts[1], port, parts[2])
			_, _ = fmt.Fprintf(w, `<!doctype html><body style="margin:0;background:#fff">%s<iframe src="%s" style="border:0;width:340px;height:240px"></iframe>`, tick, nest)
		case len(parts) == 1 && parts[0] == "plain":
			_, _ = fmt.Fprintf(w, `<!doctype html><body style="margin:0;background:#fff">%s<p>plain</p>`, tick)
		case len(parts) == 2 && parts[0] == "nest" && parts[1] == "srcdoc":
			_, _ = fmt.Fprint(w, `<!doctype html><body style="margin:0;background:#fff"><iframe name="child" style="border:0;width:320px;height:220px" srcdoc="<body style='margin:0;background:#f0f'>"></iframe>`)
		case len(parts) == 2 && parts[0] == "nest" && parts[1] == "blank":
			_, _ = fmt.Fprint(w, `<!doctype html><body style="margin:0;background:#fff"><iframe name="child" style="border:0;width:320px;height:220px"></iframe><script>frames.child.document.body.style.cssText = 'margin:0;background:#f0f';</script>`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCDPPaintPathsAcrossNestedFrames(t *testing.T) {
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("CI Chrome required")
		}
		t.Skip(harnessChromeNotice)
	}
	fixture := paintFixture(t)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	ordinary := "http://ordinary.guard.test:" + port
	protected := "http://seven.guard.test:" + port
	srv := pageServer(t)
	b := launchBrowserOn(t, 0, "--host-resolver-rules=MAP *.guard.test 127.0.0.1")
	gate := newGuardGate(t, b.endpoint, srv.URL)
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--origin", protected, "--name", "paint-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatal("paint measurement browser missing")
	}
	for _, mode := range []string{"start", "running", "layers"} {
		for _, kind := range []string{"blank", "srcdoc"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				p := *env.p
				p.t = t
				for _, leg := range []string{"control", "guarded", "ordinary"} {
					nest := "seven"
					if leg == "ordinary" {
						nest = "ordinary"
					}
					opts := map[string]any{"mode": mode, "kind": kind, "leg": leg, "browser": hello.Browsers[0].ID, "ws": strings.Replace(gate.srv.URL, "http", "ws", 1) + "/browser", "page": ordinary + "/top/" + nest + "/" + kind, "plain": ordinary + "/plain"}
					var raw json.RawMessage
					p.evalWithin("linkTest.paint("+jsArgs(opts)+")", &raw, 2*time.Minute)
					path := filepath.Join("..", "..", "artifacts", "paint", mode, kind)
					if err := os.MkdirAll(path, 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(path, leg+".json"), raw, 0600); err != nil {
						t.Fatal(err)
					}
					var row paintRow
					if err := json.Unmarshal(raw, &row); err != nil {
						t.Fatalf("%s: %v: %.2000s", leg, err, raw)
					}
					t.Logf("%s %s %s: %.2000s", mode, kind, leg, raw)
					checkPaintRow(t, row, leg, nest, ordinary, protected)
				}
			})
		}
	}
}

func checkPaintRow(t *testing.T, row paintRow, leg, nest, ordinary, protected string) {
	t.Helper()
	want := protected
	if nest == "ordinary" {
		want = ordinary
	}
	if row.Mode == "" || row.Leg != leg || row.Setup.Layout.Nest != want || row.Setup.Layout.Child != want || row.Setup.Painted < paintThreshold {
		t.Fatalf("%s: setup did not paint the nested frame on %s: %+v", leg, want, row.Setup)
	}
	switch row.Mode {
	case "start":
		c := row.Cast
		switch {
		case c == nil:
			t.Fatalf("%s: no cast result", leg)
		case leg == "guarded" && (c.Marked != 0 || (c.Start.OK && c.Frames != 0)):
			t.Errorf("guarded: %d of %d frames painted the nested frame, start=%+v", c.Marked, c.Frames, c.Start)
		case leg != "guarded" && (!c.Start.OK || c.Marked == 0):
			t.Errorf("%s: cast did not show the nested frame: %+v", leg, c)
		}
	case "running":
		c := row.Cast
		switch {
		case c == nil || row.Setup.Away == nil || *row.Setup.Away >= paintThreshold:
			t.Fatalf("%s: away page missing or painted: %+v", leg, row.Setup)
		case row.RestoreType != "BackForwardCacheRestore" || row.RestoredPaint < paintThreshold:
			t.Fatalf("%s: restore not observed from the cache: %s paint=%d", leg, row.RestoreType, row.RestoredPaint)
		case !c.Start.OK || c.Before == 0 || c.BeforeMarked != 0:
			t.Errorf("%s: cast was not running before the restore: %+v", leg, c)
		case leg == "guarded" && c.After.Marked != 0:
			t.Errorf("guarded: %d of %d frames after the restore painted the nested frame", c.After.Marked, c.After.Frames)
		case leg != "guarded" && c.After.Marked == 0:
			t.Errorf("%s: cast after the restore did not show the nested frame: %+v", leg, c.After)
		}
	case "layers":
		l := row.Layers
		switch {
		case l == nil || l.Layers == 0:
			t.Fatalf("%s: no painted layers: %+v", leg, l)
		case leg == "guarded" && (l.Marked != 0 || l.LogShown || l.Made != 0 || l.Replayed != 0 || l.Profiled != 0 || l.Logged != 0):
			t.Errorf("guarded: layer snapshots reached the nested frame: %+v", l)
		case leg != "guarded" && (l.Marked == 0 || l.Profiled == 0 || l.Logged == 0):
			t.Errorf("%s: layer snapshots did not show the nested frame: %+v", leg, l)
		}
	default:
		t.Fatalf("unknown mode %q", row.Mode)
	}
}
