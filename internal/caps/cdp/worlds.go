package cdp

import (
	"encoding/json"
	"strconv"
	"strings"
)

const (
	refusedWorld = "that execution context belongs to a browser extension"
	maxWorlds    = 4096
)

type worldSet struct {
	contexts map[int64]bool
	uniques  map[string]bool
	scripts  map[string]bool
	names    map[string]bool
	order    []func()
}

type execContext struct {
	ID       int64  `json:"id"`
	Origin   string `json:"origin"`
	Name     string `json:"name"`
	UniqueID string `json:"uniqueId"`
	AuxData  struct {
		IsDefault bool   `json:"isDefault"`
		FrameID   string `json:"frameId"`
	} `json:"auxData"`
}

func (g *guard) worldsLocked(target string) *worldSet {
	w := g.worlds[target]
	if w == nil {
		w = &worldSet{contexts: map[int64]bool{}, uniques: map[string]bool{}, scripts: map[string]bool{}, names: map[string]bool{}}
		g.worlds[target] = w
	}
	return w
}

func (w *worldSet) remember(forget func()) {
	w.order = append(w.order, forget)
	if len(w.order) > maxWorlds {
		w.order[0]()
		w.order = w.order[1:]
	}
}

func (g *guard) hideContextLocked(target string, c execContext) {
	w := g.worldsLocked(target)
	if !w.contexts[c.ID] {
		w.contexts[c.ID] = true
		id := c.ID
		w.remember(func() { delete(w.contexts, id) })
	}
	if c.UniqueID != "" && !w.uniques[c.UniqueID] {
		w.uniques[c.UniqueID] = true
		g.worldUniques[c.UniqueID] = target
		u := c.UniqueID
		w.remember(func() {
			delete(w.uniques, u)
			delete(g.worldUniques, u)
		})
	}
	if c.Name != "" && !w.names[c.Name] {
		w.names[c.Name] = true
		n := c.Name
		w.remember(func() { delete(w.names, n) })
	}
}

func (g *guard) hideScriptLocked(target, id string) {
	w := g.worldsLocked(target)
	if id == "" || w.scripts[id] {
		return
	}
	w.scripts[id] = true
	w.remember(func() { delete(w.scripts, id) })
}

func (g *guard) forgetWorldsLocked(target string) {
	w := g.worlds[target]
	if w == nil {
		return
	}
	for u := range w.uniques {
		delete(g.worldUniques, u)
	}
	delete(g.worlds, target)
}

func (g *guard) worldContextLocked(target string, c execContext) bool {
	if !g.o.URL(c.Origin) || (c.AuxData.IsDefault && c.AuxData.FrameID == target) {
		return false
	}
	g.hideContextLocked(target, c)
	return true
}

func (g *guard) contextHiddenLocked(target string, id int64) bool {
	w := g.worlds[target]
	return w != nil && w.contexts[id]
}

func remoteContext(id string) (int64, bool) {
	if strings.HasPrefix(id, "{") {
		var legacy struct {
			InjectedScriptID *int64 `json:"injectedScriptId"`
		}
		if json.Unmarshal([]byte(id), &legacy) != nil || legacy.InjectedScriptID == nil {
			return 0, false
		}
		return *legacy.InjectedScriptID, true
	}
	parts := strings.Split(id, ".")
	if len(parts) != 3 {
		return 0, false
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	return n, err == nil
}

func (g *guard) worldCheckLocked(c *command) string {
	if len(g.worlds) == 0 {
		return ""
	}
	target := ""
	if t := g.sessionTargetLocked(c.session); t != nil {
		target = t.id
	}
	if g.worldScan(c.params, target, 0) {
		return refusedWorld
	}
	return ""
}

func (g *guard) worldScan(v any, target string, depth int) bool {
	if depth > maxScanDepth {
		return false
	}
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if skipKeys[k] && k != "arguments" {
				continue
			}
			if g.worldKey(k, val, target) || g.worldScan(val, target, depth+1) {
				return true
			}
		}
	case []any:
		for _, val := range t {
			if g.worldScan(val, target, depth+1) {
				return true
			}
		}
	}
	return false
}

func (g *guard) worldKey(k string, val any, target string) bool {
	s, isString := val.(string)
	w := g.worlds[target]
	switch {
	case k == "uniqueContextId" && isString:
		_, hidden := g.worldUniques[s]
		return hidden
	case w == nil:
		return false
	case k == "contextId" || k == "executionContextId":
		n, ok := val.(json.Number)
		if !ok {
			return false
		}
		id, err := n.Int64()
		return err == nil && w.contexts[id]
	case !isString:
		return false
	case k == "objectId" || strings.HasSuffix(k, "ObjectId") || k == "callFrameId":
		id, ok := remoteContext(s)
		return ok && w.contexts[id]
	case k == "scriptId":
		return w.scripts[s]
	case k == "executionContextName":
		return w.names[s]
	}
	return false
}

func (g *guard) navWorldLocked(nt *navTarget, m *message) {
	if m.Method != "Runtime.executionContextCreated" || nt.target == "" {
		return
	}
	var p struct {
		Context execContext `json:"context"`
	}
	if json.Unmarshal(m.Params, &p) == nil {
		g.worldContextLocked(nt.target, p.Context)
	}
}

func (g *guard) worldTargetLocked(sessionID string) string {
	if t := g.sessionTargetLocked(sessionID); t != nil {
		return t.id
	}
	return ""
}

func (g *guard) contextGoneEvent(m *message, data []byte) browserVerdict {
	var p struct {
		ID       int64  `json:"executionContextId"`
		UniqueID string `json:"executionContextUniqueId"`
	}
	_ = json.Unmarshal(m.Params, &p)
	target := g.worldTargetLocked(m.SessionID)
	if _, hidden := g.worldUniques[p.UniqueID]; (p.UniqueID != "" && hidden) || g.contextHiddenLocked(target, p.ID) {
		return browserVerdict{}
	}
	return browserVerdict{forward: data}
}

func (g *guard) contextEvent(m *message, data []byte) browserVerdict {
	var p struct {
		ID      int64 `json:"executionContextId"`
		Details struct {
			ID int64 `json:"executionContextId"`
		} `json:"exceptionDetails"`
	}
	_ = json.Unmarshal(m.Params, &p)
	target := g.worldTargetLocked(m.SessionID)
	if g.contextHiddenLocked(target, p.ID) || g.contextHiddenLocked(target, p.Details.ID) {
		return browserVerdict{}
	}
	return browserVerdict{forward: data}
}

func (g *guard) scriptEvent(m *message, data []byte) browserVerdict {
	var p struct {
		ScriptID string `json:"scriptId"`
		ID       int64  `json:"executionContextId"`
	}
	_ = json.Unmarshal(m.Params, &p)
	target := g.worldTargetLocked(m.SessionID)
	if !g.contextHiddenLocked(target, p.ID) {
		return browserVerdict{forward: data}
	}
	g.hideScriptLocked(target, p.ScriptID)
	return browserVerdict{}
}

func (g *guard) breakpointEvent(m *message, data []byte) browserVerdict {
	var p struct {
		Location struct {
			ScriptID string `json:"scriptId"`
		} `json:"location"`
	}
	_ = json.Unmarshal(m.Params, &p)
	if w := g.worlds[g.worldTargetLocked(m.SessionID)]; w != nil && w.scripts[p.Location.ScriptID] {
		return browserVerdict{}
	}
	return browserVerdict{forward: data}
}

func (g *guard) frameHiddenLocked(w *worldSet, f any) bool {
	frame, _ := f.(map[string]any)
	id, _ := frame["callFrameId"].(string)
	if n, ok := remoteContext(id); ok && w.contexts[n] {
		return true
	}
	loc, _ := frame["location"].(map[string]any)
	script, _ := loc["scriptId"].(string)
	return w.scripts[script]
}

func (g *guard) debuggerPausedEvent(m *message, data []byte) browserVerdict {
	w := g.worlds[g.worldTargetLocked(m.SessionID)]
	if w == nil {
		return browserVerdict{forward: data}
	}
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		frames, _ := p["callFrames"].([]any)
		if len(frames) > 0 && g.frameHiddenLocked(w, frames[0]) {
			msg, _ := g.newCallLocked(m.SessionID, "Debugger.resume", nil, false)
			return false, false, [][]byte{msg}
		}
		kept := make([]any, 0, len(frames))
		for _, f := range frames {
			if !g.frameHiddenLocked(w, f) {
				kept = append(kept, f)
			}
		}
		if len(kept) == len(frames) {
			return true, false, nil
		}
		p["callFrames"] = kept
		return true, true, nil
	})
}
