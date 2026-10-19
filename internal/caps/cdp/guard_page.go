package cdp

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

type command struct {
	id      int64
	method  string
	session string
	params  map[string]any
}

func (c *command) bytes() []byte {
	m := map[string]any{"id": c.id, "method": c.method}
	if c.session != "" {
		m["sessionId"] = c.session
	}
	if c.params != nil {
		m["params"] = c.params
	}
	return encode(m)
}

func (c *command) str(key string) string {
	s, _ := c.params[key].(string)
	return s
}

type pageVerdict struct {
	forward []byte
	reply   []byte
	history *command
	cmd     *command
}

func decodeObject(raw json.RawMessage) (map[string]any, bool) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil || d.More() {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

func parseCommand(data []byte) (*command, int64, bool) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, -1, false
	}
	var id json.Number
	if raw := top["id"]; len(raw) == 0 || raw[0] == '"' || json.Unmarshal(raw, &id) != nil {
		return nil, -1, false
	}
	n, err := id.Int64()
	if err != nil {
		return nil, -1, false
	}
	c := &command{id: n}
	if json.Unmarshal(top["method"], &c.method) != nil || c.method == "" {
		return nil, n, false
	}
	if raw, ok := top["sessionId"]; ok && json.Unmarshal(raw, &c.session) != nil {
		return nil, n, false
	}
	if raw, ok := top["params"]; ok && string(raw) != "null" {
		p, ok := decodeObject(raw)
		if !ok {
			return nil, n, false
		}
		c.params = p
	}
	return c, n, true
}

func (g *guard) fromCommand(c *command, id int64, ok bool) pageVerdict {
	if !ok {
		if id < 0 {
			return pageVerdict{}
		}
		return pageVerdict{reply: refusal(id, "", "not a CDP command")}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if reason := g.checkLocked(c); reason != "" {
		g.logf("cdp: refused %s: %s", c.method, reason)
		return pageVerdict{reply: refusal(c.id, c.session, reason)}
	}
	if c.method == "Page.navigateToHistoryEntry" {
		return pageVerdict{history: c}
	}
	return g.admitLocked(c)
}

func (g *guard) admitLocked(c *command) pageVerdict {
	k := callKey{c.session, c.id}
	g.pending[k] = resultKind(c.method)
	g.noteBodyCallLocked(k, c)
	g.noteContinueLocked(k, c)
	if t := c.str("targetId"); c.method == "Target.attachToTarget" && t != "" {
		g.attachCalls[k] = t
		g.explicit[t]++
	}
	return pageVerdict{forward: c.bytes(), cmd: c}
}

func (g *guard) afterHistory(c *command, res message, ok bool) pageVerdict {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !ok {
		return pageVerdict{reply: refusal(c.id, c.session, "can't tell where the history entry leads")}
	}
	var h struct {
		Entries []struct {
			ID           json.Number `json:"id"`
			URL          string      `json:"url"`
			UserTypedURL string      `json:"userTypedURL"`
		} `json:"entries"`
	}
	_ = json.Unmarshal(res.Result, &h)
	want, _ := c.params["entryId"].(json.Number)
	for _, e := range h.Entries {
		if e.ID.String() == want.String() && (g.o.URL(e.URL) || g.o.URL(e.UserTypedURL)) {
			return pageVerdict{reply: refusal(c.id, c.session, refusedTarget)}
		}
	}
	if g.sessionRefusedLocked(c.session) {
		return pageVerdict{reply: refusal(c.id, c.session, refusedTarget)}
	}
	return g.admitLocked(c)
}

func resultKind(method string) callKind {
	switch method {
	case "Target.getTargets":
		return kindTargets
	case "Target.getTargetInfo":
		return kindTargetInfo
	case "Network.getCookies", "Network.getAllCookies", "Storage.getCookies", "Page.getCookies":
		return kindCookies
	case "Target.attachToBrowserTarget":
		return kindBrowserSession
	case "Target.createBrowserContext":
		return kindContext
	case "Target.createTarget":
		return kindCreate
	case "Target.attachToTarget":
		return kindAttach
	case "Page.printToPDF", "Network.loadNetworkResource", "IO.resolveBlob":
		return kindStreams
	}
	return kindPlain
}

func (g *guard) sessionRefusedLocked(id string) bool {
	if id == "" {
		return false
	}
	if id == g.relaySession || g.hidden[id] || g.refused[id] || g.held[id] != nil || g.navSessions[id] != nil {
		return true
	}
	if s, ok := g.sessions[id]; ok {
		return s.refused || g.targetProtectedLocked(s.target)
	}
	return false
}

func (g *guard) checkLocked(c *command) string {
	if g.lost {
		return refusedLost
	}
	if reason := g.unknownCheckLocked(c.session); reason != "" {
		return reason
	}
	if g.sessionRefusedLocked(c.session) {
		return refusedTarget
	}
	if reason := g.navCheckLocked(c.session); reason != "" {
		return reason
	}
	k := callKey{c.session, c.id}
	if _, busy := g.pending[k]; busy {
		return "id " + itoa(c.id) + " is in use"
	}
	if _, busy := g.relayCalls[k]; busy {
		return "id " + itoa(c.id) + " is in use"
	}
	if rule, ok := methodRules[c.method]; ok {
		if reason := rule(g, c); reason != "" {
			return reason
		}
	}
	if reason := g.worldCheckLocked(c); reason != "" {
		return reason
	}
	return g.scan(c.params, c, exemptKeys[c.method], 0)
}

var exemptKeys = map[string]map[string]bool{
	"Network.getCookies":      {"urls": true},
	"Page.getCookies":         {"urls": true},
	"Target.detachFromTarget": {"targetId": true},
	"Target.getTargetInfo":    {"targetId": true},
}

var methodRules = map[string]func(g *guard, c *command) string{
	"Target.exposeDevToolsProtocol": func(*guard, *command) string {
		return "Target.exposeDevToolsProtocol gives a page the whole browser, seven included"
	},
	"Target.sendMessageToTarget": func(*guard, *command) string {
		return "Target.sendMessageToTarget can't be checked; attach with flatten: true"
	},
	"Tracing.start": func(*guard, *command) string {
		return "Tracing.start records every tab, seven's too"
	},
	"Extensions.loadUnpacked": func(*guard, *command) string {
		return "Extensions.loadUnpacked could load an extension into seven's origin"
	},
	"Target.getTargetInfo": func(g *guard, c *command) string {
		if g.hiddenTargetLocked(c.str("targetId")) {
			return refusedTarget
		}
		return ""
	},
	"Fetch.enable":                (*guard).fetchRule,
	"IO.read":                     (*guard).streamRule,
	"Network.replayXHR":           (*guard).replayRule,
	"Network.clearBrowserCookies": (*guard).clearCookiesRule,
	"Storage.clearCookies":        contextRule(refusedCookies),
	"Browser.resetPermissions":    contextRule("Browser.resetPermissions resets seven's permissions too"),
	"Browser.setPermission":       (*guard).allOriginsRule,
	"Browser.grantPermissions":    (*guard).allOriginsRule,
	"Network.setCookie":           (*guard).cookieRule,
	"Network.deleteCookies":       (*guard).cookieRule,
	"Page.deleteCookie":           (*guard).cookieRule,
	"Network.setCookies":          (*guard).cookiesRule,
	"Storage.setCookies":          (*guard).cookiesRule,
}

func (g *guard) fetchRule(c *command) string {
	if c.session == "" || g.browserSessions[c.session] {
		return "Fetch.enable on the browser intercepts seven's requests too"
	}
	return ""
}

func (g *guard) clearCookiesRule(c *command) string {
	if t := g.sessionTargetLocked(c.session); t != nil && t.ctx != "" && g.ownContexts[t.ctx] {
		return ""
	}
	return refusedCookies
}

func contextRule(reason string) func(g *guard, c *command) string {
	return func(g *guard, c *command) string {
		if ctx := c.str("browserContextId"); ctx != "" && g.ownContexts[ctx] {
			return ""
		}
		return reason
	}
}

func (g *guard) allOriginsRule(c *command) string {
	if _, ok := c.params["origin"]; ok {
		return ""
	}
	if ctx := c.str("browserContextId"); ctx != "" && g.ownContexts[ctx] {
		return ""
	}
	return c.method + " without an origin reaches seven's origin too"
}

func (g *guard) cookieRule(c *command) string {
	if g.cookieParamProtectedLocked(c.params) {
		return refusedCookies
	}
	return ""
}

func (g *guard) cookiesRule(c *command) string {
	list, _ := c.params["cookies"].([]any)
	for _, item := range list {
		if m, ok := item.(map[string]any); ok && g.cookieParamProtectedLocked(m) {
			return refusedCookies
		}
	}
	return ""
}

func (g *guard) cookieParamProtectedLocked(p map[string]any) bool {
	if g.cookieProtected(p) {
		return true
	}
	if d, _ := p["domain"].(string); d != "" {
		return false
	}
	raw, _ := p["url"].(string)
	if raw == "" {
		raw, _ = p["cookieUrl"].(string)
	}
	if raw == "" {
		return false
	}
	return g.o.URL(raw) || g.o.CookieDomain(protect.Host(raw))
}

var (
	urlKeys = map[string]bool{
		"url": true, "origin": true, "securityOrigin": true, "ownerOrigin": true, "issuerOrigin": true,
		"topLevelOrigin": true, "topLevelSite": true, "embeddedOrigin": true, "requestingOrigin": true,
		"scopeURL": true, "manifestId": true, "installUrlOrOrigin": true,
	}
	idKeys   = map[string]bool{"targetId": true, "frameId": true, "openerId": true}
	skipKeys = map[string]bool{
		"arguments": true, "expression": true, "functionDeclaration": true, "value": true, "headers": true,
		"postData": true, "html": true, "source": true, "text": true, "data": true, "body": true,
	}
	requestKeys = map[string]bool{"requestId": true, "interceptionId": true}
	continueOK  = map[string]bool{
		"Fetch.continueRequest": true, "Fetch.continueResponse": true, "Fetch.failRequest": true,
		"Fetch.fulfillRequest": true, "Fetch.continueWithAuth": true, "Network.continueInterceptedRequest": true,
	}
)

const maxScanDepth = 24

func (g *guard) scan(v any, c *command, exempt map[string]bool, depth int) string {
	if depth > maxScanDepth {
		return ""
	}
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if exempt[k] || skipKeys[k] {
				continue
			}
			if reason := g.scanKey(k, val, c); reason != "" {
				return reason
			}
			if reason := g.scan(val, c, exempt, depth+1); reason != "" {
				return reason
			}
		}
	case []any:
		for _, val := range t {
			if reason := g.scan(val, c, exempt, depth+1); reason != "" {
				return reason
			}
		}
	}
	return ""
}

func (g *guard) scanKey(k string, val any, c *command) string {
	if k == "urls" {
		list, _ := val.([]any)
		for _, item := range list {
			if u, ok := item.(string); ok && g.o.URL(u) {
				return refusedTarget
			}
		}
		return ""
	}
	s, isString := val.(string)
	if !isString {
		return ""
	}
	return g.scanString(k, s, c)
}

func (g *guard) scanString(k, s string, c *command) string {
	switch {
	case k == "topLevelSite" && g.siteProtected(s):
		return refusedTarget
	case urlKeys[k] && g.o.URL(s):
		return refusedTarget
	case k == "storageKey" && g.o.StorageKey(s):
		return refusedTarget
	case k == "cacheId" && g.o.StorageKey(strings.SplitN(s, "|", 2)[0]):
		return refusedTarget
	case idKeys[k] && g.targetProtectedLocked(s):
		return refusedTarget
	case k == "sessionId" && s != "" && (s == g.relaySession || g.navSessions[s] != nil):
		return refusedTarget
	case k == "sessionId" && g.sessionRefusedLocked(s) && c.method != "Target.detachFromTarget":
		return refusedTarget
	case (k == "versionId" || k == "registrationId") && g.swProtected[s]:
		return refusedTarget
	case requestKeys[k]:
		return g.requestRule(s, c)
	}
	return ""
}

func (g *guard) requestRule(id string, c *command) string {
	switch {
	case continueOK[c.method] && g.handed[requestKey{c.session, id}] == 0:
		return refusedPaused
	case !continueOK[c.method] && g.requestMarkedLocked(c.session, id):
		return refusedBody
	}
	return ""
}
