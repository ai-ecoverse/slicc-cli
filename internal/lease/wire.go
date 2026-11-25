package lease

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

type open struct {
	Kind   string `json:"kind"`
	Mode   string `json:"mode"`
	Key    jwk    `json:"jwk"`
	Allow  []Rule `json:"allow,omitempty"`
	TTL    int    `json:"ttlSeconds,omitempty"`
	Parent string `json:"parent,omitempty"`
	Reason string `json:"reason,omitempty"`
	JWT    string `json:"jwt,omitempty"`
}

func read(s io.Reader, v any) bool {
	var h [4]byte
	if _, e := io.ReadFull(s, h[:]); e != nil {
		return false
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > 65536 {
		return false
	}
	b := make([]byte, n)
	_, e := io.ReadFull(s, b)
	return e == nil && strict(b, v)
}
func send(s *link.Stream, v any) bool {
	b, e := json.Marshal(v)
	if e != nil || len(b) > 65536 {
		return false
	}
	buf := make([]byte, len(b)+4)
	binary.BigEndian.PutUint32(buf, uint32(len(b)))
	copy(buf[4:], b)
	ok, e := s.TryWrite(buf)
	return ok && e == nil
}
func failure(err error, id int) map[string]any {
	m := map[string]any{"type": "error", "reason": err.Error(), "message": "Lease operation refused."}
	if errors.Is(err, denied) {
		m["message"] = "Outside scope or document changed. Request the exact URL or ask the owner for help; do not retry a site action automatically."
	}
	if errors.Is(err, unknown) {
		m["message"] = "The form action may already have run. Inspect the task tab; do not retry automatically."
	}
	if id > 0 {
		m["id"] = id
	}
	return m
}
func (b *Broker) Serve(s *link.Stream) {
	h, ok := s.Link().PeerHello()
	if s.Shared() || !ok || !slices.Equal(h.Caps, []string{Cap}) {
		_ = s.Reset("E_UNSUPPORTED")
		return
	}
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		_ = s.Reset("E_BUSY")
		return
	}
	r := s.ReserveAllocation(256<<10, false)
	if r == nil {
		_ = s.Reset("E_BUSY")
		return
	}
	defer r.Release()
	b.handshake(s)
}
func (b *Broker) handshake(s *link.Stream) {
	var o open
	if len(s.OpenPayload()) > 24576 || !strict(s.OpenPayload(), &o) || o.Kind != Cap {
		_ = s.Reset("E_INVALID")
		return
	}
	pub, jkt := o.Key.key()
	if pub == nil {
		_ = s.Reset("E_INVALID")
		return
	}
	var c claims
	var binding []byte
	switch o.Mode {
	case "request":
		if !b.validRequest(o) {
			_ = s.Reset("E_INVALID")
			return
		}
		binding = decode(jkt, 32)
	case "data":
		if o.TTL != 0 || o.Parent != "" || o.Allow != nil || o.Reason != "" {
			_ = s.Reset("E_INVALID")
			return
		}
		var e error
		c, e = b.verify(o.JWT, jkt)
		if e == nil {
			_, e = b.current(c)
		}
		if e != nil {
			_ = s.Reset(e.Error())
			return
		}
		digest := sha256.Sum256([]byte(o.JWT))
		binding = digest[:]
	default:
		_ = s.Reset("E_INVALID")
		return
	}
	nonce := random(32)
	end := time.Now().Add(30 * time.Second)
	_ = s.SetReadDeadline(end)
	if s.Accept(map[string]string{"nonce": enc.EncodeToString(nonce)}) != nil {
		return
	}
	var p struct {
		Type string `json:"type"`
		Sig  string `json:"sig"`
	}
	if !read(s, &p) || p.Type != "proof" || !time.Now().Before(end) || !ed25519.Verify(pub, proof(o.Mode, nonce, binding), decode(p.Sig, 64)) {
		_ = s.Reset("E_AUTH")
		return
	}
	if o.Mode == "request" {
		b.request(s, o, jkt)
		return
	}
	a, e := b.current(c)
	if e != nil {
		_ = s.Reset(e.Error())
		return
	}
	b.data(s, a)
}
func (b *Broker) validRequest(o open) bool {
	return o.JWT == "" && len(o.Reason) <= 256 && ValidRules(o.Allow, b.origins) && (o.Parent == "" && o.TTL > 0 && o.TTL <= MaxTTL || o.Parent != "" && o.TTL == 0 && len(o.Parent) <= 128)
}
func (b *Broker) request(s *link.Stream, o open, jkt string) {
	p, e := b.register(jkt, o, s.Done())
	if e != nil {
		_ = s.Reset(e.Error())
		return
	}
	defer func() { _ = b.Deny(p.Request) }()
	if !send(s, map[string]string{"type": "pending", "request": p.Request}) {
		_ = s.Reset("E_BUSY")
		return
	}
	timer := time.NewTimer(time.Until(p.until))
	defer timer.Stop()
	select {
	case <-s.Done():
		return
	case <-timer.C:
		_ = s.Reset("E_EXPIRED")
	case d := <-p.deliver:
		var msg any
		if d.err != nil {
			msg = failure(d.err, 0)
		} else {
			msg = map[string]string{"type": "grant", "request": p.Request, "jwt": d.jwt}
		}
		if !send(s, msg) {
			_ = s.Reset("E_BUSY")
			return
		}
		_ = s.CloseWrite()
		time.AfterFunc(3*time.Second, func() { _ = s.Reset("E_CLOSED") })
	}
}
func (b *Broker) data(s *link.Stream, a *active) {
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	stop := context.AfterFunc(a.ctx, func() {
		reason := "E_REVOKED"
		if time.Now().Unix() >= a.Exp {
			reason = "E_EXPIRED"
		}
		_ = s.Reset(reason)
	})
	defer stop()
	go func() {
		select {
		case <-s.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	_ = s.SetReadDeadline(time.Time{})
	if !send(s, map[string]string{"type": "ready"}) {
		_ = s.Reset("E_BUSY")
		return
	}
	last := 0
	for {
		var r struct {
			Type string          `json:"type"`
			ID   int             `json:"id"`
			Op   string          `json:"op"`
			Args json.RawMessage `json:"args"`
		}
		if !read(s, &r) || r.Type != "action" || r.ID <= last || r.ID > 1<<31-1 {
			_ = s.Reset("E_INVALID")
			return
		}
		last = r.ID
		if _, e := b.current(a.claims); e != nil {
			_ = s.Reset(e.Error())
			return
		}
		e := b.action(ctx, a, r.Op, r.Args, func(result any) bool { return send(s, map[string]any{"type": "result", "id": r.ID, "result": result}) })
		if e != nil && !send(s, failure(e, r.ID)) {
			_ = s.Reset("E_BUSY")
			return
		}
	}
}
