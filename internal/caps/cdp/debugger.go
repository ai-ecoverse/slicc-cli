package cdp

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
	"github.com/coder/websocket"
)

type debuggerLifetime int

const (
	debuggerUnknown debuggerLifetime = iota
	debuggerPersistent
	debuggerDocument
	debuggerCleanup
	debuggerControl
)

var debuggerLifetimes = map[string]debuggerLifetime{
	"Debugger.enable":                       debuggerPersistent,
	"Debugger.setInstrumentationBreakpoint": debuggerPersistent,
	"Debugger.getScriptSource":              debuggerDocument,
	"Debugger.evaluateOnCallFrame":          debuggerDocument,
	"Debugger.disable":                      debuggerCleanup,
	"Debugger.removeBreakpoint":             debuggerCleanup,
	"Debugger.resume":                       debuggerControl,
}

const debuggerReadLimit = 2 * time.Second
const debuggerPauseLimit = 5 * time.Second
const debuggerBytesLimit = 64 << 20

func debuggerBudgetFromEnv() int {
	bytes, err := strconv.Atoi(strings.TrimSpace(os.Getenv("SLICC_CDP_DEBUGGER_BYTES")))
	if err != nil || bytes <= 0 {
		return debuggerBytesLimit
	}
	return min(bytes, debuggerBytesLimit)
}

type debuggerScript struct {
	context int64
	unique  string
	data    []byte
}

type debuggerScriptInfo struct {
	context  int64
	evaluate bool
}

type debuggerState struct {
	owner             string
	target            string
	generation        uint64
	enabled           bool
	full              bool
	scripts           map[string]debuggerScript
	scriptInfo        map[string]debuggerScriptInfo
	publishedContexts map[int64]string
	breakpoints       map[string]bool
	pause             *debuggerPause
	resuming          *debuggerPause
}

type debuggerPause struct {
	state           *debuggerState
	generation      uint64
	deadline        time.Time
	instrumentation bool
	verifying       bool
	published       bool
	data            []byte
	frames          map[string]bool
	scripts         map[string]bool
	contexts        map[int64]string
}

type debuggerReply struct {
	state      *debuggerState
	method     string
	generation uint64
}

func debuggerDomain(method string) bool {
	return strings.HasPrefix(method, "Debugger.") || strings.HasPrefix(method, "DOMDebugger.") || strings.HasPrefix(method, "EventBreakpoints.")
}

func (g *guard) debuggerAdmissionLocked(c *command) string {
	t := g.sessionTargetLocked(c.session)
	if t == nil || t.gone || !navTypes[t.typ] || g.o.Loaded(t.url) {
		return refusedTarget
	}
	if reason := g.frameContentCheckLocked(&command{method: "Page.getFrameTree", params: map[string]any{}}, t.id); reason != "" {
		return reason
	}
	if w := g.worlds[t.id]; w != nil && (w.full || len(w.contexts) != 0) {
		return refusedWorld
	}
	return g.navCheckLocked(c.session)
}

func (g *guard) debuggerCheckLocked(c *command) string {
	if !debuggerDomain(c.method) {
		return ""
	}
	class := debuggerLifetimes[c.method]
	if class == debuggerUnknown || class == debuggerDocument {
		return refusedLaterDocuments
	}
	if reason := g.debuggerAdmissionLocked(c); reason != "" {
		return reason
	}
	s := g.debuggers[c.session]
	if s != nil && s.full {
		return refusedTarget
	}
	if c.method == "Debugger.enable" {
		for owner, other := range g.debuggers {
			if owner != c.session && other.target == g.sessions[c.session].target {
				return refusedTarget
			}
		}
		return ""
	}
	if s == nil || !s.enabled || s.generation != g.targets[s.target].documentRevision || s.full {
		return refusedTarget
	}
	if class == debuggerControl {
		p := s.pause
		if p == nil || !p.published || !g.debuggerContextsLocked(s, p) || p.generation != s.generation || !time.Now().Before(p.deadline) {
			return refusedTarget
		}
	}
	if c.method == "Debugger.removeBreakpoint" && !s.breakpoints[c.str("breakpointId")] {
		return refusedTarget
	}
	return ""
}

func (g *guard) noteDebuggerLocked(k callKey, c *command) {
	if !debuggerDomain(c.method) {
		return
	}
	s := g.debuggers[c.session]
	if s == nil {
		t := g.sessionTargetLocked(c.session)
		s = &debuggerState{owner: c.session, target: t.id, generation: t.documentRevision, scripts: map[string]debuggerScript{}, scriptInfo: map[string]debuggerScriptInfo{}, publishedContexts: map[int64]string{}, breakpoints: map[string]bool{}}
		g.debuggers[c.session] = s
	}
	g.debuggerReplies[k] = debuggerReply{state: s, method: c.method, generation: s.generation}
	if c.method == "Debugger.resume" {
		g.releaseDebuggerPauseLocked(s.resuming)
		g.releaseDebuggerPauseLocked(s.pause)
		s.resuming = s.pause
		s.pause = nil
	}
	if c.method == "Debugger.removeBreakpoint" {
		delete(s.breakpoints, c.str("breakpointId"))
	}
}

func (g *guard) debuggerReplyLocked(k callKey, m *message) bool {
	r, ok := g.debuggerReplies[k]
	delete(g.debuggerReplies, k)
	if !ok {
		return true
	}
	s := r.state
	if g.debuggers[s.owner] != s || s.generation != r.generation {
		return false
	}
	switch r.method {
	case "Debugger.enable":
		s.enabled = m.Error == nil
		if !s.enabled {
			g.dropDebuggerLocked(s)
		}
	case "Debugger.disable":
		g.dropDebuggerLocked(s)
	case "Debugger.setInstrumentationBreakpoint":
		var result struct {
			BreakpointID string `json:"breakpointId"`
		}
		if m.Error == nil && json.Unmarshal(m.Result, &result) == nil && result.BreakpointID != "" {
			s.breakpoints[result.BreakpointID] = true
		}
	}
	return true
}

func (g *guard) debuggerEventLocked(m *message, data []byte) (browserVerdict, bool) {
	switch m.Method {
	case "Debugger.paused":
		return g.holdDebuggerLocked(m, data), true
	case "Debugger.scriptParsed", "Debugger.scriptFailedToParse":
		return browserVerdict{send: g.debuggerScriptLocked(m, data)}, true
	case "Debugger.resumed":
		return g.debuggerResumedLocked(m, data), true
	}
	return browserVerdict{}, strings.HasPrefix(m.Method, "Debugger.")
}

func (g *guard) clearDebuggerScriptsLocked(s *debuggerState) {
	for id := range s.scriptInfo {
		g.debuggerBytes -= len(id)
	}
	for _, script := range s.scripts {
		g.debuggerBytes -= len(script.data)
	}
	clear(s.scriptInfo)
	clear(s.scripts)
}

func (g *guard) releaseDebuggerPauseLocked(p *debuggerPause) {
	if p != nil {
		g.debuggerBytes -= len(p.data)
		p.data = nil
	}
}

func (g *guard) dropDebuggerLocked(s *debuggerState) {
	g.clearDebuggerScriptsLocked(s)
	g.releaseDebuggerPauseLocked(s.pause)
	g.releaseDebuggerPauseLocked(s.resuming)
	delete(g.debuggers, s.owner)
}

func (g *guard) fullDebuggerLocked(s *debuggerState) [][]byte {
	s.full = true
	g.clearDebuggerScriptsLocked(s)
	if s.pause == nil {
		return nil
	}
	resume := g.resumeDebuggerLocked(s)
	s.resuming = nil
	return [][]byte{resume}
}

func (g *guard) debuggerScriptLocked(m *message, data []byte) [][]byte {
	g.noteScriptLocked(m)
	s := g.debuggers[m.SessionID]
	if s == nil || s.full {
		return nil
	}
	var p struct {
		ScriptID     string  `json:"scriptId"`
		Context      int64   `json:"executionContextId"`
		URL          *string `json:"url"`
		EmbedderName *string `json:"embedderName"`
	}
	if json.Unmarshal(m.Params, &p) != nil || p.ScriptID == "" || p.Context == 0 || (p.URL != nil && *p.URL != "" && g.o.Loaded(*p.URL)) || len(s.scriptInfo) >= maxWorlds || len(data) > 1<<20 {
		return g.fullDebuggerLocked(s)
	}
	keep := g.debuggerAdmissionLocked(&command{session: m.SessionID}) == ""
	added := 0
	if _, known := s.scriptInfo[p.ScriptID]; !known {
		added += len(p.ScriptID)
	}
	if keep {
		added += len(data) - len(s.scripts[p.ScriptID].data)
	}
	if added > g.debuggerLimit-g.debuggerBytes {
		return g.fullDebuggerLocked(s)
	}
	g.debuggerBytes += added
	s.scriptInfo[p.ScriptID] = debuggerScriptInfo{context: p.Context, evaluate: p.URL != nil && *p.URL == "" && p.EmbedderName != nil && *p.EmbedderName == ""}
	if !keep {
		return nil
	}
	unique := ""
	if shown := g.shown[s.target]; shown != nil && shown.contexts[p.Context] != nil {
		unique = shown.contexts[p.Context].unique
	}
	s.scripts[p.ScriptID] = debuggerScript{context: p.Context, unique: unique, data: append([]byte(nil), data...)}
	return nil
}

func (g *guard) resumeDebuggerLocked(s *debuggerState) []byte {
	g.releaseDebuggerPauseLocked(s.resuming)
	g.releaseDebuggerPauseLocked(s.pause)
	s.resuming = nil
	t := g.targets[s.target]
	if s.pause != nil && s.pause.published && t != nil && s.generation == t.documentRevision && g.debuggerContextsLocked(s, s.pause) && g.debuggerAdmissionLocked(&command{session: s.owner}) == "" {
		s.resuming = s.pause
	}
	s.pause = nil
	msg, _ := g.newCallLocked(s.owner, "Debugger.resume", nil, false)
	return msg
}

func (g *guard) debuggerResumedLocked(m *message, data []byte) browserVerdict {
	s := g.debuggers[m.SessionID]
	if s == nil || s.resuming == nil {
		return browserVerdict{}
	}
	p := s.resuming
	s.resuming = nil
	if p.published && p.generation == s.generation && g.debuggerContextsLocked(s, p) && g.debuggerAdmissionLocked(&command{session: s.owner}) == "" {
		return browserVerdict{forward: data}
	}
	return browserVerdict{}
}

func (g *guard) privateDebuggerInstrumentationLocked(s *debuggerState, id string) bool {
	info, ok := s.scriptInfo[id]
	if !ok {
		return false
	}
	if info.evaluate {
		return true
	}
	shown := g.shown[s.target]
	if shown == nil {
		return false
	}
	context := shown.contexts[info.context]
	return context != nil && context.unique != "" && context.defaultWorld && context.frame == s.target && s.publishedContexts[info.context] == context.unique
}

func (g *guard) holdDebuggerLocked(m *message, data []byte) browserVerdict {
	s := g.debuggers[m.SessionID]
	if s == nil {
		msg, _ := g.newCallLocked(m.SessionID, "Debugger.resume", nil, false)
		return browserVerdict{send: [][]byte{msg}}
	}
	if !s.enabled || s.full || len(data) > 1<<20 || g.sessionRefusedLocked(m.SessionID) || g.debuggerAdmissionLocked(&command{session: m.SessionID}) != "" {
		return browserVerdict{send: [][]byte{g.resumeDebuggerLocked(s)}}
	}
	var event struct {
		Reason string `json:"reason"`
		Data   struct {
			ScriptID string `json:"scriptId"`
		} `json:"data"`
		CallFrames []struct {
			Location struct {
				ScriptID string `json:"scriptId"`
			} `json:"location"`
		} `json:"callFrames"`
	}
	if json.Unmarshal(m.Params, &event) != nil {
		return browserVerdict{send: [][]byte{g.resumeDebuggerLocked(s)}}
	}
	if event.Reason == "instrumentation" {
		script := event.Data.ScriptID
		if script == "" && len(event.CallFrames) != 0 {
			script = event.CallFrames[0].Location.ScriptID
		}
		if g.privateDebuggerInstrumentationLocked(s, script) {
			resume := g.resumeDebuggerLocked(s)
			s.resuming = nil
			return browserVerdict{send: [][]byte{resume}}
		}
	}
	p := s.pause
	if p == nil {
		p = &debuggerPause{state: s, generation: s.generation, deadline: time.Now().Add(debuggerPauseLimit)}
		s.pause = p
	}
	if p.published || p.verifying || p.generation != s.generation || !time.Now().Before(p.deadline) {
		return browserVerdict{send: [][]byte{g.resumeDebuggerLocked(s)}}
	}
	if event.Reason == "instrumentation" {
		if p.instrumentation || len(s.breakpoints) == 0 {
			return browserVerdict{send: [][]byte{g.resumeDebuggerLocked(s)}}
		}
		p.instrumentation = true
		msg := g.callbackLocked(s.owner, "Debugger.pause", nil, func(answer message) [][]byte {
			if s.pause != p || p.generation != s.generation {
				return nil
			}
			if answer.Error != nil || !time.Now().Before(p.deadline) || g.debuggerAdmissionLocked(&command{session: s.owner}) != "" {
				return [][]byte{g.resumeDebuggerLocked(s)}
			}
			resume, _ := g.newCallLocked(s.owner, "Debugger.resume", nil, false)
			return [][]byte{resume}
		})
		return browserVerdict{send: [][]byte{msg}, debuggerDeadlines: []*debuggerPause{p}}
	}
	return g.holdDebuggerDataLocked(p, data)
}

func (g *guard) holdDebuggerDataLocked(p *debuggerPause, data []byte) browserVerdict {
	if len(data) > g.debuggerLimit-g.debuggerBytes {
		return browserVerdict{send: g.fullDebuggerLocked(p.state)}
	}
	p.verifying = true
	g.debuggerBytes += len(data)
	p.data = append([]byte(nil), data...)
	return browserVerdict{debuggerReads: []*debuggerPause{p}, debuggerDeadlines: []*debuggerPause{p}}
}

func (g *guard) debuggerTreeLocked(s *debuggerState, m message) bool {
	if m.Error != nil || g.debuggerAdmissionLocked(&command{session: s.owner}) != "" || g.sessionRefusedLocked(s.owner) {
		return false
	}
	var result struct {
		FrameTree map[string]any `json:"frameTree"`
	}
	if json.Unmarshal(m.Result, &result) != nil {
		return false
	}
	frame, _ := result.FrameTree["frame"].(map[string]any)
	t := g.targets[s.target]
	if frame["id"] != s.target || frame["loaderId"] != t.document || t.document == "" || t.documentRevision != s.generation {
		return false
	}
	pending := []map[string]any{result.FrameTree}
	for len(pending) != 0 {
		node := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		f, _ := node["frame"].(map[string]any)
		url, _ := f["url"].(string)
		origin, _ := f["securityOrigin"].(string)
		unreachable, _ := f["unreachableUrl"].(string)
		if (protect.Scheme(url) != "http" && protect.Scheme(url) != "https") || (protect.Scheme(origin) != "http" && protect.Scheme(origin) != "https") || g.o.Loaded(url) || g.o.Loaded(origin) || unreachable != "" {
			return false
		}
		children, _ := node["childFrames"].([]any)
		for _, child := range children {
			if child, ok := child.(map[string]any); ok {
				pending = append(pending, child)
			} else {
				return false
			}
		}
	}
	return true
}

func (g *guard) debuggerContextsLocked(s *debuggerState, p *debuggerPause) bool {
	shown := g.shown[s.target]
	if shown == nil || shown.full || len(p.contexts) == 0 {
		return false
	}
	for id, unique := range p.contexts {
		context := shown.contexts[id]
		if unique == "" || context == nil || context.unique != unique || !context.defaultWorld || context.frame != s.target {
			return false
		}
	}
	return true
}

func (g *guard) debuggerIDsLocked(s *debuggerState, p *debuggerPause, root any) bool {
	pending := []any{root}
	for len(pending) != 0 {
		value := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		switch object := value.(type) {
		case map[string]any:
			for key, value := range object {
				id, _ := value.(string)
				switch {
				case key == "scriptId":
					script, ok := s.scripts[id]
					context := g.shown[s.target].contexts[script.context]
					if !ok || context == nil || script.unique == "" || context.unique != script.unique || !context.defaultWorld || context.frame != s.target {
						return false
					}
					p.scripts[id] = true
					p.contexts[script.context] = script.unique
				case key == "callFrameId":
					if !p.frames[id] {
						return false
					}
				case key == "objectId" || strings.HasSuffix(key, "ObjectId"):
					id, ok := remoteContext(id)
					context := g.shown[s.target].contexts[id]
					if !ok || context == nil || p.contexts[id] == "" || p.contexts[id] != context.unique || !context.defaultWorld || context.frame != s.target {
						return false
					}
				}
				pending = append(pending, value)
			}
		case []any:
			pending = append(pending, object...)
		}
	}
	return true
}

func (g *guard) publishDebugger(p *debuggerPause, m message) browserVerdict {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := p.state
	if g.debuggers[s.owner] != s || s.pause != p {
		return browserVerdict{}
	}
	if p.generation != s.generation || !time.Now().Before(p.deadline) || !g.debuggerTreeLocked(s, m) {
		return browserVerdict{send: [][]byte{g.resumeDebuggerLocked(s)}}
	}
	var event struct {
		Params struct {
			CallFrames []struct {
				ID       string `json:"callFrameId"`
				Location struct {
					Script string `json:"scriptId"`
				} `json:"location"`
			} `json:"callFrames"`
		} `json:"params"`
	}
	shown := g.shown[s.target]
	if json.Unmarshal(p.data, &event) != nil || len(event.Params.CallFrames) == 0 || shown == nil || shown.full {
		return browserVerdict{send: [][]byte{g.resumeDebuggerLocked(s)}}
	}
	p.frames = map[string]bool{}
	p.scripts = map[string]bool{}
	p.contexts = map[int64]string{}
	var page [][]byte
	for _, frame := range event.Params.CallFrames {
		script, ok := s.scripts[frame.Location.Script]
		context := shown.contexts[script.context]
		id, placed := remoteContext(frame.ID)
		if !ok || !placed || id != script.context || context == nil || script.unique == "" || context.unique != script.unique || !context.defaultWorld || context.frame != s.target || frame.ID == "" {
			return browserVerdict{send: [][]byte{g.resumeDebuggerLocked(s)}}
		}
		p.frames[frame.ID] = true
		p.contexts[id] = script.unique
		if !p.scripts[frame.Location.Script] {
			page = append(page, script.data)
		}
		p.scripts[frame.Location.Script] = true
	}
	root, ok := decodeObject(p.data)
	if !ok || !g.debuggerIDsLocked(s, p, root) {
		return browserVerdict{send: [][]byte{g.resumeDebuggerLocked(s)}}
	}
	return browserVerdict{page: append(page, p.data)}
}

func (g *guard) recheckDebuggersLocked() [][]byte {
	var send [][]byte
	for owner, s := range g.debuggers {
		t := g.targets[s.target]
		if t == nil || g.sessions[owner] == nil {
			g.dropDebuggerLocked(s)
			continue
		}
		if s.generation != t.documentRevision || g.debuggerAdmissionLocked(&command{session: owner}) != "" || (s.pause != nil && s.pause.published && !g.debuggerContextsLocked(s, s.pause)) {
			if s.pause != nil {
				send = append(send, g.resumeDebuggerLocked(s))
			}
			g.clearDebuggerScriptsLocked(s)
			g.releaseDebuggerPauseLocked(s.resuming)
			clear(s.publishedContexts)
			s.resuming = nil
			s.full = false
			s.generation = t.documentRevision
		}
	}
	return send
}

func (g *guard) expireDebugger(p *debuggerPause) []byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.debuggers[p.state.owner] != p.state || p.state.pause != p {
		return nil
	}
	return g.resumeDebuggerLocked(p.state)
}

func (g *guard) debuggerPlacementPending(p *debuggerPause, m message) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := p.state
	if g.debuggers[s.owner] != s || s.pause != p || p.generation != s.generation || !time.Now().Before(p.deadline) || m.Error != nil || g.debuggerAdmissionLocked(&command{session: s.owner}) != "" || g.sessionRefusedLocked(s.owner) {
		return false
	}
	var result struct {
		FrameTree map[string]any `json:"frameTree"`
	}
	if json.Unmarshal(m.Result, &result) != nil {
		return false
	}
	frame, _ := result.FrameTree["frame"].(map[string]any)
	t := g.targets[s.target]
	if frame["id"] != s.target || frame["loaderId"] != t.document || t.document == "" || t.documentRevision != s.generation {
		return false
	}
	return g.debuggerUnsettledTreeLocked(s.target, result.FrameTree)
}

func (g *guard) debuggerUnsettledTreeLocked(target string, tree map[string]any) bool {
	pending := []map[string]any{tree}
	waiting := false
	for len(pending) != 0 {
		node := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		f, _ := node["frame"].(map[string]any)
		id, valid := f["id"].(string)
		if !valid || id == "" {
			return false
		}
		url, _ := f["url"].(string)
		origin, _ := f["securityOrigin"].(string)
		unreachable, _ := f["unreachableUrl"].(string)
		if g.o.Loaded(url) || g.o.Loaded(origin) || unreachable != "" {
			return false
		}
		ordinary := (protect.Scheme(url) == "http" || protect.Scheme(url) == "https") && (protect.Scheme(origin) == "http" || protect.Scheme(origin) == "https")
		if !ordinary {
			if f["id"] == target || (url != "" && url != "about:blank") || (origin != "" && origin != "://") {
				return false
			}
			waiting = true
		}
		children, _ := node["childFrames"].([]any)
		for _, child := range children {
			child, ok := child.(map[string]any)
			if !ok {
				return false
			}
			pending = append(pending, child)
		}
	}
	return waiting
}

func (g *guard) waitDebuggerPlacement(p *debuggerPause, read func() message, done <-chan struct{}) message {
	answer := read()
	for g.debuggerPlacementPending(p, answer) {
		timer := time.NewTimer(min(25*time.Millisecond, time.Until(p.deadline)))
		select {
		case <-done:
			timer.Stop()
			return message{Error: json.RawMessage(`{"code":-32000}`)}
		case <-timer.C:
		}
		if !time.Now().Before(p.deadline) {
			return message{Error: json.RawMessage(`{"code":-32000}`)}
		}
		answer = read()
	}
	return answer
}

func (rl *relay) debuggerRead(p *debuggerPause) message {
	ctx, cancel := context.WithTimeout(context.Background(), min(debuggerReadLimit, time.Until(p.deadline)))
	defer cancel()
	msg, call := rl.g.call(p.state.owner, "Page.getFrameTree", nil)
	defer rl.g.cancel(call)
	if rl.conn.Write(ctx, websocket.MessageText, msg) == nil {
		select {
		case answer := <-call.ch:
			return answer
		case <-ctx.Done():
		case <-rl.s.Done():
		}
	}
	return message{Error: json.RawMessage(`{"code":-32000}`)}
}

func (rl *relay) writeDebugger(msg []byte) bool {
	ctx, cancel := context.WithTimeout(context.Background(), debuggerReadLimit)
	defer cancel()
	if rl.conn.Write(ctx, websocket.MessageText, msg) != nil {
		rl.browserDone.Store(true)
		rl.kill("ECONNRESET: browser socket closed")
		return false
	}
	return true
}

func (rl *relay) verifyDebugger(p *debuggerPause) {
	v := rl.g.publishDebugger(p, rl.g.waitDebuggerPlacement(p, func() message { return rl.debuggerRead(p) }, rl.s.Done()))
	for _, msg := range v.send {
		rl.writeDebugger(msg)
	}
	if len(v.page) == 0 {
		return
	}
	var frames []byte
	for _, msg := range v.page {
		frames = append(frames, rl.g.pageFrame(msg)...)
	}
	rl.pageMu.Lock()
	rl.g.mu.Lock()
	s := p.state
	var resume []byte
	if rl.g.debuggers[s.owner] == s && s.pause == p && s.generation == p.generation && time.Now().Before(p.deadline) && rl.g.debuggerContextsLocked(s, p) && rl.g.debuggerAdmissionLocked(&command{session: s.owner}) == "" {
		ok, err := rl.s.TryWrite(frames)
		if ok && err == nil {
			p.published = true
			for id, unique := range p.contexts {
				s.publishedContexts[id] = unique
			}
		} else {
			resume = rl.g.resumeDebuggerLocked(s)
		}
	}
	rl.g.mu.Unlock()
	rl.pageMu.Unlock()
	if resume != nil {
		rl.writeDebugger(resume)
	}
}

func (rl *relay) expireDebuggerLater(p *debuggerPause) {
	time.AfterFunc(time.Until(p.deadline), func() {
		if msg := rl.g.expireDebugger(p); msg != nil {
			rl.writeDebugger(msg)
		}
	})
}
