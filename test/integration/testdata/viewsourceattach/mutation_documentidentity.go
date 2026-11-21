package cdp

import (
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

type docIdentity struct {
	frameID  string
	loaderID string
	url      string
	full     bool
}

type docIdentityAdmit struct {
	taint uint64
}

type docIdentityHold struct {
	taint           uint64
	seq             uint64
	data            []byte
	id              int64
	session         string
	target          string
	method          string
	extensionAfter  bool
	extensionFrames []string
	arrived         time.Time
	deadline        time.Time
	timer           *time.Timer
	reservation     *link.Reservation
}

type docIdentityAsk struct {
	target     string
	started    time.Time
	infoDone   bool
	infoID     docIdentity
	askingInfo bool
}

func (g *guard) noteOrdinaryDocIdentityFromTargetLocked(t *target) { _ = t }

func (g *guard) noteOrdinaryDocIdentityFromNavLocked(nt *navTarget) { _ = nt }

func (g *guard) noteDocIdentityWatchURLLocked(target, url string) { _, _ = target, url }

func (g *guard) docIdentityFetchGateCoversLocked(url string) bool { _ = url; return false }

func (g *guard) docIdentityNavFetchArmedLocked(target string) bool { _ = target; return false }

func (g *guard) docIdentityStartedNetworkAllowLocked(target, url string) bool {
	_, _ = target, url
	return false
}

func (g *guard) dropDocIdentityHoldLocked(k callKey) {}

func (g *guard) noteDocIdentityStartedNavigatingLocked(target, url string) { _, _ = target, url }

func (g *guard) noteDocIdentityPlaceLocked(target, url string) { _, _ = target, url }

func (g *guard) recordDocIdentityChromeErrorNavLocked(nt *navTarget) { _ = nt }

func (g *guard) docIdentityLiveChromeErrorNavLocked(target string) bool { _ = target; return false }

func (g *guard) docIdentityLiveChromeErrorMatchLocked(target, infoURL string) bool {
	_, _ = target, infoURL
	return false
}

func (g *guard) docIdentityTargetURLIsLiveChromeErrorDestLocked(target, url string) bool {
	_, _ = target, url
	return false
}

func (g *guard) noteDocIdentityAdmitLocked(c *command) { _ = c }

func (g *guard) dropDocIdentityCallLocked(k callKey) {}

func (g *guard) holdDocIdentityReplyLocked(k callKey, m *message, data []byte) (send, page [][]byte, held bool) {
	return nil, nil, false
}

func (g *guard) pruneDocIdentitySessionLocked(session string) {}

func (g *guard) pruneDocIdentityTargetLocked(target string) {}

func (g *guard) releaseDocIdentityReservationLocked(h *docIdentityHold) {}

func (g *guard) expireDocIdentityHold(k callKey) {}
