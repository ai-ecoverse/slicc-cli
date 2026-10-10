package cdp

import (
	"context"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

const UpdateTimeout = 10 * time.Second

type pusher struct {
	wake chan struct{}
}

type update struct {
	Kind     string         `json:"kind"`
	Browsers []link.Browser `json:"browsers"`
}

func (r *Registry) hello(h *link.Hello) {
	r.mu.Lock()
	r.helloVersion = r.version
	r.mu.Unlock()
	h.Browsers = r.List()
}

func (r *Registry) changed() {
	r.mu.Lock()
	r.version++
	pushers := make([]*pusher, 0, len(r.links))
	for _, p := range r.links {
		pushers = append(pushers, p)
	}
	r.mu.Unlock()
	for _, p := range pushers {
		p.poke()
	}
}

func (p *pusher) poke() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (r *Registry) Attach(l *link.Link) {
	go r.attach(l)
}

func (r *Registry) attach(l *link.Link) {
	select {
	case <-l.Ready():
	case <-l.Done():
		return
	}
	h, _ := l.PeerHello()
	if !h.HasCap(UpdateCap) {
		return
	}
	p := &pusher{wake: make(chan struct{}, 1)}
	r.mu.Lock()
	if _, dup := r.links[l]; dup {
		r.mu.Unlock()
		return
	}
	r.links[l] = p
	if r.version != r.helloVersion {
		p.poke()
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.links, l)
		r.mu.Unlock()
	}()
	for {
		select {
		case <-l.Done():
			return
		case <-p.wake:
		}
		r.push(l)
	}
}

func (r *Registry) push(l *link.Link) {
	ctx, cancel := context.WithTimeout(context.Background(), UpdateTimeout)
	s, err := l.Open(ctx, update{Kind: UpdateKind, Browsers: r.List()}, link.OpenOptions{Shared: true})
	if err != nil {
		cancel()
		r.logf("cdp: browsers update: %v", err)
		return
	}
	_ = s.CloseWrite()
	go func() {
		defer cancel()
		select {
		case <-s.Done():
		case <-ctx.Done():
			_ = s.Reset("ETIMEDOUT")
		}
	}()
}
