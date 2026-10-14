package integration

import (
	"context"
	"crypto/x509"
	"io"
	"testing"

	httpcap "github.com/ai-ecoverse/slicc-cli/internal/caps/http"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/resolve"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
)

func setupInProcess(t *testing.T, pol *policy.Policy, register func(n *node.Node)) *linkEnv {
	t.Helper()
	srv := pageServer(t)
	b, p := launchWithPage(t, srv.URL+"/index.html")
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	logs := &syncBuffer{}
	a := &attachProc{stderr: logs, stdin: inW, stdout: outR}
	relayStdio(t, p, a)
	logf := func(format string, args ...any) { _, _ = logs.Write([]byte(sprintf(format, args...) + "\n")) }
	n := node.New(node.Config{Name: "it-inproc", Version: "test", Mode: "local", Policy: pol, Exit: true, Logf: logf, Status: logf})
	register(n)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		defer close(served)
		conn, err := signal.NewStdio(inR, outW)
		if err != nil {
			return
		}
		_ = n.Serve(ctx, conn)
	}()
	t.Cleanup(func() {
		cancel()
		_ = inW.Close()
		_ = outR.Close()
		<-served
		if t.Failed() {
			t.Logf("in-process node log:\n%s", logs.String())
		}
	})
	env := &linkEnv{t: t, b: b, p: p, a: a, stdio: true, origin: srv.URL}
	env.waitUp("")
	return env
}

func TestLinkKindsWithInjectedResolverAndRoots(t *testing.T) {
	dns := newFakeDNS(t)
	dns.set("mixed.slicc.test", "127.0.0.1", "10.0.0.1", "169.254.169.254", "93.184.215.14", "::1", "fd00::1", "2606:2800:21f:cb07:6820:80da:af6b:8b2c", "::ffff:93.184.215.14")
	dns.set("private.slicc.test", "10.0.0.1", "::1", "fd00::1")
	dns.set("v4only.slicc.test", "93.184.215.14")
	dns.set("web.slicc.test", "127.0.0.1")
	allow, err := policy.ParseRules([]string{"127.0.0.1/32"})
	if err != nil {
		t.Fatal(err)
	}
	pol := policy.Remote().With(allow, nil)
	up := newUpstream(t)
	roots := x509.NewCertPool()
	roots.AddCert(up.tls.Certificate())
	roots.AddCert(up.h2.Certificate())
	env := setupInProcess(t, pol, func(n *node.Node) {
		n.Handle(resolve.Cap, resolve.Kind, resolve.Handler(&policy.Resolver{Policy: pol, Base: dns.resolver()}, n.Logf))
		n.Handle(httpcap.Cap, httpcap.Kind, httpcap.Handler(httpcap.Options{Dialer: dns.dialer(pol), RootCAs: roots, Logf: n.Logf}))
	})
	t.Run("resolve filters refused addresses", func(t *testing.T) {
		env.wantAddresses(t, "mixed.slicc.test.", 0, "127.0.0.1", "93.184.215.14", "2606:2800:21f:cb07:6820:80da:af6b:8b2c")
		env.wantAddresses(t, "mixed.slicc.test", 4, "127.0.0.1", "93.184.215.14")
		env.wantAddresses(t, "MIXED.slicc.test", 6, "2606:2800:21f:cb07:6820:80da:af6b:8b2c")
	})
	t.Run("resolve answers not mine", func(t *testing.T) {
		env.wantAddresses(t, "private.slicc.test", 0)
		env.wantAddresses(t, "missing.slicc.test", 0)
		env.wantAddresses(t, "v4only.slicc.test", 6)
		env.wantAddresses(t, "10.0.0.1", 0)
		env.wantAddresses(t, "127.0.0.1", 0, "127.0.0.1")
	})
	t.Run("http trusts the injected root", func(t *testing.T) {
		for _, shared := range []bool{false, true} {
			r := env.http(t, map[string]any{"url": up.tls.URL + "/coded?enc=br,gzip", "method": "GET", "headers": [][2]string{}}, shared)
			if r.Reset != "" || r.Status != 200 || r.body(t) != repeatPlain() {
				t.Fatalf("TLS with the test root (shared=%v): %+v", shared, r)
			}
		}
	})
	t.Run("http over HTTP/2", func(t *testing.T) {
		r := env.http(t, map[string]any{"url": up.h2.URL + "/proto", "method": "POST", "headers": [][2]string{{"Cookie", "x=1"}, {"Cookie", "y=2"}}, "body64": "aGk="}, false)
		want := [][2]string{{"set-cookie", "a=1"}, {"set-cookie", "b=2"}}
		if r.Reset != "" || r.Status != 200 || r.StatusText != "OK" || r.body(t) != "HTTP/2.0 x=1; y=2" {
			t.Fatalf("h2: %+v", r)
		}
		if got := r.Headers[len(r.Headers)-2:]; got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("h2 headers %q", r.Headers)
		}
		if _, ok := r.header("content-encoding"); ok {
			t.Fatalf("h2 headers %q", r.Headers)
		}
	})
	t.Run("http resolves through the node", func(t *testing.T) {
		r := env.get(t, "http://web.slicc.test:"+portOf(t, up.srv.URL)+"/target")
		if r.Status != 200 || r.body(t) != "followed" {
			t.Fatalf("named fetch: %+v", r)
		}
		for url, code := range map[string]string{
			"http://missing.slicc.test/":                      "ENOTFOUND",
			"http://private.slicc.test/":                      "EACCES",
			"http://10.0.0.1:" + portOf(t, up.srv.URL) + "/":  "EACCES",
			"http://[::1]:" + portOf(t, up.srv.URL) + "/":     "EACCES",
			"https://web.slicc.test:" + portOf(t, up.tls.URL): "ECONNREFUSED",
		} {
			r := env.http(t, map[string]any{"url": url, "method": "GET", "headers": [][2]string{}}, false)
			if r.Reset != code {
				t.Fatalf("%s: %+v, want %s", url, r, code)
			}
		}
	})
}
