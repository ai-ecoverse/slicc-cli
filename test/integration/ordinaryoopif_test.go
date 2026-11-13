package integration

import (
	"context"
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

func ordinaryOOPIFFixture(t *testing.T) *httptest.Server {
	t.Helper()
	files := http.FileServer(http.Dir(filepath.Join("testdata", "link")))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, port, _ := strings.Cut(r.Host, ":")
		frame := "http://frame.other.test:" + port + "/inner?in=" + r.URL.Path[1:]
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/seven-framed":
			_, _ = fmt.Fprintf(w, `<!doctype html><body><script type="module" src="/page.js"></script><iframe src="%s"></iframe>`, frame)
		case "/vault-framed", "/framed":
			_, _ = fmt.Fprintf(w, `<!doctype html><body><iframe src="%s"></iframe>`, frame)
		case "/inner":
			_, _ = fmt.Fprint(w, `<!doctype html><body><p id="inner">inner</p>`)
		default:
			w.Header().Del("Content-Type")
			files.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func rawIframeTargets(t *testing.T, b *chromeProc, host string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var res struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			URL      string `json:"url"`
		} `json:"targetInfos"`
	}
	var ids []string
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if err := b.client.Call(ctx, "", "Target.getTargets", map[string]any{}, &res); err != nil {
			t.Fatal(err)
		}
		ids = ids[:0]
		for _, info := range res.TargetInfos {
			if info.Type == "iframe" && strings.HasPrefix(info.URL, host) {
				ids = append(ids, info.TargetID)
			}
		}
		if len(ids) >= 2 {
			return ids
		}
	}
	t.Fatalf("protected pages' cross-site iframes never appeared: %+v", res.TargetInfos)
	return nil
}

func guardLines(a *attachProc) string {
	var out []string
	for _, line := range strings.Split(a.stderr.String(), "\n") {
		if strings.Contains(line, "cdp:") {
			out = append(out, line)
		}
	}
	if len(out) > 200 {
		out = out[len(out)-200:]
	}
	return strings.Join(out, "\n")
}

func TestCDPOrdinaryCrossSiteIframeReachesAStockClient(t *testing.T) {
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("CI Chrome required")
		}
		t.Skip(harnessChromeNotice)
	}
	fixture := ordinaryOOPIFFixture(t)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	seven := "http://seven.protected.test:" + port
	vault := "http://vault.secret.test:" + port
	ordinary := "http://page.ordinary.test:" + port
	frameHost := "http://frame.other.test:" + port
	b := launchBrowserOn(t, 0, "--site-per-process", "--unsafely-treat-insecure-origin-as-secure="+seven, "--host-resolver-rules=MAP *.test 127.0.0.1")
	host := b.openPage(t, seven+"/seven-framed")
	b.openTarget(t, vault+"/vault-framed")
	protectedFrames := rawIframeTargets(t, b, frameHost)
	env := &linkEnv{t: t, b: b, origin: seven, p: host}
	env.a = startAttach(t, "--cdp", b.endpoint, "--origin", seven, "--origin", vault, "--name", "ordinary-oopif-node")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatalf("browsers %+v", hello.Browsers)
	}
	browser := hello.Browsers[0].ID
	for _, shape := range []struct {
		name       string
		pageEnable bool
	}{{"bios", false}, {"plain", false}, {"plain", true}} {
		label := fmt.Sprintf("%s-page-enable-%t", shape.name, shape.pageEnable)
		t.Run(label, func(t *testing.T) {
			opts := map[string]any{"browser": browser, "shape": shape.name, "pageEnable": shape.pageEnable, "url": ordinary + "/framed", "child": frameHost + "/inner", "waitMs": 12000}
			var raw json.RawMessage
			env.p.evalWithin("linkTest.ordinaryOOPIF("+jsArgs(opts)+")", &raw, 90*time.Second)
			var row struct {
				Child *struct {
					ID  string `json:"id"`
					URL string `json:"url"`
				} `json:"child"`
				Read *struct {
					OK    bool   `json:"ok"`
					Value string `json:"value"`
				} `json:"read"`
				Lookups map[string]struct {
					OK    bool   `json:"ok"`
					Error string `json:"error"`
				} `json:"lookups"`
				Explicit map[string]struct {
					OK    bool   `json:"ok"`
					Error string `json:"error"`
				} `json:"explicit"`
			}
			if err := json.Unmarshal(raw, &row); err != nil {
				t.Fatal(err)
			}
			if row.Child == nil || row.Read == nil || !row.Read.OK || row.Read.Value != "inner" {
				t.Fatalf("an ordinary page's cross-site iframe never reached the client: %s\nguard:\n%s", raw, guardLines(env.a))
			}
			for id, answer := range row.Lookups {
				if !answer.OK {
					t.Errorf("Target.getTargetInfo on the announced ordinary iframe %s failed: %s\nguard:\n%s", id, raw, guardLines(env.a))
				}
			}
			for id, answer := range row.Explicit {
				if !answer.OK {
					t.Fatalf("attach by id to the ordinary iframe %s was refused: %s\nguard:\n%s", id, raw, guardLines(env.a))
				}
			}
		})
	}
	t.Run("protected", func(t *testing.T) {
		var raw json.RawMessage
		env.p.evalWithin("linkTest.protectedOOPIF("+jsArgs(map[string]any{"browser": browser, "frames": protectedFrames})+")", &raw, 60*time.Second)
		var row struct {
			Rows map[string]struct {
				Listed bool `json:"listed"`
				Info   struct {
					OK bool `json:"ok"`
				} `json:"info"`
				Attach struct {
					OK    bool   `json:"ok"`
					Error string `json:"error"`
				} `json:"attach"`
			} `json:"rows"`
			Seen []string `json:"seen"`
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatal(err)
		}
		if len(row.Rows) != len(protectedFrames) || len(row.Seen) != 0 {
			t.Fatalf("a cross-site iframe in seven or the vault reached the client: %s", raw)
		}
		for id, r := range row.Rows {
			if r.Listed || r.Info.OK || r.Attach.OK || !strings.Contains(r.Attach.Error, "slicc: refused") {
				t.Fatalf("cross-site iframe %s in seven or the vault was reachable: %s", id, raw)
			}
		}
	})
}
