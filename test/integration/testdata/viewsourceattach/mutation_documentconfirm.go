package cdp

import "time"

type docConfirm struct {
	target   string
	holds    map[string]*docHold
	ready    chan struct{}
	start    time.Time
	deadline time.Time
	done     bool
	failed   bool
}

type docHold struct {
	parent   string
	typ      string
	waiting  bool
	event    []byte
	reply    []byte
	replyID  int64
	replySid string
	hasEvent bool
	hasReply bool
}

func (g *guard) docConfirmWaitLocked(session string) <-chan struct{} {
	return nil
}

func (g *guard) pruneDocConfirmSessionLocked(session string) {}

func (g *guard) pruneDocConfirmTargetLocked(target string) {}

func (g *guard) expireDocConfirm(target string) {}

func (g *guard) failDocConfirmLocked(dc *docConfirm) [][]byte {
	return nil
}

func (g *guard) docConfirmAttachEventLocked(m *message, data []byte, session string, waiting bool, info targetInfo, send [][]byte) (browserVerdict, bool) {
	return browserVerdict{}, false
}

func (g *guard) holdDocConfirmReplyLocked(attached string, m *message, res map[string]any) (held bool, send [][]byte, page [][]byte) {
	return false, nil, nil
}
