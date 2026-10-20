package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

const fragmentPage = `<!doctype html><title>seven</title><script>
document.title = location.href;
addEventListener('hashchange', () => { document.title = location.href; });
</script>`

type fragmentTarget struct {
	TargetID string `json:"targetId"`
	URL      string `json:"url"`
	Title    string `json:"title"`
}

func TestCDPKeepsSevensURLFragmentOutOfTargets(t *testing.T) {
	b := launchBrowserOn(t, 0, "--host-resolver-rules=MAP *.sliccy.ai 127.0.0.1")
	srv := pageServer(t)
	seven := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(fragmentPage))
	}))
	t.Cleanup(seven.Close)
	origin := "http://seven.sliccy.ai:" + seven.URL[strings.LastIndexByte(seven.URL, ':')+1:]
	e := &protectEnv{linkEnv: &linkEnv{t: t, b: b, origin: srv.URL}, seven: origin}
	e.a = startAttach(t, "--cdp", b.endpoint, "--origin", srv.URL, "--origin", origin, "--name", "it-node")
	e.p = b.openPage(t, srv.URL+"/index.html")
	e.waitUp("")

	tailscale := e.openDirect(t, origin+"/#tailscale=fake-value-0011")
	proxy := e.openDirect(t, origin+"/#proxy=http://x&key=fake-value-0012")
	var ok map[string]any
	e.evalJSON(t, "linkTest.protect.watchFragments('chrome')", &ok)
	callback := e.openDirect(t, origin+"/auth/callback#access_token=fake-value-0013")
	e.setHashDirect(t, tailscale, "tailscale=fake-value-0014")
	ids := []string{tailscale, proxy, callback}
	direct := pageTargets(t, b)
	for id, want := range map[string]string{tailscale: "fake-value-0014", proxy: "fake-value-0012", callback: "fake-value-0013"} {
		if !strings.Contains(direct[id], want) {
			t.Fatalf("Chrome itself lists %s at %q, so the probe went untested", id, direct[id])
		}
	}
	time.Sleep(time.Second)

	var r struct {
		Received string           `json:"received"`
		Listed   []fragmentTarget `json:"listed"`
		Changed  []fragmentTarget `json:"changed"`
	}
	e.evalJSON(t, "linkTest.protect.watchedFragments("+jsArgs(ids)+")", &r)
	for _, v := range []string{"fake-value-0011", "fake-value-0012", "fake-value-0013", "fake-value-0014"} {
		if strings.Contains(r.Received, v) {
			t.Errorf("%s reached the relay client: %s", v, r.Received)
		}
	}
	listed := map[string]fragmentTarget{}
	for _, l := range r.Listed {
		listed[l.TargetID] = l
	}
	for _, id := range ids {
		if l, ok := listed[id]; !ok || l.URL != origin || l.Title != origin {
			t.Errorf("Target.getTargets lists %s as %+v, want its URL and title cut to %s", id, l, origin)
		}
	}
	changed := false
	for _, c := range r.Changed {
		changed = changed || c.TargetID == tailscale
	}
	if !changed {
		t.Errorf("no Target.targetInfoChanged for the tab whose fragment changed, so that step went untested: %+v", r.Changed)
	}
}

func (e *protectEnv) openDirect(t *testing.T, u string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var res struct {
		TargetID string `json:"targetId"`
	}
	if err := e.b.client.Call(ctx, "", "Target.createTarget", map[string]any{"url": u}, &res); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = e.b.client.Call(ctx, "", "Target.closeTarget", map[string]any{"targetId": res.TargetID}, nil)
	})
	e.evalDirect(t, res.TargetID, "document.readyState === 'complete' && document.title.includes('#')")
	return res.TargetID
}

func (e *protectEnv) setHashDirect(t *testing.T, targetID, hash string) {
	t.Helper()
	e.evalDirect(t, targetID, "(location.hash = "+jsArgs(hash)+", document.title.endsWith("+jsArgs(hash)+"))")
}

func (e *protectEnv) evalDirect(t *testing.T, targetID, expr string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := e.b.client.Attach(ctx, targetID)
	if err != nil {
		t.Fatalf("attach %s directly: %v", targetID, err)
	}
	defer func() { _ = s.Detach(context.Background()) }()
	for {
		var done bool
		if err := s.Evaluate(ctx, expr, cdp.EvaluateOptions{ReturnByValue: true}, &done); err == nil && done {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s on %s never held", expr, targetID)
		case <-time.After(100 * time.Millisecond):
		}
	}
}
