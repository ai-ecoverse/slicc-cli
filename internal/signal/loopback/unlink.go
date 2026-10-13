package loopback

import (
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
		p, ok, err := l.cfg.Peers.Forget(fp)
		switch {
		case err != nil:
			l.cfg.Status("could not forget the pairing: %v", err)
		case ok:
			l.cfg.Status("unlinked by seven on %s (%s); it must pair again to link", p.Origin, ShortFingerprint(fp))
		}
	}
	_ = s.Link().Close()
}

func (l *Listener) LinkDown(lk *link.Link) {
	fp := PeerFingerprint(lk)
	if fp == "" {
		return
	}
	if _, pinned := l.cfg.Peers.Origin(fp); pinned {
		return
	}
	l.mu.Lock()
	c := l.conns[fp]
	if c != nil {
		delete(l.conns, fp)
	}
	l.mu.Unlock()
	if c != nil {
		_ = c.Close()
		l.cfg.Logf("loopback: released %s, which is no longer paired", ShortFingerprint(fp))
	}
}
