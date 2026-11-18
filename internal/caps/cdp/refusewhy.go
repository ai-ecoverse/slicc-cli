package cdp

func (g *guard) refuseWhyLocked(c *command, target, frame, why string) string {
	if why == "" {
		why = "unspecified"
	}
	class := ""
	probe := ""
	if frame != "" {
		class = g.frameLatchClassLocked(frame, target)
	} else if target != "" {
		class = g.framesProtectedWhyLocked(target)
	}
	g.logf("cdp: refused %s session=%s target=%s frame=%s why=%s class=%s%s", c.method, c.session, target, frame, why, class, probe)
	return refusedTarget
}

func (g *guard) sessionRefusedWhyLocked(id string) string {
	if id == "" {
		return ""
	}
	if id == g.relaySession {
		return "relaySession"
	}
	if g.hidden[id] {
		return "hidden-session"
	}
	if g.refused[id] {
		return "refused-session"
	}
	if g.held[id] != nil {
		return "held-session"
	}
	if g.navSessions[id] != nil {
		return "navSessions"
	}
	if s, ok := g.sessions[id]; ok {
		if s.refused {
			return "session.refused"
		}
		if g.targetProtectedLocked(s.target) {
			return "targetProtected:" + s.target
		}
		if g.iframeFollowRefusedLocked(id) {
			return "iframeFollowRefused"
		}
	}
	return ""
}

func (g *guard) framesProtectedWhyLocked(target string) string {
	if g.iframeProtectedLocked(target) {
		return "iframeProtected"
	}
	w := g.worlds[target]
	if w == nil {
		return ""
	}
	if w.full {
		return "worlds-full"
	}
	for id, context := range w.contexts {
		if context.protectedFrame == "" || g.extensionFrameIDLocked(context.protectedFrame) {
			continue
		}
		return "protectedFrame=" + context.protectedFrame + " ctx=" + itoa(id) + " name=" + context.name
	}
	return ""
}
