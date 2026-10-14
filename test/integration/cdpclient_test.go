package integration

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

func TestAttachTimeCDPReadsFollowNoRedirects(t *testing.T) {
	elsewhere := newRecordingCDP(t)
	redirecting := newRecordingCDP(t)
	redirecting.redirectTo(elsewhere.URL + "/json/version")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if ws, err := cdp.Discover(ctx, redirecting.URL); err == nil {
		t.Fatalf("Discover followed a redirect to %s", ws)
	}
	if c, err := cdp.Dial(ctx, redirecting.URL); err == nil {
		_ = c.Close()
		t.Fatal("Dial followed a redirect")
	}
	ws := "ws" + strings.TrimPrefix(redirecting.URL, "http") + "/devtools/browser/x"
	if c, err := cdp.Dial(ctx, ws); err == nil {
		_ = c.Close()
		t.Fatal("the WebSocket handshake followed a redirect")
	}
	if got := elsewhere.got(); len(got) != 0 || elsewhere.conns.Load() != 0 {
		t.Fatalf("a redirect was followed: %q", got)
	}
}

func TestAttachTimeCDPReadsUseNoProxy(t *testing.T) {
	proxy := newRecordingCDP(t)
	env := isolatedEnv(t, "HTTP_PROXY="+proxy.URL, "http_proxy="+proxy.URL, "HTTPS_PROXY="+proxy.URL, "NO_PROXY=", "no_proxy=")
	for _, target := range []string{"http://192.0.2.1:9400", "ws://192.0.2.1:9400/devtools/browser/x"} {
		r := runBin(t, env, binary, "attach", "--cdp", target)
		if r.code != 1 || !strings.Contains(r.stderr, "192.0.2.1:9400") {
			t.Fatalf("attach --cdp %s: %+v", target, r)
		}
	}
	if got := proxy.got(); len(got) != 0 || proxy.conns.Load() != 0 {
		t.Fatalf("attach went through the proxy from the environment: %q", got)
	}
}

func TestTheCDPRelayFollowsNoRedirects(t *testing.T) {
	b := launchBrowser(t)
	srv := pageServer(t)
	extra := newRecordingCDP(t)
	elsewhere := newRecordingCDP(t)
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", b.endpoint, "--cdp-extra", extra.URL, "--origin", srv.URL, "--name", "it-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var h struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &h)
	if len(h.Browsers) != 2 || h.Browsers[1].ID != "chrome-2" {
		t.Fatalf("browsers %+v", h.Browsers)
	}
	before := len(extra.got())
	extra.redirectTo(elsewhere.URL + "/json/version")
	var r struct {
		Reset string `json:"reset"`
		Error string `json:"error"`
	}
	env.p.eval("linkTest.cdp.version('chrome-2')", &r)
	if !strings.HasPrefix(r.Reset, "ECONNREFUSED") {
		t.Fatalf("a cdp stream to a redirecting browser: %+v", r)
	}
	if got := elsewhere.got(); len(got) != 0 || elsewhere.conns.Load() != 0 {
		t.Fatalf("the relay followed a redirect: %q", got)
	}
	for _, req := range extra.got()[before:] {
		if req != "GET /json/version" {
			t.Fatalf("the relay sent %q", req)
		}
	}
}

func TestSetupNeverNamesOriginsThatArentSeven(t *testing.T) {
	b := launchBrowser(t)
	srv := pageServer(t)
	other := plainServer(t)
	otherHost := strings.TrimPrefix(other.URL, "http://")
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", b.endpoint, "--origin", srv.URL, "--name", "it-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var ok bool
	env.p.eval(`(() => { const f = document.createElement('iframe'); f.src = `+jsArgs(other.URL+"/?n=frame")+`; document.body.appendChild(f); return true; })()`, &ok)
	env.a.waitStderr(t, "ignored a context on another origin", 30*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := env.p.session.Call(ctx, "Page.navigate", map[string]any{"url": other.URL + "/?n=away"}, nil); err != nil {
		t.Fatal(err)
	}
	env.a.waitStderr(t, "the seven tab left seven", 30*time.Second)
	if out := env.a.stderr.String(); strings.Contains(out, otherHost) {
		t.Fatalf("stderr names an origin that isn't seven (%s):\n%s", otherHost, out)
	}
	if out := env.a.stderr.String(); !strings.Contains(out, strings.TrimPrefix(srv.URL, "http://")) {
		t.Fatalf("stderr should still name the seven origin:\n%s", out)
	}
}

type foreignEvent struct {
	Event     string `json:"event"`
	Browser   string `json:"browser"`
	Name      string `json:"name"`
	Profile   string `json:"profile"`
	CDP       string `json:"cdp"`
	OpenedTab *bool  `json:"openedTab"`
}

func TestDefaultNoticesABrowserSliccDidntLaunch(t *testing.T) {
	srv := pageServer(t)
	pageURL := srv.URL + "/index.html"
	for _, withTab := range []bool{false, true} {
		for _, mode := range []string{"--json", "--plain"} {
			t.Run(mode+" tab="+strconv.FormatBool(withTab), func(t *testing.T) {
				b := launchBrowser(t)
				if withTab {
					b.openPage(t, pageURL)
				}
				h := newFakeHome(t)
				writeActivePort(t, h.chromeDataDir(), activePortOf(t, b))
				f := startFlow(t, append(h.env, "NO_COLOR=1"), mode, "--page", pageURL)
				what := "opened a new seven tab"
				if withTab {
					what = "found a seven tab"
				}
				line := "slicc: using Google Chrome (profile default) at " + b.endpoint + ", which slicc didn't launch; " + what + ". Use --browser or --cdp to choose another."
				f.waitStderr(t, line)
				if mode == "--json" {
					f.next(t, "discovered")
					f.next(t, "attached")
					e := f.next(t, "foreign-browser")
					var fe foreignEvent
					if err := json.Unmarshal([]byte(e.raw), &fe); err != nil {
						t.Fatal(err)
					}
					id := "cdp:127.0.0.1:" + strconv.Itoa(b.port)
					if fe.Browser != id || fe.Name != "Google Chrome" || fe.Profile != "default" || fe.CDP != b.endpoint || fe.OpenedTab == nil || *fe.OpenedTab == withTab {
						t.Fatalf("foreign-browser %s", e.raw)
					}
				}
				if strings.Count(f.stderr.String(), "which slicc didn't launch") != 1 {
					t.Fatalf("stderr:\n%s", f.stderr.String())
				}
			})
		}
	}
}

func TestDefaultNoNoticeForItsOwnOrANamedBrowser(t *testing.T) {
	srv := pageServer(t)
	pageURL := srv.URL + "/index.html"
	cases := []struct {
		name  string
		setup func(t *testing.T, h fakeHome, b *chromeProc) []string
	}{
		{"seven profile", func(t *testing.T, h fakeHome, b *chromeProc) []string {
			writeActivePort(t, filepath.Join(h.profilesDir(), "it-own"), activePortOf(t, b))
			return nil
		}},
		{"--cdp", func(t *testing.T, h fakeHome, b *chromeProc) []string {
			writeActivePort(t, h.chromeDataDir(), activePortOf(t, b))
			return []string{"--cdp", "localhost:" + strconv.Itoa(b.port)}
		}},
		{"--browser", func(t *testing.T, h fakeHome, b *chromeProc) []string {
			writeActivePort(t, h.chromeDataDir(), activePortOf(t, b))
			return []string{"--browser", "cdp:127.0.0.1:" + strconv.Itoa(b.port)}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := launchBrowser(t)
			h := newFakeHome(t)
			args := append([]string{"--json", "--page", pageURL}, c.setup(t, h, b)...)
			f := startFlow(t, h.env, args...)
			f.next(t, "discovered")
			if a := f.next(t, "attached"); a.CDP != b.endpoint {
				t.Fatalf("attached %s", a.raw)
			}
			f.next(t, "linked")
			if strings.Contains(f.stderr.String(), "didn't launch") {
				t.Fatalf("stderr:\n%s", f.stderr.String())
			}
		})
	}
}

func TestAnExpiredCallLeavesTheSharedCDPConnectionUp(t *testing.T) {
	b := launchBrowser(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := cdp.Dial(ctx, b.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	var created struct {
		TargetID string `json:"targetId"`
	}
	if err := c.Call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &created); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.closeTarget(created.TargetID) })
	s, err := c.Attach(ctx, created.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	slow := make(chan error, 1)
	var got int
	go func() {
		slow <- s.Evaluate(ctx, "new Promise((r) => setTimeout(() => r(7), 1000))", cdp.EvaluateOptions{AwaitPromise: true, ReturnByValue: true}, &got)
	}()
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if err := c.Call(cancelled, "", "Browser.getVersion", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("a call with a cancelled context: %v", err)
	}
	expired, stopExpired := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer stopExpired()
	if err := s.Call(expired, "Runtime.evaluate", map[string]any{"expression": "1"}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a call with an expired deadline: %v", err)
	}
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			racing, stopRacing := context.WithTimeout(ctx, time.Duration(i%8)*50*time.Microsecond)
			defer stopRacing()
			_ = c.Call(racing, "", "Browser.getVersion", nil, nil)
		}()
	}
	wg.Wait()
	if err := <-slow; err != nil || got != 7 {
		t.Fatalf("the concurrent call: %v, %d", err, got)
	}
	select {
	case <-c.Done():
		t.Fatalf("the shared connection closed: %v", c.Err())
	default:
	}
	var version struct {
		Product string `json:"product"`
	}
	if err := c.Call(ctx, "", "Browser.getVersion", nil, &version); err != nil || version.Product == "" {
		t.Fatalf("a call after the expired ones: %v, %+v", err, version)
	}
}
