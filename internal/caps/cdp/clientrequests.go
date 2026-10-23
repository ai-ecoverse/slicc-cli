package cdp

import (
	"encoding/json"
	"strings"
)

const refusedClientHop = "slicc can't classify every redirect of this client-issued request before sending it"
const refusedLegacyMutation = "Network.continueInterceptedRequest with url or rawResponse follows redirects without hop control; use Fetch instead"

type clientMark struct{ key requestKey }
type clientRequestState struct {
	pending   int
	committed bool
}

func clientMutation(c *command) bool {
	switch c.method {
	case "Fetch.continueRequest":
		_, ok := c.params["url"]
		return ok
	case "Fetch.continueWithAuth":
		return true
	case "Fetch.fulfillRequest", "Fetch.continueResponse":
		code, _ := c.params["responseCode"].(json.Number)
		n, _ := code.Int64()
		if n >= 300 && n < 400 || c.params["binaryResponseHeaders"] != nil {
			return true
		}
		headers, _ := c.params["responseHeaders"].([]any)
		for _, h := range headers {
			header, _ := h.(map[string]any)
			name, _ := header["name"].(string)
			if strings.EqualFold(name, "Location") {
				return true
			}
		}
		return false
	}
	return false
}

func (g *guard) clientRequestRuleLocked(c *command, id string) string {
	if !clientMutation(c) {
		return ""
	}
	s := g.sessions[c.session]
	if s == nil {
		return refusedClientHop
	}
	nt := g.navTargets[s.target]
	key := g.pauseLinks[requestKey{c.session, id}]
	if nt == nil || nt.worker || nt.failed || !nt.done || key.request == "" || key.session != s.target {
		return refusedClientHop
	}
	if g.clientRequests[key] == nil && len(g.clientRequests) >= maxMarked {
		return refusedClientHop
	}
	return ""
}

func (g *guard) linkPauseLocked(session, id, network string) {
	s := g.sessions[session]
	if s == nil || id == "" || network == "" {
		return
	}
	key := requestKey{session, id}
	if _, ok := g.pauseLinks[key]; !ok && len(g.pauseLinks) >= maxMarked {
		return
	}
	g.pauseLinks[key] = requestKey{s.target, network}
}

func (g *guard) noteClientRequestLocked(k callKey, c *command) {
	if !clientMutation(c) {
		return
	}
	key := g.pauseLinks[requestKey{c.session, c.str("requestId")}]
	if key.request == "" {
		return
	}
	state := g.clientRequests[key]
	if state == nil {
		state = &clientRequestState{}
		g.clientRequests[key] = state
	}
	state.pending++
	g.clientCalls[k] = clientMark{key}
}

func (g *guard) clientRequestAnsweredLocked(k callKey, failed bool) {
	mark, ok := g.clientCalls[k]
	if !ok {
		return
	}
	delete(g.clientCalls, k)
	state := g.clientRequests[mark.key]
	if state == nil {
		return
	}
	state.pending--
	state.committed = state.committed || !failed
	if state.pending == 0 && !state.committed {
		delete(g.clientRequests, mark.key)
	}
}

func (g *guard) clientOwnedLocked(target, network string) bool {
	return network != "" && g.clientRequests[requestKey{target, network}] != nil
}

func (g *guard) forgetClientTargetLocked(target string) {
	for k := range g.clientRequests {
		if k.session == target {
			delete(g.clientRequests, k)
		}
	}
	for k, v := range g.pauseLinks {
		if v.session == target {
			delete(g.pauseLinks, k)
		}
	}
}

func (g *guard) legacyMutationRule(c *command) string {
	if _, ok := c.params["url"]; ok {
		return refusedLegacyMutation
	}
	if _, ok := c.params["rawResponse"]; ok {
		return refusedLegacyMutation
	}
	return ""
}
