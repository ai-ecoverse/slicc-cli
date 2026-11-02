package screen

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
)

type Bootstrap struct {
	V                int    `json:"v"`
	Launch           string `json:"launch"`
	SafetyPath       string `json:"safetyPath"`
	FramesPath       string `json:"framesPath"`
	Challenge        string `json:"challenge"`
	ParentAuditToken string `json:"parentAuditToken"`
}

func (b Bootstrap) Valid() bool {
	if b.V != 1 || !validID(b.Launch, 32) || !validID(b.Challenge, 32) || b.SafetyPath == b.FramesPath {
		return false
	}
	for _, p := range []string{b.SafetyPath, b.FramesPath} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || len([]byte(p)) > 103 || filepath.Dir(p) != filepath.Dir(b.SafetyPath) {
			return false
		}
	}
	_, e := ParsePeer(b.ParentAuditToken)
	return e == nil
}

type authHello struct {
	V           int    `json:"v"`
	Lane        string `json:"lane"`
	Launch      string `json:"launch"`
	Challenge   string `json:"challenge"`
	ClientNonce string `json:"clientNonce"`
	ServerNonce string `json:"serverNonce,omitempty"`
	Connection  string `json:"connection,omitempty"`
}

func readHello(c net.Conn, v any) error {
	var size [4]byte
	if _, e := io.ReadFull(c, size[:]); e != nil {
		return e
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > 4096 {
		return ErrSize
	}
	p := make([]byte, int(n))
	if _, e := io.ReadFull(c, p); e != nil {
		return e
	}
	return Strict(p, v, 4096)
}
func writeHello(c net.Conn, v any) error {
	p, e := json.Marshal(v)
	if e != nil {
		return ErrProtocol
	}
	if len(p) > 4096 {
		return ErrSize
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(p)))
	if e = writeFull(c, n[:]); e != nil {
		return e
	}
	return writeFull(c, p)
}

type recipient struct {
	Fingerprint string `json:"fingerprint"`
	Setup       string `json:"setup"`
}
type hostEvidence struct {
	AuditToken       string `json:"auditToken"`
	BundleIdentifier string `json:"bundleIdentifier"`
}
type evidence struct {
	Location   string         `json:"location"`
	Provenance string         `json:"provenance"`
	Hosts      []hostEvidence `json:"hosts"`
}
type safety struct {
	registration *linkRegistration
	Type         string     `json:"type"`
	V            int        `json:"v"`
	Launch       string     `json:"launch"`
	Connection   string     `json:"connection"`
	Seq          Counter    `json:"seq,omitempty"`
	Echo         Counter    `json:"echo,omitempty"`
	Link         *string    `json:"link,omitempty"`
	Session      *string    `json:"session,omitempty"`
	Recipient    *recipient `json:"recipient,omitempty"`
	Evidence     *evidence  `json:"browserEvidence,omitempty"`
	Request      string     `json:"request,omitempty"`
	Binding      *Binding   `json:"binding,omitempty"`
	Scope        string     `json:"scope,omitempty"`
	Limits       *Limits    `json:"limits,omitempty"`
	Reason       string     `json:"reason,omitempty"`
}

func (m safety) MarshalJSON() ([]byte, error) {
	type common struct {
		Type       string  `json:"type"`
		V          int     `json:"v"`
		Launch     string  `json:"launch"`
		Connection string  `json:"connection"`
		Request    string  `json:"request"`
		Link       *string `json:"link"`
		Session    *string `json:"session"`
	}
	base := common{m.Type, m.V, m.Launch, m.Connection, m.Request, m.Link, m.Session}
	if m.Type == "revoked" {
		return json.Marshal(base)
	}
	if m.Type == "revoke" {
		return json.Marshal(struct {
			common
			Reason string `json:"reason"`
		}{base, m.Reason})
	}
	type alias safety
	return json.Marshal(alias(m))
}

type Client struct {
	writeRecord   func(net.Conn, any, []byte, time.Duration) error
	verifyGate    chan struct{}
	authUntil     time.Time
	broker        *Broker
	bootstrap     Bootstrap
	peer          Peer
	verify        Verifier
	safety        *net.UnixConn
	frames        *net.UnixConn
	connection    string
	write         chan safety
	stop          chan struct{}
	once          sync.Once
	mu            sync.Mutex
	sent          Counter
	received      Counter
	echoed        Counter
	health        time.Time
	echoAt        time.Time
	stopAck       <-chan struct{}
	revokeUntil   time.Time
	revokeID      string
	revokeAck     chan struct{}
	revokeMessage safety
	ready         chan struct{}
	readyOnce     sync.Once
}

func Start(ctx context.Context, n *node.Node, audit Audit) (*Client, error) {
	return start(ctx, n, audit, bootstrapFile, VerifyApp, monitorPeer)
}
func start(ctx context.Context, n *node.Node, audit Audit, open func() (*os.File, error), verify Verifier, monitor func(*Client) error) (*Client, error) {
	deadline := time.Now().Add(3 * time.Second)
	f, openErr := open()
	if openErr != nil {
		return nil, ErrAccess
	}
	defer func() { _ = f.Close() }()
	if e := f.SetReadDeadline(deadline); e != nil {
		return nil, ErrAccess
	}
	var size [4]byte
	if _, e := io.ReadFull(f, size[:]); e != nil {
		return nil, ErrAccess
	}
	length := binary.BigEndian.Uint32(size[:])
	if length == 0 || length > 4096 {
		return nil, ErrSize
	}
	p := make([]byte, int(length))
	if _, e := io.ReadFull(f, p); e != nil {
		return nil, ErrAccess
	}
	var b Bootstrap
	if e := Strict(p, &b, 4096); e != nil || !b.Valid() {
		return nil, ErrAccess
	}
	var extra [1]byte
	if _, e := f.Read(extra[:]); e != io.EOF {
		return nil, ErrAccess
	}
	c, e := connect(ctx, b, verify, audit, deadline)
	if e != nil {
		return nil, e
	}
	if e = monitor(c); e != nil {
		c.Close()
		return nil, e
	}
	c.broker.Register(n)

	return c, nil
}
func Connect(ctx context.Context, b Bootstrap, verify Verifier, audit Audit) (*Client, error) {
	return connect(ctx, b, verify, audit, time.Now().Add(3*time.Second))
}
func connect(ctx context.Context, b Bootstrap, verify Verifier, audit Audit, deadline time.Time) (*Client, error) {
	if !b.Valid() || verify == nil {
		return nil, ErrAccess
	}
	peer, _ := ParsePeer(b.ParentAuditToken)
	var e error
	c := &Client{verifyGate: make(chan struct{}, 1), authUntil: deadline, bootstrap: b, peer: peer, verify: verify, write: make(chan safety, 16), stop: make(chan struct{}), sent: "0", received: "0", echoed: "0", ready: make(chan struct{})}
	c.broker = NewBroker(audit, nil)
	c.broker.nativeRevoke = c.revokeBound
	c.broker.nativeStop = c.stopBound
	c.broker.waitStop = c.waitRevocation
	c.broker.onLinked = c.registerLink
	c.broker.registrationRequired = true
	c.broker.authorized = c.authorized
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	c.authUntil = deadline

	if c.safety, e = c.dial(ctx, b.SafetyPath, "safety", deadline); e != nil {
		return nil, e
	}
	if c.frames, e = c.dial(ctx, b.FramesPath, "frames", deadline); e != nil {
		_ = c.safety.Close()
		return nil, e
	}
	c.mu.Lock()
	c.health = time.Now()
	c.echoAt = c.health
	c.mu.Unlock()
	go c.writer(ctx)
	go c.reader()
	go c.images()
	go c.watchdog(ctx)
	return c.awaitReady(ctx, deadline)
}
func (c *Client) awaitReady(ctx context.Context, deadline time.Time) (*Client, error) {
	select {
	case <-c.ready:
		if !time.Now().Before(deadline) {
			c.Close()
			return nil, ErrAccess
		}
		c.mu.Lock()
		c.authUntil = time.Time{}
		c.mu.Unlock()
		return c, nil
	case <-c.stop:
		return nil, ErrAccess
	case <-ctx.Done():
		c.Close()
		return nil, ctx.Err()
	case <-time.After(time.Until(deadline)):
		c.Close()
		return nil, ErrAccess
	}
}
func (c *Client) Broker() *Broker { return c.broker }
func (c *Client) base(t string) safety {
	return safety{Type: t, V: 1, Launch: c.bootstrap.Launch, Connection: c.connection}
}
func (c *Client) queue(m safety) bool {
	select {
	case <-c.stop:
		return false
	default:
	}
	select {
	case c.write <- m:
		return true
	default:
		c.Close()
		return false
	}
}
func (c *Client) Close() {
	c.once.Do(func() {
		close(c.stop)
		c.broker.Close()
		if c.safety != nil {
			_ = c.safety.Close()
		}
		if c.frames != nil {
			_ = c.frames.Close()
		}
	})
}
func (c *Client) writer(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	heartbeat := func() bool {
		select {
		case <-c.ready:
			if !c.healthy() {
				return false
			}
		default:
		}
		c.mu.Lock()
		next, e := c.sent.Next()
		if e != nil {
			c.mu.Unlock()
			return false
		}
		c.sent = next
		echo := c.received
		c.mu.Unlock()
		m := c.base("heartbeat")
		m.Seq = next
		m.Echo = echo
		return c.send(m) == nil
	}
	if !heartbeat() {
		c.Close()
		return
	}
	for {
		select {
		case <-c.stop:
			return
		case <-ctx.Done():
			c.Close()
			return
		case <-ticker.C:
			if !heartbeat() {
				c.Close()
				return
			}
		case m := <-c.write:
			if c.send(m) != nil {
				c.Close()
				return
			}
		}
	}
}
func (c *Client) send(m safety) error {
	if m.Type == "link" && !c.broker.registrationCurrent(m.registration) {
		return nil
	}
	if c.verifyPeer(c.safety) != nil {
		return ErrAccess
	}
	if m.Type == "link" && !c.broker.registrationCurrent(m.registration) {
		return nil
	}
	write := c.writeRecord
	if write == nil {
		write = WriteRecord
	}
	if e := write(c.safety, m, nil, time.Second); e != nil {
		return e
	}
	if m.Type == "link" && !c.broker.completeRegistration(m.registration) {
		return ErrStale
	}
	return nil
}

func (c *Client) reader() {
	var budget tokenBudget
	for {
		var raw json.RawMessage
		_, e := ReadRecord(privateConn{c.safety}, &raw, 0, time.Second)
		if e != nil {
			c.Close()
			return
		}
		if e = c.verifyPeer(c.safety); e != nil {
			c.Close()
			return
		}
		if !budget.take(time.Now(), 16) {
			c.Close()
			return
		}
		var m safety
		if e = Strict(raw, &m, 4096); e != nil || m.V != 1 || m.Launch != c.bootstrap.Launch || m.Connection != c.connection || !c.fields(raw, m.Type) {
			c.Close()
			return
		}
		if !c.handle(m) {
			c.Close()
			return
		}
	}
}
func (c *Client) fields(raw []byte, t string) bool {
	allowed := map[string]bool{"type": true, "v": true, "launch": true, "connection": true}
	fields := ""
	switch t {
	case "heartbeat":
		fields = "seq echo"
	case "grant":
		fields = "request binding scope limits"
	case "revoke":
		fields = "request link session reason"
	case "revoked":
		fields = "request link session"
	default:
		return false
	}
	for _, f := range splitFields(fields) {
		allowed[f] = true
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || len(obj) != len(allowed) {
		return false
	}
	for f := range obj {
		if !allowed[f] {
			return false
		}
	}
	return true
}
func splitFields(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}
func (c *Client) handle(m safety) bool {
	switch m.Type {
	case "heartbeat":
		return c.heartbeat(m)
	case "grant":
		return c.handleGrant(m)
	case "revoke":
		if !validID(m.Request, 16) || (m.Reason == "active" || !validReason(m.Reason)) || !c.current(m.Link, m.Session) {
			return false
		}
		c.broker.Revoke(m.Reason)
		reply := c.base("revoked")
		reply.Request = m.Request
		reply.Link = m.Link
		reply.Session = m.Session
		return c.queue(reply)
	case "revoked":
		c.mu.Lock()
		defer c.mu.Unlock()
		same := func(a, b *string) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }
		if m.Request != c.revokeID || c.revokeAck == nil || !same(m.Link, c.revokeMessage.Link) || !same(m.Session, c.revokeMessage.Session) {
			return false
		}
		select {
		case <-c.revokeAck:
			return true
		default:
			if time.Now().After(c.revokeUntil) {
				return false
			}
			close(c.revokeAck)
		}
		return true
	}
	return false
}
func (c *Client) handleGrant(m safety) bool {
	select {
	case <-c.ready:
	default:
		return false
	}
	if !validID(m.Request, 16) || m.Binding == nil || m.Limits == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	admission := c.broker.awaitRegistration(ctx, m.Binding.Link, c.stop)
	cancel()
	if admission == nil {
		admission = c.broker.Grant(*m.Binding, m.Scope, *m.Limits)
	}
	if e := admission; e != nil {
		reason := "peer-untrusted"
		if errors.Is(e, ErrAudit) {
			reason = "audit"
		}
		c.revoke(reason)
		return true
	}
	reply := c.base("granted")
	reply.Request = m.Request
	reply.Binding = m.Binding
	return c.queue(reply)
}

func validReason(s string) bool {
	for _, r := range []string{"active", "awaiting-consent", "paused", "stopped", "source-changed", "protection", "permission", "indicator", "shortcut", "audit", "peer-untrusted", "peer-lost", "link-lost", "lease-expired", "stalled", "resource-limit", "incompatible"} {
		if s == r {
			return true
		}
	}
	return false
}
func (c *Client) current(l, s *string) bool {
	state := c.broker.Snapshot()
	if l == nil {
		return state.Link == "" && state.Binding == nil && s == nil
	}
	id := state.Link
	if state.Binding != nil {
		id = state.Binding.Link
	}
	if *l != id {
		return false
	}
	session := state.LastSession
	if state.Binding != nil {
		session = &state.Binding.Session
	}
	return (s == nil && session == nil) || (s != nil && session != nil && *s == *session)
}
func (c *Client) revoke(reason string) {
	c.broker.revokeExpected(reason, nil, true)
}
func (c *Client) revokeBound(r Revocation) <-chan struct{} {
	select {
	case <-c.stop:
		return nil
	default:
	}
	m := c.base("revoke")
	m.Request = ID(16)
	m.Reason = r.Reason
	if r.Link != "" {
		id := r.Link
		m.Link = &id
	}
	m.Session = r.Session
	c.mu.Lock()
	if c.revokeAck != nil {
		select {
		case <-c.revokeAck:
		default:
			ack := c.revokeAck
			c.mu.Unlock()
			return ack
		}
	}
	c.revokeID = m.Request
	c.revokeMessage = m
	c.revokeAck = make(chan struct{})
	c.revokeUntil = time.Now().Add(1500 * time.Millisecond)
	ack, until := c.revokeAck, c.revokeUntil
	c.mu.Unlock()
	go c.revokeDeadline(ack, until)
	select {
	case c.write <- m:
	default:
		go c.Close()
	}
	return ack
}
func (c *Client) stopBound(r Revocation) <-chan struct{} {
	done := make(chan struct{})
	c.mu.Lock()
	prior := c.revokeAck
	c.stopAck = done
	c.mu.Unlock()
	deadline := time.Now().Add(1500 * time.Millisecond)
	go c.completeStop(r, prior, done, deadline)
	return done
}
func (c *Client) completeStop(r Revocation, prior <-chan struct{}, done chan struct{}, deadline time.Time) {
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if c.waitRevocation(ctx, prior) != nil {
		c.Close()
		return
	}
	ack := c.revokeBound(r)
	if c.waitRevocation(ctx, ack) != nil {
		c.Close()
		return
	}
	close(done)
}

func (c *Client) revokeDeadline(ack <-chan struct{}, until time.Time) {
	timer := time.NewTimer(time.Until(until))
	defer timer.Stop()
	select {
	case <-ack:
		return
	case <-c.stop:
		return
	case <-timer.C:
	}
	select {
	case <-ack:
		return
	default:
		c.Close()
	}
}
func (c *Client) authorized() bool {
	if !c.healthy() {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopAck != nil {
		select {
		case <-c.stopAck:
		default:
			return false
		}
	}
	if c.revokeAck == nil {
		return true
	}
	select {
	case <-c.revokeAck:
		return true
	default:
		return false
	}
}

func (c *Client) waitStop(ctx context.Context) error {
	c.mu.Lock()
	var ack <-chan struct{} = c.revokeAck
	if c.stopAck != nil {
		select {
		case <-c.stopAck:
		default:
			ack = c.stopAck
		}
	}
	c.mu.Unlock()
	return c.waitRevocation(ctx, ack)
}
func (c *Client) waitRevocation(ctx context.Context, ack <-chan struct{}) error {
	select {
	case <-c.stop:
		return ErrAccess
	default:
	}
	if ack == nil {
		return nil
	}
	select {
	case <-ack:
		return nil
	case <-ctx.Done():
		return ErrAccess
	case <-c.stop:
		return ErrAccess
	}
}
func (c *Client) watchdog(ctx context.Context) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.broker.checkImageLease()
			c.mu.Lock()
			expired := time.Since(c.health) >= 1500*time.Millisecond || time.Since(c.echoAt) >= 1500*time.Millisecond
			c.mu.Unlock()
			if expired {
				c.broker.Revoke("lease-expired")
				c.Close()
				return
			}
		case <-ctx.Done():
			c.Close()
			return
		case <-c.stop:
			return
		}
	}
}
func (c *Client) images() {
	for {
		var frame Frame
		var started time.Time
		body, e := readRecord(privateConn{c.frames}, &frame, FrameBytes, 2*time.Second, func(now time.Time) { started = now })
		if e != nil {
			c.Close()
			return
		}
		if c.verifyPeer(c.frames) != nil || frame.Launch != c.bootstrap.Launch || frame.Connection != c.connection {
			c.Close()
			return
		}
		if time.Since(started) > 500*time.Millisecond {
			continue
		}
		if e = c.broker.Frame(frame, body); e != nil {
			c.Close()
			return
		}
	}
}

func (c *Client) registerLink(l *link.Link) {
	r, e := c.broker.registrationSnapshot(l)
	if errors.Is(e, ErrStale) {
		return
	}
	if e != nil {
		c.Close()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if c.waitStop(ctx) != nil {
		c.Close()
		return
	}
	c.queueRegistration(r)
}
func (c *Client) queueRegistration(r *linkRegistration) bool {
	if !c.broker.registrationCurrent(r) {
		return false
	}
	m := c.base("link")
	m.registration = r
	m.Link = &r.id
	m.Recipient = &recipient{Fingerprint: r.fingerprint, Setup: "hub"}
	m.Evidence = &evidence{Location: "unknown", Provenance: "unresolved", Hosts: []hostEvidence{}}
	return c.queue(m)
}

func (c *Client) healthy() bool {
	select {
	case <-c.stop:
		return false
	default:
	}
	select {
	case <-c.ready:
	default:
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Since(c.health) < 1500*time.Millisecond && time.Since(c.echoAt) < 1500*time.Millisecond
}

type tokenBudget struct {
	at     time.Time
	tokens float64
}

func (b *tokenBudget) take(now time.Time, rate float64) bool {
	if b.at.IsZero() {
		b.tokens = rate
	} else {
		b.tokens = min(rate, b.tokens+now.Sub(b.at).Seconds()*rate)
	}
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (c *Client) dial(ctx context.Context, path, lane string, deadline time.Time) (*net.UnixConn, error) {
	b := c.bootstrap
	conn, e := (&net.Dialer{Deadline: deadline}).DialContext(ctx, "unix", path)
	if e != nil {
		return nil, ErrAccess
	}
	u := conn.(*net.UnixConn)
	_ = u.SetDeadline(deadline)
	fail := func() (*net.UnixConn, error) { _ = u.Close(); return nil, ErrAccess }
	if e = c.verifyPeer(u); e != nil {
		return fail()
	}
	hello := authHello{V: 1, Lane: lane, Launch: b.Launch, Challenge: b.Challenge, ClientNonce: ID(32), Connection: c.connection}
	if e = writeHello(u, hello); e != nil {
		return fail()
	}
	var response authHello
	if e = readHello(privateConn{u}, &response); e != nil {
		return fail()
	}
	if e = c.verifyPeer(u); e != nil {
		return fail()
	}
	if response.V != 1 || response.Lane != lane || response.Launch != b.Launch || response.Challenge != b.Challenge || response.ClientNonce != hello.ClientNonce || !validID(response.ServerNonce, 32) || !validID(response.Connection, 32) {
		return fail()
	}
	if c.connection != "" && c.connection != response.Connection {
		return fail()
	}
	c.connection = response.Connection
	return u, nil
}

func (c *Client) heartbeat(m safety) bool {
	seq, e := m.Seq.Value()
	if e != nil {
		return false
	}
	echo, e := m.Echo.Value()
	if e != nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	old, _ := c.received.Value()
	sent, _ := c.sent.Value()
	acked, _ := c.echoed.Value()
	now := time.Now()
	if seq <= old || echo > sent || echo < acked || now.Sub(c.health) > 1500*time.Millisecond || now.Sub(c.echoAt) > 1500*time.Millisecond {
		return false
	}
	c.received = m.Seq
	c.health = now
	if echo > acked {
		c.echoed = m.Echo
		c.echoAt = now
	}
	if echo > 0 {
		c.readyOnce.Do(func() { close(c.ready) })
	}
	return true

}

func (c *Client) Authenticated() bool { return c.healthy() }

func (c *Client) verifyPeer(conn *net.UnixConn) error {
	select {
	case <-c.stop:
		return ErrAccess
	default:
	}
	if c.verify == nil || c.verifyGate == nil {
		return ErrAccess
	}
	c.mu.Lock()
	until := c.authUntil
	c.mu.Unlock()
	by := time.Now().Add(500 * time.Millisecond)
	if !until.IsZero() && until.Before(by) {
		by = until
	}
	wait := time.Until(by)
	if wait <= 0 {
		return ErrAccess
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case c.verifyGate <- struct{}{}:
	case <-timer.C:
		return ErrAccess
	case <-c.stop:
		return ErrAccess
	}
	result := make(chan error, 1)
	go func() { defer func() { <-c.verifyGate }(); result <- c.verify(conn, c.peer) }()
	select {
	case e := <-result:
		return c.checkedVerification(e, by)
	case <-timer.C:
		return ErrAccess
	case <-c.stop:
		return ErrAccess
	}
}

func (c *Client) checkedVerification(e error, by time.Time) error {
	select {
	case <-c.stop:
		return ErrAccess
	default:
	}
	if e != nil || !time.Now().Before(by) {
		return ErrAccess
	}
	return nil
}
