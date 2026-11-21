package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func diagShapeFixture(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		marker := r.URL.Query().Get("marker")
		if marker == "" {
			marker = "DIAGSHAPE_MARKER"
		}
		_, _ = fmt.Fprintf(w, "<!doctype html><title>diagshape %s</title><body data-marker=\"%s\"><p>DIAGSHAPE_DOC %s</p>", marker, marker, marker)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDiagShapeWrapperDocumentsUnderLaggingGate(t *testing.T) {
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("CI Chrome required")
		}
		t.Skip(harnessChromeNotice)
	}
	fixture := diagShapeFixture(t)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	vault := "http://vault.sliccy.ai:" + port
	ordinary := "http://ordinary.guard.test:" + port
	srv := pageServer(t)
	b := launchBrowserOn(t, 0,
		"--host-resolver-rules=MAP vault.sliccy.ai 127.0.0.1, MAP *.guard.test 127.0.0.1, MAP *.sliccy.ai 127.0.0.1",
		"--unsafely-treat-insecure-origin-as-secure="+vault+","+ordinary,
	)
	gate := newGuardGate(t, b.endpoint, srv.URL)
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--origin", vault, "--name", "diagshape-lag-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatal("diagshape browser missing")
	}
	ws := strings.Replace(gate.srv.URL, "http", "ws", 1) + "/browser"
	marker := "DIAGSHAPE_MARKER"
	forms := []string{"blob", "filesystem", "sandbox-blob"}
	for _, name := range forms {
		name := name
		t.Run(name, func(t *testing.T) {
			p := *env.p
			p.t = t
			stderrBefore := env.a.stderr.String()
			opts := map[string]any{
				"form":             name,
				"marker":           marker,
				"browser":          hello.Browsers[0].ID,
				"ws":               ws,
				"gate":             gate.srv.URL + "/gate",
				"vault":            vault,
				"ordinary":         ordinary,
				"vaultDoc":         vault + "/doc?n=diagshape-blob&marker=" + marker,
				"ordinaryDoc":      ordinary + "/doc?n=diagshape-ordinary&marker=" + marker,
				"lag":              1,
				"lagHoldMs":        500,
				"settleMs":         10000,
				"protectedOrigins": []string{vault},
				"ordinaryOrigin":   ordinary,
			}
			var raw json.RawMessage
			p.evalWithin("linkTest.diagShape("+jsArgs(opts)+")", &raw, 2*time.Minute)
			var result struct {
				Form   string   `json:"form"`
				Lines  []string `json:"lines"`
				Failed string   `json:"failed"`
				Reason string   `json:"reason"`
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatalf("diagshape decode: %v raw=%s", err, raw)
			}
			for _, line := range result.Lines {
				t.Logf("DIAGSHAPE form=%s lag=1 %s", name, line)
			}
			stderrAfter := env.a.stderr.String()
			added := stderrAfter
			if strings.HasPrefix(stderrAfter, stderrBefore) {
				added = stderrAfter[len(stderrBefore):]
			}
			for _, line := range strings.Split(added, "\n") {
				if strings.Contains(line, "DIAGSHAPE") {
					t.Logf("DIAGSHAPE form=%s lag=1 guard-log %s", name, strings.TrimSpace(line))
				}
			}
			t.Logf("DIAGSHAPE form=%s lag=1 raw=%s", name, raw)
		})
	}
}
