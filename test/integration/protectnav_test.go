package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

const swScript = `self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()));
self.addEventListener('fetch', (e) => {
  if (new URL(e.request.url).pathname.startsWith('/sw/page')) e.respondWith(new Response('<!doctype html><title>served by the worker</title>sw', { headers: { 'content-type': 'text/html' } }));
});`

const swRegister = `<!doctype html><script>
navigator.serviceWorker.register('/sw.js', { scope: '/sw/' }).then(() => navigator.serviceWorker.ready).then(() => { document.title = 'ready'; });
</script>`

type navLog struct {
	mu      sync.Mutex
	hits    []string
	beacons map[string][]string
}

func (l *navLog) hit(host, uri string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits = append(l.hits, host+" "+uri)
}

func (l *navLog) beacon(tag, origin string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.beacons[tag] = append(l.beacons[tag], origin)
}

func (l *navLog) seen(hosts []string, nonce string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, h := range l.hits {
		for _, host := range hosts {
			if strings.HasPrefix(h, host+" ") && strings.Contains(h, nonce) {
				out = append(out, h)
			}
		}
	}
	return out
}

func (l *navLog) origins(tag string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.beacons[tag]...)
}

func navServer(t *testing.T, log *navLog) *httptest.Server {
	t.Helper()
	html := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(body))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/doc", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		html(w, "<!doctype html><title>nav "+r.URL.Query().Get("n")+"</title><p>doc")
	})
	mux.HandleFunc("/frame", func(w http.ResponseWriter, r *http.Request) {
		html(w, `<!doctype html><title>frames</title><iframe id="f" src="about:blank"></iframe><iframe id="g" src="about:blank"></iframe>`)
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Query().Get("to"), http.StatusFound)
	})
	vectorRoutes(mux, log)
	mux.HandleFunc("/beacon", func(w http.ResponseWriter, r *http.Request) {
		log.beacon(r.URL.Query().Get("tag"), r.URL.Query().Get("origin"))
		w.WriteHeader(http.StatusNoContent)
	})
	stop := make(chan struct{})
	mux.HandleFunc("/hang", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-stop:
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write([]byte(swScript))
	})
	mux.HandleFunc("/sw/register.html", func(w http.ResponseWriter, r *http.Request) { html(w, swRegister) })
	mux.HandleFunc("/sw/", func(w http.ResponseWriter, r *http.Request) {
		html(w, "<!doctype html><title>from the network</title>")
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.hit(r.Host, r.URL.RequestURI())
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(stop) })
	return srv
}

type navEnv struct {
	*linkEnv
	log     *navLog
	port    string
	allowed string
	cross   string
	seven   string
	vault   string
	handler http.Handler
	n       int
}

func setupNav(t *testing.T) *navEnv {
	t.Helper()
	b := launchBrowserOn(t, 0, "--host-resolver-rules=MAP *.sliccy.ai 127.0.0.1,MAP *.test 127.0.0.1")
	srv := pageServer(t)
	log := &navLog{beacons: map[string][]string{}}
	nav := navServer(t, log)
	port := nav.URL[strings.LastIndexByte(nav.URL, ':')+1:]
	env := &navEnv{
		linkEnv: &linkEnv{t: t, b: b, origin: srv.URL},
		log:     log,
		port:    port,
		allowed: "http://localhost:" + port,
		cross:   "http://other.test:" + port,
		seven:   nav.URL,
		vault:   "http://vault.sliccy.ai:" + port,
		handler: nav.Config.Handler,
	}
	env.a = startAttach(t, "--cdp", b.endpoint, "--origin", srv.URL, "--origin", nav.URL, "--name", "it-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	return env
}

func TestCDPProtectsAcrossNavigation(t *testing.T) {
	env := setupNav(t)
	t.Run("an untouched tab still loads both protected origins", env.testUntouched)
	t.Run("an init script, then a script navigation onto seven", env.testScriptNavigation)
	t.Run("an init script, then a script navigation onto the vault", env.testVaultNavigation)
	t.Run("a server redirect into a protected origin", env.testRedirect)
	t.Run("a popup from an allowed tab", env.testPopupNavigation)
	t.Run("iframes that attach without a URL", env.testFrames)
	t.Run("a popup that never gets a URL", env.testNoURL)
	t.Run("a service worker can't serve a touched tab", env.testServiceWorker)
	t.Run("the page's own Fetch and Network", env.testOwnFetch)
	t.Run("the link still works", env.testNavStillLinked)
}

func (e *navEnv) nonce(kind string) string {
	e.n++
	return fmt.Sprintf("%s-%d-%d", kind, e.n, time.Now().UnixNano())
}

func (e *navEnv) protectedHits(nonce string) []string {
	return e.log.seen([]string{"127.0.0.1:" + e.port, "vault.sliccy.ai:" + e.port}, nonce)
}

func (e *navEnv) directHref(t *testing.T, targetID string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := e.b.client.Attach(ctx, targetID)
	if err != nil {
		t.Fatalf("attach %s directly: %v", targetID, err)
	}
	defer func() { _ = s.Detach(context.Background()) }()
	var href string
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		err = s.Evaluate(ctx, "document.readyState === 'complete' ? location.href : ''", cdp.EvaluateOptions{ReturnByValue: true}, &href)
		if (err == nil && href != "") || time.Now().After(deadline) {
			return href
		}
	}
}

func (e *navEnv) closeTarget(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = e.b.client.Call(ctx, "", "Target.closeTarget", map[string]any{"targetId": id}, nil)
}

func (e *navEnv) directLoad(t *testing.T, u string) (string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var res struct {
		TargetID string `json:"targetId"`
	}
	if err := e.b.client.Call(ctx, "", "Target.createTarget", map[string]any{"url": u}, &res); err != nil {
		t.Fatal(err)
	}
	defer e.closeTarget(res.TargetID)
	s, err := e.b.client.Attach(ctx, res.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Detach(context.Background()) }()
	var out struct {
		Href  string `json:"href"`
		Title string `json:"title"`
	}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		err := s.Evaluate(ctx, "document.readyState === 'complete' && document.title ? { href: location.href, title: document.title } : null", cdp.EvaluateOptions{ReturnByValue: true}, &out)
		if err == nil && out.Title != "" {
			break
		}
	}
	return out.Href, out.Title
}

func (e *navEnv) assertBlocked(t *testing.T, targetID, nonce, tag string, wantAllowedBeacon bool) {
	t.Helper()
	if hits := e.protectedHits(nonce); len(hits) > 0 {
		t.Errorf("the protected document was requested from a touched target: %v", hits)
	}
	if href := e.directHref(t, targetID); !strings.HasPrefix(href, "chrome-error://") {
		t.Errorf("the touched target is at %q, want the error page", href)
	}
	origins := e.log.origins(tag)
	allowedBeacon := false
	for _, o := range origins {
		if o == e.seven || o == e.vault {
			t.Errorf("the injected script ran on a protected origin: %v", origins)
		}
		allowedBeacon = allowedBeacon || o == e.allowed || o == e.cross
	}
	if wantAllowedBeacon && !allowedBeacon {
		t.Errorf("the injected script never ran on the allowed page, so the probe went untested: %v", origins)
	}
}

func (e *navEnv) testUntouched(t *testing.T) {
	for _, base := range []string{e.seven, e.vault} {
		u := base + "/doc?n=" + e.nonce("untouched")
		href, title := e.directLoad(t, u)
		if href != u || !strings.HasPrefix(title, "nav untouched") {
			t.Fatalf("a tab nobody attached over cdp didn't load %s: %q %q", u, href, title)
		}
	}
}

type navInitResult struct {
	TargetID string `json:"targetId"`
	Before   string `json:"before"`
	Detached bool   `json:"detached"`
}

func (e *navEnv) navInit(t *testing.T, tag, away string) navInitResult {
	t.Helper()
	var r navInitResult
	start := e.allowed + "/doc?n=start"
	next := e.allowed + "/doc?n=next-" + tag
	(&protectEnv{linkEnv: e.linkEnv}).evalJSON(t, "linkTest.protect.navInit("+jsArgs("chrome", e.allowed, tag, start, next, away)+")", &r)
	if r.Before != next {
		t.Fatalf("the allowed navigation didn't land: %+v", r)
	}
	return r
}

func (e *navEnv) testScriptNavigation(t *testing.T) {
	nonce := e.nonce("script")
	r := e.navInit(t, nonce, e.seven+"/doc?n="+nonce)
	defer e.closeTarget(r.TargetID)
	if !r.Detached {
		t.Errorf("the page's session stayed attached: %+v", r)
	}
	e.assertBlocked(t, r.TargetID, nonce, nonce, true)
}

func (e *navEnv) testVaultNavigation(t *testing.T) {
	nonce := e.nonce("vault")
	r := e.navInit(t, nonce, e.vault+"/doc?n="+nonce)
	defer e.closeTarget(r.TargetID)
	e.assertBlocked(t, r.TargetID, nonce, nonce, true)
}

func (e *navEnv) testRedirect(t *testing.T) {
	for _, base := range []string{e.seven, e.vault} {
		nonce := e.nonce("redirect")
		away := e.allowed + "/redirect?to=" + url.QueryEscape(base+"/doc?n="+nonce)
		r := e.navInit(t, nonce, away)
		e.assertBlocked(t, r.TargetID, nonce, nonce, true)
		e.closeTarget(r.TargetID)
	}
}

func (e *navEnv) testPopupNavigation(t *testing.T) {
	nonce := e.nonce("popup")
	direct := e.vault + "/doc?n=" + e.nonce("direct")
	var r struct {
		Popup    string          `json:"popup"`
		URL      string          `json:"url"`
		Waiting  bool            `json:"waiting"`
		Before   string          `json:"before"`
		Detached bool            `json:"detached"`
		Direct   []string        `json:"direct"`
		Popups   []protectTarget `json:"popups"`
	}
	popup := e.allowed + "/doc?n=popup"
	(&protectEnv{linkEnv: e.linkEnv}).evalJSON(t, "linkTest.protect.navPopup("+jsArgs("chrome", e.allowed, nonce, e.allowed+"/doc?n=opener", popup, e.seven+"/doc?n="+nonce, direct)+")", &r)
	if r.URL != popup || r.Waiting || !strings.HasPrefix(r.Before, popup) {
		t.Fatalf("the allowed popup: %+v", r)
	}
	e.assertBlocked(t, r.Popup, nonce, nonce, true)
	if len(r.Direct) != 0 {
		t.Errorf("the page got a session to a popup on the vault: %v", r.Direct)
	}
	found := false
	for _, p := range r.Popups {
		defer e.closeTarget(p.TargetID)
		if p.TargetID == r.Popup {
			continue
		}
		found = true
		if p.URL != e.vault {
			t.Errorf("the vault popup is listed as %q, want %q", p.URL, e.vault)
		}
		if href := e.directHref(t, p.TargetID); href != direct {
			t.Errorf("the vault popup the page never touched is at %q, want %q", href, direct)
		}
	}
	if !found {
		t.Errorf("no popup on the vault: %+v", r.Popups)
	}
}

func (e *navEnv) testFrames(t *testing.T) {
	nonce := e.nonce("frame")
	cross := e.cross + "/doc?n=cross"
	var r struct {
		TargetID string `json:"targetId"`
		Cross    struct {
			Type    string `json:"type"`
			URL     string `json:"url"`
			Waiting bool   `json:"waiting"`
			Href    string `json:"href"`
		} `json:"cross"`
		Later  []string `json:"later"`
		Frames []string `json:"frames"`
	}
	(&protectEnv{linkEnv: e.linkEnv}).evalJSON(t, "linkTest.protect.navFrame("+jsArgs("chrome", e.allowed, nonce, e.allowed+"/frame", cross, e.vault+"/doc?n="+nonce)+")", &r)
	defer e.closeTarget(r.TargetID)
	if r.Cross.Type != "iframe" || r.Cross.URL != cross || r.Cross.Waiting || r.Cross.Href != cross {
		t.Fatalf("the allowed cross-site iframe: %+v", r.Cross)
	}
	if len(r.Later) != 0 {
		t.Errorf("the page got a session to the vault iframe: %v", r.Later)
	}
	if hits := e.protectedHits(nonce); len(hits) > 0 {
		t.Errorf("the vault iframe was requested from a touched target: %v", hits)
	}
	for _, o := range e.log.origins(nonce) {
		if o == e.vault {
			t.Errorf("the injected script ran in the vault iframe: %v", e.log.origins(nonce))
		}
	}
}

func (e *navEnv) testServiceWorker(t *testing.T) {
	if _, title := e.directLoad(t, e.seven+"/sw/register.html"); title != "ready" {
		t.Fatalf("the service worker didn't register: %q", title)
	}
	nonce := e.nonce("sw")
	if _, title := e.directLoad(t, e.seven+"/sw/page?n=control-"+nonce); title != "served by the worker" {
		t.Fatalf("the service worker doesn't serve an untouched tab, so the test can't tell: %q", title)
	}
	r := e.navInit(t, nonce, e.seven+"/sw/page?n="+nonce)
	defer e.closeTarget(r.TargetID)
	e.assertBlocked(t, r.TargetID, nonce, nonce, true)
}

func (e *navEnv) testOwnFetch(t *testing.T) {
	nonce := e.nonce("fetch")
	next := e.allowed + "/doc?n=own-fetch"
	var r struct {
		TargetID string   `json:"targetId"`
		Href     string   `json:"href"`
		Title    string   `json:"title"`
		Sub      string   `json:"sub"`
		Sent     []string `json:"sent"`
		Paused   []struct {
			URL      string `json:"url"`
			Type     string `json:"type"`
			Response bool   `json:"response"`
		} `json:"paused"`
		Detached bool `json:"detached"`
	}
	(&protectEnv{linkEnv: e.linkEnv}).evalJSON(t, "linkTest.protect.navFetch("+jsArgs("chrome", e.allowed, e.allowed+"/doc?n=fetch-start", next, e.seven+"/doc?n="+nonce)+")", &r)
	defer e.closeTarget(r.TargetID)
	if r.Href != next || r.Title != "nav own-fetch" || !strings.Contains(r.Sub, "doc") {
		t.Fatalf("allowed loads under the page's own Fetch: %+v", r)
	}
	request, response, sent := false, false, false
	for _, p := range r.Paused {
		if p.URL == next && p.Type == "Document" {
			request = request || !p.Response
			response = response || p.Response
		}
		if strings.Contains(p.URL, nonce) {
			t.Errorf("the page's Fetch saw the protected document request: %+v", p)
		}
	}
	for _, u := range r.Sent {
		sent = sent || u == next
	}
	if !request || !response || !sent {
		t.Errorf("the page's Fetch and Network missed its own navigation: %+v", r)
	}
	e.assertBlocked(t, r.TargetID, nonce, "", false)
}

func (e *navEnv) testNavStillLinked(t *testing.T) {
	var up bool
	e.p.eval("linkTest.status().up", &up)
	if !up {
		t.Fatal("the link went down")
	}
}

func (e *navEnv) testNoURL(t *testing.T) {
	var r struct {
		TargetID string          `json:"targetId"`
		Attached []string        `json:"attached"`
		Popups   []protectTarget `json:"popups"`
	}
	(&protectEnv{linkEnv: e.linkEnv}).evalJSON(t, "linkTest.protect.navHang("+jsArgs("chrome", e.allowed+"/doc?n=hang-opener", e.allowed+"/hang", 12000)+")", &r)
	defer e.closeTarget(r.TargetID)
	for _, p := range r.Popups {
		defer e.closeTarget(p.TargetID)
	}
	if len(r.Popups) != 1 || r.Popups[0].URL != "" {
		t.Fatalf("the popup that never got a URL: %+v", r.Popups)
	}
	if len(r.Attached) != 0 {
		t.Errorf("the page got a session to a popup that never got a URL: %v", r.Attached)
	}
	e.a.waitStderr(t, "cdp: an auto-attached target never got a URL", 5*time.Second)
}
