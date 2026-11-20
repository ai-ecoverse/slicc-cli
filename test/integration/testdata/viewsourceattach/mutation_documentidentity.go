package cdp

import "time"

type docIdentity struct {
	frameID  string
	loaderID string
	url      string
	full     bool
}

type docIdentityAdmit struct {
	baseline docIdentity
	known    bool
}

type docIdentityHold struct {
	baseline docIdentity
	known    bool
	data     []byte
	id       int64
	session  string
	target   string
	arrived  time.Time
	deadline time.Time
	timer    *time.Timer
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

func (g *guard) noteDocIdentityAdmitLocked(c *command) { _ = c }

func (g *guard) dropDocIdentityCallLocked(k callKey) {}

func (g *guard) holdDocIdentityReplyLocked(k callKey, m *message, data []byte) (send, page [][]byte, held bool) {
	return nil, nil, false
}

func (g *guard) pruneDocIdentitySessionLocked(session string) {}

func (g *guard) pruneDocIdentityTargetLocked(target string) {}

func (g *guard) releaseDocIdentityReservationLocked(h *docIdentityHold) {}

func (g *guard) expireDocIdentityHold(k callKey) {}
