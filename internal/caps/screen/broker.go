package screen

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
)

type State struct {
	Kind        string   `json:"kind"`
	V           int      `json:"v"`
	Seq         Counter  `json:"stateSeq"`
	Caps        []string `json:"caps"`
	Binding     *Binding `json:"binding,omitempty"`
	Scope       string   `json:"scope,omitempty"`
	Reason      string   `json:"reason"`
	Limits      *Limits  `json:"limits,omitempty"`
	Link        string   `json:"link,omitempty"`
	LastSession *string  `json:"lastSession,omitempty"`
}

func (s State) MarshalJSON() ([]byte, error) {
	if len(s.Caps) > 0 {
		type active struct {
			Kind    string   `json:"kind"`
			V       int      `json:"v"`
			Seq     Counter  `json:"stateSeq"`
			Caps    []string `json:"caps"`
			Binding *Binding `json:"binding"`
			Scope   string   `json:"scope"`
			Reason  string   `json:"reason"`
			Limits  *Limits  `json:"limits"`
		}
		return json.Marshal(active{s.Kind, s.V, s.Seq, s.Caps, s.Binding, s.Scope, s.Reason, s.Limits})
	}
	type disabled struct {
		Kind        string   `json:"kind"`
		V           int      `json:"v"`
		Seq         Counter  `json:"stateSeq"`
		Caps        []string `json:"caps"`
		Link        string   `json:"link"`
		LastSession *string  `json:"lastSession"`
		Reason      string   `json:"reason"`
	}
	return json.Marshal(disabled{s.Kind, s.V, s.Seq, []string{}, s.Link, s.LastSession, s.Reason})
}

type Audit func(event, reason, scope, session string) error

type Broker struct {
	mu             sync.Mutex
	writeMu        sync.Mutex
	generation     atomic.Uint64
	enabled        atomic.Bool
	authorized     func() bool
	grantSeq       Counter
	l              *link.Link
	linkID         string
	lastSession    *string
	binding        *Binding
	scope          string
	limits         Limits
	frameSeq       Counter
	stateSeq       Counter
	state          State
	wake           chan struct{}
	active         *link.Stream
	streamReady    bool
	openBudget     tokenBudget
	stopPending    bool
	pendingGrant   bool
	pendingStopped bool
	resourceLogged bool
	revoke         func(string)
	nativeRevoke   func(Revocation)
	waitStop       func(context.Context) error
	audit          Audit
	closed         atomic.Bool
	frameAt        time.Time
	lastImage      time.Time
	byteAt         time.Time
	tokens         float64
	fps            int
	maxWidth       int
	maxHeight      int
}

func NewBroker(audit Audit, revoke func(string)) *Broker {
	return &Broker{wake: make(chan struct{}, 1), limits: DefaultLimits(), grantSeq: "0", frameSeq: "0", stateSeq: "0", audit: audit, revoke: revoke}
}
func (b *Broker) Register(n *node.Node) {
	n.HandleKind(Cap, b.handleScreen)
	n.HandleKind("screen-stop", b.handleStop)
	n.OnHello(func(h *link.Hello) { s := supported([]string{"window", "display"}); h.Screen = &s })
	n.Subscribe(func(e node.Event) {
		switch e.Kind {
		case node.EventConnecting:
			b.Attach(e.Link)
		case node.EventLinkDown:
			b.Revoke("link-lost")
		}
	})
}
func (b *Broker) Attach(l *link.Link) {
	b.Revoke("link-lost")
	b.mu.Lock()
	if b.closed.Load() {
		b.mu.Unlock()
		return
	}
	b.l = l
	b.linkID = ID(16)
	b.lastSession = nil
	b.resourceLogged = false
	b.stateSeq = "0"
	b.grantSeq = "0"
	b.mu.Unlock()
	l.OnScreenLimit(func() { b.Revoke("resource-limit") })
	go func() {
		select {
		case <-l.Ready():
			b.mu.Lock()
			if b.l == l {
				b.publishLocked("awaiting-consent")
			}
			b.mu.Unlock()
			b.states(l)
		case <-l.Done():
		}
	}()
}
func (b *Broker) LinkID() string  { b.mu.Lock(); defer b.mu.Unlock(); return b.linkID }
func (b *Broker) Snapshot() State { b.mu.Lock(); defer b.mu.Unlock(); return b.state }
func (b *Broker) Grant(binding Binding, scope string, limits Limits) error {
	generation := b.generation.Load()
	if b.authorized != nil && !b.authorized() {
		return ErrAccess
	}
	if !validGrantShape(binding, scope, limits) {
		return ErrProtocol
	}
	b.mu.Lock()
	if b.closed.Load() || b.l == nil || b.binding != nil || b.pendingGrant || binding.Link != b.linkID {
		b.mu.Unlock()
		return ErrStale
	}
	if !b.l.ScreenAllowed() {
		b.mu.Unlock()
		return ErrAccess
	}
	peer, ok := b.l.PeerHello()
	if !ok || !peer.HasCap(PageCap) {
		b.mu.Unlock()
		return ErrAccess
	}
	next, _ := binding.GrantEpoch.Value()
	old, _ := b.grantSeq.Value()
	if next <= old || (b.lastSession != nil && *b.lastSession == binding.Session) {
		b.mu.Unlock()
		return ErrStale
	}
	if b.audit == nil {
		b.mu.Unlock()
		return ErrAudit
	}
	b.pendingGrant = true
	b.pendingStopped = false
	link := b.l
	b.mu.Unlock()
	auditErr := b.audit("grant-intent", "active", scope, binding.Session)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pendingGrant = false
	if auditErr != nil {
		return ErrAudit
	}
	if b.closed.Load() || b.l != link || b.binding != nil || b.generation.Load() != generation || (b.authorized != nil && !b.authorized()) {
		return ErrAccess
	}
	b.enabled.Store(true)
	b.grantSeq = binding.GrantEpoch
	session := binding.Session
	b.binding = &binding
	b.lastSession = &session
	b.scope = scope
	b.limits = limits
	b.frameSeq = "0"
	b.frameAt = time.Time{}
	b.lastImage = time.Now()
	b.byteAt = time.Now()
	b.tokens = float64(limits.FrameBytes)
	if !b.publishLocked("active") {
		b.binding = nil
		return ErrStale
	}
	audit := b.audit
	go func() {
		if audit("grant", "active", scope, session) != nil {
			b.Revoke("audit")
		}
	}()
	return nil
}

func (b *Broker) publishLocked(reason string) bool {
	next, e := b.stateSeq.Next()
	if e != nil {
		b.enabled.Store(false)
		b.binding = nil
		if b.l != nil {
			go func() { _ = b.l.Close() }()
		}
		return false
	}
	b.stateSeq = next
	s := State{Kind: PageCap, V: 1, Seq: next, Caps: []string{}, Link: b.linkID, LastSession: b.lastSession, Reason: reason}
	if b.binding != nil {
		binding := *b.binding
		limits := b.limits
		s.Caps = []string{Cap}
		s.Binding = &binding
		s.Scope = b.scope
		s.Limits = &limits
		s.Reason = "active"
	}
	b.state = s
	select {
	case b.wake <- struct{}{}:
	default:
	}
	return true
}
func (b *Broker) Revoke(reason string) { b.revokeExpected(reason, nil) }

type stopExpectation struct {
	link    *link.Link
	id      string
	session *string
}
type Revocation struct {
	Reason  string
	Link    string
	Session *string
}

func (b *Broker) revokeExpected(reason string, expected *stopExpectation) bool {
	if reason == "active" || !validReason(reason) {
		reason = "peer-untrusted"
	}
	if expected == nil {
		b.enabled.Store(false)
		b.generation.Add(1)
	}
	b.mu.Lock()
	if expected != nil {
		if !b.matchesStopLocked(expected) {
			b.mu.Unlock()
			return false
		}
		b.enabled.Store(false)
		b.generation.Add(1)
	}
	had := b.binding != nil
	firstResource := reason == "resource-limit" && !b.resourceLogged
	if firstResource {
		b.resourceLogged = true
	}
	pending := b.pendingGrant && !b.pendingStopped
	if pending {
		b.pendingStopped = true
	}
	b.binding = nil
	s := b.active
	b.active = nil
	if b.l != nil {
		b.publishLocked(reason)
	}
	if had && b.nativeRevoke != nil {
		b.nativeRevoke(Revocation{reason, b.linkID, b.lastSession})
	}
	f := b.revoke
	audit := b.audit
	b.mu.Unlock()
	if s != nil {
		go func() { _ = s.Reset("EACCES") }()
	}
	if had && f != nil {
		go f(reason)
	}
	if (had || pending || firstResource) && audit != nil {
		go func() {
			event := "stop"
			if reason == "resource-limit" {
				event = "refusal"
			}
			_ = audit(event, reason, "", "")
		}()
	}

	return true
}

func (b *Broker) matchesStopLocked(expected *stopExpectation) bool {
	if b.l != expected.link || b.linkID != expected.id {
		return false
	}
	if expected.session == nil || b.lastSession == nil {
		return expected.session == nil && b.lastSession == nil
	}
	return *expected.session == *b.lastSession
}

func (b *Broker) Close() { b.closed.Store(true); b.Revoke("peer-lost") }

func (b *Broker) states(l *link.Link) {
	for {
		select {
		case <-l.Done():
			return
		case <-b.wake:
		}
		b.mu.Lock()
		if b.l != l {
			b.mu.Unlock()
			return
		}
		state := b.state
		b.mu.Unlock()
		peer, ok := l.PeerHello()
		if !ok || !peer.HasCap(PageCap) {
			b.Revoke("incompatible")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		s, e := l.Open(ctx, state, link.OpenOptions{Shared: true})
		cancel()
		if e != nil {
			b.Revoke("stalled")
			return
		}
		_ = s.CloseWrite()
		select {
		case <-s.Done():
		case <-l.Done():
			return
		case <-time.After(10 * time.Second):
			b.Revoke("stalled")
			_ = s.Reset("ETIMEDOUT")
			return
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-l.Done():
			return
		}
	}
}
func (b *Broker) handleScreen(s *link.Stream) {
	o, e := parseOpen(s.OpenPayload())
	if e != nil {
		_ = s.Reset(e.Error())
		return
	}
	if s.Shared() {
		_ = s.Reset("EPROTO")
		return
	}
	b.mu.Lock()
	if !b.openBudget.take(time.Now(), 10) {
		b.mu.Unlock()
		b.Revoke("resource-limit")
		_ = s.Reset("EBUSY")
		return
	}
	if !b.enabled.Load() || b.l != s.Link() || b.binding == nil {
		b.mu.Unlock()
		_ = s.Reset("EACCES")
		return
	}
	if *b.binding != o.Binding {
		b.mu.Unlock()
		_ = s.Reset("ESTALE")
		return
	}
	if b.active != nil {
		b.mu.Unlock()
		_ = s.Reset("EBUSY")
		return
	}
	peer, ok := s.Link().PeerHello()
	if !ok || !peer.HasCap(PageCap) {
		b.mu.Unlock()
		_ = s.Reset("EACCES")
		return
	}
	b.active = s
	b.streamReady = false
	b.fps = min(o.MaxFPS, b.limits.MaxFPS)
	b.maxWidth = min(o.MaxWidth, b.limits.LongEdge)
	b.maxHeight = min(o.MaxHeight, b.limits.LongEdge)
	o.MaxFPS = b.fps
	o.MaxWidth = b.maxWidth
	o.MaxHeight = b.maxHeight
	b.mu.Unlock()
	opened := struct {
		V         int     `json:"v"`
		Binding   Binding `json:"binding"`
		Codec     string  `json:"codec"`
		MaxWidth  int     `json:"maxWidth"`
		MaxHeight int     `json:"maxHeight"`
		MaxFPS    int     `json:"maxFps"`
	}{1, o.Binding, "jpeg", o.MaxWidth, o.MaxHeight, o.MaxFPS}
	if e := s.Accept(opened); e != nil {
		b.clearStream(s)
		return
	}
	b.mu.Lock()
	if b.active == s && b.binding != nil && *b.binding == o.Binding && b.enabled.Load() {
		b.streamReady = true
	}
	b.mu.Unlock()
	var one [1]byte
	_ = s.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, e := s.Read(one[:])
	if n != 0 || !errors.Is(e, io.EOF) {
		b.clearStream(s)
		_ = s.Reset("EPROTO")
		return
	}
	<-s.Done()
	b.clearStream(s)
}
func (b *Broker) clearStream(s *link.Stream) {
	b.mu.Lock()
	if b.active == s {
		b.active = nil
	}
	b.mu.Unlock()
}
func (b *Broker) handleStop(s *link.Stream) {
	var o Stop
	if e := Strict(s.OpenPayload(), &o, HeaderBytes); e != nil {
		_ = s.Reset(e.Error())
		return
	}
	if o.Kind != "screen-stop" || o.V != 1 || !validID(o.Link, 16) {
		_ = s.Reset("EPROTO")
		return
	}
	b.mu.Lock()
	if b.stopPending {
		b.mu.Unlock()
		_ = s.Reset("EBUSY")
		return
	}
	b.stopPending = true
	defer func() { b.mu.Lock(); b.stopPending = false; b.mu.Unlock() }()
	b.mu.Unlock()
	if !b.revokeExpected("stopped", &stopExpectation{s.Link(), o.Link, o.Session}) {
		_ = s.Reset("ESTALE")
		return
	}
	if b.waitStop != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		e := b.waitStop(ctx)
		cancel()
		if e != nil {
			_ = s.Reset("ECONNRESET")
			return
		}
	}
	if e := s.Accept(struct {
		V       int  `json:"v"`
		Stopped bool `json:"stopped"`
	}{1, true}); e == nil {
		_ = s.CloseWrite()
		_ = s.SetReadDeadline(time.Now().Add(3 * time.Second))
		var p [1]byte
		n, e := s.Read(p[:])
		if n != 0 || !errors.Is(e, io.EOF) {
			_ = s.Reset("EPROTO")
		}
	}
}
func (b *Broker) Frame(f Frame, jpeg []byte) error {
	generation := b.generation.Load()
	if b.authorized != nil && !b.authorized() {
		return ErrAccess
	}
	b.mu.Lock()
	if !b.enabled.Load() || b.binding == nil || b.closed.Load() {
		b.mu.Unlock()
		return ErrAccess
	}
	if e := CheckFrame(f, jpeg, *b.binding, b.frameSeq, b.limits); e != nil {
		b.mu.Unlock()
		return e
	}
	b.frameSeq = f.Seq
	b.lastImage = time.Now()
	now := time.Now()
	b.tokens = min(float64(b.limits.FrameBytes), b.tokens+now.Sub(b.byteAt).Seconds()*float64(b.limits.BytesPerSecond))
	b.byteAt = now
	s := b.active
	if s == nil || !b.streamReady || now.Sub(b.frameAt) < time.Second/time.Duration(b.fps) || b.tokens < float64(len(jpeg)) {
		b.mu.Unlock()
		return nil
	}
	if f.Width > b.maxWidth || f.Height > b.maxHeight {
		b.mu.Unlock()
		_ = s.Reset("EMSGSIZE")
		b.clearStream(s)
		return nil
	}
	if !b.enabled.Load() || generation != b.generation.Load() {
		b.mu.Unlock()
		return ErrAccess
	}
	b.frameAt = now
	b.tokens -= float64(len(jpeg))
	f.Launch = ""
	f.Connection = ""
	b.mu.Unlock()
	if !b.writeMu.TryLock() {
		return nil
	}
	defer b.writeMu.Unlock()
	conn := frameConn{Conn: s.Conn(), s: s, b: b, generation: generation, deadline: time.Now().Add(2 * time.Second)}
	if e := WriteRecord(conn, f, jpeg, 2*time.Second); e != nil {
		if errors.Is(e, os.ErrDeadlineExceeded) {
			b.Revoke("stalled")
		}
		_ = s.Reset("ETIMEDOUT")
		b.clearStream(s)
		return nil
	}
	return nil
}

func parseOpen(payload []byte) (Open, error) {
	var o Open
	if e := Strict(payload, &o, HeaderBytes); e != nil {
		return o, e
	}
	if o.V != 1 || o.Kind != Cap || !o.Binding.Valid() {
		return o, ErrProtocol
	}
	if o.MaxFPS == 0 {
		o.MaxFPS = 5
	}
	if o.MaxWidth == 0 {
		o.MaxWidth = LongEdge
	}
	if o.MaxHeight == 0 {
		o.MaxHeight = LongEdge
	}
	if o.MaxFPS < 1 || o.MaxFPS > 10 || o.MaxWidth < 1 || o.MaxHeight < 1 || o.MaxWidth > LongEdge || o.MaxHeight > LongEdge {
		return o, errors.New("EINVAL")
	}

	return o, nil
}

func (b *Broker) checkImageLease() {
	b.mu.Lock()
	stalled := b.binding != nil && !b.lastImage.IsZero() && time.Since(b.lastImage) > 2*time.Second
	b.mu.Unlock()
	if stalled {
		b.Revoke("stalled")
	}
}

func validGrantShape(b Binding, scope string, l Limits) bool {
	return b.Valid() && l.Valid() && (scope == "window" || scope == "display")
}
