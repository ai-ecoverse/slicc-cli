package cdp

import (
	"strconv"
	"strings"
	"time"
)

func (g *guard) cmdArrivalLocked(c *command) time.Time {
	if c == nil {
		return time.Time{}
	}
	k := callKey{c.session, c.id}
	if g.cmdArrivals == nil {
		g.cmdArrivals = map[callKey]time.Time{}
	}
	if at, ok := g.cmdArrivals[k]; ok {
		return at
	}
	at := time.Now()
	g.cmdArrivals[k] = at
	return at
}

func (g *guard) cmdElapsedLocked(c *command) int64 {
	at := g.cmdArrivalLocked(c)
	if at.IsZero() {
		return 0
	}
	return time.Since(at).Milliseconds()
}

func (g *guard) cmdRoleLocked(session string) string {
	if session == "" {
		return "browser"
	}
	if g.held[session] != nil {
		return "held"
	}
	if g.hidden[session] {
		return "hidden"
	}
	if g.refused[session] {
		return "hidden"
	}
	s := g.sessions[session]
	if s == nil {
		return "unknown"
	}
	if s.refused || g.targetProtectedLocked(s.target) {
		return "hidden"
	}
	switch s.typ {
	case "page", "background_page", "webview":
		return "page"
	case "iframe":
		return "ordinary-iframe"
	default:
		if s.typ == "" {
			return "unknown"
		}
		return s.typ
	}
}

func (g *guard) cmdNavStateLocked(session string) string {
	parts := []string{"held=" + strconv.FormatBool(g.held[session] != nil)}
	s := g.sessions[session]
	target := ""
	if s != nil {
		target = s.target
		parts = append(parts, "waiting="+strconv.FormatBool(s.waiting))
	} else if h := g.held[session]; h != nil {
		target = h.target
		parts = append(parts, "waiting="+strconv.FormatBool(h.waiting), "resumed="+strconv.FormatBool(h.resumed))
	}
	if target == "" {
		parts = append(parts, "nav=absent", "confirm=absent", "rev=-")
		return strings.Join(parts, " ")
	}
	nt := g.navTargets[target]
	if nt == nil {
		parts = append(parts, "nav=absent", "confirm=absent", "rev=-", "target="+target)
		return strings.Join(parts, " ")
	}
	switch {
	case !nt.done:
		parts = append(parts, "nav=open")
	case nt.failed:
		parts = append(parts, "nav=failed")
	default:
		parts = append(parts, "nav=done")
	}
	switch {
	case nt.confirm == nil:
		parts = append(parts, "confirm=absent")
	case !nt.confirm.done:
		parts = append(parts, "confirm=open")
	case nt.confirm.failed:
		parts = append(parts, "confirm=failed")
	default:
		parts = append(parts, "confirm=done")
	}
	parts = append(parts, "rev="+strconv.Itoa(nt.revision), "target="+target)
	return strings.Join(parts, " ")
}

func (g *guard) cmdTraceLocked(c *command, action string, extra ...string) {
	if c == nil || g.logf == nil {
		return
	}
	msg := "cdp: cmdtrace action=" + action +
		" elapsedMs=" + strconv.FormatInt(g.cmdElapsedLocked(c), 10) +
		" method=" + c.method +
		" id=" + strconv.FormatInt(c.id, 10) +
		" session=" + c.session +
		" role=" + g.cmdRoleLocked(c.session) +
		" " + g.cmdNavStateLocked(c.session)
	if len(extra) > 0 {
		msg += " " + strings.Join(extra, " ")
	}
	g.logf("%s", msg)
}

func (g *guard) cmdTraceStateLocked(kind, session, target string, extra ...string) {
	if g.logf == nil {
		return
	}
	msg := "cdp: cmdtrace action=state kind=" + kind + " session=" + session + " target=" + target
	if len(extra) > 0 {
		msg += " " + strings.Join(extra, " ")
	}
	if session != "" {
		msg += " role=" + g.cmdRoleLocked(session) + " " + g.cmdNavStateLocked(session)
	}
	g.logf("%s", msg)
}
