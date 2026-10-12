package integration

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	httpcap "github.com/ai-ecoverse/slicc-cli/internal/caps/http"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
)

const (
	credHeader = "Bearer cred-header-0451"
	credQuery  = "cred-query-0451"
	credBody   = "cred-body-0451"
	credPath   = "/cred-path-0451"
)

type mapDialer struct {
	routes map[string]string
	base   net.Dialer
}

func (d *mapDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if to, ok := d.routes[address]; ok {
		return d.base.DialContext(ctx, network, to)
	}
	return nil, fmt.Errorf("no route to %s", address)
}

func credUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "https://example.com/elsewhere", http.StatusFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Set-Cookie", "c=1")
		_, _ = fmt.Fprintf(w, "%s %s %s %s", r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCredentialedHTTP(t *testing.T) {
	up := credUpstream(t)
	roots := x509.NewCertPool()
	roots.AddCert(up.Certificate())
	d := &mapDialer{routes: map[string]string{
		"example.com:443":   up.Listener.Addr().String(),
		"mismatch.test:443": up.Listener.Addr().String(),
		"closed.test:443":   closedAddr(t),
	}}
	env := setupInProcess(t, policy.Local(), func(n *node.Node) {
		httpcap.RegisterOptions(n, httpcap.Options{Dialer: d, RootCAs: roots, Logf: n.Logf})
	})
	var h struct {
		Caps []string `json:"caps"`
	}
	env.p.eval("linkTest.ready()", &h)
	if !slices.Contains(h.Caps, "credentialed") || !slices.Contains(h.Caps, "http") {
		t.Fatalf("caps %v", h.Caps)
	}
	cred := map[string]any{"credentialed": true}
	t.Run("passes the request on", func(t *testing.T) {
		for _, shared := range []bool{false, true} {
			r := env.credHTTP(t, map[string]any{"url": "https://example.com" + credPath + "?t=" + credQuery, "method": "POST", "headers": [][2]string{{"Authorization", credHeader}}, "body64": base64.StdEncoding.EncodeToString([]byte(credBody))}, shared, cred)
			want := "POST " + credPath + "?t=" + credQuery + " " + credHeader + " " + credBody
			if r.Reset != "" || r.Status != 200 || r.body(t) != want {
				t.Fatalf("credentialed POST (shared=%v): %+v", shared, r)
			}
		}
	})
	t.Run("redirects aren't followed", func(t *testing.T) {
		r := env.credHTTP(t, map[string]any{"url": "https://example.com/redirect", "method": "GET", "headers": [][2]string{{"Authorization", credHeader}}}, false, cred)
		if loc, _ := r.header("location"); r.Status != 302 || loc != "https://example.com/elsewhere" {
			t.Fatalf("redirect: %+v", r)
		}
	})
	t.Run("only https on port 443", func(t *testing.T) {
		for _, u := range []string{"http://example.com/", "https://example.com:8443/", "http://example.com:443/"} {
			r := env.credHTTP(t, map[string]any{"url": u, "method": "GET", "headers": [][2]string{{"Authorization", credHeader}}}, false, cred)
			if r.Reset != "EACCES" || !strings.Contains(r.Detail, "https on port 443") {
				t.Fatalf("%s: %+v", u, r)
			}
		}
		for _, open := range []map[string]any{{"credentialed": "yes"}, {"credentialed": 1}} {
			r := env.credHTTP(t, map[string]any{"url": "http://example.com/", "method": "GET", "headers": [][2]string{}}, false, open)
			if r.Reset != "EACCES" {
				t.Fatalf("%v: %+v", open, r)
			}
		}
	})
	t.Run("errors are scrubbed", func(t *testing.T) {
		for host, code := range map[string]string{"closed.test": "ECONNREFUSED", "mismatch.test": "ECONNREFUSED", "nowhere.test": "ECONNREFUSED"} {
			r := env.credHTTP(t, map[string]any{"url": "https://" + host + credPath + "?t=" + credQuery, "method": "GET", "headers": [][2]string{{"Authorization", credHeader}}}, false, cred)
			if r.Reset != code || !strings.HasPrefix(r.Detail, "fetch failed: ") {
				t.Fatalf("%s: %+v", host, r)
			}
			if strings.Contains(r.Detail, credQuery) || strings.Contains(r.Detail, credPath) || strings.Contains(r.Detail, credHeader) {
				t.Fatalf("%s: the reset shows the request: %q", host, r.Detail)
			}
		}
	})
	t.Run("nothing of a credentialed request is logged", func(t *testing.T) {
		logs := env.a.stderr.String()
		for _, s := range []string{credHeader, credQuery, credBody, credPath} {
			if strings.Contains(logs, s) {
				t.Fatalf("the node log shows %q:\n%s", s, logs)
			}
		}
		if !strings.Contains(logs, "http POST https://example.com: 200") {
			t.Fatalf("no request line in the log:\n%s", logs)
		}
	})
}

func (e *linkEnv) credHTTP(t *testing.T, req map[string]any, shared bool, open map[string]any) httpResult {
	t.Helper()
	var r httpResult
	e.p.eval("linkTest.http("+jsArgs(req, shared, 1<<20, open)+")", &r)
	if r.Error != "" {
		t.Fatalf("http %v: %s", req["url"], r.Error)
	}
	return r
}

func TestCredentialedCapInHello(t *testing.T) {
	env := setupLink(t, false)
	var h struct {
		Caps []string `json:"caps"`
	}
	env.p.eval("linkTest.ready()", &h)
	if !slices.Contains(h.Caps, "credentialed") {
		t.Fatalf("caps %v", h.Caps)
	}
	r := env.credHTTP(t, map[string]any{"url": "http://" + env.closed + credPath + "?t=" + credQuery, "method": "GET", "headers": [][2]string{{"Authorization", credHeader}}}, false, map[string]any{"credentialed": true})
	if r.Reset != "EACCES" {
		t.Fatalf("credentialed plain http: %+v", r)
	}
	for _, s := range []string{credHeader, credQuery, credPath} {
		if strings.Contains(env.a.stderr.String(), s) {
			t.Fatalf("slicc attach's log shows %q:\n%s", s, env.a.stderr.String())
		}
	}
}
