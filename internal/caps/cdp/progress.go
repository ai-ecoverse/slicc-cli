package cdp

import (
	"os"
	"strings"
	"sync"
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
	return min(d, def)
}

func (b guardBounds) period() time.Duration {
	return max(min(b.idle, b.limit)/4, minWatchPeriod)
}

type readClock struct {
	mu    sync.Mutex
	total time.Duration
	since time.Time
}

func (c *readClock) left(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.since.IsZero() {
		c.since = now
	}
}

func (c *readClock) back(now time.Time) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.since.IsZero() {
		return 0
	}
	d := max(now.Sub(c.since), 0)
	c.total += d
	c.since = time.Time{}
	return d
}

func (c *readClock) away(now time.Time) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.since.IsZero() {
		return c.total
	}
	return c.total + max(now.Sub(c.since), 0)
}

func (g *guard) movedLocked(nt *navTarget, now time.Time) {
	nt.progress, nt.awayAt = now, g.reads.away(now)
}

func (b guardBounds) expired(nt *navTarget, now time.Time, away time.Duration) string {
	if now.Sub(nt.start) >= b.limit {
		return "its guard wasn't ready in " + b.limit.String()
	}
	last := nt.start
	if nt.progress.After(last) {
		last = nt.progress
	}
	if now.Sub(last)-(away-nt.awayAt) >= b.idle {
		return "its guard made no progress in " + b.idle.String()
	}
	return ""
}

func (g *guard) progressLocked(m *message) {
	if len(g.navTargets) == 0 && len(g.unknown) == 0 {
		return
	}
	now := time.Now()
	if u, ok := g.unknown[m.SessionID]; ok && m.ID == nil {
		g.movedLocked(u, now)
		return
	}
	if nt, ok := g.navSessions[m.SessionID]; ok && m.SessionID != "" {
		g.movedLocked(nt, now)
	}
}

func (g *guard) navWatch(now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	away := g.reads.away(now)
	for _, target := range g.navTargets {
		for _, nt := range []*navTarget{target, target.confirm} {
			if nt == nil || nt.done {
				continue
			}
			if reason := g.bounds.expired(nt, now, away); reason != "" {
				kind := "a target's navigation guard"
				if nt.worker {
					kind = "a worker's guard"
				}
				g.logf("cdp: %s failed: %s; refusing its commands", kind, reason)
				nt.reason = reason
				nt.settle(true)
			}
		}
	}
	g.resolveUnknownLocked()
	for _, u := range g.unknown {
		if u.done {
			continue
		}
		if reason := g.bounds.expired(u, now, away); reason != "" {
			g.logf("cdp: a session slicc doesn't know never registered: %s; refusing its commands", reason)
			u.reason = reason
			u.settle(true)
		}
	}
}

func (g *guard) watch(stop <-chan struct{}) {
	t := time.NewTicker(g.bounds.period())
	defer t.Stop()
	for {
		select {
		case now := <-t.C:
			g.navWatch(now)
		case <-stop:
			return
		}
	}
}
