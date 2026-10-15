package cdp

import (
	"strings"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func redactURL(raw string) string {
	return protect.Redact(raw)
}

func (g *guard) siteProtected(site string) bool {
	return site != "" && (g.o.URL(site) || g.o.CookieDomain(protect.Host(site)))
}

func (g *guard) cookieProtected(c map[string]any) bool {
	if c == nil {
		return false
	}
	if d, _ := c["domain"].(string); g.o.CookieDomain(d) {
		return true
	}
	switch pk := c["partitionKey"].(type) {
	case string:
		return g.siteProtected(pk)
	case map[string]any:
		site, _ := pk["topLevelSite"].(string)
		return g.siteProtected(site)
	}
	return false
}

func (g *guard) filterCookies(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	kept := make([]any, 0, len(list))
	for _, item := range list {
		if c, _ := item.(map[string]any); g.cookieProtected(c) {
			continue
		}
		kept = append(kept, item)
	}
	return kept
}

func socketURL(raw string) string {
	for from, to := range map[string]string{"ws:": "http:", "wss:": "https:"} {
		if len(raw) >= len(from) && strings.EqualFold(raw[:len(from)], from) {
			return to + raw[len(from):]
		}
	}
	return raw
}

func (g *guard) socketCreatedEvent(m *message, data []byte) browserVerdict {
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		id, _ := p["requestId"].(string)
		u, _ := p["url"].(string)
		if g.o.URL(socketURL(strings.TrimSpace(u))) {
			g.markRequestLocked(m.SessionID, id)
		}
		return true, false, nil
	})
}

func (g *guard) markedDropEvent(m *message, data []byte) browserVerdict {
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		id, _ := p["requestId"].(string)
		return !g.requestMarkedLocked(m.SessionID, id), false, nil
	})
}

func stripHeaders(v any, names ...string) bool {
	h, ok := v.(map[string]any)
	if !ok {
		return false
	}
	changed := false
	for k := range h {
		for _, n := range names {
			if strings.EqualFold(k, n) {
				delete(h, k)
				changed = true
			}
		}
	}
	return changed
}

func stripHeaderList(p map[string]any, field string, names ...string) bool {
	list, ok := p[field].([]any)
	if !ok {
		return false
	}
	kept := make([]any, 0, len(list))
	for _, item := range list {
		e, _ := item.(map[string]any)
		n, _ := e["name"].(string)
		drop := false
		for _, want := range names {
			drop = drop || strings.EqualFold(n, want)
		}
		if !drop {
			kept = append(kept, item)
		}
	}
	p[field] = kept
	return len(kept) != len(list)
}

func stripResponse(r map[string]any) bool {
	if r == nil {
		return false
	}
	changed := stripHeaders(r["headers"], "set-cookie", "set-cookie2")
	changed = stripHeaders(r["requestHeaders"], "cookie") || changed
	for _, k := range []string{"headersText", "requestHeadersText"} {
		if _, ok := r[k]; ok {
			delete(r, k)
			changed = true
		}
	}
	return changed
}

func (g *guard) requestEvent(m *message, data []byte) browserVerdict {
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		id, _ := p["requestId"].(string)
		req, _ := p["request"].(map[string]any)
		u, _ := req["url"].(string)
		redirect, _ := p["redirectResponse"].(map[string]any)
		ru, _ := redirect["url"].(string)
		if g.o.URL(u) || g.o.URL(ru) {
			g.markRequestLocked(m.SessionID, id)
		}
		if !g.requestMarkedLocked(m.SessionID, id) {
			return true, false, nil
		}
		changed := stripHeaders(req["headers"], "cookie")
		return true, stripResponse(redirect) || changed, nil
	})
}

func (g *guard) requestExtra(p map[string]any, marked bool) bool {
	changed := g.filterAssociated(p, "associatedCookies")
	if changed || marked {
		changed = stripHeaders(p["headers"], "cookie") || changed
	}
	return changed
}

func (g *guard) filterAssociated(p map[string]any, field string) bool {
	list, ok := p[field].([]any)
	if !ok {
		return false
	}
	kept := make([]any, 0, len(list))
	for _, item := range list {
		e, _ := item.(map[string]any)
		if c, _ := e["cookie"].(map[string]any); g.cookieProtected(c) {
			continue
		}
		kept = append(kept, item)
	}
	p[field] = kept
	return len(kept) != len(list)
}

func (g *guard) responseEvent(m *message, data []byte) browserVerdict {
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		id, _ := p["requestId"].(string)
		r, _ := p["response"].(map[string]any)
		u, _ := r["url"].(string)
		if g.o.URL(u) {
			g.markRequestLocked(m.SessionID, id)
		}
		if !g.requestMarkedLocked(m.SessionID, id) {
			return true, false, nil
		}
		return true, stripResponse(r), nil
	})
}

func (g *guard) responseExtra(p map[string]any, marked bool) bool {
	changed := g.filterAssociated(p, "blockedCookies")
	changed = g.filterAssociated(p, "exemptedCookies") || changed
	if !marked {
		return changed
	}
	changed = stripHeaders(p["headers"], "set-cookie", "set-cookie2") || changed
	for _, k := range []string{"headersText", "blockedCookies", "exemptedCookies"} {
		if _, ok := p[k]; ok {
			delete(p, k)
			changed = true
		}
	}
	return changed
}

func pausedIDs(method string, p map[string]any) (string, string) {
	rid, _ := p["requestId"].(string)
	if method == "Network.requestIntercepted" {
		iid, _ := p["interceptionId"].(string)
		return iid, rid
	}
	nid, _ := p["networkId"].(string)
	return rid, nid
}

func (g *guard) pausedEvent(m *message, data []byte) browserVerdict {
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		id, other := pausedIDs(m.Method, p)
		req, _ := p["request"].(map[string]any)
		u, _ := req["url"].(string)
		if g.o.URL(u) {
			delete(g.handed, requestKey{m.SessionID, id})
			g.markRequestLocked(m.SessionID, id)
			g.markRequestLocked(m.SessionID, other)
			return false, false, [][]byte{g.settlePausedLocked(m, p, id)}
		}
		g.handOutLocked(requestKey{m.SessionID, id})
		if g.requestMarkedLocked(m.SessionID, other) {
			g.markRequestLocked(m.SessionID, id)
		}
		if !g.requestMarkedLocked(m.SessionID, id) {
			return true, false, nil
		}
		changed := stripHeaderList(p, "responseHeaders", "set-cookie", "set-cookie2")
		changed = stripHeaders(p["responseHeaders"], "set-cookie", "set-cookie2") || changed
		changed = stripHeaders(req["headers"], "cookie") || changed
		return true, changed, nil
	})
}

func (g *guard) noteContinueLocked(k callKey, c *command) {
	if !continueOK[c.method] {
		return
	}
	for key := range requestKeys {
		if id := c.str(key); id != "" {
			req := requestKey{c.session, id}
			g.continues[k] = handedPause{req, g.handed[req]}
		}
	}
}

func (g *guard) continuedLocked(k callKey, failed bool) {
	h, ok := g.continues[k]
	if !ok {
		return
	}
	delete(g.continues, k)
	if !failed && g.handed[h.req] == h.pause {
		delete(g.handed, h.req)
	}
}

func (g *guard) settlePausedLocked(m *message, p map[string]any, id string) []byte {
	defaultAuth := map[string]any{"response": "Default"}
	document := p["resourceType"] == "Document"
	var method string
	params := map[string]any{"requestId": id}
	switch m.Method {
	case "Network.requestIntercepted":
		g.logf("cdp: settled an intercepted request into a protected origin without showing it to the page")
		method = "Network.continueInterceptedRequest"
		params = map[string]any{"interceptionId": id}
		if _, ok := p["authChallenge"]; ok {
			params["authChallengeResponse"] = defaultAuth
		} else if document {
			params["errorReason"] = "BlockedByClient"
		}
	case "Fetch.authRequired":
		g.logf("cdp: answered an auth challenge from a protected origin without showing it to the page")
		method = "Fetch.continueWithAuth"
		params["authChallengeResponse"] = defaultAuth
	default:
		if document {
			g.logf("cdp: failed a document request into a protected origin that a page session paused")
			method = "Fetch.failRequest"
			params["errorReason"] = "BlockedByClient"
		} else {
			g.logf("cdp: continued a request into a protected origin without showing it to the page")
			method = "Fetch.continueRequest"
		}
	}
	msg, _ := g.newCallLocked(m.SessionID, method, params, false)
	return msg
}

type handedPause struct {
	req   requestKey
	pause int
}

func (g *guard) handOutLocked(k requestKey) {
	if k.request == "" {
		return
	}
	g.pauses++
	_, known := g.handed[k]
	g.handed[k] = g.pauses
	if known {
		return
	}
	g.handedOrder = append(g.handedOrder, k)
	for len(g.handedOrder) > maxSettled {
		delete(g.handed, g.handedOrder[0])
		g.handedOrder = g.handedOrder[1:]
	}
}
