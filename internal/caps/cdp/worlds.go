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
		if len(kept) == len(frames) {
			return true, false, nil
		}
		p["callFrames"] = kept
		return true, true, nil
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

func (g *guard) frameOutLocked(w *worldSet, v any, anonymous bool) bool {
	f, _ := v.(map[string]any)
	if f == nil {
		return false
	}
	url, _ := f["url"].(string)
	script, _ := f["scriptId"].(string)
	return g.scriptOutLocked(w, url, script, anonymous)
}

func (g *guard) keptLocked(w *worldSet, list []any, anonymous bool) ([]any, bool) {
	kept := make([]any, 0, len(list))
	for _, item := range list {
		if !g.frameOutLocked(w, item, anonymous) {
			kept = append(kept, item)
		}
	}
	return kept, len(kept) != len(list)
}

func coverageList(list []any) bool {
	for _, item := range list {
		e, _ := item.(map[string]any)
		_, functions := e["functions"]
		_, entries := e["entries"]
		if (functions || entries) && e["scriptId"] != nil {
			return true
		}
	}
	return false
}

func (g *guard) scrubWorldMapLocked(w *worldSet, t map[string]any) (bool, []any) {
	changed := false
	var more []any
	for k, val := range t {
		if opaque[k] {
			continue
		}
		list, isList := val.([]any)
		switch {
		case isList && k == "callFrames":
			if kept, cut := g.keptLocked(w, list, false); cut {
				t[k], changed = kept, true
			}
			continue
		case isList && coverageList(list):
			if kept, cut := g.keptLocked(w, list, true); cut {
				t[k], changed = kept, true
				val = kept
			}
		case k == "callFrame" && g.frameOutLocked(w, val, true):
			t[k], changed = hiddenFrame, true
			delete(t, "positionTicks")
			continue
		case k == "initiator":
			changed = g.scrubInitiatorLocked(val) || changed
		}
		more = append(more, val)
	}
	return changed, more
}

func (g *guard) scrubInitiatorLocked(v any) bool {
	ini, _ := v.(map[string]any)
	if u, _ := ini["url"].(string); u == "" || !g.o.URL(u) {
		return false
	}
	delete(ini, "url")
	delete(ini, "lineNumber")
	delete(ini, "columnNumber")
	return true
}

func (g *guard) scrubStacksLocked(w *worldSet, root any) bool {
	changed := false
	stack := []any{root}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch t := v.(type) {
		case map[string]any:
			cut, more := g.scrubWorldMapLocked(w, t)
			changed = cut || changed
			stack = append(stack, more...)
		case []any:
			stack = append(stack, t...)
		}
	}
	return changed
}

func (g *guard) scrubWorlds(m map[string]any) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	session, _ := m["sessionId"].(string)
	target := g.worldTargetLocked(session)
	changed := g.scrubStacksLocked(g.worlds[target], m)
	for _, key := range []string{"params", "result"} {
		if p, _ := m[key].(map[string]any); p != nil && g.fromWorldLocked(target, p) {
			g.cutWorldRequest(p)
			changed = true
		}
	}
	return changed
}

func (g *guard) pageSessionLocked(sessionID string) bool {
	t := g.sessionTargetLocked(sessionID)
	return t != nil && navTypes[t.typ]
}

func (g *guard) consoleMessageEvent(m *message, data []byte) browserVerdict {
	var p struct {
		Message struct {
			URL string `json:"url"`
		} `json:"message"`
	}
	_ = json.Unmarshal(m.Params, &p)
	if g.pageSessionLocked(m.SessionID) && (p.Message.URL == "" || g.o.URL(p.Message.URL)) {
		return browserVerdict{}
	}
	return browserVerdict{forward: data}
}

func (g *guard) logEntryEvent(m *message, data []byte) browserVerdict {
	w := g.worlds[g.worldTargetLocked(m.SessionID)]
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		e, _ := p["entry"].(map[string]any)
		u, _ := e["url"].(string)
		if (u != "" && g.o.URL(u)) || g.scrubStacksLocked(w, e) {
			return false, false, nil
		}
		return true, false, nil
	})
}

func (g *guard) consoleProfileEvent(m *message, data []byte) browserVerdict {
	if g.pageSessionLocked(m.SessionID) {
		return browserVerdict{}
	}
	return browserVerdict{forward: data}
}

func (g *guard) initiatorLocked(sessionID string, p map[string]any) bool {
	return g.initiatorOfLocked(g.worldTargetLocked(sessionID), p)
}

func (g *guard) initiatorOfLocked(target string, p map[string]any) bool {
	ini, _ := p["initiator"].(map[string]any)
	if ini == nil {
		return false
	}
	return g.scrubStacksLocked(g.worlds[target], ini) || g.scrubInitiatorLocked(ini)
}

func (g *guard) narrowBindingLocked(c *command) {
	if c.method != "Runtime.addBinding" || !g.pageSessionLocked(c.session) || c.params == nil {
		return
	}
	_, byID := c.params["executionContextId"]
	_, byName := c.params["executionContextName"]
	if !byID && !byName {
		c.params["executionContextName"] = ""
	}
}

func (g *guard) forgetWorldSessionLocked(id string) {
	for _, w := range g.worlds {
		for _, wc := range w.contexts {
			delete(wc.seen, id)
		}
	}
}

const (
	maxWorldRequests = 65536
	maxHeldPauses    = 4096
)

var worldDropped = map[string]bool{"postData": true, "postDataEntries": true, "headersText": true, "requestHeadersText": true, "associatedCookies": true, "exemptedCookies": true, "blockedCookies": true}

var requestIDKeys = map[string]bool{"requestId": true, "networkId": true, "transportId": true}

type heldPause struct {
	m    message
	data []byte
}

func requestIDIn(p map[string]any) string {
	for _, k := range []string{"requestId", "transportId"} {
		if id, _ := p[k].(string); id != "" {
			return id
		}
	}
	return ""
}

func (g *guard) worldRequestLocked(target, id string) {
	k := requestKey{target, id}
	if id == "" || target == "" || g.worldRequests[k] {
		return
	}
	if g.worldRequestCount[target] >= maxWorldRequests {
		if !g.worldRequestsFull[target] {
			g.logf("cdp: a target made more requests from extension worlds than slicc tracks; cutting every request there to its origin")
		}
		g.worldRequestsFull[target] = true
		return
	}
	g.worldRequests[k] = true
	g.worldRequestCount[target]++
}

func (g *guard) worldRequestKnownLocked(target, id string) bool {
	return id != "" && (g.worldRequests[requestKey{target, id}] || g.worldRequestsFull[target])
}

func (g *guard) forgetWorldRequestsLocked(target string) {
	for k := range g.worldRequests {
		if k.session == target {
			delete(g.worldRequests, k)
		}
	}
	for k := range g.navSeen {
		if k.session == target {
			delete(g.navSeen, k)
		}
	}
	delete(g.worldRequestCount, target)
	delete(g.worldRequestsFull, target)
}

func (g *guard) fromWorldLocked(target string, root any) bool {
	if target == "" || (len(g.worldRequests) == 0 && !g.worldRequestsFull[target]) {
		return false
	}
	stack := []any{root}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch t := v.(type) {
		case map[string]any:
			for k, val := range t {
				if id, ok := val.(string); ok && requestIDKeys[k] && g.worldRequestKnownLocked(target, id) {
					return true
				}
				stack = append(stack, val)
			}
		case []any:
			stack = append(stack, t...)
		}
	}
	return false
}

func (g *guard) navRequestLocked(nt *navTarget, m *message) browserVerdict {
	if m.Method != "Network.requestWillBeSent" && m.Method != "Network.webSocketCreated" && m.Method != "Network.webTransportCreated" {
		return browserVerdict{}
	}
	p, ok := decodeObject(m.Params)
	if !ok {
		return browserVerdict{}
	}
	id := requestIDIn(p)
	if g.initiatorOfLocked(nt.target, p) {
		g.worldRequestLocked(nt.target, id)
	}
	if id == "" {
		return browserVerdict{}
	}
	k := requestKey{nt.target, id}
	if !g.navSeen[k] {
		g.navSeen[k] = true
		g.navSeenOrder = append(g.navSeenOrder, k)
		if len(g.navSeenOrder) > maxWorldRequests {
			delete(g.navSeen, g.navSeenOrder[0])
			g.navSeenOrder = g.navSeenOrder[1:]
		}
	}
	return g.releasePausesLocked(k)
}

func (g *guard) releasePausesLocked(k requestKey) browserVerdict {
	held := g.heldPauses[k]
	delete(g.heldPauses, k)
	g.heldPauseCount -= len(held)
	var out browserVerdict
	for _, h := range held {
		if g.sessionRefusedLocked(h.m.SessionID) || g.sessionTargetLocked(h.m.SessionID) == nil {
			continue
		}
		v := pauseHandler(g, &h.m, h.data)
		if v.forward != nil {
			out.page = append(out.page, v.forward)
		}
		out.page = append(out.page, v.page...)
		out.send = append(out.send, v.send...)
		out.extra = append(out.extra, v.extra...)
		out.answers = append(out.answers, v.answers...)
	}
	return out
}

func (g *guard) expirePause(k requestKey) (page, send [][]byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.heldPauses[k]) > 0 {
		g.logf("cdp: handed out a paused request the navigation guard never reported")
	}
	v := g.releasePausesLocked(k)
	return v.page, v.send
}

var pauseHandler = afterRequestBy((*guard).pausedEvent, levelSettled, pausedIDOf)

func (g *guard) pauseGateEvent(m *message, data []byte) browserVerdict {
	var p struct {
		NetworkID string `json:"networkId"`
		RequestID string `json:"requestId"`
	}
	_ = json.Unmarshal(m.Params, &p)
	if m.Method == "Network.requestIntercepted" {
		p.NetworkID = p.RequestID
	}
	t := g.worldTargetLocked(m.SessionID)
	nt := g.navTargets[t]
	k := requestKey{t, p.NetworkID}
	if p.NetworkID == "" || nt == nil || nt.session == "" || g.navSeen[k] || g.worldRequestKnownLocked(t, p.NetworkID) {
		return pauseHandler(g, m, data)
	}
	if g.heldPauseCount >= maxHeldPauses {
		g.logf("cdp: too many paused requests wait for the navigation guard; continuing one without showing it to the page")
		return g.continuePauseLocked(m)
	}
	g.heldPauses[k] = append(g.heldPauses[k], heldPause{m: *m, data: data})
	g.heldPauseCount++
	return browserVerdict{pauses: []requestKey{k}}
}

func (g *guard) cutWorldRequest(root any) {
	stack := []any{root}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch t := v.(type) {
		case map[string]any:
			for k, val := range t {
				s, isString := val.(string)
				switch {
				case worldDropped[k]:
					delete(t, k)
				case headerLists[k]:
					t[k] = map[string]any{}
				case isString && urlKey(k) && strings.Contains(s, ":"):
					t[k] = redactURL(s)
				default:
					stack = append(stack, val)
				}
			}
		case []any:
			stack = append(stack, t...)
		}
	}
}

func (g *guard) continuePauseLocked(m *message) browserVerdict {
	p, _ := decodeObject(m.Params)
	id, other := pausedIDs(m.Method, p)
	g.markRequestLocked(m.SessionID, id)
	g.markRequestLocked(m.SessionID, other)
	params := map[string]any{"requestId": id}
	method := "Fetch.continueRequest"
	if m.Method == "Fetch.authRequired" {
		method = "Fetch.continueWithAuth"
		params["authChallengeResponse"] = map[string]any{"response": "Default"}
	}
	msg, _ := g.newCallLocked(m.SessionID, method, params, false)
	return browserVerdict{send: [][]byte{msg}}
}

func (g *guard) worldPauseLocked(m *message, p map[string]any) ([]byte, bool) {
	id, other := pausedIDs(m.Method, p)
	t := g.worldTargetLocked(m.SessionID)
	auth := m.Method == "Fetch.authRequired" && g.requestMarkedLocked(m.SessionID, id)
	if !auth && !g.worldRequestKnownLocked(t, other) && !g.worldRequestKnownLocked(t, id) {
		return nil, false
	}
	delete(g.handed, requestKey{m.SessionID, id})
	g.markRequestLocked(m.SessionID, id)
	g.markRequestLocked(m.SessionID, other)
	method, params := "Fetch.continueRequest", map[string]any{"requestId": id}
	switch m.Method {
	case "Network.requestIntercepted":
		method, params = "Network.continueInterceptedRequest", map[string]any{"interceptionId": id}
		if _, ok := p["authChallenge"]; ok {
			params["authChallengeResponse"] = map[string]any{"response": "Default"}
		}
	case "Fetch.authRequired":
		method = "Fetch.continueWithAuth"
		params["authChallengeResponse"] = map[string]any{"response": "Default"}
	}
	g.logf("cdp: continued a request a browser extension's world made without showing it to the page")
	msg, _ := g.newCallLocked(m.SessionID, method, params, false)
	return msg, true
}
