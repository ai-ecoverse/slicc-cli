package integration

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

const (
	portGuardEnv   = "SLICC_TEST_PORT_GUARD"
	firstRealPort  = 9222
	lastRealPort   = 9229
	guardHeaderTTL = 5 * time.Second
)

type portGuard struct {
	mu      sync.Mutex
	active  bool
	ports   []int
	servers []*http.Server
	lent    map[int]http.Handler
	hits    []string
}

var guard = &portGuard{lent: map[int]http.Handler{}}

func portGuardWanted() bool {
	return os.Getenv("CI") != "" || os.Getenv(portGuardEnv) == "1"
}

func (g *portGuard) start() error {
	if !portGuardWanted() {
		return nil
	}
	g.active = true
	for port := firstRealPort; port <= lastRealPort; port++ {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			if os.Getenv("CI") != "" {
				return fmt.Errorf("port guard: can't hold 127.0.0.1:%d: %w", port, err)
			}
			continue
		}
		g.serve(ln, port)
		if ln6, err := net.Listen("tcp", net.JoinHostPort("::1", strconv.Itoa(port))); err == nil {
			g.serve(ln6, port)
		}
		g.ports = append(g.ports, port)
	}
	return nil
}

func (g *portGuard) serve(ln net.Listener, port int) {
	srv := &http.Server{
		ReadHeaderTimeout: guardHeaderTTL,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if h := g.lender(port); h != nil {
				h.ServeHTTP(w, r)
				return
			}
			g.hit(fmt.Sprintf("%s %s on %s", r.Method, r.URL.RequestURI(), ln.Addr()))
			http.Error(w, "this port belongs to the developer's own browser", http.StatusServiceUnavailable)
		}),
		ConnState: func(c net.Conn, s http.ConnState) {
			if s == http.StateNew && g.lender(port) == nil {
				g.hit(fmt.Sprintf("connection to %s from %s", c.LocalAddr(), c.RemoteAddr()))
			}
		},
	}
	g.servers = append(g.servers, srv)
	go func() { _ = srv.Serve(ln) }()
}

func (g *portGuard) lender(port int) http.Handler {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lent[port]
}

func (g *portGuard) hit(what string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hits = append(g.hits, what)
}

func (g *portGuard) seen() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.hits)
}

func (g *portGuard) forget(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hits = g.hits[:n]
}

func (g *portGuard) stop() []string {
	for _, srv := range g.servers {
		_ = srv.Close()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.hits...)
}

func (g *portGuard) require(t *testing.T) {
	t.Helper()
	if !g.active || len(g.ports) == 0 {
		t.Skipf("the port guard on 127.0.0.1:%d-%d is off; it runs on CI, or with %s=1", firstRealPort, lastRealPort, portGuardEnv)
	}
}

func (g *portGuard) lend(t *testing.T, h http.Handler) int {
	t.Helper()
	g.require(t)
	port := g.ports[0]
	g.mu.Lock()
	g.lent[port] = h
	g.mu.Unlock()
	t.Cleanup(func() {
		g.mu.Lock()
		delete(g.lent, port)
		g.mu.Unlock()
	})
	return port
}

func (g *portGuard) waitFor(t *testing.T, n int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); g.seen() <= n; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the port guard saw nothing")
		}
	}
}

func TestThePortGuardCatchesAnyDialToTheRealBrowserPorts(t *testing.T) {
	guard.require(t)
	if !slices.Contains(guard.ports, firstRealPort) && os.Getenv("CI") != "" {
		t.Fatalf("the guard doesn't hold %d: %v", firstRealPort, guard.ports)
	}
	for _, port := range guard.ports {
		before := guard.seen()
		c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
		if err != nil {
			t.Fatalf("dial %d: %v", port, err)
		}
		_ = c.Close()
		guard.waitFor(t, before)
		guard.forget(before)
	}
	before := guard.seen()
	eps, _ := browser.DiscoverEndpoints(context.Background(), browser.Options{Home: t.TempDir(), ProbePorts: guard.ports[:1]})
	guard.waitFor(t, before)
	guard.forget(before)
	if len(eps) != 0 {
		t.Fatalf("the guard answered like a browser: %+v", eps)
	}
}
