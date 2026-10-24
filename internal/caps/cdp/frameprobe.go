package cdp

import "encoding/json"

type ownedFrameTree struct {
	Frame struct {
		ID string `json:"id"`
	} `json:"frame"`
	Children []ownedFrameTree `json:"childFrames"`
}

func (g *guard) probeFrameParentsLocked() [][]byte {
	if g.relaySession == "" {
		return nil
	}
	contexts := map[string]bool{}
	for _, t := range g.targets {
		if !t.gone && t.typ == "iframe" && (t.protected || g.o.Loaded(t.url) || g.framesProtectedLocked(t.id)) && g.iframePageLocked(t) == nil {
			contexts[t.ctx] = true
		}
	}
	var send [][]byte
	for _, t := range g.targets {
		if t.gone || t.typ != "page" || !contexts[t.ctx] || g.frameProbed[t.id] {
			continue
		}
		g.frameProbed[t.id] = true
		g.frameProbeAttempts[t.id]++
		send = append(send, g.callbackLocked(g.relaySession, "Target.attachToTarget", map[string]any{"targetId": t.id, "flatten": true}, func(m message) [][]byte {
			var result struct {
				SessionID string `json:"sessionId"`
			}
			if m.Error != nil || json.Unmarshal(m.Result, &result) != nil || result.SessionID == "" {
				g.frameProbeFailedLocked(t, "Target.attachToTarget")
				return nil
			}
			url := t.url
			return [][]byte{g.callbackLocked(result.SessionID, "Page.getFrameTree", nil, func(m message) [][]byte {
				g.probedFrameTreeLocked(t, url, m)
				msg, _ := g.newCallLocked(g.relaySession, "Target.detachFromTarget", map[string]any{"sessionId": result.SessionID}, false)
				return [][]byte{msg}
			})}
		}))
	}
	return send
}

func (g *guard) probedFrameTreeLocked(t *target, url string, m message) {
	var result struct {
		Tree ownedFrameTree `json:"frameTree"`
	}
	if t.gone || t.url != url || m.Error != nil || json.Unmarshal(m.Result, &result) != nil || result.Tree.Frame.ID != t.id {
		g.frameProbeFailedLocked(t, "Page.getFrameTree")
		return
	}
	frames := map[string]bool{}
	queue := []ownedFrameTree{result.Tree}
	for count := 0; len(queue) > 0 && count < g.maxWorlds; count++ {
		frame := queue[0]
		queue = queue[1:]
		if frame.Frame.ID != "" {
			frames[frame.Frame.ID] = true
		}
		queue = append(queue, frame.Children...)
	}
	g.frames[t.id] = frames
}

func (g *guard) frameProbeFailedLocked(t *target, method string) {
	g.logf("cdp: %s did not resolve a frame parent; keeping frame reads locked", method)
	if g.frameProbeAttempts[t.id] < 2 {
		delete(g.frameProbed, t.id)
	}
}
