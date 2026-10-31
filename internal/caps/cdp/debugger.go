package cdp

import (
	"context"
	"encoding/json"
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

type debuggerScript struct {
	context int64
	unique  string
	data    []byte
}

type debuggerState struct {
	owner       string
	target      string
	generation  uint64
	enabled     bool
	full        bool
	scripts     map[string]debuggerScript
	breakpoints map[string]bool
	pause       *debuggerPause
	resuming    *debuggerPause
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
	pause      *debuggerPause
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
	if class == debuggerUnknown {
		return refusedLaterDocuments
	}
	if reason := g.debuggerAdmissionLocked(c); reason != "" {
		return reason
	}
	s := g.debuggers[c.session]
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
	if class == debuggerDocument || class == debuggerControl {
		p := s.pause
		if p == nil || !p.published || !g.debuggerContextsLocked(s, p) || p.generation != s.generation || !time.Now().Before(p.deadline) {
			return refusedTarget
		}
		if c.method == "Debugger.getScriptSource" && !p.scripts[c.str("scriptId")] {
			return refusedTarget
		}
		if c.method == "Debugger.evaluateOnCallFrame" && !p.frames[c.str("callFrameId")] {
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
		s = &debuggerState{owner: c.session, target: t.id, generation: t.documentRevision, scripts: map[string]debuggerScript{}, breakpoints: map[string]bool{}}
		g.debuggers[c.session] = s
	}
	g.debuggerReplies[k] = debuggerReply{state: s, method: c.method, generation: s.generation, pause: s.pause}
	if c.method == "Debugger.resume" {
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
			delete(g.debuggers, s.owner)
		}
	case "Debugger.disable":
		delete(g.debuggers, s.owner)
	case "Debugger.setInstrumentationBreakpoint":
		var result struct {
			BreakpointID string `json:"breakpointId"`
		}
		if m.Error == nil && json.Unmarshal(m.Result, &result) == nil && result.BreakpointID != "" {
			s.breakpoints[result.BreakpointID] = true
		}
	case "Debugger.getScriptSource", "Debugger.evaluateOnCallFrame":
		return s.pause == r.pause && r.pause != nil && r.pause.published && g.debuggerContextsLocked(s, r.pause) && time.Now().Before(r.pause.deadline) && g.debuggerAdmissionLocked(&command{session: s.owner}) == ""
	}
	return true
}

func (g *guard) debuggerEventLocked(m *message, data []byte) (browserVerdict, bool) {
	switch m.Method {
	case "Debugger.paused":
		return g.holdDebuggerLocked(m, data), true
	case "Debugger.scriptParsed", "Debugger.scriptFailedToParse":
		g.debuggerScriptLocked(m, data)
		return browserVerdict{}, true
	case "Debugger.resumed":
		return g.debuggerResumedLocked(m, data), true
	}
	return browserVerdict{}, strings.HasPrefix(m.Method, "Debugger.")
}

func (g *guard) debuggerScriptLocked(m *message, data []byte) {
	g.noteScriptLocked(m)
	s := g.debuggers[m.SessionID]
	if s == nil || g.debuggerAdmissionLocked(&command{session: m.SessionID}) != "" {
		return
	}
	var p struct {
		ScriptID string `json:"scriptId"`
		Context  int64  `json:"executionContextId"`
		URL      string `json:"url"`
	}
	if json.Unmarshal(m.Params, &p) != nil || p.ScriptID == "" || p.Context == 0 || (p.URL != "" && g.o.Loaded(p.URL)) || len(s.scripts) >= maxWorlds || len(data) > 1<<20 {
		s.full = true
		return
	}
	unique := ""
	if shown := g.shown[s.target]; shown != nil && shown.contexts[p.Context] != nil {
		unique = shown.contexts[p.Context].unique
	}
	s.scripts[p.ScriptID] = debuggerScript{context: p.Context, unique: unique, data: append([]byte(nil), data...)}
}

func (g *guard) resumeDebuggerLocked(s *debuggerState) []byte {
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
	}
	if json.Unmarshal(m.Params, &event) != nil {
		return browserVerdict{send: [][]byte{g.resumeDebuggerLocked(s)}}
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
	p.verifying = true
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
			delete(g.debuggers, owner)
			continue
		}
		if s.generation != t.documentRevision || g.debuggerAdmissionLocked(&command{session: owner}) != "" || (s.pause != nil && s.pause.published && !g.debuggerContextsLocked(s, s.pause)) {
			if s.pause != nil {
				send = append(send, g.resumeDebuggerLocked(s))
			}
			clear(s.scripts)
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

func (rl *relay) debuggerRead(s *debuggerState) message {
	ctx, cancel := context.WithTimeout(context.Background(), debuggerReadLimit)
	defer cancel()
	msg, call := rl.g.call(s.owner, "Page.getFrameTree", nil)
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
		_ = rl.conn.CloseNow()
		return false
	}
	return true
}

func (rl *relay) verifyDebugger(p *debuggerPause) {
	v := rl.g.publishDebugger(p, rl.debuggerRead(p.state))
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

func (rl *relay) resolveDebugger(c *command) pageVerdict {
	rl.g.mu.Lock()
	s := rl.g.debuggers[c.session]
	if s == nil || s.pause == nil {
		rl.g.mu.Unlock()
		return pageVerdict{reply: refusal(c.id, c.session, refusedTarget)}
	}
	p := s.pause
	rl.g.mu.Unlock()
	read := rl.debuggerRead(s)
	rl.g.mu.Lock()
	defer rl.g.mu.Unlock()
	if rl.g.debuggers[c.session] != s || s.pause != p || !rl.g.debuggerTreeLocked(s, read) || rl.g.checkLocked(c) != "" {
		return pageVerdict{reply: refusal(c.id, c.session, refusedTarget)}
	}
	return rl.g.admitLocked(c)
}
