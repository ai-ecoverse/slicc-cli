package integration

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

const protectedBody = "fake-body-0008"

var redirectMarkers = []string{"fake-value-0003", "fake-value-0004", "fake-value-0005", "fake-value-0006", "/auth/callback", "/oauth/mcp-callback", protectedBody}

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

func (r redirectSeen) carrying(marker string) []string {
	var out []string
	for _, e := range r.Events {
		if strings.Contains(e.Text, marker) {
			out = append(out, e.Method+" "+e.Text)
		}
	}
	for _, c := range r.Results {
		if strings.Contains(c.Text, marker) {
			out = append(out, c.Call+" "+c.Text)
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
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<!doctype html><title>seven</title><p>" + protectedBody))
		case r.URL.Path == "/redirect":
			redirect(w, r, to[n])
		case r.URL.Path == "/flip" && flipped.Swap(true):
			redirect(w, r, to[n])
		case r.URL.Path == "/embed":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><title>embed</title><link rel="stylesheet" href="/redirect?n=seven"><script src="/redirect?n=seven"></script><img src="/redirect?n=seven"><iframe src="/redirect?n=vault"></iframe>`))
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
	to["kept"] = hop + "/landing?code=kept-value-0007"
	via := func(n string) string { return hop + "/redirect?n=" + n }
	t.Run("redirect headers into seven and the vault are cut", func(t *testing.T) {
		var r redirectSeen
		env.evalJSON(t, "linkTest.protect.redirectHeaders("+jsArgs("chrome", hop+"/start", []string{via("seven"), via("vault"), via("kept")})+")", &r)
		r.noMarkers(t)
		for _, got := range r.result("Network.loadNetworkResource") {
			if !strings.Contains(got, "Network.loadNetworkResource follows redirects") {
				t.Errorf("Network.loadNetworkResource wasn't refused: %s", got)
			}
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
		env.evalJSON(t, "linkTest.protect.redirectReads("+jsArgs("chrome", hop+"/embed", hop+"/flip?n=seven", seven)+")", &r)
		r.noMarkers(t)
		for _, call := range []string{"Network.loadNetworkResource", "Network.loadNetworkResource browser"} {
			if got := r.result(call); len(got) != 1 || !strings.Contains(got[0], "Network.loadNetworkResource follows redirects") {
				t.Errorf("%s wasn't refused: %v", call, got)
			}
		}
		if got := r.result("Page.captureSnapshot"); len(got) != 1 || !strings.Contains(got[0], "Page.captureSnapshot serialises every frame") {
			t.Errorf("Page.captureSnapshot wasn't refused: %v", got)
		}
		if got := r.result("Fetch.continueRequest"); len(got) != 1 || !strings.Contains(got[0], refusedText) {
			t.Errorf("Fetch.continueRequest with a url on seven wasn't refused: %v", got)
		}
		if got := r.result("xhr"); len(got) != 1 || !strings.Contains(got[0], "200 <!doctype html><title>redirect start</title>") {
			t.Errorf("the allowed XHR didn't load: %v", got)
		}
	})
}
