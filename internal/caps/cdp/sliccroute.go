package cdp

import "encoding/json"

const (
	sliccRouteKey   = "slicc"
	sliccRouteLink  = "link"
	sliccRouteField = "route"
)

func sliccRouteHint() map[string]any {
	return map[string]any{sliccRouteField: sliccRouteLink}
}

func (g *guard) noteVersionCallLocked(k callKey, method string) {
	if method != "Browser.getVersion" {
		return
	}
	if g.versionCalls == nil {
		g.versionCalls = map[callKey]bool{}
	}
	g.versionCalls[k] = true
}

func (g *guard) noteVersionReplyLocked(k callKey, failed bool) {
	if failed {
		return
	}
	if g.versionReplies == nil {
		g.versionReplies = map[callKey]int{}
	}
	g.versionReplies[k]++
}

func (g *guard) forgetVersionCallsLocked(session string) {
	if session == "" {
		return
	}
	for k := range g.versionCalls {
		if k.session == session {
			delete(g.versionCalls, k)
		}
	}
	for k := range g.versionReplies {
		if k.session == session {
			delete(g.versionReplies, k)
		}
	}
}

func (g *guard) markSliccRoute(m map[string]any) bool {
	g.mu.Lock()
	versionReply := false
	if raw, ok := m["id"].(json.Number); ok {
		if id, err := raw.Int64(); err == nil {
			session, _ := m["sessionId"].(string)
			k := callKey{session, id}
			if g.versionReplies[k] > 0 {
				versionReply = true
				g.versionReplies[k]--
				if g.versionReplies[k] == 0 {
					delete(g.versionReplies, k)
				}
			}
		}
	}
	g.mu.Unlock()
	if !versionReply {
		return false
	}
	res, ok := m["result"].(map[string]any)
	if !ok {
		return false
	}
	delete(res, sliccRouteKey)
	res[sliccRouteKey] = sliccRouteHint()
	return true
}
