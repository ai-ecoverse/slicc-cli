package lease

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
	"github.com/coder/websocket"
	whatwg "github.com/nationallibraryofnorway/whatwg-url/url"
)

type fakeCall struct {
	Method, Session string
	Params          json.RawMessage
}

type fakeChrome struct {
	mu     sync.Mutex
	in     chan []byte
	closed chan struct{}
	once   sync.Once
	calls  []fakeCall
	url    string
	loader int
}

var assignLiteral = regexp.MustCompile(`location\.assign\(("(?:[^"\\]|\\.)*")\);return`)

func newFakeChrome() *fakeChrome {
	return &fakeChrome{in: make(chan []byte, 256), closed: make(chan struct{}), url: "about:blank"}
}

func (f *fakeChrome) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	select {
	case b := <-f.in:
		return websocket.MessageText, b, nil
	case <-f.closed:
		return 0, nil, errors.New("closed")
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}

func (f *fakeChrome) Close(websocket.StatusCode, string) error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

func (f *fakeChrome) push(v any) {
	b, _ := json.Marshal(v)
	f.in <- b
}

func (f *fakeChrome) navigate(raw string) {
	f.loader++
	u, e := whatwg.Parse(raw)
	if e != nil {
		f.url = "about:blank"
		return
	}
	f.url = u.Href(false)
}

func (f *fakeChrome) frame() map[string]any {
	u, fragment, _ := strings.Cut(f.url, "#")
	m := map[string]any{"id": "T1", "url": u, "loaderId": "L" + strings.Repeat("x", f.loader)}
	if strings.Contains(f.url, "#") {
		m["urlFragment"] = "#" + fragment
	}
	if w, e := whatwg.Parse(f.url); e == nil && w.Host() != "" {
		m["securityOrigin"] = "https://" + w.Host()
	}
	return map[string]any{"frameTree": map[string]any{"frame": m}}
}

func (f *fakeChrome) Write(_ context.Context, _ websocket.MessageType, data []byte) error {
	var m struct {
		ID        int64           `json:"id"`
		Method    string          `json:"method"`
		Params    json.RawMessage `json:"params"`
		SessionID string          `json:"sessionId"`
	}
	if e := json.Unmarshal(data, &m); e != nil {
		return e
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{m.Method, m.SessionID, m.Params})
	var p struct {
		URL        string `json:"url"`
		WorldName  string `json:"worldName"`
		Expression string `json:"expression"`
	}
	_ = json.Unmarshal(m.Params, &p)
	var result any = map[string]any{}
	switch m.Method {
	case "Target.createTarget":
		f.url = "about:blank"
		result = map[string]any{"targetId": "T1"}
	case "Page.navigate":
		f.navigate(p.URL)
		f.push(map[string]any{"method": "Page.frameNavigated", "sessionId": m.SessionID, "params": map[string]any{"frame": map[string]any{"id": "T1"}}})
		result = map[string]any{"frameId": "T1", "loaderId": "L"}
	case "Target.attachToTarget":
		result = map[string]any{"sessionId": "S1"}
	case "Page.getFrameTree":
		result = f.frame()
	case "Page.createIsolatedWorld":
		f.push(map[string]any{"method": "Runtime.executionContextCreated", "sessionId": "S1", "params": map[string]any{"context": map[string]any{"id": 7, "uniqueId": "U7", "name": p.WorldName, "auxData": map[string]any{"frameId": "T1", "type": "isolated", "isDefault": false}}}})
		result = map[string]any{"executionContextId": 7}
	case "Runtime.evaluate":
		result = map[string]any{"result": map[string]any{"value": map[string]any{"ok": true, "text": "Invoice 1042"}}}
		if lit := assignLiteral.FindStringSubmatch(p.Expression); lit != nil {
			var dest string
			_ = json.Unmarshal([]byte(lit[1]), &dest)
			f.navigate(dest)
			f.push(map[string]any{"method": "Page.frameNavigated", "sessionId": "S1", "params": map[string]any{"frame": map[string]any{"id": "T1"}}})
			result = map[string]any{"result": map[string]any{"value": map[string]any{"ok": true}}}
		}
	}
	f.push(map[string]any{"id": m.ID, "result": result})
	return nil
}

func (f *fakeChrome) sent() (created, navigated, assigned []string, evaluations int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		var p struct {
			URL        string `json:"url"`
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(c.Params, &p)
		switch c.Method {
		case "Target.createTarget":
			created = append(created, p.URL)
		case "Page.navigate":
			navigated = append(navigated, p.URL)
		case "Runtime.evaluate":
			evaluations++
			if lit := assignLiteral.FindStringSubmatch(p.Expression); lit != nil {
				var dest string
				_ = json.Unmarshal([]byte(lit[1]), &dest)
				assigned = append(assigned, dest)
			}
		}
	}
	return created, navigated, assigned, evaluations
}

type canonicalFixture struct {
	b *Broker
	a *active
	f *fakeChrome
}

func newCanonicalFixture(t *testing.T, rules []Rule) canonicalFixture {
	t.Helper()
	if !ValidRules(rules, nil) {
		t.Fatalf("invalid rules %v", rules)
	}
	o, e := protect.New(nil)
	if e != nil {
		t.Fatal(e)
	}
	f := newFakeChrome()
	bs := &browserSession{tabs: map[string]*tab{}}
	bs.c = cdp.NewClient(f, bs.observe)
	ctx, cancel := context.WithCancel(context.Background())
	a := &active{claims: claims{ID: "L1", Exp: time.Now().Add(time.Hour).Unix(), CNF: confirmation{"holder"}, Allow: rules}, tab: new(tab), ctx: ctx, cancel: cancel}
	b := &Broker{bs: bs, origins: o, active: map[string]*active{"L1": a}}
	t.Cleanup(func() { cancel(); _ = bs.c.Close() })
	return canonicalFixture{b, a, f}
}

func (c canonicalFixture) act(op string, args any) error {
	raw, _ := json.Marshal(args)
	return c.b.action(context.Background(), c.a, op, raw, func(any) bool { return true })
}

func (c canonicalFixture) gotoURL(raw string) error {
	return c.act("goto", map[string]string{"url": raw})
}

var (
	billing   = []Rule{{Kind: "origin", URL: "https://billing.example"}}
	dotted    = []Rule{{Kind: "origin", URL: "https://billing.example."}}
	idn       = []Rule{{Kind: "origin", URL: "https://xn--bcher-kva.example"}}
	exactUTF8 = []Rule{{Kind: "exact", URL: "https://billing.example/r/%C3%A4"}}
	exactFrag = []Rule{{Kind: "exact", URL: "https://billing.example/r#top"}}
	exactDots = []Rule{{Kind: "exact", URL: "https://billing.example/r/1"}}
	prefix    = []Rule{{Kind: "path-prefix", URL: "https://billing.example/invoices"}}
)

var canonicalRows = []struct {
	name  string
	rules []Rule
	raw   string
	want  string
}{
	{"case", billing, "HTTPS://BILLING.Example/Path", "https://billing.example/Path"},
	{"default-port", billing, "https://billing.example:443/x", "https://billing.example/x"},
	{"empty-path", billing, "https://billing.example", "https://billing.example/"},
	{"trailing-dot-rule", dotted, "https://BILLING.example./x", "https://billing.example./x"},
	{"percent-encoding-path-query-fragment", billing, "https://billing.example/r/ä?q=ä#ä", "https://billing.example/r/%C3%A4?q=%C3%A4#%C3%A4"},
	{"percent-encoding-braces", billing, "https://billing.example/{x}", "https://billing.example/%7Bx%7D"},
	{"percent-encoding-kept", billing, "https://billing.example/%69nvoice", "https://billing.example/%69nvoice"},
	{"percent-encoding-exact", exactUTF8, "https://billing.example/r/ä", "https://billing.example/r/%C3%A4"},
	{"fragment-encoding", billing, "https://billing.example/x#a`b", "https://billing.example/x#a%60b"},
	{"fragment-exact", exactFrag, "https://Billing.EXAMPLE:443/r#top", "https://billing.example/r#top"},
	{"dot-segments-origin", billing, "https://billing.example/a/../b", "https://billing.example/b"},
	{"idn-unicode", idn, "https://bücher.example/x", "https://xn--bcher-kva.example/x"},
	{"idn-uppercase", idn, "https://BÜCHER.example/x", "https://xn--bcher-kva.example/x"},
	{"idn-fullwidth", billing, "https://ｂｉｌｌｉｎｇ.example/x", "https://billing.example/x"},
	{"path-prefix", prefix, "https://BILLING.example:443/invoices/ä", "https://billing.example/invoices/%C3%A4"},
}

var refusedRows = []struct {
	name  string
	rules []Rule
	first string
	raw   string
}{
	{"trailing-dot", billing, "https://billing.example/", "https://billing.example./x"},
	{"userinfo", billing, "https://billing.example/", "https://user@billing.example/x"},
	{"userinfo-password", billing, "https://billing.example/", "https://user:pw@billing.example/"},
	{"userinfo-empty", billing, "https://billing.example/", "https://@billing.example/"},
	{"dot-segments-exact", exactDots, "https://billing.example/r/1", "https://billing.example/r/../r/1"},
	{"canonical-over-limit", billing, "https://billing.example/", "https://billing.example/" + strings.Repeat("ä", 1000)},
	{"idn-sibling", idn, "https://xn--bcher-kva.example/", "https://bucher.example/x"},
}

func TestLeaseGotoCanonicalCreate(t *testing.T) {
	for _, r := range canonicalRows {
		t.Run(r.name, func(t *testing.T) {
			c := newCanonicalFixture(t, r.rules)
			if e := c.gotoURL(r.raw); e != nil {
				t.Fatalf("goto %q: %v", r.raw, e)
			}
			created, navigated, _, _ := c.f.sent()
			if len(created) != 1 || created[0] != "about:blank" {
				t.Fatalf("createTarget url %q, want about:blank", created)
			}
			if len(navigated) != 1 || navigated[0] != r.want {
				t.Fatalf("first Page.navigate url %q, want canonical %q (raw %q)", navigated, r.want, r.raw)
			}
		})
	}
}

func TestLeaseGotoCanonicalAssign(t *testing.T) {
	for _, r := range canonicalRows {
		t.Run(r.name, func(t *testing.T) {
			c := newCanonicalFixture(t, r.rules)
			if e := c.gotoURL(r.want); e != nil {
				t.Fatalf("first goto %q: %v", r.want, e)
			}
			if e := c.gotoURL(r.raw); e != nil {
				t.Fatalf("goto %q: %v", r.raw, e)
			}
			_, navigated, assigned, _ := c.f.sent()
			if len(navigated) != 1 || navigated[0] != r.want {
				t.Fatalf("first Page.navigate url %q, want %q", navigated, r.want)
			}
			if len(assigned) != 1 || assigned[0] != r.want {
				t.Fatalf("location.assign %q, want canonical %q (raw %q)", assigned, r.want, r.raw)
			}
		})
	}
}

func TestLeaseGotoCanonicalRefused(t *testing.T) {
	for _, r := range refusedRows {
		t.Run(r.name+"/create", func(t *testing.T) {
			c := newCanonicalFixture(t, r.rules)
			if e := c.gotoURL(r.raw); !errors.Is(e, denied) {
				t.Fatalf("goto %q: %v, want E_SCOPE", r.raw, e)
			}
			if created, navigated, _, _ := c.f.sent(); len(created) != 0 || len(navigated) != 0 {
				t.Fatalf("refused goto reached Chrome: createTarget %q, Page.navigate %q", created, navigated)
			}
		})
		t.Run(r.name+"/assign", func(t *testing.T) {
			c := newCanonicalFixture(t, r.rules)
			if e := c.gotoURL(r.first); e != nil {
				t.Fatalf("first goto %q: %v", r.first, e)
			}
			if e := c.gotoURL(r.raw); !errors.Is(e, denied) {
				t.Fatalf("goto %q: %v, want E_SCOPE", r.raw, e)
			}
			if _, navigated, assigned, n := c.f.sent(); len(navigated) != 1 || len(assigned) != 0 || n != 0 {
				t.Fatalf("refused goto reached Chrome: Page.navigate %q, location.assign %q (%d evaluations)", navigated, assigned, n)
			}
		})
	}
}

func TestLeaseGotoCanonicalDirect(t *testing.T) {
	for _, r := range canonicalRows {
		t.Run(r.name, func(t *testing.T) {
			got, ok := canonical(r.rules, r.raw, nil)
			if !ok || got != r.want || !matches(r.rules, got, nil) {
				t.Fatalf("canonical(%q) = %q, %v; want %q", r.raw, got, ok, r.want)
			}
			if again, ok := canonical(r.rules, got, nil); !ok || again != got {
				t.Fatalf("canonical form not stable: %q -> %q", got, again)
			}
		})
	}
	for _, r := range refusedRows {
		t.Run("refused/"+r.name, func(t *testing.T) {
			if got, ok := canonical(r.rules, r.raw, nil); ok {
				t.Fatalf("canonical(%q) accepted as %q", r.raw, got)
			}
		})
	}
}

func TestLeaseGotoCanonicalSnapshotUnchanged(t *testing.T) {
	c := newCanonicalFixture(t, billing)
	if e := c.gotoURL("HTTPS://BILLING.example:443/x"); e != nil {
		t.Fatal(e)
	}
	if e := c.act("snapshot", map[string]any{}); e != nil {
		t.Fatalf("snapshot after canonical goto: %v", e)
	}
}
