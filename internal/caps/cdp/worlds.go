package cdp

import (
	"encoding/json"
	"strconv"
	"strings"
)

const (
	refusedWorld = "that execution context belongs to a browser extension"
	refusedHeap  = "HeapProfiler reads the page's whole renderer, browser extensions' worlds included"
	refusedBreak = "a breakpoint condition that could run in a browser extension's world needs a url naming the page's own script"
	maxWorlds    = 4096
)

var heapOK = map[string]bool{"HeapProfiler.enable": true, "HeapProfiler.disable": true, "HeapProfiler.collectGarbage": true, "HeapProfiler.getHeapObjectId": true}

type worldCtx struct {
	unique  string
	name    string
	seen    map[string]bool
	scripts []string
}

type worldSet struct {
	contexts map[int64]*worldCtx
	scripts  map[string]int64
	full     bool
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
		w = &worldSet{contexts: map[int64]*worldCtx{}, scripts: map[string]int64{}}
		g.worlds[target] = w
	}
	return w
}

func (g *guard) hideContextLocked(target, session string, c execContext) {
	w := g.worldsLocked(target)
	if wc := w.contexts[c.ID]; wc != nil {
		wc.seen[session] = true
		return
	}
	if len(w.contexts) >= g.maxWorlds {
		if !w.full {
			g.logf("cdp: a target has more extension worlds than slicc tracks; refusing every command there that names an execution context")
		}
		w.full = true
		return
	}
	w.contexts[c.ID] = &worldCtx{unique: c.UniqueID, name: c.Name, seen: map[string]bool{session: true}}
	if c.UniqueID != "" {
		g.worldUniques[c.UniqueID] = target
	}
}

func (g *guard) forgetContextLocked(w *worldSet, id int64) {
	wc := w.contexts[id]
	if wc == nil {
		return
	}
	for _, s := range wc.scripts {
		delete(w.scripts, s)
	}
	if wc.unique != "" {
		delete(g.worldUniques, wc.unique)
	}
	delete(w.contexts, id)
}

func (g *guard) hideScriptLocked(w *worldSet, ctx int64, id string) {
	wc := w.contexts[ctx]
	if id == "" || wc == nil {
		return
	}
	if _, ok := w.scripts[id]; ok {
		return
	}
	w.scripts[id] = ctx
	wc.scripts = append(wc.scripts, id)
}

func (g *guard) forgetWorldsLocked(target string) {
	w := g.worlds[target]
	if w == nil {
		return
	}
	for id := range w.contexts {
		g.forgetContextLocked(w, id)
	}
	delete(g.worlds, target)
}

func (g *guard) worldContextLocked(target, session string, c execContext) bool {
	if !g.o.URL(c.Origin) || (c.AuxData.IsDefault && c.AuxData.FrameID == target) {
		if w := g.worlds[target]; w != nil {
			g.forgetContextLocked(w, c.ID)
		}
		return false
	}
	g.hideContextLocked(target, session, c)
	return true
}

func (g *guard) contextGoneLocked(target string, id int64, unique string) bool {
	if t, ok := g.worldUniques[unique]; ok && unique != "" {
		target = t
	}
	w := g.worlds[target]
	if w == nil {
		return false
	}
	if unique != "" {
		for cid, wc := range w.contexts {
			if wc.unique == unique {
				id = cid
			}
		}
	}
	if w.contexts[id] == nil {
		return false
	}
	g.forgetContextLocked(w, id)
	return true
}

func (g *guard) contextsClearedLocked(target, session string) {
	w := g.worlds[target]
	if w == nil {
		return
	}
	for id, wc := range w.contexts {
		if wc.seen[session] {
			g.forgetContextLocked(w, id)
		}
	}
	if len(w.contexts) < g.maxWorlds {
		w.full = false
	}
}

func (g *guard) contextHiddenLocked(target string, id int64) bool {
	w := g.worlds[target]
	return w != nil && (w.contexts[id] != nil || w.full)
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
	t := g.sessionTargetLocked(c.session)
	if t != nil && navTypes[t.typ] && strings.HasPrefix(c.method, "HeapProfiler.") && !heapOK[c.method] {
		return refusedHeap
	}
	if t != nil && navTypes[t.typ] && c.method == "Debugger.setBreakpointByUrl" && c.str("condition") != "" && c.str("url") == "" {
		return refusedBreak
	}
	if len(g.worlds) == 0 {
		return ""
	}
	target := ""
	if t != nil {
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
		return err == nil && g.contextHiddenLocked(target, id)
	case !isString:
		return false
	case k == "objectId" || strings.HasSuffix(k, "ObjectId") || k == "callFrameId":
		id, ok := remoteContext(s)
		return ok && g.contextHiddenLocked(target, id)
	case k == "scriptId":
		_, hidden := w.scripts[s]
		return hidden || w.full
	case k == "executionContextName":
		return w.full || w.named(s)
	}
	return false
}

func (w *worldSet) named(name string) bool {
	for _, wc := range w.contexts {
		if wc.name == name {
			return true
		}
	}
	return false
}

func (g *guard) navWorldLocked(nt *navTarget, m *message) {
	if nt.target == "" {
		return
	}
	switch m.Method {
	case "Runtime.executionContextCreated":
		var p struct {
			Context execContext `json:"context"`
		}
		if json.Unmarshal(m.Params, &p) == nil {
			g.worldContextLocked(nt.target, m.SessionID, p.Context)
		}
	case "Runtime.executionContextDestroyed":
		var p struct {
			ID       int64  `json:"executionContextId"`
			UniqueID string `json:"executionContextUniqueId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		g.contextGoneLocked(nt.target, p.ID, p.UniqueID)
	case "Runtime.executionContextsCleared":
		g.contextsClearedLocked(nt.target, m.SessionID)
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
	if g.contextGoneLocked(g.worldTargetLocked(m.SessionID), p.ID, p.UniqueID) {
		return browserVerdict{}
	}
	return browserVerdict{forward: data}
}

func (g *guard) contextsClearedEvent(m *message, data []byte) browserVerdict {
	g.contextsClearedLocked(g.worldTargetLocked(m.SessionID), m.SessionID)
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
	return g.stackEventLocked(m, data)
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
	g.hideScriptLocked(g.worlds[target], p.ID, p.ScriptID)
	return browserVerdict{}
}

func (g *guard) breakpointEvent(m *message, data []byte) browserVerdict {
	var p struct {
		Location struct {
			ScriptID string `json:"scriptId"`
		} `json:"location"`
	}
	_ = json.Unmarshal(m.Params, &p)
	if w := g.worlds[g.worldTargetLocked(m.SessionID)]; w != nil && g.scriptHidden(w, p.Location.ScriptID) {
		return browserVerdict{}
	}
	return browserVerdict{forward: data}
}

func (g *guard) scriptHidden(w *worldSet, id string) bool {
	_, hidden := w.scripts[id]
	return hidden
}

func (g *guard) frameHiddenLocked(target string, w *worldSet, f any) bool {
	frame, _ := f.(map[string]any)
	id, _ := frame["callFrameId"].(string)
	if n, ok := remoteContext(id); ok && g.contextHiddenLocked(target, n) {
		return true
	}
	loc, _ := frame["location"].(map[string]any)
	script, _ := loc["scriptId"].(string)
	return g.scriptHidden(w, script)
}

func (g *guard) debuggerPausedEvent(m *message, data []byte) browserVerdict {
	target := g.worldTargetLocked(m.SessionID)
	w := g.worlds[target]
	if w == nil {
		return browserVerdict{forward: data}
	}
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		frames, _ := p["callFrames"].([]any)
		if len(frames) > 0 && g.frameHiddenLocked(target, w, frames[0]) {
			msg, _ := g.newCallLocked(m.SessionID, "Debugger.resume", nil, false)
			return false, false, [][]byte{msg}
		}
		kept := make([]any, 0, len(frames))
		for _, f := range frames {
			if !g.frameHiddenLocked(target, w, f) {
				kept = append(kept, f)
			}
		}
		changed := len(kept) != len(frames)
		if changed {
			p["callFrames"] = kept
		}
		return true, g.scrubStacksLocked(w, p, 0) || changed, nil
	})
}

var hiddenFrame = map[string]any{"functionName": "(program)", "scriptId": "0", "url": "", "lineNumber": -1, "columnNumber": -1}

func (g *guard) scriptOutLocked(w *worldSet, url, script string, anonymous bool) bool {
	if url != "" && g.o.URL(url) {
		return true
	}
	if anonymous && url == "" && script != "" && script != "0" {
		return true
	}
	return w != nil && g.scriptHidden(w, script)
}

func (g *guard) scrubProfileLocked(sessionID string, v map[string]any) bool {
	w := g.worlds[g.worldTargetLocked(sessionID)]
	changed := false
	if list, ok := v["result"].([]any); ok {
		kept := make([]any, 0, len(list))
		for _, item := range list {
			e, _ := item.(map[string]any)
			url, _ := e["url"].(string)
			script, _ := e["scriptId"].(string)
			if !g.scriptOutLocked(w, url, script, true) {
				kept = append(kept, item)
			}
		}
		changed = len(kept) != len(list)
		v["result"] = kept
	}
	profile, _ := v["profile"].(map[string]any)
	nodes, _ := profile["nodes"].([]any)
	for _, item := range nodes {
		node, _ := item.(map[string]any)
		cf, _ := node["callFrame"].(map[string]any)
		url, _ := cf["url"].(string)
		script, _ := cf["scriptId"].(string)
		if cf != nil && g.scriptOutLocked(w, url, script, true) {
			node["callFrame"] = hiddenFrame
			delete(node, "positionTicks")
			changed = true
		}
	}
	return changed
}

func (g *guard) scrubStacksLocked(w *worldSet, v any, depth int) bool {
	if depth > maxScanDepth {
		return false
	}
	changed := false
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if frames, ok := val.([]any); ok && k == "callFrames" {
				kept := make([]any, 0, len(frames))
				for _, f := range frames {
					frame, _ := f.(map[string]any)
					url, _ := frame["url"].(string)
					script, _ := frame["scriptId"].(string)
					if !g.scriptOutLocked(w, url, script, false) {
						kept = append(kept, f)
					}
				}
				if len(kept) != len(frames) {
					t[k] = kept
					changed = true
				}
				continue
			}
			changed = g.scrubStacksLocked(w, val, depth+1) || changed
		}
	case []any:
		for _, val := range t {
			changed = g.scrubStacksLocked(w, val, depth+1) || changed
		}
	}
	return changed
}

func (g *guard) stackEventLocked(m *message, data []byte) browserVerdict {
	w := g.worlds[g.worldTargetLocked(m.SessionID)]
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		return true, g.scrubStacksLocked(w, p, 0), nil
	})
}

func (g *guard) profileEvent(m *message, data []byte) browserVerdict {
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		return true, g.scrubProfileLocked(m.SessionID, p), nil
	})
}
