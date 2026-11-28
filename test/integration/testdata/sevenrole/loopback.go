package loopback

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/flow"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
)

const (
	sevenRolePort  = 17117
	sevenRolePilot = "https://lease-pilot.sliccy.ai"
	sevenRoleSeven = "https://seven.sliccy.ai"
	sevenRolePage  = "1111111111111111111111111111111111111111111111111111111111111111"
)

type sevenRoleGate struct {
	l      *Listener
	dir    string
	mu     sync.Mutex
	status []string
}

func (g *sevenRoleGate) said() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return strings.Join(g.status, "\n")
}

func newSevenRoleGate(t *testing.T) *sevenRoleGate {
	t.Helper()
	origins, err := flow.PageOrigins(flow.DefaultPage, nil)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := signal.NewOrigins(origins)
	if err != nil {
		t.Fatal(err)
	}
	g := &sevenRoleGate{dir: t.TempDir()}
	id, err := LoadIdentity(g.dir)
	if err != nil {
		t.Fatal(err)
	}
	g.l = &Listener{
		cfg: Config{
			Identity: id, Peers: OpenPeers(g.dir), Origins: allowed, FirstPort: sevenRolePort, LastPort: sevenRolePort,
			NodeID: "sevenrole", Name: "sevenrole", AnswerTimeout: 5 * time.Second,
			Logf: func(string, ...any) {},
			Status: func(format string, args ...any) {
				g.mu.Lock()
				defer g.mu.Unlock()
				g.status = append(g.status, fmt.Sprintf(format, args...))
			},
		},
		port:  sevenRolePort,
		rate:  limiter{max: PairLimit, per: PairWindow},
		conns: map[string]*peerConn{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	g.l.ctx = ctx
	g.l.serve = func(ctx context.Context, c signal.Conn) error {
		for {
			m, err := c.Recv(ctx)
			if err != nil {
				return nil
			}
			if m.T == signal.TypeOffer {
				_ = c.Send(ctx, signal.Message{T: signal.TypeAnswer, Nonce: m.Nonce, SDP: "v=0\r\n"})
				_ = c.Send(ctx, signal.Message{T: signal.TypeCandidate, Nonce: m.Nonce})
			}
		}
	}
	t.Cleanup(func() {
		cancel()
		g.l.mu.Lock()
		g.l.closing = true
		conns := g.l.conns
		g.l.conns = map[string]*peerConn{}
		g.l.mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
		g.l.wg.Wait()
	})
	return g
}

func (g *sevenRoleGate) do(t *testing.T, method, origin string, body string) *httptest.ResponseRecorder {
	t.Helper()
	path := PairPath
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = "127.0.0.1:17117"
	r.Header.Set("Origin", origin)
	if method == http.MethodOptions {
		r.Header.Set("Access-Control-Request-Method", http.MethodPost)
		r.Header.Set("Access-Control-Request-Headers", "content-type")
	} else {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	g.l.ServeHTTP(w, r)
	return w
}

func sevenRolePairBody(t *testing.T, token string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"token": token, "offer": "v=0\r\na=fingerprint:sha-256 " + sevenRolePage + "\r\n"})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func wantSevenRoleForbidden(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusForbidden || w.Body.Len() != 0 {
		t.Errorf("answer %d %q, want an empty 403", w.Code, w.Body.String())
	}
	for name := range w.Header() {
		if strings.HasPrefix(http.CanonicalHeaderKey(name), "Access-Control-") {
			t.Errorf("the 403 carries %s: %q, want no Access-Control-* header", name, w.Header().Values(name))
		}
	}
}

func TestSevenRolePairingGate(t *testing.T) {
	t.Run("PilotRefused", func(t *testing.T) {
		g := newSevenRoleGate(t)
		tok := g.l.Mint()
		wantSevenRoleForbidden(t, g.do(t, http.MethodPost, sevenRolePilot, sevenRolePairBody(t, tok)))
		if _, ok := g.l.cfg.Peers.Origin(sevenRolePage, g.l.nodeFingerprint()); ok {
			t.Error("the pilot origin left a pin")
		}
		if !g.l.Valid(tok) || g.l.Used(tok) {
			t.Error("the pilot origin burned the token")
		}
		if s := g.said(); strings.Contains(s, "paired") {
			t.Errorf("status %q after a refused origin", s)
		}
	})
	t.Run("PilotPreflightRefused", func(t *testing.T) {
		g := newSevenRoleGate(t)
		wantSevenRoleForbidden(t, g.do(t, http.MethodOptions, sevenRolePilot, ""))
	})
	t.Run("SevenPreflight", func(t *testing.T) {
		g := newSevenRoleGate(t)
		w := g.do(t, http.MethodOptions, sevenRoleSeven, "")
		if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != sevenRoleSeven {
			t.Errorf("preflight from seven: %d %v", w.Code, w.Header())
		}
	})
	t.Run("SevenPairs", func(t *testing.T) {
		g := newSevenRoleGate(t)
		tok := g.l.Mint()
		w := g.do(t, http.MethodPost, sevenRoleSeven, sevenRolePairBody(t, tok))
		var got struct{ Answer, Node, Name string }
		if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != sevenRoleSeven || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Answer == "" || got.Node != "sevenrole" {
			t.Fatalf("pairing from seven: %d %q %v", w.Code, w.Body.String(), w.Header())
		}
		if origin, ok := g.l.cfg.Peers.Origin(sevenRolePage, g.l.nodeFingerprint()); !ok || origin != sevenRoleSeven {
			t.Errorf("pin = %q %v, want %s", origin, ok, sevenRoleSeven)
		}
		if !g.l.Used(tok) {
			t.Error("the pairing did not burn the token")
		}
		if s := g.said(); !strings.Contains(s, sevenRoleSeven) || !strings.Contains(s, "paired with seven on") {
			t.Errorf("status %q, want the pairing", s)
		}
	})
}
