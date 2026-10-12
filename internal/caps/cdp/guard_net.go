package cdp

import (
	"strings"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func redactURL(raw string) string {
	return protect.Redact(raw)
}

func (g *guard) filterCookies(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	kept := make([]any, 0, len(list))
	for _, item := range list {
		c, _ := item.(map[string]any)
		d, _ := c["domain"].(string)
		if c != nil && g.o.CookieDomain(d) {
			continue
		}
		kept = append(kept, item)
	}
	return kept
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

func (g *guard) requestExtraEvent(m *message, data []byte) browserVerdict {
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		id, _ := p["requestId"].(string)
		changed := g.filterAssociated(p, "associatedCookies")
		if changed || g.requestMarkedLocked(m.SessionID, id) {
			changed = stripHeaders(p["headers"], "cookie") || changed
		}
		return true, changed, nil
	})
}

func (g *guard) filterAssociated(p map[string]any, field string) bool {
	list, ok := p[field].([]any)
	if !ok {
		return false
	}
	kept := make([]any, 0, len(list))
	for _, item := range list {
		e, _ := item.(map[string]any)
		c, _ := e["cookie"].(map[string]any)
		d, _ := c["domain"].(string)
		if c != nil && g.o.CookieDomain(d) {
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

func (g *guard) responseExtraEvent(m *message, data []byte) browserVerdict {
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		id, _ := p["requestId"].(string)
		changed := g.filterAssociated(p, "blockedCookies")
		changed = g.filterAssociated(p, "exemptedCookies") || changed
		if !g.requestMarkedLocked(m.SessionID, id) {
			return true, changed, nil
		}
		changed = stripHeaders(p["headers"], "set-cookie", "set-cookie2") || changed
		for _, k := range []string{"headersText", "blockedCookies", "exemptedCookies"} {
			if _, ok := p[k]; ok {
				delete(p, k)
				changed = true
			}
		}
		return true, changed, nil
	})
}

func (g *guard) pausedEvent(m *message, data []byte) browserVerdict {
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		id, _ := p["requestId"].(string)
		if id == "" {
			id, _ = p["interceptionId"].(string)
		}
		req, _ := p["request"].(map[string]any)
		u, _ := req["url"].(string)
		if g.o.URL(u) {
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
