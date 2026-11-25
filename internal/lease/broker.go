package lease

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
	jose "github.com/go-jose/go-jose/v4"
)

type Pending struct {
	Request string `json:"request"`
	JKT     string `json:"jkt"`
	Browser string `json:"browser"`
	Link    string `json:"link"`
	Allow   []Rule `json:"allow"`
	TTL     int    `json:"ttlSeconds,omitempty"`
	Parent  string `json:"parent,omitempty"`
}
type Active struct {
	FormEnd string `json:"ordinaryForm,omitempty"`
	Lease   string `json:"lease"`
	JKT     string `json:"jkt"`
	Browser string `json:"browser"`
	Link    string `json:"link"`
	Allow   []Rule `json:"allow"`
	Exp     int64  `json:"exp"`
}
type Snapshot struct {
	Pending []Pending `json:"pending"`
	Active  []Active  `json:"active"`
}
type Decision struct {
	jwt string
	err error
}
type pending struct {
	Pending
	deliver chan Decision
	done    <-chan struct{}
	until   time.Time
}
type active struct {
	formEnd   string
	formTag   string
	submitted bool
	refs      refSet
	claims
	tab    *tab
	ctx    context.Context
	cancel context.CancelFunc
	timer  *time.Timer
}
type Broker struct {
	cleanup                                sync.WaitGroup
	mu                                     sync.Mutex
	browser, link, epoch, issuer, audience string
	public                                 verifyKey
	signer                                 jose.Signer
	origins                                *protect.Origins
	bs                                     *browserSession
	pending                                map[string]*pending
	active                                 map[string]*active
	slots                                  chan struct{}
	closed                                 bool
}

func New(ctx context.Context, browser, label string, protected []string) (*Broker, error) {
	o, e := protect.New(protected)
	if e != nil {
		return nil, invalid
	}
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return nil, e
	}
	signer, e := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: signingKey{priv}}, (&jose.SignerOptions{}).WithType(typ))
	if e != nil {
		return nil, e
	}
	bs, e := openBrowser(ctx, browser, o)
	if e != nil {
		return nil, e
	}
	i := id()
	b := &Broker{browser: browser, link: label, epoch: id(), issuer: "urn:slicc:broker:" + i, audience: "urn:slicc:cdp-lease:" + i, public: verifyKey{pub}, signer: signer, origins: o, bs: bs, pending: map[string]*pending{}, active: map[string]*active{}, slots: make(chan struct{}, 16)}
	go func() { <-bs.c.Done(); b.Close() }()
	return b, nil
}
func (b *Broker) Done() <-chan struct{} { return b.bs.c.Done() }
func (b *Broker) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	for id := range b.active {
		b.retire(id, false)
	}
	for id := range b.pending {
		b.reject(id, retired)
	}
	b.mu.Unlock()
	b.cleanup.Wait()
	_ = b.bs.c.Close()
}
func (b *Broker) reject(id string, e error) {
	if p := b.pending[id]; p != nil {
		delete(b.pending, id)
		p.deliver <- Decision{err: e}
	}
}
func (b *Broker) retire(id string, keepTab bool) {
	a := b.active[id]
	if a == nil {
		return
	}
	delete(b.active, id)
	a.timer.Stop()
	a.cancel()
	if !keepTab {
		b.cleanup.Go(func() { b.bs.discard(a.tab) })
	}
	for k, p := range b.pending {
		if p.Parent == id {
			b.reject(k, retired)
		}
	}
}
func (p *pending) stale() bool {
	select {
	case <-p.done:
		return true
	default:
		return !time.Now().Before(p.until)
	}
}
func (b *Broker) register(jkt string, o open, done <-chan struct{}) (*pending, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, retired
	}
	if len(b.pending) >= 16 {
		return nil, busy
	}
	for _, p := range b.pending {
		if !p.stale() && p.JKT == jkt && p.Parent == o.Parent {
			return nil, busy
		}
	}
	if o.Parent != "" {
		a := b.active[o.Parent]
		if a == nil || a.CNF.JKT != jkt || a.Exp <= time.Now().Unix() {
			return nil, retired
		}
		if len(o.Allow)+len(a.Allow) > 8 {
			return nil, invalid
		}
		for _, r := range o.Allow {
			if slices.Contains(a.Allow, r) {
				return nil, invalid
			}
		}
	}
	p := &pending{Pending: Pending{id(), jkt, b.browser, b.link, slices.Clone(o.Allow), o.TTL, o.Parent}, deliver: make(chan Decision, 1), done: done, until: time.Now().Add(5 * time.Minute)}
	b.pending[p.Request] = p
	return p, nil
}
func (b *Broker) List() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := Snapshot{Pending: []Pending{}, Active: []Active{}}
	for _, p := range b.pending {
		if !p.stale() {
			s.Pending = append(s.Pending, p.Pending)
		}
	}
	for _, a := range b.active {
		if time.Now().Unix() < a.Exp {
			s.Active = append(s.Active, Active{a.formEnd, a.ID, a.CNF.JKT, b.browser, b.link, slices.Clone(a.Allow), a.Exp})
		}
	}
	sort.Slice(s.Pending, func(i, j int) bool { return s.Pending[i].Request < s.Pending[j].Request })
	sort.Slice(s.Active, func(i, j int) bool { return s.Active[i].Lease < s.Active[j].Lease })
	return s
}
func (b *Broker) Deny(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending[id] == nil {
		return retired
	}
	b.reject(id, rejected)
	return nil
}
func (b *Broker) Revoke(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active[id] == nil {
		return retired
	}
	b.retire(id, false)
	return nil
}
func (b *Broker) Approve(id_ string) error { return b.approve(id_, "") }
func (b *Broker) ApproveForm(request, end string) error {
	if !FormEndpoint(end) {
		return invalid
	}
	return b.approve(request, end)
}
func (b *Broker) approve(id_, formEnd string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.pending[id_]
	if b.closed || p == nil || p.stale() {
		return retired
	}
	if formEnd != "" && (p.Parent != "" || !matches(p.Allow, formEnd, b.origins)) {
		return denied
	}
	if len(b.active) >= 4 && p.Parent == "" {
		return busy
	}
	end := time.Now().Unix() + int64(p.TTL)
	rules := slices.Clone(p.Allow)
	t := new(tab)
	if p.Parent != "" {
		a := b.active[p.Parent]
		if a == nil || a.Exp <= time.Now().Unix() || a.CNF.JKT != p.JKT {
			return retired
		}
		t = a.tab
		rules = append(slices.Clone(a.Allow), rules...)
		end = a.Exp
	}
	c := claims{b.issuer, b.audience, id(), end, confirmation{p.JKT}, b.epoch, rules}
	jwt, e := b.sign(c)
	if e != nil {
		return e
	}
	if c.Exp <= time.Now().Unix() {
		return expired
	}
	delete(b.pending, id_)
	if p.Parent != "" {
		b.retire(p.Parent, true)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(end, 0))
	a := &active{claims: c, ctx: ctx, cancel: cancel, tab: t, formEnd: formEnd, formTag: id()}
	a.timer = time.AfterFunc(time.Until(time.Unix(end, 0)), func() { _ = b.Revoke(c.ID) })
	b.active[c.ID] = a
	p.deliver <- Decision{jwt: jwt}
	return nil
}
func (b *Broker) current(c claims) (*active, error) { return b.currentAt(c, time.Now()) }
func (b *Broker) currentAt(c claims, now time.Time) (*active, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if now.Unix() >= c.Exp {
		return nil, expired
	}
	a := b.active[c.ID]
	if a == nil || b.closed || a.ctx.Err() != nil || a.CNF != c.CNF || a.Exp != c.Exp || !slices.Equal(a.Allow, c.Allow) {
		return nil, retired
	}
	return a, nil
}
func withheldResult(op string, fallback refusal) refusal {
	if op == "click" || op == "fill" {
		return unknown
	}
	return fallback
}

func (b *Broker) action(parent context.Context, a *active, op string, args json.RawMessage, send func(any) bool) error {
	var dest string
	switch op {
	case "goto":
		var p struct {
			URL string `json:"url"`
		}
		if !strict(args, &p) || !matches(a.Allow, p.URL, b.origins) {
			return denied
		}
		dest = p.URL
	case "snapshot":
		var empty struct{}
		if !strict(args, &empty) {
			return invalid
		}
	case "click", "fill":
		if a.formEnd == "" {
			return unsupported
		}
	default:
		return unsupported
	}
	if !a.tab.gate.TryLock() {
		return busy
	}
	defer a.tab.gate.Unlock()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	stop := context.AfterFunc(a.ctx, cancel)
	defer stop()
	live := func() bool { _, e := b.current(a.claims); return e == nil }
	var result map[string]any
	var st stamp
	var e error
	if a.formEnd != "" && op != "goto" {
		result, st, e = b.bs.formAction(ctx, a, b.origins, op, args, live)
	} else {
		a.refs = refSet{}
		result, st, e = b.bs.action(ctx, a.tab, a.claims, b.origins, op, dest, live)
	}
	if e != nil {
		return e
	}
	b.bs.mu.Lock()
	defer b.bs.mu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	if ctx.Err() != nil || b.closed || b.active[a.ID] != a || time.Now().Unix() >= a.Exp {
		return withheldResult(op, retired)
	}
	if a.tab.generation != st.gen {
		return withheldResult(op, denied)
	}
	if !send(result) {
		return withheldResult(op, busy)
	}
	return nil
}
