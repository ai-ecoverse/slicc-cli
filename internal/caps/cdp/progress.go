package cdp

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

const (
	GuardIdle      = 30 * time.Second
	GuardLimit     = 5 * time.Minute
	GuardIdleEnv   = "SLICC_CDP_GUARD_IDLE"
	GuardLimitEnv  = "SLICC_CDP_GUARD_LIMIT"
	minWatchPeriod = 50 * time.Millisecond
)

type guardBounds struct {
	idle  time.Duration
	limit time.Duration
}

func boundsFromEnv() guardBounds {
	return guardBounds{idle: envDuration(GuardIdleEnv, GuardIdle), limit: envDuration(GuardLimitEnv, GuardLimit)}
}

func envDuration(name string, def time.Duration) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(os.Getenv(name)))
	if err != nil || d <= 0 {
		return def
	}
	return d
}

func (b guardBounds) period() time.Duration {
	return max(min(b.idle, b.limit)/4, minWatchPeriod)
}

func (b guardBounds) expired(nt *navTarget, answered, now time.Time) string {
	if now.Sub(nt.start) >= b.limit {
		return "its guard wasn't ready in " + b.limit.String()
	}
	last := nt.start
	if nt.progress.After(last) {
		last = nt.progress
	}
	if answered.After(last) {
		last = answered
	}
	if now.Sub(last) >= b.idle {
		return "its guard made no progress in " + b.idle.String()
	}
	return ""
}

func (g *guard) progressLocked(m *message) {
	if len(g.navTargets) == 0 {
		return
	}
	now := time.Now()
	if m.ID != nil {
		g.answered = now
		return
	}
	if nt, ok := g.navSessions[m.SessionID]; ok && m.SessionID != "" {
		nt.progress = now
		return
	}
	if s, ok := g.sessions[m.SessionID]; ok {
		g.targetProgressLocked(s.target, now)
		return
	}
	if h, ok := g.held[m.SessionID]; ok {
		g.targetProgressLocked(h.target, now)
		return
	}
	if (m.SessionID == "" || m.SessionID == g.relaySession) && strings.HasPrefix(m.Method, "Target.") {
		var p struct {
			TargetInfo targetInfo `json:"targetInfo"`
			TargetID   string     `json:"targetId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		g.targetProgressLocked(p.TargetID, now)
		g.targetProgressLocked(p.TargetInfo.TargetID, now)
	}
}

func (g *guard) targetProgressLocked(targetID string, now time.Time) {
	if nt, ok := g.navTargets[targetID]; ok {
		nt.progress = now
	}
}

func (g *guard) navWatch(now time.Time) [][]byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	pending := false
	for _, nt := range g.navTargets {
		if nt.done {
			continue
		}
		if reason := g.bounds.expired(nt, g.answered, now); reason != "" {
			g.logf("cdp: a target's navigation guard failed: %s; refusing its commands", reason)
			nt.reason = reason
			nt.settle(true)
			continue
		}
		pending = true
	}
	if !pending || g.probing || g.relaySession == "" {
		return nil
	}
	g.probing = true
	return [][]byte{g.callbackLocked(g.relaySession, "Browser.getVersion", nil, func(message) [][]byte {
		g.probing = false
		return nil
	})}
}

func (g *guard) watch(stop <-chan struct{}, write func([]byte)) {
	t := time.NewTicker(g.bounds.period())
	defer t.Stop()
	for {
		select {
		case now := <-t.C:
			for _, msg := range g.navWatch(now) {
				write(msg)
			}
		case <-stop:
			return
		}
	}
}
