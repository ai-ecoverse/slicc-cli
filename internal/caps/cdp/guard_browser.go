package cdp

import (
	"encoding/json"
	"strings"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

type browserVerdict struct {
	forward []byte
	page    [][]byte
	send    [][]byte
	hold    []string
	extra   []requestKey
	answers []callKey
	lost    bool
}

func (g *guard) fromBrowser(data []byte) browserVerdict {
	var m message
	if err := json.Unmarshal(data, &m); err != nil {
		g.logf("cdp: dropped a browser message that isn't JSON")
		return browserVerdict{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	defer g.resolveUnknownLocked()
	g.progressLocked(&m)
	if m.ID != nil {
		return g.responseLocked(&m, data)
	}
	if m.SessionID != "" && m.SessionID == g.relaySession {
		return g.relayEventLocked(&m)
	}
	if nt, ok := g.navSessions[m.SessionID]; ok && m.SessionID != "" {
		return browserVerdict{send: g.navEventLocked(nt, &m)}
	}
	if g.sessionRefusedLocked(m.SessionID) {
		return browserVerdict{}
	}
	if h, ok := eventHandlers[m.Method]; ok {
		return h(g, &m, data)
	}
	if strings.HasPrefix(m.Method, "Storage.") || strings.HasPrefix(m.Method, "BackgroundService.") || strings.HasPrefix(m.Method, "DOMStorage.") {
		if p, ok := decodeObject(m.Params); ok && g.eventProtected(p, 0) {
			return browserVerdict{}
		}
	}
	return browserVerdict{forward: data}
}

func (g *guard) responseLocked(m *message, data []byte) browserVerdict {
	k := callKey{m.SessionID, *m.ID}
	if rc, ok := g.relayCalls[k]; ok {
		delete(g.relayCalls, k)
		if rc.ch != nil {
			rc.ch <- *m
		}
		if rc.fn != nil {
			return browserVerdict{send: rc.fn(*m)}
		}
		return browserVerdict{}
	}
	kind, ok := g.pending[k]
	delete(g.pending, k)
	g.continuedLocked(k, m.Error != nil)
	attached := g.attachCalls[k]
	g.attachDoneLocked(k)
	bc, body := g.bodyCalls[k]
	delete(g.bodyCalls, k)
	if g.sessionRefusedLocked(m.SessionID) {
		return browserVerdict{send: g.closeStreamLocked(bc, m)}
	}
	if body {
		return g.bodyAnswerLocked(k, bc, m, data)
	}
	if !ok || m.Error != nil || kind == kindPlain {
		return browserVerdict{forward: data}
	}
	res, ok := decodeObject(m.Result)
	if !ok {
		return browserVerdict{forward: data}
	}
	var send [][]byte
	switch kind {
	case kindTargets:
		list, _ := res["targetInfos"].([]any)
		kept := make([]any, 0, len(list))
		for _, item := range list {
			send = append(send, g.infoLocked(item)...)
			if info, _ := toInfo(item); !protect.HiddenType(info.Type) {
				kept = append(kept, item)
			}
		}
		res["targetInfos"] = kept
	case kindTargetInfo:
		send = g.infoLocked(res["targetInfo"])
	case kindCookies:
		res["cookies"] = g.filterCookies(res["cookies"])
	case kindProfile:
		g.scrubProfileLocked(m.SessionID, res)
	case kindStreams:
		g.recordStreamsLocked(res)
	case kindBrowserSession:
		if s, _ := res["sessionId"].(string); s != "" {
			g.browserSessions[s] = true
		}
	case kindContext:
		if s, _ := res["browserContextId"].(string); s != "" {
			g.ownContexts[s] = true
		}
	case kindCreate:
		id, _ := res["targetId"].(string)
		send = g.touchLocked(id, "page")
	case kindAttach:
		if t, ok := g.targets[attached]; ok {
			g.attachAnsweredLocked(m.SessionID, res, t)
			send = g.touchLocked(attached, t.typ)
		}
	}
	m.Result = encode(res)
	return browserVerdict{forward: encode(m), send: send}
}

func toInfo(v any) (targetInfo, map[string]any) {
	obj, _ := v.(map[string]any)
	var info targetInfo
	if obj == nil {
		return info, nil
	}
	_ = json.Unmarshal(encode(obj), &info)
	return info, obj
}

func (g *guard) infoLocked(v any) [][]byte {
	info, obj := toInfo(v)
	if obj == nil || info.TargetID == "" {
		return nil
	}
	send := g.observeLocked(info)
	if g.targetProtectedLocked(info.TargetID) {
		redacted := redactURL(info.URL)
		obj["url"] = redacted
		obj["title"] = redacted
	}
	return send
}

func (g *guard) relayEventLocked(m *message) browserVerdict {
	var p struct {
		TargetInfo targetInfo `json:"targetInfo"`
		TargetID   string     `json:"targetId"`
		SessionID  string     `json:"sessionId"`
	}
	_ = json.Unmarshal(m.Params, &p)
	switch m.Method {
	case "Target.targetCreated", "Target.targetInfoChanged":
		send := g.observeLocked(p.TargetInfo)
		if p.TargetInfo.URL == "" || len(g.held) == 0 {
			return browserVerdict{send: send}
		}
		v := g.releaseLocked(p.TargetInfo.TargetID, decodeParams(m.Params)["targetInfo"])
		v.send = append(send, v.send...)
		return v
	case "Target.targetDestroyed":
		g.destroyedLocked(p.TargetID)
	case "Target.detachedFromTarget":
		g.navDetachedLocked(p.SessionID)
	}
	return browserVerdict{}
}

type eventHandler func(g *guard, m *message, data []byte) browserVerdict

var eventHandlers = map[string]eventHandler{
	"Target.targetCreated":                       (*guard).targetEvent,
	"Target.targetInfoChanged":                   (*guard).targetEvent,
	"Target.targetDestroyed":                     (*guard).targetGoneEvent,
	"Target.targetCrashed":                       (*guard).targetGoneEvent,
	"Target.attachedToTarget":                    (*guard).attachedEvent,
	"Target.detachedFromTarget":                  (*guard).detachedEvent,
	"Target.receivedMessageFromTarget":           (*guard).innerMessageEvent,
	"Page.frameNavigated":                        (*guard).frameNavigatedEvent,
	"Runtime.executionContextCreated":            (*guard).contextCreatedEvent,
	"Runtime.executionContextDestroyed":          (*guard).contextGoneEvent,
	"Runtime.executionContextsCleared":           (*guard).contextsClearedEvent,
	"Runtime.consoleAPICalled":                   (*guard).contextEvent,
	"Runtime.exceptionThrown":                    (*guard).contextEvent,
	"Runtime.bindingCalled":                      (*guard).contextEvent,
	"Debugger.scriptParsed":                      (*guard).scriptEvent,
	"Debugger.scriptFailedToParse":               (*guard).scriptEvent,
	"Debugger.breakpointResolved":                (*guard).breakpointEvent,
	"Debugger.paused":                            (*guard).debuggerPausedEvent,
	"Profiler.consoleProfileStarted":             (*guard).consoleProfileEvent,
	"Profiler.consoleProfileFinished":            (*guard).consoleProfileEvent,
	"Console.messageAdded":                       (*guard).consoleMessageEvent,
	"Log.entryAdded":                             (*guard).logEntryEvent,
	"Profiler.preciseCoverageDeltaUpdate":        (*guard).profileEvent,
	"Network.requestWillBeSent":                  afterRequest((*guard).requestEvent, levelKnown),
	"Network.requestWillBeSentExtraInfo":         (*guard).extraEvent,
	"Network.responseReceived":                   afterRequest((*guard).responseEvent, levelSettled),
	"Network.responseReceivedExtraInfo":          (*guard).extraEvent,
	"Network.loadingFinished":                    afterRequest(forwardEvent, levelSettled),
	"Network.loadingFailed":                      afterRequest(forwardEvent, levelSettled),
	"Network.requestIntercepted":                 afterRequestBy((*guard).pausedEvent, levelSettled, pausedIDOf),
	"Network.webSocketCreated":                   afterRequest((*guard).socketCreatedEvent, levelSettled),
	"Network.webTransportCreated":                afterRequest((*guard).socketCreatedEvent, levelSettled),
	"Network.webSocketWillSendHandshakeRequest":  (*guard).markedDropEvent,
	"Network.webSocketHandshakeResponseReceived": (*guard).markedDropEvent,
	"Network.webSocketFrameError":                (*guard).markedDropEvent,
	"Network.eventSourceMessageReceived":         (*guard).markedDropEvent,
	"Network.dataReceived":                       (*guard).markedDropEvent,
	"Fetch.requestPaused":                        afterRequestBy((*guard).pausedEvent, levelSettled, pausedIDOf),
	"Fetch.authRequired":                         afterRequestBy((*guard).pausedEvent, levelSettled, pausedIDOf),
	"ServiceWorker.workerRegistrationUpdated":    (*guard).swRegistrationsEvent,
	"ServiceWorker.workerVersionUpdated":         (*guard).swVersionsEvent,
	"ServiceWorker.workerErrorReported":          (*guard).swErrorEvent,
}

func (g *guard) withParams(m *message, data []byte, fn func(p map[string]any) (keep, changed bool, send [][]byte)) browserVerdict {
	p, ok := decodeObject(m.Params)
	if !ok {
		return browserVerdict{forward: data}
	}
	keep, changed, send := fn(p)
	switch {
	case !keep:
		return browserVerdict{send: send}
	case changed:
		m.Params = encode(p)
		return browserVerdict{forward: encode(m), send: send}
	}
	return browserVerdict{forward: data, send: send}
}

func (g *guard) targetEvent(m *message, data []byte) browserVerdict {
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		send := g.infoLocked(p["targetInfo"])
		info, _ := toInfo(p["targetInfo"])
		return !protect.HiddenType(info.Type), g.targetProtectedLocked(info.TargetID), send
	})
}

func (g *guard) targetGoneEvent(m *message, data []byte) browserVerdict {
	var p struct {
		TargetID string `json:"targetId"`
	}
	_ = json.Unmarshal(m.Params, &p)
	if g.hiddenTargetLocked(p.TargetID) {
		return browserVerdict{}
	}
	return browserVerdict{forward: data}
}

func (g *guard) hiddenTargetLocked(id string) bool {
	t, ok := g.targets[id]
	return ok && protect.HiddenType(t.typ)
}

func (g *guard) attachedEvent(m *message, data []byte) browserVerdict {
	var p struct {
		SessionID          string     `json:"sessionId"`
		TargetInfo         targetInfo `json:"targetInfo"`
		WaitingForDebugger bool       `json:"waitingForDebugger"`
	}
	_ = json.Unmarshal(m.Params, &p)
	if p.SessionID == "" || p.SessionID == g.relaySession {
		return browserVerdict{}
	}
	info := p.TargetInfo
	if t, ok := g.targets[info.TargetID]; ok && info.URL == "" && t.url != "" {
		info.URL = t.url
	}
	send := g.observeLocked(info)
	if g.targetProtectedLocked(info.TargetID) {
		return browserVerdict{send: append(send, g.hideLocked(&heldSession{id: p.SessionID, parent: m.SessionID}))}
	}
	if params := decodeParams(m.Params); info.URL == "" && (info.Type == "page" || info.Type == "iframe") && g.explicit[info.TargetID] == 0 && params != nil {
		return browserVerdict{send: append(send, g.holdLocked(m.SessionID, params, info, p.WaitingForDebugger)...), hold: []string{p.SessionID}}
	}
	g.sessions[p.SessionID] = &session{id: p.SessionID, target: info.TargetID, parent: m.SessionID, typ: info.Type}
	if info.Type == "browser" {
		g.browserSessions[p.SessionID] = true
	}
	return browserVerdict{forward: data, send: append(send, g.touchLocked(info.TargetID, info.Type)...)}
}

func (g *guard) attachAnsweredLocked(parent string, res map[string]any, t *target) {
	sid, _ := res["sessionId"].(string)
	if sid == "" || sid == g.relaySession || g.sessions[sid] != nil || g.held[sid] != nil || g.hidden[sid] {
		return
	}
	g.sessions[sid] = &session{id: sid, target: t.id, parent: parent, typ: t.typ}
	if t.typ == "browser" {
		g.browserSessions[sid] = true
	}
}

func (g *guard) detachedEvent(m *message, data []byte) browserVerdict {
	var p struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(m.Params, &p)
	if p.SessionID != "" && p.SessionID == g.relaySession {
		g.lost = true
		return browserVerdict{lost: true}
	}
	if _, ok := g.held[p.SessionID]; ok {
		delete(g.held, p.SessionID)
		g.forgetSessionLocked(p.SessionID)
		return browserVerdict{}
	}
	if g.hidden[p.SessionID] {
		delete(g.hidden, p.SessionID)
		g.forgetSessionLocked(p.SessionID)
		return browserVerdict{}
	}
	g.forgetSessionLocked(p.SessionID)
	return browserVerdict{forward: data}
}

func (g *guard) innerMessageEvent(m *message, data []byte) browserVerdict {
	var p struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(m.Params, &p)
	if g.sessionRefusedLocked(p.SessionID) {
		return browserVerdict{}
	}
	return browserVerdict{forward: data}
}

func (g *guard) frameNavigatedEvent(m *message, data []byte) browserVerdict {
	var p struct {
		Frame struct {
			ID             string `json:"id"`
			ParentID       string `json:"parentId"`
			SecurityOrigin string `json:"securityOrigin"`
		} `json:"frame"`
	}
	_ = json.Unmarshal(m.Params, &p)
	t := g.sessionTargetLocked(m.SessionID)
	if t == nil || p.Frame.ParentID != "" || p.Frame.ID != t.id || !g.o.URL(p.Frame.SecurityOrigin) {
		return browserVerdict{forward: data}
	}
	return browserVerdict{send: g.markLocked(t)}
}

func (g *guard) contextCreatedEvent(m *message, data []byte) browserVerdict {
	var p struct {
		Context execContext `json:"context"`
	}
	_ = json.Unmarshal(m.Params, &p)
	t := g.sessionTargetLocked(m.SessionID)
	switch {
	case t == nil:
		return browserVerdict{forward: data}
	case p.Context.AuxData.IsDefault && p.Context.AuxData.FrameID == t.id && g.o.URL(p.Context.Origin):
		return browserVerdict{send: g.markLocked(t)}
	case g.worldContextLocked(t.id, m.SessionID, p.Context):
		return browserVerdict{}
	}
	return browserVerdict{forward: data}
}

func (g *guard) eventProtected(v any, depth int) bool {
	if depth > maxScanDepth {
		return false
	}
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			s, isString := val.(string)
			switch {
			case isString && (urlKeys[k] || k == "scriptURL") && g.o.URL(s):
				return true
			case isString && k == "storageKey" && g.o.StorageKey(s):
				return true
			case !isString && g.eventProtected(val, depth+1):
				return true
			}
		}
	case []any:
		for _, val := range t {
			if g.eventProtected(val, depth+1) {
				return true
			}
		}
	}
	return false
}

func (g *guard) swRegistrationsEvent(m *message, data []byte) browserVerdict {
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		return true, g.filterWorkers(p, "registrations", "scopeURL", "registrationId"), nil
	})
}

func (g *guard) swVersionsEvent(m *message, data []byte) browserVerdict {
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		return true, g.filterWorkers(p, "versions", "scriptURL", "versionId"), nil
	})
}

func (g *guard) swErrorEvent(m *message, data []byte) browserVerdict {
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		e, _ := p["errorMessage"].(map[string]any)
		reg, _ := e["registrationId"].(string)
		return !g.swProtected[reg] && !g.eventProtected(e, 0), false, nil
	})
}

func (g *guard) filterWorkers(p map[string]any, field, urlKey, idKey string) bool {
	list, _ := p[field].([]any)
	kept := make([]any, 0, len(list))
	for _, item := range list {
		w, _ := item.(map[string]any)
		u, _ := w[urlKey].(string)
		id, _ := w[idKey].(string)
		reg, _ := w["registrationId"].(string)
		if g.o.URL(u) || g.swProtected[reg] {
			if len(g.swProtected) < maxMarked {
				g.swProtected[id] = true
			}
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) == len(list) {
		return false
	}
	p[field] = kept
	return true
}
