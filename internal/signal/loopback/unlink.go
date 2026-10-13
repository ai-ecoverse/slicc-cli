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
