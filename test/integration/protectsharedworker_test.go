package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func sharedWorkerServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/shared.js" {
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write([]byte(`onconnect = (e) => e.ports[0].postMessage("up");`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>shared</title><script>new SharedWorker("/shared.js", "probe").port.start()</script>`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCDPLetsGoOfASharedWorkerThatEndedSoChromeSurvivesTheNextAutoAttach(t *testing.T) {
	for _, tc := range []struct {
		name     string
		explicit bool
	}{{"auto-attached by the page", false}, {"also attached by the page", true}} {
		t.Run(tc.name, func(t *testing.T) { sharedWorkerEnded(t, tc.explicit) })
	}
}

func sharedWorkerEnded(t *testing.T, explicit bool) {
	b := launchBrowserOn(t, 0)
	srv := pageServer(t)
	work := sharedWorkerServer(t)
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", b.endpoint, "--origin", srv.URL, "--name", "it-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var r struct {
		Worker   string     `json:"worker"`
		Ended    bool       `json:"ended"`
		Released bool       `json:"released"`
		Again    cdpOutcome `json:"again"`
	}
	(&protectEnv{linkEnv: env}).evalJSON(t, "linkTest.protect.sharedWorkerEnded("+jsArgs("chrome", work.URL+"/shared.html", explicit)+")", &r)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.client.Call(ctx, "", "Browser.getVersion", nil, nil); err != nil {
		t.Fatalf("Chrome died after a browser-level auto-attach that followed a shared worker's end (%v): %s", err, crashLines(b))
	}
	if r.Worker == "" || !r.Ended {
		t.Fatalf("the page never saw its auto-attached shared worker end, so the test can't tell: %+v", r)
	}
	if !r.Released {
		t.Errorf("the page's own session on the shared worker that ended stayed attached: %+v", r)
	}
	if !r.Again.OK {
		t.Errorf("auto-attach again after the shared worker ended: %+v", r.Again)
	}
}

func crashLines(b *chromeProc) string {
	log, _ := os.ReadFile(b.stderr)
	var out []string
	for _, l := range strings.Split(string(log), "\n") {
		if strings.Contains(l, "Check failed") || strings.Contains(l, "Received signal") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "; ")
}
