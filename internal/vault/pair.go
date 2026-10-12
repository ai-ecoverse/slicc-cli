package vault

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

const (
	BindingName  = "sliccVaultSend"
	ReceiverName = "sliccVaultReceive"
	retryEvery   = 250 * time.Millisecond
)

var localNetworkPermissions = []string{"local-network-access", "local-network", "loopback-network"}

type PairOptions struct {
	Client   *cdp.Client
	Listener *Listener
	KeyDir   string
	Seven    func(origin string) bool
	Node     string
	Name     string
	Jerk     bool
	Logf     func(format string, args ...any)
	Status   func(format string, args ...any)
}

type Pairer struct {
	o      PairOptions
	mu     sync.Mutex
	target string
	cancel context.CancelFunc
	done   chan struct{}
}

func NewPairer(o PairOptions) *Pairer {
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.Status == nil {
		o.Status = func(string, ...any) {}
	}
	return &Pairer{o: o}
}

func (p *Pairer) Target(t cdp.TargetInfo) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.target == t.TargetID {
		return
	}
	p.stopLocked()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	p.target, p.cancel, p.done = t.TargetID, cancel, done
	go func() {
		defer close(done)
		if err := p.run(ctx, t.TargetID); err != nil && ctx.Err() == nil {
			p.o.Logf("vault: pairing on %s: %v", t.TargetID, err)
		}
	}()
}

func (p *Pairer) stopLocked() {
	if p.cancel != nil {
		p.cancel()
		<-p.done
		p.cancel, p.done, p.target = nil, nil, ""
	}
}

func (p *Pairer) Close() {
	p.mu.Lock()
	p.stopLocked()
	p.mu.Unlock()
}

type attempt struct {
	session string
	ctxID   int
	nonce   string
	vault   string
	seven   string
	keyPath string
	cancel  context.CancelFunc
}

type run struct {
	p        *Pairer
	page     *cdp.Session
	mainID   string
	seven    string
	children map[string]*child
	cur      *attempt
	pending  *candidate
}

type child struct {
	frameID  string
	prepared bool
}

type candidate struct {
	session string
	ec      cdp.ExecutionContext
}

func (p *Pairer) run(ctx context.Context, targetID string) error {
	c := p.o.Client
	sub := c.Subscribe()
	defer sub.Close()
	s, err := c.Attach(ctx, targetID)
	if err != nil {
		return err
	}
	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Detach(dctx)
	}()
	r := &run{p: p, page: s, mainID: targetID, children: map[string]*child{}}
	p.grant(ctx)
	if err := r.prepare(ctx, s); err != nil {
		return err
	}
	if err := s.Call(ctx, "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true}, nil); err != nil {
		p.o.Logf("vault: auto-attach: %v", err)
	}
	for {
		select {
		case ev, ok := <-sub.Events():
			if !ok {
				return c.Err()
			}
			r.handle(ctx, ev)
		case <-ctx.Done():
			r.abandon()
			return nil
		}
	}
}

func (p *Pairer) grant(ctx context.Context) {
	for _, origin := range p.o.Listener.Origins() {
		for _, name := range localNetworkPermissions {
			params := map[string]any{"permission": map[string]any{"name": name}, "setting": "granted", "origin": origin}
			if err := p.o.Client.Call(ctx, "", "Browser.setPermission", params, nil); err != nil {
				p.o.Logf("vault: grant %s to the vault origin: %v", name, err)
			}
		}
	}
}

func (r *run) prepare(ctx context.Context, s *cdp.Session) error {
	if err := s.AddBinding(ctx, BindingName); err != nil {
		return err
	}
	return s.Call(ctx, "Runtime.enable", nil, nil)
}

func (r *run) sessionOK(id string) bool {
	if id == r.page.ID {
		return true
	}
	_, ok := r.children[id]
	return ok
}

func (r *run) handle(ctx context.Context, ev cdp.Event) {
	switch ev.Method {
	case "Target.attachedToTarget":
		if ev.SessionID == r.page.ID {
			r.attached(ctx, ev.Params)
		}
		return
	case "Target.targetInfoChanged":
		var d struct {
			TargetInfo cdp.TargetInfo `json:"targetInfo"`
		}
		if json.Unmarshal(ev.Params, &d) == nil {
			r.infoChanged(ctx, d.TargetInfo)
		}
		return
	case "Target.detachedFromTarget":
		var d struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(ev.Params, &d) == nil {
			delete(r.children, d.SessionID)
			if r.cur != nil && r.cur.session == d.SessionID {
				r.abandon()
			}
		}
		return
	}
	if !r.sessionOK(ev.SessionID) {
		return
	}
	switch ev.Method {
	case "Runtime.executionContextCreated":
		var d struct {
			Context cdp.ExecutionContext `json:"context"`
		}
		if json.Unmarshal(ev.Params, &d) == nil {
			r.contextCreated(ctx, ev.SessionID, d.Context)
		}
	case "Runtime.executionContextDestroyed":
		var d struct {
			ID int `json:"executionContextId"`
		}
		if json.Unmarshal(ev.Params, &d) == nil && r.cur != nil && r.cur.session == ev.SessionID && r.cur.ctxID == d.ID {
			r.abandon()
		}
	case "Runtime.executionContextsCleared":
		if ev.SessionID == r.page.ID {
			r.seven, r.pending = "", nil
		}
		if r.cur != nil && r.cur.session == ev.SessionID {
			r.abandon()
		}
	case "Runtime.bindingCalled":
		var b cdp.BindingCalled
		if json.Unmarshal(ev.Params, &b) == nil && b.Name == BindingName {
			r.bindingCalled(ev.SessionID, b)
		}
	}
}

func (r *run) attached(ctx context.Context, params json.RawMessage) {
	var d struct {
		SessionID  string         `json:"sessionId"`
		TargetInfo cdp.TargetInfo `json:"targetInfo"`
	}
	if json.Unmarshal(params, &d) != nil {
		return
	}
	if d.TargetInfo.Type != "iframe" {
		s := &cdp.Session{Client: r.p.o.Client, ID: d.SessionID}
		go func() { _ = s.Detach(ctx) }()
		return
	}
	r.children[d.SessionID] = &child{frameID: d.TargetInfo.TargetID}
	r.prepareChild(ctx, d.SessionID, d.TargetInfo.URL)
}

func (r *run) infoChanged(ctx context.Context, t cdp.TargetInfo) {
	for id, c := range r.children {
		if c.frameID == t.TargetID {
			r.prepareChild(ctx, id, t.URL)
		}
	}
}

func (r *run) prepareChild(ctx context.Context, sessionID, rawURL string) {
	c := r.children[sessionID]
	if c == nil || c.prepared || !r.vaultOrigin(originOf(rawURL)) {
		return
	}
	c.prepared = true
	s := &cdp.Session{Client: r.p.o.Client, ID: sessionID}
	go func() {
		if err := r.prepare(ctx, s); err != nil {
			r.p.o.Logf("vault: the vault frame's session: %v", err)
		}
	}()
}

func (r *run) vaultOrigin(o string) bool {
	for _, v := range r.p.o.Listener.Origins() {
		if v == o {
			return true
		}
	}
	return false
}

func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func (r *run) contextCreated(ctx context.Context, session string, ec cdp.ExecutionContext) {
	if !ec.AuxData.IsDefault {
		return
	}
	if session == r.page.ID && ec.AuxData.FrameID == r.mainID {
		r.seven = ec.Origin
		if c := r.pending; c != nil {
			r.pending = nil
			r.contextCreated(ctx, c.session, c.ec)
		}
		return
	}
	if !r.vaultOrigin(ec.Origin) {
		return
	}
	if r.seven == "" {
		r.pending = &candidate{session: session, ec: ec}
		return
	}
	if !r.p.o.Seven(r.seven) {
		return
	}
	if c := r.children[session]; session != r.page.ID && (c == nil || c.frameID != ec.AuxData.FrameID) {
		return
	}
	if !r.childOfMain(ctx, session, ec.AuxData.FrameID) {
		r.p.o.Logf("vault: ignoring a vault frame that isn't a child of seven's main frame")
		return
	}
	r.abandon()
	a := &attempt{session: session, ctxID: ec.ID, nonce: nonce(), vault: ec.Origin, seven: r.seven}
	a.keyPath = KeyPath(r.p.o.KeyDir, a.vault, a.seven)
	actx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	r.cur = a
	go r.deliver(actx, a)
}

func (r *run) childOfMain(ctx context.Context, session, frameID string) bool {
	var tree struct {
		FrameTree frameTree `json:"frameTree"`
	}
	s := &cdp.Session{Client: r.p.o.Client, ID: session}
	if err := s.Call(ctx, "Page.getFrameTree", nil, &tree); err != nil {
		return false
	}
	f := tree.FrameTree.find(frameID)
	return f != nil && f.Frame.ParentID == r.mainID
}

type frameTree struct {
	Frame struct {
		ID       string `json:"id"`
		ParentID string `json:"parentId"`
	} `json:"frame"`
	ChildFrames []frameTree `json:"childFrames"`
}

func (t *frameTree) find(id string) *frameTree {
	if t.Frame.ID == id {
		return t
	}
	for i := range t.ChildFrames {
		if f := t.ChildFrames[i].find(id); f != nil {
			return f
		}
	}
	return nil
}

func (r *run) abandon() {
	if r.cur != nil {
		r.cur.cancel()
		r.cur = nil
	}
}

func nonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return b64.EncodeToString(b)
}

type pairMessage struct {
	T     string `json:"t"`
	V     int    `json:"v"`
	Nonce string `json:"nonce"`
	Node  string `json:"node"`
	Name  string `json:"name"`
	Seven string `json:"seven"`
	Port  int    `json:"port"`
	KeyID string `json:"keyId,omitempty"`
	Jerk  bool   `json:"jerk,omitempty"`
}

func (r *run) deliver(ctx context.Context, a *attempt) {
	msg := pairMessage{T: "pair", V: 1, Nonce: a.nonce, Node: r.p.o.Node, Name: r.p.o.Name, Seven: a.seven, Port: r.p.o.Listener.Port(), Jerk: r.p.o.Jerk}
	if key, err := LoadKey(a.keyPath); err == nil {
		msg.KeyID = KeyID(key)
	}
	b, _ := json.Marshal(msg)
	expr := fmt.Sprintf("typeof globalThis.%[1]s !== 'function' ? 'binding' : typeof globalThis.%[2]s !== 'function' ? 'receiver' : (globalThis.%[2]s(%[3]s), 'ok')", BindingName, ReceiverName, b)
	s := &cdp.Session{Client: r.p.o.Client, ID: a.session}
	for {
		var res string
		err := s.Evaluate(ctx, expr, cdp.EvaluateOptions{ContextID: a.ctxID, ReturnByValue: true}, &res)
		if err == nil && res == "ok" {
			r.p.o.Logf("vault: asked the vault frame to pair")
			return
		}
		if err == nil && res == "binding" {
			_ = s.AddBinding(ctx, BindingName)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryEvery):
		}
	}
}

type pairReply struct {
	T      string `json:"t"`
	Nonce  string `json:"nonce"`
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

func (r *run) bindingCalled(session string, b cdp.BindingCalled) {
	a := r.cur
	if a == nil || a.session != session || a.ctxID != b.ExecutionContextID {
		return
	}
	var m pairReply
	if json.Unmarshal([]byte(b.Payload), &m) != nil || m.Nonce != a.nonce {
		return
	}
	switch m.T {
	case "refused":
		r.p.o.Status("vault: pairing refused: %s", m.Reason)
	case "paired":
		key, err := r.keyOf(a, m.Key)
		if err != nil {
			r.p.o.Status("vault: pairing failed: %v", err)
			return
		}
		r.p.o.Listener.SetKey(key)
		r.p.o.Status("vault: paired, listening on 127.0.0.1:%d", r.p.o.Listener.Port())
	default:
		return
	}
	a.cancel()
	r.cur = nil
}

func (r *run) keyOf(a *attempt, fresh string) ([]byte, error) {
	if fresh == "" {
		return LoadKey(a.keyPath)
	}
	key, err := b64.DecodeString(fresh)
	if err != nil || len(key) != KeySize {
		return nil, fmt.Errorf("the vault sent a malformed key")
	}
	return key, SaveKey(a.keyPath, key)
}
