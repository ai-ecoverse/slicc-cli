package fs

import (
	"context"
	"errors"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

const (
	UpdateKind    = "mounts"
	UpdateCap     = "mounts"
	UpdateTimeout = 10 * time.Second
)

var (
	ErrTaken  = errors.New("fs: the mount name is taken")
	ErrFenced = errors.New("fs: slicc never shares this folder")
)

type pusher struct {
	wake chan struct{}
}

type update struct {
	Kind   string       `json:"kind"`
	Mounts []link.Mount `json:"mounts"`
}

func (s *Server) hello(h *link.Hello) {
	s.mu.Lock()
	s.helloVersion = s.version
	s.mu.Unlock()
	h.Mounts = s.Hello()
}

func (s *Server) Add(m Mount) error {
	if Fenced(m.Root) {
		return ErrFenced
	}
	s.mu.Lock()
	if s.byName[m.Name] != nil {
		s.mu.Unlock()
		return ErrTaken
	}
	mm := m
	s.mounts = append(s.mounts, &mm)
	s.byName[m.Name] = &mm
	s.mu.Unlock()
	s.changed()
	return nil
}

func (s *Server) Live() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pushers) > 0
}

func (s *Server) Remove(name string) bool {
	s.mu.Lock()
	m := s.byName[name]
	if m == nil {
		s.mu.Unlock()
		return false
	}
	delete(s.byName, name)
	for i, x := range s.mounts {
		if x == m {
			s.mounts = append(s.mounts[:i:i], s.mounts[i+1:]...)
			break
		}
	}
	sessions := make([]*session, 0, len(s.sessions))
	for _, ss := range s.sessions {
		sessions = append(sessions, ss)
	}
	var watches []*watchStream
	for w := range s.watches {
		if w.covers(m) {
			watches = append(watches, w)
		}
	}
	s.mu.Unlock()
	for _, ss := range sessions {
		ss.drop(m)
	}
	for _, w := range watches {
		w.fail("ENOENT: no such folder")
	}
	s.changed()
	return true
}

func (ss *session) drop(m *Mount) {
	ss.mu.Lock()
	var files []*handle
	for fh, h := range ss.handles {
		if h.m == m {
			delete(ss.handles, fh)
			files = append(files, h)
		}
	}
	delete(ss.count, m)
	ss.mu.Unlock()
	for _, h := range files {
		if h.file != nil {
			_ = h.file.Close()
		}
	}
}

func (s *Server) changed() {
	s.mu.Lock()
	s.version++
	pushers := make([]*pusher, 0, len(s.pushers))
	for _, p := range s.pushers {
		pushers = append(pushers, p)
	}
	s.mu.Unlock()
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

func (s *Server) Attach(l *link.Link) {
	go s.attach(l)
}

func (s *Server) attach(l *link.Link) {
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
	s.mu.Lock()
	if _, dup := s.pushers[l]; dup || s.closed {
		s.mu.Unlock()
		return
	}
	s.pushers[l] = p
	if s.version != s.helloVersion {
		p.poke()
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pushers, l)
		s.mu.Unlock()
	}()
	for {
		select {
		case <-l.Done():
			return
		case <-p.wake:
		}
		s.push(l)
	}
}

func (s *Server) push(l *link.Link) {
	ctx, cancel := context.WithTimeout(context.Background(), UpdateTimeout)
	defer cancel()
	st, err := l.Open(ctx, update{Kind: UpdateKind, Mounts: s.Hello()}, link.OpenOptions{Shared: true})
	if err != nil {
		s.logf("fs: mounts update: %v", err)
		return
	}
	_ = st.CloseWrite()
	select {
	case <-st.Done():
	case <-ctx.Done():
		_ = st.Reset("ETIMEDOUT")
	}
}
