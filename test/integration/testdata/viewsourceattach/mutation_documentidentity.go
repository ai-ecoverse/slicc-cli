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

func (a docIdentity) equal(b docIdentity) bool {
	return a.frameID == b.frameID && a.loaderID == b.loaderID && a.url == b.url && a.full == b.full
}

type docIdentityAdmit struct {
	baseline docIdentity
	known    bool
	taint    uint64
}

type docIdentityHold struct {
	baseline        docIdentity
	known           bool
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
	treeDone   bool
	infoID     docIdentity
	askingInfo bool
	askingTree bool
}

func (g *guard) noteOrdinaryDocIdentityFromTargetLocked(t *target) { _ = t }

func (g *guard) noteOrdinaryDocIdentityFromNavLocked(nt *navTarget) { _ = nt }

func (g *guard) noteDocIdentityWatchURLLocked(target, url string) { _, _ = target, url }

func (g *guard) noteDocIdentityPlaceLocked(target, url string) { _, _ = target, url }

func (g *guard) noteDocIdentityChromeErrorCommitLocked(target, url string) { _, _ = target, url }

func (g *guard) noteDocIdentityAdmitLocked(c *command) { _ = c }

func (g *guard) dropDocIdentityCallLocked(k callKey) {}

func (g *guard) holdDocIdentityReplyLocked(k callKey, m *message, data []byte) (send, page [][]byte, held bool) {
	return nil, nil, false
}

func (g *guard) pruneDocIdentitySessionLocked(session string) {}

func (g *guard) pruneDocIdentityTargetLocked(target string) {}

func (g *guard) releaseDocIdentityReservationLocked(h *docIdentityHold) {}

func (g *guard) expireDocIdentityHold(k callKey) {}
