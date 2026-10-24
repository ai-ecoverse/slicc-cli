package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCDPProtectedPageFramesKeepOrdinaryTabsUsable(t *testing.T) {
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("CI Chrome required")
		}
		t.Skip(harnessChromeNotice)
	}
	for _, mode := range []string{"direct", "nested"} {
		t.Run(mode, func(t *testing.T) {
			fixture := oopifFixture(t)
			port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
			seven := "http://seven.protected.test:" + port
			vault := "http://vault.secret.test:" + port
			ordinary := "http://page.ordinary.test:" + port
			srv := pageServer(t)
			b := launchBrowserOn(t, 0, "--site-per-process", "--host-resolver-rules=MAP *.test 127.0.0.1")
			protected := b.openPage(t, seven+"/protected-top?mode="+mode)
			var loaded bool
			protected.evalWithin(`new Promise((resolve) => { const check = () => globalThis.frameLoaded ? resolve(true) : setTimeout(check, 20); check(); })`, &loaded, 15*time.Second)
			if !loaded {
				t.Fatal("protected fixture frames were not loaded before attach")
			}
			plain := b.openPage(t, ordinary+"/daily")
			gate := newGuardGate(t, b.endpoint, srv.URL)
			env := &linkEnv{t: t, b: b, origin: srv.URL}
			env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--origin", seven, "--origin", vault, "--name", "daily-frames-node")
			env.p = b.openPage(t, srv.URL+"/index.html")
			env.waitUp("")
			var hello struct {
				Browsers []helloBrowser `json:"browsers"`
			}
			env.p.eval("linkTest.ready()", &hello)
			if len(hello.Browsers) != 1 {
				t.Fatal("daily frames browser missing")
			}
			opts := map[string]any{"mode": mode, "browser": hello.Browsers[0].ID, "protectedId": protected.target, "ordinaryId": plain.target, "vault": vault + "/child", "ws": strings.Replace(gate.srv.URL, "http", "ws", 1) + "/browser"}
			var raw json.RawMessage
			env.p.evalWithin("linkTest.dailyFrames("+jsArgs(opts)+")", &raw, 2*time.Minute)
			path := filepath.Join("..", "..", "artifacts", "daily-frames")
			if err := os.MkdirAll(path, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, mode+".json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: %.5000s", mode, raw)
			var row struct {
				Info struct {
					ParentFrameID string `json:"parentFrameId"`
				} `json:"info"`
				Setup      oopifAnswer `json:"setup"`
				Screenshot oopifAnswer `json:"screenshot"`
				Document   struct {
					OK    bool   `json:"ok"`
					Shown bool   `json:"shown"`
					Error string `json:"error"`
				} `json:"document"`
				Input struct {
					OK    bool   `json:"ok"`
					Keys  int    `json:"keys"`
					Error string `json:"error"`
				} `json:"input"`
				Cast struct {
					OK     bool `json:"ok"`
					Marked int  `json:"marked"`
				} `json:"cast"`
				ProtectedAttach oopifAnswer `json:"protectedAttach"`
			}
			if err := json.Unmarshal(raw, &row); err != nil {
				t.Fatal(err)
			}
			if row.Info.ParentFrameID == "" || (row.Info.ParentFrameID == protected.target) != (mode == "direct") || !row.Setup.OK || row.Setup.Marked < 1000 {
				t.Fatalf("preloaded frame relationship/control not established: %s", raw)
			}
			if !row.Screenshot.OK || row.Screenshot.Marked < 1000 || !row.Cast.OK || row.Cast.Marked == 0 || !row.Document.OK || !row.Document.Shown || !row.Input.OK || row.Input.Keys != 1 {
				t.Errorf("ordinary tab lost rendering, DOM or Input: %s", raw)
			}
			if row.ProtectedAttach.OK || !strings.Contains(row.ProtectedAttach.Error, "slicc: refused: target is on seven's origin") {
				t.Errorf("protected page attach was not refused: %+v", row.ProtectedAttach)
			}
		})
	}
}
