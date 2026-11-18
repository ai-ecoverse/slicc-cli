package cdp

import (
	"strconv"
	"strings"
	"time"
)

type cmdResume struct {
	who     string
	id      int64
	at      time.Time
	replied bool
	ok      bool
}

func (g *guard) cmdTraceEpochLocked() int64 {
	if g.cmdTraceT0.IsZero() {
		g.cmdTraceT0 = time.Now()
	}
	return time.Since(g.cmdTraceT0).Milliseconds()
}

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
	_ = g.cmdTraceEpochLocked()
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
		" who=client" +
		" role=" + g.cmdRoleLocked(c.session) +
		" " + g.cmdNavStateLocked(c.session)
	if len(extra) > 0 {
		msg += " " + strings.Join(extra, " ")
	}
	g.logf("%s", msg)
}

func (g *guard) cmdTraceSendLocked(session, method string, id int64, who string, target string, extra ...string) {
	if g.logf == nil {
		return
	}
	msg := "cdp: cmdtrace action=send" +
		" elapsedMs=" + strconv.FormatInt(g.cmdTraceEpochLocked(), 10) +
		" method=" + method +
		" id=" + strconv.FormatInt(id, 10) +
		" session=" + session +
		" who=" + who +
		" target=" + target +
		" role=" + g.cmdRoleLocked(session) +
		" " + g.cmdNavStateLocked(session)
	if len(extra) > 0 {
		msg += " " + strings.Join(extra, " ")
	}
	g.logf("%s", msg)
}

func (g *guard) cmdTraceGuardCallLocked(sessionID, method string, id int64, params any) {
	extra := []string{}
	target := g.sessionTargetIDLocked(sessionID)
	p, _ := params.(map[string]any)
	switch method {
	case "Target.attachToTarget":
		if tid, _ := p["targetId"].(string); tid != "" {
			target = tid
		}
		if flat, ok := p["flatten"].(bool); ok {
			extra = append(extra, "flatten="+strconv.FormatBool(flat))
		}
	case "Target.setAutoAttach":
		if v, ok := p["autoAttach"].(bool); ok {
			extra = append(extra, "autoAttach="+strconv.FormatBool(v))
		}
		if v, ok := p["waitForDebuggerOnStart"].(bool); ok {
			extra = append(extra, "waitForDebuggerOnStart="+strconv.FormatBool(v))
		}
		if v, ok := p["flatten"].(bool); ok {
			extra = append(extra, "flatten="+strconv.FormatBool(v))
		}
	case "Runtime.runIfWaitingForDebugger":
		extra = append(extra, "resume=true")
		g.noteResumeLocked(sessionID, "guard", id)
	}
	g.cmdTraceSendLocked(sessionID, method, id, "guard", target, extra...)
}

func (g *guard) noteResumeLocked(session, who string, id int64) {
	if session == "" {
		return
	}
	if g.cmdResumes == nil {
		g.cmdResumes = map[string]cmdResume{}
	}
	g.cmdResumes[session] = cmdResume{who: who, id: id, at: time.Now()}
}

func (g *guard) noteResumeReplyLocked(session string, id int64, ok bool) {
	r, have := g.cmdResumes[session]
	if !have || r.id != id {
		return
	}
	r.replied = true
	r.ok = ok
	g.cmdResumes[session] = r
}

func (g *guard) cmdTraceForwardLocked(c *command) {
	extra := []string{"startsUnheld=" + strconv.FormatBool(g.startsUnheldLocked(c))}
	switch c.method {
	case "Runtime.runIfWaitingForDebugger":
		g.noteResumeLocked(c.session, "client", c.id)
		extra = append(extra, "resume=true")
		extra = append(extra, g.resumeStateLocked(c.session)...)
	case "Target.setAutoAttach", "Page.enable":
		extra = append(extra, g.resumeStateLocked(c.session)...)
	}
	g.cmdTraceLocked(c, "forward", extra...)
}

func (g *guard) resumeStateLocked(session string) []string {
	s := g.sessions[session]
	waiting := s != nil && s.waiting
	h := g.held[session]
	guardOwns := waiting || (h != nil && h.waiting && !h.resumed)
	out := []string{
		"sessionWaiting=" + strconv.FormatBool(waiting),
		"guardOwnsResume=" + strconv.FormatBool(guardOwns),
	}
	r, have := g.cmdResumes[session]
	if !have {
		return append(out, "priorResume=none")
	}
	age := time.Since(r.at).Milliseconds()
	out = append(out,
		"priorResume="+r.who,
		"priorResumeId="+strconv.FormatInt(r.id, 10),
		"priorResumeAgeMs="+strconv.FormatInt(age, 10),
		"priorResumeReplied="+strconv.FormatBool(r.replied),
		"priorResumeOk="+strconv.FormatBool(r.ok),
	)
	return out
}

func (g *guard) cmdTraceStateLocked(kind, session, target string, extra ...string) {
	if g.logf == nil {
		return
	}
	msg := "cdp: cmdtrace action=state kind=" + kind +
		" elapsedMs=" + strconv.FormatInt(g.cmdTraceEpochLocked(), 10) +
		" session=" + session + " target=" + target
	if len(extra) > 0 {
		msg += " " + strings.Join(extra, " ")
	}
	if session != "" {
		msg += " role=" + g.cmdRoleLocked(session) + " " + g.cmdNavStateLocked(session)
	}
	g.logf("%s", msg)
}

func (g *guard) sessionTargetIDLocked(session string) string {
	if s := g.sessions[session]; s != nil {
		return s.target
	}
	if h := g.held[session]; h != nil {
		return h.target
	}
	if t := g.hiddenOwner[session]; t != "" {
		return t
	}
	return ""
}
