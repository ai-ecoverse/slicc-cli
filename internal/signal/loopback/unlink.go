package loopback

import (
	"context"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

const (
	CapUnlink  = "unlink-loopback"
	KindUnlink = "unlink"
)

func PeerFingerprint(l *link.Link) string {
	pc := l.PeerConnection()
	if pc == nil {
		return ""
	}
	desc := pc.RemoteDescription()
	if desc == nil {
		return ""
	}
	fp, err := Fingerprint(desc.SDP)
	if err != nil {
		return ""
	}
	return fp
}

func (l *Listener) Unlink(s *link.Stream) {
	fp := PeerFingerprint(s.Link())
	if s.Accept(nil) == nil {
		_ = s.CloseWrite()
	}
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
	}
	if fp != "" {
		p, ok, err := l.cfg.Peers.Forget(fp, l.nodeFingerprint())
		switch {
		case err != nil:
			l.cfg.Status("could not forget the pairing: %v", err)
		case ok:
			l.cfg.Status("unlinked by seven on %s (%s); it must pair again to link", p.Origin, ShortFingerprint(fp))
		}
	}
	_ = s.Link().Close()
}

const sweepEvery = 5 * time.Second

func (l *Listener) LinkUp(lk *link.Link) {
	if fp := PeerFingerprint(lk); fp != "" {
		l.mu.Lock()
		l.live[fp] = lk
		l.mu.Unlock()
	}
}

func (l *Listener) LinkDown(lk *link.Link) {
	fp := PeerFingerprint(lk)
	if fp == "" {
		return
	}
	l.mu.Lock()
	if l.live[fp] == lk {
		delete(l.live, fp)
	}
	l.mu.Unlock()
	l.release(fp)
}

func (l *Listener) release(fp string) {
	if _, pinned := l.cfg.Peers.Origin(fp, l.nodeFingerprint()); pinned {
		return
	}
	l.mu.Lock()
	c := l.conns[fp]
	if c == nil || l.live[fp] != nil || c.busy > 0 {
		l.mu.Unlock()
		return
	}
	delete(l.conns, fp)
	l.mu.Unlock()
	_ = c.Close()
	l.cfg.Logf("loopback: released %s, which is no longer paired", ShortFingerprint(fp))
}

func (l *Listener) sweep(ctx context.Context) {
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		l.mu.Lock()
		fps := make([]string, 0, len(l.conns))
		for fp := range l.conns {
			fps = append(fps, fp)
		}
		l.mu.Unlock()
		for _, fp := range fps {
			l.release(fp)
		}
	}
}
