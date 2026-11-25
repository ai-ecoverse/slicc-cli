package lease

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"slices"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
	jose "github.com/go-jose/go-jose/v4"
)

type expansionFixture struct {
	b      *Broker
	jkt    string
	parent string
	allow  []Rule
	done   chan struct{}
}

func expansionBroker(t *testing.T) *Broker {
	t.Helper()
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	signer, e := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: signingKey{priv}}, (&jose.SignerOptions{}).WithType(typ))
	if e != nil {
		t.Fatal(e)
	}
	o, e := protect.New(nil)
	if e != nil {
		t.Fatal(e)
	}
	i := id()
	return &Broker{browser: "http://127.0.0.1:9", link: "work", epoch: id(), issuer: "urn:slicc:broker:" + i, audience: "urn:slicc:cdp-lease:" + i, public: verifyKey{pub}, signer: signer, origins: o, pending: map[string]*pending{}, active: map[string]*active{}, slots: make(chan struct{}, 16)}
}

func expansionHolder(t *testing.T) (jwk, string) {
	t.Helper()
	pub, _, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	j := jwk{Crv: "Ed25519", Kty: "OKP", X: enc.EncodeToString(pub)}
	if p, jkt := j.key(); p != nil {
		return j, jkt
	}
	return expansionHolder(t)
}

func expansionGrant(t *testing.T, b *Broker, jkt string, o open, done chan struct{}) claims {
	t.Helper()
	p, e := b.register(jkt, o, done)
	if e != nil {
		t.Fatalf("register %v: %v", o.Allow, e)
	}
	if e := b.Approve(p.Request); e != nil {
		t.Fatalf("approve %v: %v", o.Allow, e)
	}
	d := <-p.deliver
	if d.err != nil {
		t.Fatal(d.err)
	}
	c, e := b.verify(d.jwt, jkt)
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func newExpansionFixture(t *testing.T) expansionFixture {
	t.Helper()
	b := expansionBroker(t)
	j, jkt := expansionHolder(t)
	done := make(chan struct{})
	t.Cleanup(func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, a := range b.active {
			a.timer.Stop()
			a.cancel()
		}
	})
	allow := []Rule{{Kind: "origin", URL: "https://billing.example"}}
	c := expansionGrant(t, b, jkt, open{Kind: Cap, Mode: "request", Key: j, Allow: allow, TTL: 3600}, done)
	return expansionFixture{b: b, jkt: jkt, parent: c.ID, allow: allow, done: done}
}

func (f expansionFixture) expand(rules ...Rule) open {
	return open{Kind: Cap, Mode: "request", Allow: rules, Parent: f.parent}
}

func (f expansionFixture) unchanged(t *testing.T) {
	t.Helper()
	s := f.b.List()
	if len(s.Pending) != 0 {
		t.Fatalf("refused expansion left a pending request an approver could issue: %+v", s.Pending)
	}
	if len(s.Active) != 1 || s.Active[0].Lease != f.parent || !slices.Equal(s.Active[0].Allow, f.allow) {
		t.Fatalf("parent lease changed: %+v", s.Active)
	}
}

var nonExactExpansions = []struct {
	name  string
	rules []Rule
}{
	{"origin", []Rule{{Kind: "origin", URL: "https://receipts.example"}}},
	{"path-prefix", []Rule{{Kind: "path-prefix", URL: "https://receipts.example/r"}}},
	{"root-path-prefix", []Rule{{Kind: "path-prefix", URL: "https://receipts.example/"}}},
	{"exact-then-origin", []Rule{{Kind: "exact", URL: "https://receipts.example/r/1042"}, {Kind: "origin", URL: "https://receipts.example"}}},
	{"exact-then-path-prefix", []Rule{{Kind: "exact", URL: "https://receipts.example/r/1042"}, {Kind: "path-prefix", URL: "https://receipts.example/r"}}},
}

func TestLeaseExpansionRefusesNonExact(t *testing.T) {
	for _, c := range nonExactExpansions {
		t.Run(c.name, func(t *testing.T) {
			f := newExpansionFixture(t)
			o := f.expand(c.rules...)
			if !f.b.validRequest(o) {
				t.Fatalf("wire shape refused before the broker rule: %v", c.rules)
			}
			p, e := f.b.register(f.jkt, o, f.done)
			if !errors.Is(e, invalid) || p != nil {
				t.Fatalf("expansion %v: got %v, want E_INVALID", c.rules, e)
			}
			f.unchanged(t)
			if e := f.b.Approve(""); !errors.Is(e, retired) {
				t.Fatalf("approve after refusal: %v", e)
			}
		})
	}
}

func TestLeaseExpansionRefusesUnknownKind(t *testing.T) {
	for _, kind := range []string{"", "Exact", "EXACT", "exact ", "url"} {
		t.Run("kind="+kind, func(t *testing.T) {
			f := newExpansionFixture(t)
			p, e := f.b.register(f.jkt, f.expand(Rule{Kind: kind, URL: "https://receipts.example/r/1042"}), f.done)
			if !errors.Is(e, invalid) || p != nil {
				t.Fatalf("kind %q: got %v, want E_INVALID", kind, e)
			}
			f.unchanged(t)
		})
	}
}

func TestLeaseExpansionExactAccepted(t *testing.T) {
	for _, c := range []struct {
		name  string
		rules []Rule
	}{
		{"one", []Rule{{Kind: "exact", URL: "https://receipts.example/r/1042"}}},
		{"root", []Rule{{Kind: "exact", URL: "https://receipts.example/"}}},
		{"query-fragment", []Rule{{Kind: "exact", URL: "https://receipts.example/r?id=1042#top"}}},
		{"two", []Rule{{Kind: "exact", URL: "https://receipts.example/r/1042"}, {Kind: "exact", URL: "https://receipts.example/r/1043"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newExpansionFixture(t)
			o := f.expand(c.rules...)
			if !f.b.validRequest(o) {
				t.Fatalf("wire shape refused: %v", c.rules)
			}
			cl := expansionGrant(t, f.b, f.jkt, o, f.done)
			want := append(slices.Clone(f.allow), c.rules...)
			if !slices.Equal(cl.Allow, want) || cl.ID == f.parent {
				t.Fatalf("replacement allow %v, want %v", cl.Allow, want)
			}
			s := f.b.List()
			if len(s.Active) != 1 || s.Active[0].Lease != cl.ID {
				t.Fatalf("parent not replaced: %+v", s.Active)
			}
		})
	}
}

func TestLeaseExpansionCorrectedAfterRefusal(t *testing.T) {
	f := newExpansionFixture(t)
	if _, e := f.b.register(f.jkt, f.expand(Rule{Kind: "origin", URL: "https://receipts.example"}), f.done); !errors.Is(e, invalid) {
		t.Fatalf("origin expansion: %v", e)
	}
	cl := expansionGrant(t, f.b, f.jkt, f.expand(Rule{Kind: "exact", URL: "https://receipts.example/r/1042"}), f.done)
	if len(cl.Allow) != 2 || cl.Allow[1].Kind != "exact" {
		t.Fatalf("corrected expansion: %v", cl.Allow)
	}
}

func TestLeaseExpansionTopLevelUnchanged(t *testing.T) {
	for _, c := range []struct {
		name  string
		rules []Rule
	}{
		{"origin", []Rule{{Kind: "origin", URL: "https://billing.example"}}},
		{"path-prefix", []Rule{{Kind: "path-prefix", URL: "https://billing.example/invoices"}}},
		{"root-path-prefix", []Rule{{Kind: "path-prefix", URL: "https://billing.example/"}}},
		{"exact", []Rule{{Kind: "exact", URL: "https://billing.example/i/1042"}}},
		{"mixed", []Rule{{Kind: "origin", URL: "https://billing.example"}, {Kind: "path-prefix", URL: "https://receipts.example/r"}, {Kind: "exact", URL: "https://help.example/a"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := expansionBroker(t)
			j, jkt := expansionHolder(t)
			t.Cleanup(func() {
				for _, a := range b.active {
					a.timer.Stop()
					a.cancel()
				}
			})
			o := open{Kind: Cap, Mode: "request", Key: j, Allow: c.rules, TTL: 60}
			if !b.validRequest(o) {
				t.Fatalf("wire shape refused: %v", c.rules)
			}
			cl := expansionGrant(t, b, jkt, o, make(chan struct{}))
			if !slices.Equal(cl.Allow, c.rules) {
				t.Fatalf("allow %v, want %v", cl.Allow, c.rules)
			}
		})
	}
}
