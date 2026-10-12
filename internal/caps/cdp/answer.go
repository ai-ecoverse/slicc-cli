package cdp

import (
	"encoding/json"
	"sort"
)

const (
	maxHeldAnswers = 256
	maxStreams     = 4096
	refusedBody    = "the response came from seven's origin"
	refusedUnknown = "slicc hasn't seen this request, so it could be seven's"
	refusedStream  = "slicc didn't hand out this stream, so it could be seven's"
)

type bodyMethod struct {
	key    string
	need   reqLevel
	stream bool
}

var bodyMethods = map[string]bodyMethod{
	"Network.getResponseBody":                         {"requestId", levelSettled, false},
	"Network.searchInResponseBody":                    {"requestId", levelSettled, false},
	"Network.streamResourceContent":                   {"requestId", levelSettled, false},
	"Audits.getEncodedResponse":                       {"requestId", levelSettled, false},
	"Network.getRequestPostData":                      {"requestId", levelKnown, false},
	"Network.getResponseBodyForInterception":          {"interceptionId", levelSettled, false},
	"Network.takeResponseBodyForInterceptionAsStream": {"interceptionId", levelSettled, true},
	"Fetch.getResponseBody":                           {"requestId", levelSettled, false},
	"Fetch.takeResponseBodyAsStream":                  {"requestId", levelSettled, true},
}

type bodyCall struct {
	req    requestKey
	need   reqLevel
	stream bool
}

type heldAnswer struct {
	call bodyCall
	m    message
	data []byte
}

func (g *guard) noteBodyCallLocked(k callKey, c *command) {
	b, ok := bodyMethods[c.method]
	if !ok {
		return
	}
	g.bodyCalls[k] = bodyCall{req: requestKey{c.session, c.str(b.key)}, need: b.need, stream: b.stream}
}

func (g *guard) bodyAnswerLocked(k callKey, bc bodyCall, m *message, data []byte) browserVerdict {
	if m.Error != nil {
		return browserVerdict{forward: data}
	}
	if bc.req.request == "" || g.requestMarkedLocked(bc.req.session, bc.req.request) {
		return g.refuseAnswerLocked(k, bc, m, refusedBody)
	}
	if g.seen[bc.req] >= bc.need {
		return g.allowAnswerLocked(bc, m, data)
	}
	if len(g.heldAnswers) >= maxHeldAnswers {
		g.logf("cdp: too many answers wait for their request; refusing one")
		return g.refuseAnswerLocked(k, bc, m, refusedUnknown)
	}
	g.heldAnswers[k] = &heldAnswer{call: bc, m: *m, data: data}
	return browserVerdict{answers: []callKey{k}}
}

func (g *guard) allowAnswerLocked(bc bodyCall, m *message, data []byte) browserVerdict {
	if bc.stream {
		if res, ok := decodeObject(m.Result); ok {
			g.recordStreamsLocked(res)
		}
	}
	return browserVerdict{forward: data}
}

func (g *guard) refuseAnswerLocked(k callKey, bc bodyCall, m *message, reason string) browserVerdict {
	g.logf("cdp: refused the answer to a body request: %s", reason)
	return browserVerdict{forward: refusal(k.id, k.session, reason), send: g.closeStreamLocked(bc, m)}
}

func (g *guard) closeStreamLocked(bc bodyCall, m *message) [][]byte {
	if !bc.stream || m.Error != nil {
		return nil
	}
	var res struct {
		Stream string `json:"stream"`
	}
	if json.Unmarshal(m.Result, &res) != nil || res.Stream == "" {
		return nil
	}
	msg, _ := g.newCallLocked(m.SessionID, "IO.close", map[string]any{"handle": res.Stream}, false)
	return [][]byte{msg}
}

func (g *guard) releaseAnswersLocked(req requestKey) (page, send [][]byte) {
	var keys []callKey
	for k, h := range g.heldAnswers {
		if h.call.req == req {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].id < keys[j].id })
	marked := g.requestMarkedLocked(req.session, req.request)
	for _, k := range keys {
		h := g.heldAnswers[k]
		var v browserVerdict
		switch {
		case marked:
			v = g.refuseAnswerLocked(k, h.call, &h.m, refusedBody)
		case g.seen[req] >= h.call.need:
			v = g.allowAnswerLocked(h.call, &h.m, h.data)
		default:
			continue
		}
		delete(g.heldAnswers, k)
		page = append(page, v.forward)
		send = append(send, v.send...)
	}
	return page, send
}

func (g *guard) expireAnswer(k callKey) (page, send [][]byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	h, ok := g.heldAnswers[k]
	if !ok {
		return nil, nil
	}
	delete(g.heldAnswers, k)
	v := g.refuseAnswerLocked(k, h.call, &h.m, refusedUnknown)
	if g.sessionRefusedLocked(k.session) {
		return nil, v.send
	}
	return [][]byte{v.forward}, v.send
}

func (g *guard) forgetAnswersLocked(session string) {
	for k := range g.heldAnswers {
		if k.session == session {
			delete(g.heldAnswers, k)
		}
	}
}

func (g *guard) recordStreamsLocked(res map[string]any) {
	if s, _ := res["stream"].(string); s != "" {
		g.addStreamLocked(s)
	}
	if r, _ := res["resource"].(map[string]any); r != nil {
		if s, _ := r["stream"].(string); s != "" {
			g.addStreamLocked(s)
		}
	}
	if u, _ := res["uuid"].(string); u != "" {
		g.addStreamLocked("blob:" + u)
	}
}

func (g *guard) addStreamLocked(handle string) {
	if g.streams[handle] {
		return
	}
	g.streams[handle] = true
	g.streamOrder = append(g.streamOrder, handle)
	for len(g.streamOrder) > maxStreams {
		delete(g.streams, g.streamOrder[0])
		g.streamOrder = g.streamOrder[1:]
	}
}

func (g *guard) streamRule(c *command) string {
	if g.streams[c.str("handle")] {
		return ""
	}
	return refusedStream
}
