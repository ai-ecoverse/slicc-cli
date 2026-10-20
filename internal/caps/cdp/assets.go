package cdp

import (
	"encoding/json"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	maxAssets = 65536
	maxChain  = 32
)

var assetKinds = map[string]string{"styleSheetId": "sheet:", "scriptId": "script:"}

type assetSet struct {
	keys map[string]bool
	full bool
}

func (s *assetSet) has(k string) bool {
	return s != nil && (s.full || s.keys[k])
}

func (s *assetSet) surely(k string) bool {
	return s != nil && !s.full && s.keys[k]
}

func addAsset(m map[string]*assetSet, owner, k string) {
	s := m[owner]
	if s == nil {
		s = &assetSet{keys: map[string]bool{}}
		m[owner] = s
	}
	if !s.keys[k] && len(s.keys) >= maxAssets {
		s.full = true
		return
	}
	s.keys[k] = true
}

func (g *guard) chainLocked(k requestKey, p map[string]any) {
	req, _ := p["request"].(map[string]any)
	u, _ := req["url"].(string)
	chain := g.navChains[k]
	if _, redirect := p["redirectResponse"]; !redirect {
		chain = nil
	}
	chain = append(chain, u)
	if len(chain) > maxChain {
		chain = chain[len(chain)-maxChain:]
	}
	g.navChains[k] = chain
	into := g.clean
	for _, c := range chain {
		if g.o.Loaded(c) {
			into = g.tainted
		}
	}
	for _, t := range chain {
		if canon, ok := protect.Canonical(t); ok {
			addAsset(into, k.session, canon)
		}
	}
}

func (g *guard) taintedLocked(session, u string) bool {
	canon, ok := protect.Canonical(u)
	return !ok || g.tainted[g.worldTargetLocked(session)].has(canon)
}

func (g *guard) assetURLLocked(session, u string) bool {
	return u != "" && (g.o.Loaded(u) || g.taintedLocked(session, u))
}

func (g *guard) styleSheetEvent(m *message, data []byte) browserVerdict {
	var p struct {
		Header struct {
			StyleSheetID string `json:"styleSheetId"`
			SourceURL    string `json:"sourceURL"`
		} `json:"header"`
	}
	_ = json.Unmarshal(m.Params, &p)
	if g.assetURLLocked(m.SessionID, p.Header.SourceURL) {
		addAsset(g.assets, m.SessionID, assetKinds["styleSheetId"]+p.Header.StyleSheetID)
	}
	return browserVerdict{forward: data}
}

func (g *guard) unseenWorkerScriptLocked(session, u string) bool {
	t := g.sessionTargetLocked(session)
	if t == nil || !workerTypes[t.typ] || u == "" {
		return false
	}
	canon, ok := protect.Canonical(u)
	if main, _ := protect.Canonical(t.url); ok && canon == main {
		return false
	}
	return !ok || g.tainted[t.id].has(canon) || !g.clean[t.id].surely(canon)
}

func (g *guard) noteScriptLocked(m *message) {
	var p struct {
		ScriptID string `json:"scriptId"`
		URL      string `json:"url"`
	}
	_ = json.Unmarshal(m.Params, &p)
	if g.assetURLLocked(m.SessionID, p.URL) || g.unseenWorkerScriptLocked(m.SessionID, p.URL) {
		addAsset(g.assets, m.SessionID, assetKinds["scriptId"]+p.ScriptID)
	}
}

func (g *guard) resourceRule(c *command) string {
	u := c.str("url")
	if protect.Unparseable(u) {
		return refusedParse
	}
	if g.taintedLocked(c.session, u) {
		return refusedBody
	}
	return ""
}

var keptRuleKeys = map[string]bool{"styleSheetId": true, "origin": true}

func (g *guard) scrubAssets(m map[string]any) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	session, _ := m["sessionId"].(string)
	set := g.assets[session]
	if set == nil {
		return false
	}
	changed := false
	stack := []any{m["params"], m["result"]}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch t := v.(type) {
		case map[string]any:
			if id, ok := t["styleSheetId"].(string); ok && set.has(assetKinds["styleSheetId"]+id) {
				for k := range t {
					if !keptRuleKeys[k] {
						delete(t, k)
						changed = true
					}
				}
				continue
			}
			for _, val := range t {
				stack = append(stack, val)
			}
		case []any:
			stack = append(stack, t...)
		}
	}
	return changed
}
