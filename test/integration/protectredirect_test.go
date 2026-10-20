package integration

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const protectedBody = "fake-body-0008"

var redirectMarkers = []string{"fake-value-0003", "fake-value-0004", "fake-value-0005", "fake-value-0006", "/auth/callback", "/oauth/mcp-callback", "fake-value-0031", protectedBody}

type redirectSeen struct {
	Events []struct {
		Method string `json:"method"`
		Text   string `json:"text"`
	} `json:"events"`
	Results []struct {
		Call string `json:"call"`
		Text string `json:"text"`
	} `json:"results"`
}

func markerForms(marker string) []string {
	forms := []string{marker, url.QueryEscape(marker), url.PathEscape(marker)}
	for off := range 3 {
		enc := base64.StdEncoding.EncodeToString(append(make([]byte, off), marker...))
		forms = append(forms, enc[(off*4+2)/3:len(enc)-4])
	}
	return forms
}

func (r redirectSeen) carrying(marker string) []string {
	var out []string
	for _, form := range markerForms(marker) {
		for _, e := range r.Events {
			if strings.Contains(e.Text, form) {
				out = append(out, e.Method+" "+e.Text)
			}
		}
		for _, c := range r.Results {
			if c.Call != "fetch" && strings.Contains(c.Text, form) {
				out = append(out, c.Call+" "+c.Text)
			}
		}
	}
	return out
}

func (r redirectSeen) result(call string) []string {
	var out []string
	for _, c := range r.Results {
		if c.Call == call {
			out = append(out, c.Text)
		}
	}
	return out
}

func (r redirectSeen) noMarkers(t *testing.T) {
	t.Helper()
	for _, marker := range redirectMarkers {
		for _, m := range r.carrying(marker) {
			t.Errorf("the page saw %s in %s", marker, m)
		}
	}
}

func redirectServer(t *testing.T, to map[string]string) *httptest.Server {
	t.Helper()
	var flipped atomic.Bool
	redirect := func(w http.ResponseWriter, r *http.Request, to string) {
		w.Header().Set("Content-Location", to)
		w.Header().Set("Refresh", "0;url="+to)
		w.Header().Set("Link", "<"+to+">; rel=preconnect")
		http.Redirect(w, r, to, http.StatusFound)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := r.URL.Query().Get("n")
		switch {
		case strings.Contains(r.Host, ".sliccy.ai"):
			switch r.URL.Query().Get("as") {
			case "css":
				w.Header().Set("Content-Type", "text/css")
				_, _ = w.Write([]byte("body { --seven: '" + protectedBody + "'; }"))
			case "js":
				w.Header().Set("Content-Type", "text/javascript")
				_, _ = w.Write([]byte("var seven = '" + protectedBody + "';"))
			default:
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte("<!doctype html><title>seven</title><p>" + protectedBody))
			}
		case r.URL.Path == "/redirect":
			redirect(w, r, to[n])
		case r.URL.Path == "/flip" && flipped.Swap(true):
			redirect(w, r, to[n])
		case r.URL.Path == "/assets":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><title>assets</title><style>.own { color: red; }</style><link rel="stylesheet" href="/redirect?n=seven-css"><link rel="stylesheet" href="` + to["asset"] + `?as=css"><script src="/redirect?n=seven-js"></script><script src="` + to["asset"] + `?as=js"></script><script>new Worker("/worker.js"); new SharedWorker("/worker.js?kind=shared")</script><body>`))
		case r.URL.Path == "/worker.js":
			w.Header().Set("Content-Type", "text/javascript")
			imports := `importScripts("/redirect?n=seven-js", "` + to["asset"] + `?as=js&worker=1", "/lib.js");`
			if r.URL.Query().Get("kind") == "" {
				imports += ` new Worker("/worker.js?kind=nested");`
			}
			_, _ = w.Write([]byte(imports))
		case r.URL.Path == "/sw.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write([]byte(`importScripts("` + to["asset"] + `?as=js&sw=1", "/lib.js");`))
		case r.URL.Path == "/r/sw.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write([]byte(`importScripts("/redirect?n=seven-js");`))
		case r.URL.Path == "/lib.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write([]byte("var lib = 'ordinary-lib-0033';"))
		case r.URL.Path == "/embed":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><title>embed</title><link rel="stylesheet" href="/redirect?n=seven"><script src="/redirect?n=seven"></script><img src="/redirect?n=seven"><iframe src="/redirect?n=vault"></iframe><iframe src="/redirect?n=frag"></iframe>`))
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<!doctype html><title>redirect start</title>"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCDPCutsProtectedRedirects(t *testing.T) {
	b := launchBrowserOn(t, 0, "--host-resolver-rules=MAP *.sliccy.ai 127.0.0.1")
	env := setupProtectOn(t, b)
	to := map[string]string{}
	srv := redirectServer(t, to)
	hop := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	su, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	seven := "http://seven.sliccy.ai:" + su.Port() + "/auth/callback?code=fake-value-0003&state=fake-value-0004"
	to["seven"] = seven
	to["vault"] = "http://vault.sliccy.ai:" + su.Port() + "/oauth/mcp-callback?code=fake-value-0005&state=fake-value-0006"
	to["seven-css"] = seven + "&as=css"
	to["seven-js"] = seven + "&as=js"
	to["asset"] = "http://seven.sliccy.ai:" + su.Port() + "/asset"
	to["nested"] = hop + "/landing?next=" + url.QueryEscape(seven)
	to["half"] = hop + "/landing?next=" + strings.ReplaceAll(url.QueryEscape(seven), "%2F", "/")
	to["half2"] = hop + "/landing?next=" + strings.Replace(url.QueryEscape(seven), "%3A", ":", 1)
	to["frag"] = seven + "#access_token=fake-value-0031"
	to["kept"] = hop + "/landing?code=kept-value-0007#kept-frag-0032"
	via := func(n string) string { return hop + "/redirect?n=" + n }
	t.Run("redirect headers into seven and the vault are cut", func(t *testing.T) {
		var r redirectSeen
		env.evalJSON(t, "linkTest.protect.redirectHeaders("+jsArgs("chrome", hop+"/start", []string{via("seven"), via("vault"), via("nested"), via("half"), via("half2"), via("frag"), via("kept")})+")", &r)
		r.noMarkers(t)
		for _, got := range r.result("Network.loadNetworkResource") {
			if !strings.Contains(got, "Network.loadNetworkResource follows redirects") {
				t.Errorf("Network.loadNetworkResource wasn't refused: %s", got)
			}
		}
		paused := map[string]int{}
		for _, c := range r.Results {
			n, ok := strings.CutPrefix(c.Call, "Fetch.getResponseBody "+hop+"/redirect?n=")
			if !ok {
				continue
			}
			paused[n]++
			refused := strings.Contains(c.Text, "slicc: refused: the response came from seven's origin")
			if protected := n != "kept"; refused != protected {
				t.Errorf("Fetch.getResponseBody on the paused redirect %s: %s", n, c.Text)
			}
		}
		for _, n := range []string{"seven", "vault", "frag", "kept"} {
			if paused[n] == 0 {
				t.Errorf("no Fetch.getResponseBody on a paused redirect %s: %v", n, paused)
			}
		}
		sevenOrigin := "http://seven.sliccy.ai:" + su.Port()
		inPlace := false
		for _, e := range r.Events {
			if e.Method == "Network.responseReceivedExtraInfo" && strings.Contains(e.Text, `"statusCode":302`) && strings.Contains(e.Text, `"headersText":"`) && strings.Contains(e.Text, `Location: `+sevenOrigin+`\r\n`) {
				inPlace = true
			}
		}
		if !inPlace {
			t.Error("no 302 extra info into seven kept its headersText with the Location cut in place")
		}
		kept := false
		for _, m := range r.carrying("kept-frag-0032") {
			kept = kept || strings.HasPrefix(m, "Network.requestWillBeSent ") && strings.Contains(m, `"urlFragment":"#kept-frag-0032"`)
		}
		if !kept {
			t.Error("a redirect to an unprotected URL lost its urlFragment")
		}
		for _, want := range []string{"Network.responseReceivedExtraInfo", "Network.requestWillBeSent", "Fetch.requestPaused"} {
			seen := map[string]bool{}
			for _, m := range r.carrying("kept-value-0007") {
				if !strings.HasPrefix(m, want+" ") {
					continue
				}
				for _, h := range []string{"Location", "Content-Location", "Refresh", "Link"} {
					seen[h] = seen[h] || strings.Contains(m, `"`+h+`"`)
				}
			}
			for _, h := range []string{"Location", "Content-Location", "Refresh", "Link"} {
				if !seen[h] {
					t.Errorf("a redirect to an unprotected URL lost its %s in %s", h, want)
				}
			}
		}
	})
	t.Run("nothing reads a redirected protected body", func(t *testing.T) {
		var r redirectSeen
		env.evalJSON(t, "linkTest.protect.redirectReads("+jsArgs("chrome", hop+"/embed", hop+"/flip?n=seven", seven, via("seven"))+")", &r)
		r.noMarkers(t)
		for _, call := range []string{"Network.loadNetworkResource", "Network.loadNetworkResource browser"} {
			if got := r.result(call); len(got) != 1 || !strings.Contains(got[0], "Network.loadNetworkResource follows redirects") {
				t.Errorf("%s wasn't refused: %v", call, got)
			}
		}
		if got := r.result("Page.captureSnapshot"); len(got) != 1 || !strings.Contains(got[0], "Page.captureSnapshot serialises every frame") {
			t.Errorf("Page.captureSnapshot wasn't refused: %v", got)
		}
		if got := r.result("Fetch.continueRequest redirector"); len(got) != 1 || !strings.Contains(got[0], "slicc: refused: that URL redirects to seven's origin") {
			t.Errorf("Fetch.continueRequest with a url that redirected into seven wasn't refused: %v", got)
		}
		if got := r.result("Fetch.continueRequest"); len(got) != 1 || !strings.Contains(got[0], refusedText) {
			t.Errorf("Fetch.continueRequest with a url on seven wasn't refused: %v", got)
		}
		if got := r.result("xhr"); len(got) != 1 || !strings.Contains(got[0], "200 <!doctype html><title>redirect start</title>") {
			t.Errorf("the allowed XHR didn't load: %v", got)
		}
	})
	t.Run("protected stylesheets and scripts can't be read", func(t *testing.T) {
		var r redirectSeen
		env.evalJSON(t, "linkTest.protect.redirectAssets("+jsArgs("chrome", hop+"/assets")+")", &r)
		for call, want := range map[string]int{"Page.getResourceContent": 12, "CSS.getStyleSheetText": 2, "Debugger.getScriptSource": 2, "Debugger.searchInContent": 2, "worker Debugger.getScriptSource": 2, "worker Debugger.searchInContent": 2, "shared Debugger.getScriptSource": 3, "nested Debugger.getScriptSource": 2} {
			refused := 0
			for _, got := range r.result(call) {
				if strings.Contains(got, "slicc: refused: ") {
					refused++
				}
			}
			if refused < want {
				t.Errorf("%s was refused %d times, want at least %d: %v", call, refused, want, r.result(call))
			}
		}
		for _, kind := range []string{"worker", "nested"} {
			lib := false
			for _, got := range r.result(kind + " Debugger.getScriptSource") {
				lib = lib || strings.Contains(got, "ordinary-lib-0033")
			}
			if !lib {
				t.Errorf("an ordinary %s import can't be read: %v", kind, r.result(kind+" Debugger.getScriptSource"))
			}
		}
		own := false
		for _, got := range r.result("CSS.getStyleSheetText") {
			own = own || strings.Contains(got, ".own")
		}
		if !own {
			t.Errorf("the page's own stylesheet can't be read: %v", r.result("CSS.getStyleSheetText"))
		}
		r.noMarkers(t)
	})
	t.Run("a service worker starts, its protected import can't be read, and a redirected import fails in Chrome", func(t *testing.T) {
		var r struct {
			redirectSeen
			Log []string `json:"log"`
		}
		env.evalJSONWithin(t, "linkTest.protect.serviceWorkerImports("+jsArgs("chrome", hop+"/start")+")", &r, 60*time.Second)
		t.Logf("steps: %v", r.Log)
		r.noMarkers(t)
		if got := r.result("sw ready"); len(got) != 1 || !strings.Contains(got[0], `"result":"ready"`) {
			t.Errorf("the service worker didn't start: %v", got)
		}
		refused, lib := 0, false
		for _, got := range r.result("sw Debugger.getScriptSource") {
			if strings.Contains(got, "slicc: refused: ") {
				refused++
			}
			lib = lib || strings.Contains(got, "ordinary-lib-0033")
		}
		if refused < 1 || !lib {
			t.Errorf("the service worker's direct protected import refused %d times, ordinary import readable %v: %v", refused, lib, r.result("sw Debugger.getScriptSource"))
		}
		if got := r.result("early Debugger.enable"); len(got) != 1 || !strings.Contains(got[0], `"ok":true`) {
			t.Errorf("an explicit attach to a running shared worker stalled: %v", got)
		}
		sharedRefused := 0
		for _, got := range r.result("shared Debugger.getScriptSource") {
			if strings.Contains(got, "slicc: refused: the response came from seven's origin") {
				sharedRefused++
			}
		}
		if sharedRefused < 2 {
			t.Errorf("a shared worker started under auto-attach: %d protected imports refused: %v", sharedRefused, r.result("shared Debugger.getScriptSource"))
		}
		if got := r.result("sw redirect"); len(got) != 1 || !strings.Contains(got[0], "ServiceWorker script evaluation failed") {
			t.Errorf("a service worker importing through a redirect registered: %v", got)
		}
	})
}
