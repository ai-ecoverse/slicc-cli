package cdp

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

type extensionPaintDeadline struct {
	key        callKey
	generation uint64
	deadline   time.Time
}

type browserVerdict struct {
	releases          []*link.Reservation
	forward           []byte
	page              [][]byte
	send              [][]byte
	hold              []string
	extra             []requestKey
	answers           []callKey
	pauses            []requestKey
	extensionPaints   []extensionPaintDeadline
	lost              bool
	debuggerReads     []*debuggerPause
	debuggerDeadlines []*debuggerPause
}

func (g *guard) childDocWalkOutsideMu(data []byte, m *message) (method string, out []byte, drop bool, st *childDocStripState, event bool) {
	if m.ID != nil {
		g.mu.Lock()
		method, snap, _ := g.peekChildDocReplyLocked(m)
		g.mu.Unlock()
		if method != "" {
			out, drop, st = applyChildDocOnSnapshot(data, method, snap, m.ID)
		}
		return method, out, drop, st, false
	}
	if !childDocEventWalkMethods[m.Method] || !childDocEventsStrip() || (m.Method == "DOM.childNodeInserted" && !childDocChildInsertedStrip()) {
		return "", nil, false, nil, false
	}
	g.mu.Lock()
	snap := g.snapshotChildDocLocked(m.SessionID)
	g.mu.Unlock()
	out, drop, st = applyChildDocOnSnapshot(data, m.Method, snap, nil)
	return "", out, drop, st, true
}

func (g *guard) fromBrowser(data []byte) (verdict browserVerdict) {
	var m message
	if err := json.Unmarshal(data, &m); err != nil {
		g.logf("cdp: dropped a browser message that isn't JSON")
		return browserVerdict{}
	}
	childMethod, childOut, childDrop, childSt, childEvent := g.childDocWalkOutsideMu(data, &m)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.docIdentitySeq++
	defer func() { verdict.releases = g.takeReleasesLocked() }()
	defer func() { verdict.page = append(verdict.page, g.takeAnsweredHeldLocked()...) }()
	defer g.resolveUnknownLocked()
	frameSend := g.frameEventLocked(&m)
	defer func() {
		verdict.send = append(verdict.send, g.recheckDebuggersLocked()...)
		recheck := m.ID != nil
		switch m.Method {
		case "Target.targetCreated", "Target.targetInfoChanged", "Target.targetDestroyed", "Target.attachedToTarget", "Runtime.executionContextCreated", "Runtime.executionContextDestroyed", "Runtime.executionContextsCleared", "Page.frameAttached", "Page.frameDetached", "Page.frameNavigated":
			recheck = true
		}
		if recheck {
			verdict.send = append(verdict.send, g.probeFrameParentsLocked()...)
			verdict.send = append(verdict.send, g.probeIframeOwnersLocked()...)
			verdict.send = append(verdict.send, g.cutFrameCastsLocked()...)
		}
	}()
	defer func() {
		settled := g.settleHeldWorkersLocked()
		verdict.send = append(verdict.send, settled.send...)
		verdict.page = append(verdict.page, settled.page...)
		unresolved := g.settleUnresolvedHistoryHoldsLocked()
		verdict.send = append(verdict.send, unresolved.send...)
		verdict.page = append(verdict.page, unresolved.page...)
		verdict.page = append(verdict.page, g.takeContextSettlePageLocked()...)
		verdict.send = append(verdict.send, g.takeContextSettleSendLocked()...)
		verdict.send = append(verdict.send, frameSend...)
	}()
	g.progressLocked(&m)
	if m.ID != nil {
		if childMethod != "" {
			return g.finishChildDocReplyLocked(&m, data, childMethod, childOut, childDrop, childSt)
		}
		return g.answerLocked(&m, data)
	}
	if v, handled := g.debuggerEventLocked(&m, data); handled {
		return v
	}
	if m.SessionID != "" && m.SessionID == g.relaySession {
		return g.relayEventLocked(&m)
	}
	if nt, ok := g.navSessions[m.SessionID]; ok && m.SessionID != "" {
		v := g.navRequestLocked(nt, &m)
		v.send = append(v.send, g.navEventLocked(nt, &m)...)
		return g.withPendingPageLocked(v)
	}
	if m.SessionID != "" && g.gone[m.SessionID] {
		return browserVerdict{}
	}
	if g.sessionRefusedLocked(m.SessionID) {
		if m.Method == "Target.attachedToTarget" && (g.attachedWorkerLocked(&m) || g.attachedIframeLocked(&m) || g.held[m.SessionID] != nil) {
			return g.attachedUnderHeldLocked(&m, data)
		}
		return browserVerdict{}
	}
	if !errorPageEvent(m.Method) && (g.errorPageSessionLocked(m.SessionID) || g.sessionUnconfirmedLocked(m.SessionID)) {
		switch m.Method {
		case "Debugger.scriptParsed", "Debugger.scriptFailedToParse":
			g.scriptEvent(&m, data)
		case "Debugger.paused":
			msg, _ := g.newCallLocked(m.SessionID, "Debugger.resume", nil, false)
			return browserVerdict{send: [][]byte{msg}}
		}
		return browserVerdict{}
	}
	if childEvent {
		return g.finishChildDocEventLocked(&m, data, childOut, childDrop, childSt)
	}
	return g.pageBrowserEventLocked(&m, data)
}

func (g *guard) finishChildDocEventLocked(m *message, raw, out []byte, drop bool, st *childDocStripState) browserVerdict {
	rebuild := g.applyChildDocMarksLocked(m.SessionID, st)
	if drop {
		return browserVerdict{page: rebuild}
	}
	if out == nil {
		out = raw
	}
	if childDocBytesReplaced(raw, out) {
		if err := json.Unmarshal(out, m); err != nil {
			return browserVerdict{page: rebuild}
		}
	}
	v := g.pageBrowserEventAfterChildDocLocked(m, out)
	v.page = append(rebuild, v.page...)
	return v
}

func (g *guard) finishChildDocReplyLocked(m *message, raw []byte, method string, out []byte, drop bool, st *childDocStripState) browserVerdict {
	k := callKey{m.SessionID, *m.ID}
	if _, ok := g.pending[k]; !ok {
		return browserVerdict{}
	}
	if g.pendingMethods[k] != method {
		if _, ok := g.takeResponseMetaLocked(m); !ok {
			return browserVerdict{}
		}
		rebuild := g.applyChildDocMarksLocked(m.SessionID, st)
		return browserVerdict{forward: refusal(*m.ID, m.SessionID, refusedTarget), page: rebuild}
	}
	delete(g.pendingMethods, k)
	rebuild := g.applyChildDocMarksLocked(m.SessionID, st)
	if drop {
		return browserVerdict{page: rebuild}
	}
	if out == nil {
		if st != nil && st.hitProtected {
			out = refusal(*m.ID, m.SessionID, refusedTarget)
		} else {
			out = raw
		}
	}
	if childDocBytesReplaced(raw, out) {
		g.refreshChildDocMessage(m, out)
	}
	v := g.answerLocked(m, out)
	v.page = append(rebuild, v.page...)
	return v
}

func (g *guard) pageBrowserEventAfterChildDocLocked(m *message, data []byte) browserVerdict {
	if h, ok := eventHandlers[m.Method]; ok {
		return h(g, m, data)
	}
	if g.storageEventDropped(m) {
		return browserVerdict{}
	}
	if g.frameContentEventDropped(m) {
		return browserVerdict{}
	}
	return browserVerdict{forward: data}
}

func (g *guard) pageBrowserEventLocked(m *message, data []byte) browserVerdict {
	return g.pageBrowserEventAfterChildDocLocked(m, data)
}

func (g *guard) storageEventDropped(m *message) bool {
	if !strings.HasPrefix(m.Method, "Storage.") && !strings.HasPrefix(m.Method, "BackgroundService.") && !strings.HasPrefix(m.Method, "DOMStorage.") {
		return false
	}
	p, ok := decodeObject(m.Params)
	return ok && (g.eventProtected(p, 0) || credentialNamed(p, 0))
}

func (g *guard) ownsClientAnswerLocked(k callKey) bool {
	g.expireRetiredLocked(time.Now())
	if _, retired := g.retiredCalls[k]; retired {
		delete(g.retiredCalls, k)
		return false
	}
	_, ok := g.pending[k]
	return ok
}

func (g *guard) relayAnswerLocked(k callKey, m *message) (browserVerdict, bool) {
	rc, ok := g.relayCalls[k]
	if !ok {
		return browserVerdict{}, false
	}
	delete(g.relayCalls, k)
	if rc.ch != nil {
		rc.ch <- *m
	}
	if rc.fn != nil {
		return browserVerdict{send: rc.fn(*m), page: g.takePendingPageLocked()}, true
	}
	return browserVerdict{page: g.takePendingPageLocked()}, true
}

type responseMeta struct {
	k           callKey
	kind        callKind
	childMethod string
	ownerFrame  string
	ownerCall   bool
	versionCall bool
	markMethod  string
	treeCall    bool
	attached    string
	bc          bodyCall
	body        bool
	outlived    bool
}

func (g *guard) takeResponseMetaLocked(m *message) (responseMeta, bool) {
	k := callKey{m.SessionID, *m.ID}
	if !g.ownsClientAnswerLocked(k) {
		g.dropChildDocMethodLocked(k)
		return responseMeta{}, false
	}
	meta := responseMeta{k: k}
	meta.ownerFrame, meta.ownerCall = g.extensionOwnerCalls[k]
	delete(g.extensionOwnerCalls, k)
	meta.versionCall = g.versionCalls[k]
	delete(g.versionCalls, k)
	meta.markMethod = g.extensionMarkCalls[k]
	delete(g.extensionMarkCalls, k)
	meta.kind = g.pending[k]
	meta.childMethod = g.pendingMethods[k]
	delete(g.pending, k)
	delete(g.pendingMethods, k)
	meta.treeCall = g.treeCalls[k]
	delete(g.treeCalls, k)
	g.clientRequestAnsweredLocked(k, m.Error != nil)
	g.continuedLocked(k, m.Error != nil)
	meta.attached = g.attachCalls[k]
	g.attachDoneLocked(k)
	meta.bc, meta.body = g.bodyCalls[k]
	delete(g.bodyCalls, k)
	meta.outlived = g.outlivedLocked(k)
	return meta, true
}

func (g *guard) responseLocked(m *message, data []byte) browserVerdict {
	k := callKey{m.SessionID, *m.ID}
	if v, handled := g.relayAnswerLocked(k, m); handled {
		return v
	}
	meta, ok := g.takeResponseMetaLocked(m)
	if !ok {
		return browserVerdict{}
	}
	acceptedAttach := false
	defer func() {
		if meta.kind == kindAttach && !acceptedAttach {
			g.dropPendingTouchLocked(meta.k)
		}
	}()
	if !g.debuggerReplyLocked(meta.k, m) {
		return browserVerdict{forward: refusal(*m.ID, m.SessionID, refusedTarget)}
	}
	if g.sessionRefusedLocked(m.SessionID) {
		return browserVerdict{forward: refusal(*m.ID, m.SessionID, refusedTarget), send: g.closeStreamLocked(meta.bc, m)}
	}
	if meta.outlived {
		g.logf("cdp: dropped an answer from before a protected error page committed")
		return browserVerdict{forward: refusal(*m.ID, m.SessionID, refusedOutlived), send: g.closeStreamLocked(meta.bc, m)}
	}
	data = g.plainErrorLocked(m, data)
	if stripped, drop, handled := g.stripChildDocReplyBeforeHoldLocked(m, data, meta.childMethod); handled {
		if drop {
			return browserVerdict{}
		}
		data = stripped
	}
	if meta.body {
		return g.bodyAnswerLocked(meta.k, meta.bc, m, data)
	}
	if meta.ownerCall {
		return g.extensionOwnerAnswerLocked(meta.ownerFrame, m, data)
	}
	g.noteVersionReplyLocked(meta.k, !meta.versionCall || m.Error != nil)
	g.noteExtensionMarkReplyLocked(meta.k, meta.markMethod, m.Error != nil)
	var treePage, treeSend [][]byte
	if meta.treeCall && m.Error == nil {
		if t := g.sessionTargetLocked(m.SessionID); t != nil {
			if g.treeMainProtectedLocked(t, m.Result) {
				return browserVerdict{forward: refusal(*m.ID, m.SessionID, refusedTarget), send: g.markLocked(t)}
			}
			g.noteFrameTreeResultLocked(t.id, m.Result)
		}
		treePage = g.takeContextSettlePageLocked()
		treeSend = g.takeContextSettleSendLocked()
	}
	if m.Error != nil || meta.kind == kindPlain {
		return browserVerdict{forward: data, send: treeSend, page: treePage}
	}
	res, ok := decodeObject(m.Result)
	if !ok {
		return browserVerdict{forward: data, send: treeSend, page: treePage}
	}
	if meta.kind == kindAttach {
		if v, handled, accepted := g.attachReplyHoldLocked(meta.attached, m, res, treeSend, treePage); handled {
			acceptedAttach = accepted
			return v
		}
	}
	send, hide := g.applyResultKindLocked(meta.kind, meta.attached, m.SessionID, res)
	send = append(treeSend, send...)
	acceptedAttach = meta.kind != kindAttach || !hide
	if hide {
		return g.hiddenResultLocked(meta.kind, m, res, browserVerdict{send: send, page: treePage})
	}
	m.Result = encode(res)
	return browserVerdict{forward: encode(m), send: send, page: treePage}
}

func (g *guard) attachReplyHoldLocked(attached string, m *message, res map[string]any, treeSend, treePage [][]byte) (browserVerdict, bool, bool) {
	if refused, cutSend := g.attachReplyCutLocked(attached, m, res); refused {
		return browserVerdict{forward: refusal(*m.ID, m.SessionID, refusedTarget), send: append(treeSend, cutSend...), page: append(treePage, g.takePendingPageLocked()...)}, true, false
	}
	if held, holdSend, holdPage := g.holdHistoryAttachReplyLocked(attached, m, res); held {
		sid, _ := res["sessionId"].(string)
		return browserVerdict{send: append(treeSend, holdSend...), page: append(treePage, holdPage...), hold: []string{sid}}, true, true
	}
	if held, holdSend, holdPage := g.holdDocConfirmReplyLocked(attached, m, res); held {
		holdSend = append(holdSend, g.noteExplicitDocConfirmObserveLocked(attached)...)
		return browserVerdict{send: append(treeSend, holdSend...), page: append(treePage, holdPage...)}, true, true
	}
	return browserVerdict{}, false, false
}

func (g *guard) applyResultKindLocked(kind callKind, attached, session string, res map[string]any) ([][]byte, bool) {
	switch kind {
	case kindTargets:
		return g.applyTargetsResultLocked(res), false
	case kindTargetInfo:
		info, _ := toInfo(res["targetInfo"])
		send := g.infoLocked(res["targetInfo"])
		hide := g.listingHiddenLocked(info)
		if !hide {
			g.noteWorkerShownLocked(info)
		}
		return send, hide
	case kindCookies:
		res["cookies"] = g.filterCookies(res["cookies"])
	case kindTrustTokens:
		res["tokens"] = filterTrustTokens(res["tokens"])
	case kindAffectedURLs:
		res["matchedUrls"] = filterStrings(res["matchedUrls"], protect.CredentialURL)
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
		return g.touchLocked(id, "page"), false
	case kindAttach:
		return g.applyAttachResultLocked(attached, session, res)
	case kindScript:
		if id, _ := res["identifier"].(string); id != "" {
			g.scripts[admittedScriptKey(session, id)] = true
		}
	}
	return nil, false
}

func (g *guard) applyTargetsResultLocked(res map[string]any) [][]byte {
	list, _ := res["targetInfos"].([]any)
	kept := make([]any, 0, len(list))
	var send [][]byte
	for _, item := range list {
		info, _ := toInfo(item)
		if info.TargetID != "" && !g.opened[info.TargetID] {
			g.listed[info.TargetID] = true
			if !g.preserveOpenedSeenLocked(info.TargetID) {
				g.forgetOpenedSeenLocked(info.TargetID)
			}
		}
		send = append(send, g.infoLocked(item)...)
		if g.listedLocked(info) && !g.listingHiddenLocked(info) {
			kept = append(kept, item)
			g.noteWorkerShownLocked(info)
		}
	}
	res["targetInfos"] = kept
	return send
}

func (g *guard) attachReplyCutLocked(attached string, m *message, res map[string]any) (bool, [][]byte) {
	if !attachReplyRefuseProtected() {
		return false, nil
	}
	sid, _ := res["sessionId"].(string)
	t := g.targets[attached]
	if sid == "" || sid == g.relaySession || t == nil || t.typ == "browser" || t.typ == "iframe" || workerTypes[t.typ] {
		return false, nil
	}
	s := g.sessions[sid]
	cut := g.hidden[sid] || g.refused[sid] || g.docFailed[sid] || (s != nil && s.refused)
	if !cut && !t.protected {
		return false, nil
	}
	g.logf("cdp: refused an attach answered after its target moved onto a protected origin target=%s session=%s", t.id, sid)
	g.noteAttachCutLocked(sid)
	if dc := g.docInflight[attached]; dc != nil && !dc.done && dc.holds[sid] != nil {
		return true, g.failDocConfirmLocked(dc)
	}
	if cut || g.gone[sid] {
		return true, nil
	}
	g.noteHiddenLocked(sid, t.id)
	g.addRefusedLocked(sid)
	if g.docFailed == nil {
		g.docFailed = map[string]bool{}
	}
	g.docFailed[sid] = true
	msg, _ := g.newCallLocked(m.SessionID, "Target.detachFromTarget", map[string]any{"sessionId": sid}, false)
	return true, [][]byte{msg}
}

func (g *guard) applyAttachResultLocked(attached, session string, res map[string]any) ([][]byte, bool) {
	if g.workerConcealedLocked(attached) || g.protectedWorkerIDLocked(attached) || g.iframeConcealedLocked(attached) || g.protectedIframeIDLocked(attached) {
		return nil, true
	}
	g.markTouchedLocked(attached)
	if t, ok := g.targets[attached]; ok {
		if workerTypes[t.typ] || t.typ == "iframe" {
			t.shown = true
		}
		g.attachAnsweredLocked(session, res, t)
		send := g.touchLocked(attached, t.typ)
		send = append(send, g.noteExplicitDocConfirmObserveLocked(attached)...)
		return send, false
	}
	g.logf("cdp: attach counted as touch without a known target target=%s", attached)
	return nil, false
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
	if g.blankFailedLoadInfoLocked(obj, info) {
		return send
	}
	g.redactInfoLocked(obj, info)
	return send
}

func (g *guard) redactInfoLocked(obj map[string]any, info targetInfo) bool {
	if obj == nil {
		return false
	}
	if g.blankFailedLoadInfoLocked(obj, info) {
		return true
	}
	if !g.targetProtectedLocked(info.TargetID) && !g.infoVerdictLocked(info).protected {
		return false
	}
	if frameCarrierListingRedactEnabled() {
		redacted := redactURL(info.URL)
		obj["url"] = redacted
		obj["title"] = redacted
	}
	return true
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
		g.noteStreamOpenedLocked(p.TargetInfo)
		send := g.observeLocked(p.TargetInfo)
		if p.TargetInfo.URL == "" || len(g.held) == 0 {
			return browserVerdict{send: send}
		}
		v := g.releaseLocked(p.TargetInfo.TargetID, decodeParams(m.Params)["targetInfo"])
		v.send = append(send, v.send...)
		return v
	case "Target.targetDestroyed":
		page, goneSend := g.destroyedLocked(p.TargetID)
		return browserVerdict{send: goneSend, page: page}
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
	"Inspector.targetCrashed":                    (*guard).workerEndedEvent,
	"Target.attachedToTarget":                    (*guard).attachedEvent,
	"Target.detachedFromTarget":                  (*guard).detachedEvent,
	"Target.receivedMessageFromTarget":           (*guard).innerMessageEvent,
	"Page.frameNavigated":                        (*guard).frameNavigatedEvent,
	"Page.frameStartedNavigating":                (*guard).frameStartedNavigatingEvent,
	"Page.documentOpened":                        (*guard).documentOpenedEvent,
	"Page.screencastFrame":                       (*guard).screencastFrameEvent,
	"Runtime.executionContextCreated":            (*guard).contextCreatedEvent,
	"Runtime.executionContextDestroyed":          (*guard).contextGoneEvent,
	"Runtime.executionContextsCleared":           (*guard).contextsClearedEvent,
	"Runtime.consoleAPICalled":                   (*guard).contextEvent,
	"Runtime.exceptionThrown":                    (*guard).contextEvent,
	"Runtime.bindingCalled":                      (*guard).contextEvent,
	"Debugger.scriptParsed":                      (*guard).scriptEvent,
	"CSS.styleSheetAdded":                        (*guard).styleSheetEvent,
	"Debugger.scriptFailedToParse":               (*guard).scriptEvent,
	"Debugger.breakpointResolved":                (*guard).breakpointEvent,
	"Debugger.paused":                            (*guard).debuggerPausedEvent,
	"Profiler.consoleProfileStarted":             (*guard).consoleProfileEvent,
	"Profiler.consoleProfileFinished":            (*guard).consoleProfileEvent,
	"Console.messageAdded":                       (*guard).consoleMessageEvent,
	"Log.entryAdded":                             (*guard).logEntryEvent,
	"Audits.issueAdded":                          (*guard).auditIssueEvent,
	"Network.requestWillBeSent":                  afterRequest((*guard).requestEvent, levelKnown),
	"Network.requestWillBeSentExtraInfo":         (*guard).extraEvent,
	"Network.responseReceived":                   afterRequest((*guard).responseEvent, levelSettled),
	"Network.responseReceivedExtraInfo":          (*guard).extraEvent,
	"Network.loadingFinished":                    afterRequest(forwardEvent, levelSettled),
	"Network.loadingFailed":                      afterRequest(forwardEvent, levelSettled),
	"Network.requestIntercepted":                 (*guard).pauseGateEvent,
	"Network.webSocketCreated":                   afterRequest((*guard).socketCreatedEvent, levelSettled),
	"Network.webTransportCreated":                afterRequest((*guard).socketCreatedEvent, levelSettled),
	"Network.webSocketWillSendHandshakeRequest":  (*guard).markedDropEvent,
	"Network.webSocketHandshakeResponseReceived": (*guard).markedDropEvent,
	"Network.webSocketFrameError":                (*guard).markedDropEvent,
	"Network.eventSourceMessageReceived":         (*guard).markedDropEvent,
	"Network.dataReceived":                       (*guard).markedDropEvent,
	"Fetch.requestPaused":                        (*guard).pauseGateEvent,
	"Fetch.authRequired":                         (*guard).pauseGateEvent,
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
	var v browserVerdict
	switch {
	case !keep:
		v = browserVerdict{send: send}
	case changed:
		m.Params = encode(p)
		v = browserVerdict{forward: encode(m), send: send}
	default:
		v = browserVerdict{forward: data, send: send}
	}
	return g.withPendingPageLocked(v)
}

func (g *guard) targetEvent(m *message, data []byte) browserVerdict {
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		info, obj := toInfo(p["targetInfo"])
		g.noteStreamOpenedLocked(info)
		send := g.infoLocked(p["targetInfo"])
		if g.listingHiddenLocked(info) {
			return false, false, send
		}
		keep := g.listedLocked(info)
		if keep {
			g.noteWorkerShownLocked(info)
		}
		st := g.errorProofOfLocked(info.TargetID)
		blanked := obj != nil && (st == errorProofPending || st == errorProofFailed || st == errorProofProtected || g.failedLoadNeedsProofLocked(g.targets[info.TargetID]))
		changed := obj != nil && (g.targetProtectedLocked(info.TargetID) || g.infoVerdictLocked(info).protected || blanked)
		return keep, changed, send
	})
}

func (g *guard) protectedWorkerLocked(info targetInfo) bool {
	return workerTypes[info.Type] && (g.targetProtectedLocked(info.TargetID) || g.infoVerdictLocked(info).loaded() || g.workerConcealedLocked(info.TargetID))
}

func (g *guard) iframeHiddenForParentLocked(t *target) bool {
	return t != nil && g.iframeFromProtectedAncestorLocked(t)
}

func (g *guard) protectedIframeLocked(info targetInfo) bool {
	if info.Type != "iframe" {
		return false
	}
	t := g.targets[info.TargetID]
	v := g.infoVerdictLocked(info)
	if g.extensionFrameTargetLocked(t) || v.extensionFrame {
		return g.iframeHiddenForParentLocked(t)
	}
	return g.targetProtectedLocked(info.TargetID) || v.loaded() || g.iframeConcealedLocked(info.TargetID)
}

func (g *guard) listingHiddenLocked(info targetInfo) bool {
	return g.protectedWorkerLocked(info) || g.protectedIframeLocked(info)
}

func (g *guard) protectedWorkerIDLocked(id string) bool {
	t := g.targets[id]
	return t != nil && workerTypes[t.typ] && t.protected
}

func (g *guard) protectedIframeIDLocked(id string) bool {
	t := g.targets[id]
	if t == nil || t.typ != "iframe" {
		return false
	}
	if g.extensionFrameTargetLocked(t) {
		return g.iframeHiddenForParentLocked(t)
	}
	return t.protected
}

func (g *guard) listedLocked(info targetInfo) bool {
	return !protect.HiddenType(info.Type) && !g.infoVerdictLocked(info).unparseable
}

func (g *guard) targetGoneEvent(m *message, data []byte) browserVerdict {
	var p struct {
		TargetID string `json:"targetId"`
	}
	_ = json.Unmarshal(m.Params, &p)
	var send [][]byte
	var page [][]byte
	if m.Method == "Target.targetDestroyed" {
		g.forgetAncestryDisputedLocked(p.TargetID)
		page, send = g.destroyedLocked(p.TargetID)
	}
	if g.hiddenTargetLocked(p.TargetID) || (m.Method == "Target.targetDestroyed" && (g.protectedWorkerIDLocked(p.TargetID) || g.workerConcealedLocked(p.TargetID) || g.workerUnshownLocked(p.TargetID) || g.protectedIframeIDLocked(p.TargetID) || g.iframeConcealedLocked(p.TargetID) || g.iframeUnshownLocked(p.TargetID))) {
		return browserVerdict{send: send, page: page}
	}
	return browserVerdict{forward: data, send: send, page: page}
}

func (g *guard) hiddenTargetLocked(id string) bool {
	t, ok := g.targets[id]
	return ok && protect.HiddenType(t.typ)
}

func (g *guard) attachedWorkerLocked(m *message) bool {
	var p struct {
		TargetInfo targetInfo `json:"targetInfo"`
	}
	_ = json.Unmarshal(m.Params, &p)
	return workerTypes[p.TargetInfo.Type]
}

func (g *guard) attachedIframeLocked(m *message) bool {
	var p struct {
		TargetInfo targetInfo `json:"targetInfo"`
	}
	_ = json.Unmarshal(m.Params, &p)
	return p.TargetInfo.Type == "iframe"
}

func (g *guard) attachedShouldHideLocked(info targetInfo, parentSession string) bool {
	if info.URL == "" && g.blockedChildren[info.TargetID] != "" {
		return true
	}
	if workerTypes[info.Type] && g.sessionRefusedLocked(parentSession) {
		return true
	}
	return g.targetProtectedLocked(info.TargetID)
}

func (g *guard) attachedHoldParams(m *message, pSession string, waiting bool, info targetInfo) map[string]any {
	params := decodeParams(m.Params)
	if params != nil {
		return params
	}
	return map[string]any{"sessionId": pSession, "waitingForDebugger": waiting, "targetInfo": map[string]any{"targetId": info.TargetID, "type": info.Type, "url": info.URL, "openerId": info.OpenerID, "openerFrameId": info.OpenerFrameID, "parentFrameId": info.ParentFrameID, "parentId": info.ParentID}}
}

func (g *guard) attachedUnderHeldLocked(m *message, data []byte) browserVerdict {
	v := g.attachedEvent(m, data)
	v.forward = nil
	v.page = nil
	return v
}

func (g *guard) attachedHideOrHoldLocked(m *message, pSession string, waiting bool, info targetInfo, send [][]byte) (browserVerdict, bool) {
	return g.attachedHideOrHoldDecisionLocked(m, pSession, waiting, info, send, true)
}

func (g *guard) attachedHideOrHoldDecisionLocked(m *message, pSession string, waiting bool, info targetInfo, send [][]byte, allowHistory bool) (browserVerdict, bool) {
	if workerTypes[info.Type] && g.sessionRefusedLocked(m.SessionID) {
		if t := g.targets[info.TargetID]; t != nil {
			t.protected = true
		}
	}
	if g.attachedShouldHideLocked(info, m.SessionID) {
		h := &heldSession{id: pSession, parent: m.SessionID, target: info.TargetID, typ: info.Type, waiting: waiting, at: time.Now()}
		send = append(send, g.hideLocked(h)...)
		var page [][]byte
		if info.Type == "page" {
			var part browserVerdict
			if !g.browserRootSessionLocked(m.SessionID) {
				part = g.detachAnnouncedTabSessionLocked(m.SessionID, true)
			} else {
				part = g.detachUnmatchedEmptyTabsAroundLocked(time.Now(), true, "immediate hide", info.TargetID)
			}
			send = append(send, part.send...)
			page = append(page, part.page...)
		}
		return browserVerdict{send: send, page: page}, true
	}
	if v, handled := g.holdFailedLoadAttachLocked(m, pSession, waiting, info, send); handled {
		return v, true
	}
	if allowHistory {
		if v, handled := g.historyHoldAttachLocked(m, pSession, waiting, info, send); handled {
			return v, true
		}
	}
	if g.workerUnresolvedLocked(g.targets[info.TargetID]) || g.iframeUnresolvedLocked(g.targets[info.TargetID]) {
		return browserVerdict{send: append(send, g.holdLocked(m.SessionID, g.attachedHoldParams(m, pSession, waiting, info), info, waiting)...), hold: []string{pSession}}, true
	}
	if params := decodeParams(m.Params); info.URL == "" && (info.Type == "page" || info.Type == "iframe") && g.explicit[info.TargetID] == 0 && params != nil {
		return browserVerdict{send: append(send, g.holdLocked(m.SessionID, params, info, waiting)...), hold: []string{pSession}}, true
	}
	return browserVerdict{}, false
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
	g.noteStreamOpenedLocked(info)
	g.attachParent = m.SessionID
	var send [][]byte
	if info.Type == "iframe" {
		send = append(send, g.noteIframeAttachParentLocked(info)...)
	}
	send = append(send, g.observeLocked(info)...)
	if s := g.sessions[p.SessionID]; attachAnnounceDropCut() && (g.docFailed[p.SessionID] || g.attachCut[p.SessionID] || (s != nil && s.refused)) {
		g.attachParent = ""
		return browserVerdict{send: send}
	}
	if v, handled := g.attachedHideOrHoldLocked(m, p.SessionID, p.WaitingForDebugger, info, send); handled {
		g.attachParent = ""
		return v
	}
	if v, handled := g.docConfirmAttachEventLocked(m, data, p.SessionID, p.WaitingForDebugger, info, send); handled {
		g.attachParent = ""
		return v
	}
	g.attachParent = ""
	g.noteWorkerShownLocked(info)
	g.sessions[p.SessionID] = &session{id: p.SessionID, target: info.TargetID, parent: m.SessionID, typ: info.Type, waiting: p.WaitingForDebugger, emptyAtAnnounce: info.Type == "tab" && info.URL == "", at: time.Now()}
	if info.Type == "browser" {
		g.browserSessions[p.SessionID] = true
	}
	if info.Type == "tab" {
		g.adoptHeldFlatPageLocked(p.SessionID, info.OpenerID)
	}
	send = append(send, g.touchLocked(info.TargetID, info.Type)...)
	if info.Type == "page" {
		send = append(send, g.scheduleDocConfirmObserveLocked(info.TargetID, g.docConfirmObservePathLocked(info.TargetID, info.Type))...)
	}
	if params := decodeParams(m.Params); params != nil {
		if obj, _ := params["targetInfo"].(map[string]any); g.redactInfoLocked(obj, info) {
			m.Params = encode(params)
			return browserVerdict{forward: encode(m), send: send}
		}
	}
	return browserVerdict{forward: data, send: send}
}

func (g *guard) workerEndedEvent(m *message, data []byte) browserVerdict {
	s, ok := g.sessions[m.SessionID]
	if !ok || s.typ != "shared_worker" {
		return browserVerdict{forward: data}
	}
	g.logf("cdp: detached a session on a shared worker that ended target=%s session=%s parent=%s", s.target, s.id, s.parent)
	var send [][]byte
	if s.waiting {
		send = append(send, g.resumeLocked(s.id, s.target))
	}
	msg, _ := g.newCallLocked(s.parent, "Target.detachFromTarget", map[string]any{"sessionId": s.id}, false)
	return browserVerdict{forward: data, send: append(send, msg)}
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
	if p.SessionID != "" && g.gone[p.SessionID] {
		return browserVerdict{}
	}
	page, send := g.cascadeOrphansLocked(p.SessionID)
	page = append(page, g.takePendingMissingLocked(p.SessionID)...)
	g.navDetachedLocked(p.SessionID)
	if h, ok := g.held[p.SessionID]; ok {
		g.logf("cdp: Chrome detached a held auto-attached session target=%s session=%s parent=%s waiting=%t", h.target, h.id, h.parent, h.waiting)
		delete(g.held, p.SessionID)
		g.refuseHeldWaitLocked(h.target)
		if h.historyParent != "" || h.historyUnresolved {
			g.answerHistoryHeldReplyLocked(h)
		}
		part := g.detachAnnouncedTabsForPageLocked(h, true, "destroy-while-held")
		g.forgetSessionLocked(p.SessionID)
		return browserVerdict{send: append(send, part.send...), page: append(append(append(page, part.page...), g.takeAnsweredHeldLocked()...), g.takePendingPageLocked()...)}
	}
	if g.hidden[p.SessionID] {
		delete(g.hidden, p.SessionID)
		delete(g.hiddenOwner, p.SessionID)
		g.forgetSessionLocked(p.SessionID)
		return browserVerdict{send: send, page: append(page, g.takePendingPageLocked()...)}
	}
	g.forgetSessionLocked(p.SessionID)
	page = append(page, data)
	return browserVerdict{send: send, page: append(page, g.takePendingPageLocked()...)}
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

func (g *guard) frameStartedNavigatingEvent(m *message, data []byte) browserVerdict {
	var p struct {
		FrameID string `json:"frameId"`
		URL     string `json:"url"`
	}
	_ = json.Unmarshal(m.Params, &p)
	t := g.sessionTargetLocked(m.SessionID)
	if t != nil && p.FrameID == t.id && g.errorProofOfLocked(t.id) != errorProofNone {
		if chromeErrorDocument(t.url) || chromeErrorDocument(p.URL) || p.URL == "" {
			g.resetFailedLoadProofLocked(t.id)
		}
	}
	if nt, ok := g.navSessions[m.SessionID]; ok {
		return browserVerdict{forward: data, send: g.navEventLocked(nt, m)}
	}
	return browserVerdict{forward: data}
}

func (g *guard) frameNavigatedEvent(m *message, data []byte) browserVerdict {
	var p struct {
		Type  string `json:"type"`
		Frame struct {
			ID             string `json:"id"`
			ParentID       string `json:"parentId"`
			SecurityOrigin string `json:"securityOrigin"`
			URL            string `json:"url"`
			UnreachableURL string `json:"unreachableUrl"`
		} `json:"frame"`
	}
	_ = json.Unmarshal(m.Params, &p)
	var decided [][]byte
	if p.Frame.ID != "" {
		raw := p.Frame.URL
		if protect.Inheriting(raw) {
			raw = p.Frame.SecurityOrigin
		}
		decided = g.noteChildFrameLocked(p.Frame.ID, raw)
		g.queueContextSettleLocked()
	}
	var cut [][]byte
	if page := g.worldTargetLocked(m.SessionID); page != "" && p.Frame.ID != "" && p.Frame.ParentID != "" {
		raw := p.Frame.URL
		if g.extensionFrameURLLocked(p.Frame.UnreachableURL) {
			raw = p.Frame.UnreachableURL
		}
		if g.extensionFrameURLLocked(raw) {
			g.noteExtensionFrameURLLocked(page, p.Frame.ID, raw)
			cut = append(cut, g.cutCastsLocked(page)...)
		} else {
			g.forgetExtensionFrameURLLocked(page, p.Frame.ID)
		}
	}
	t := g.sessionTargetLocked(m.SessionID)
	settle := g.takeContextSettlePageLocked()
	settleSend := g.takeContextSettleSendLocked()
	if t == nil || (p.Frame.ParentID != "" && t.typ != "iframe") || p.Frame.ID != t.id {
		return browserVerdict{forward: data, send: append(append(decided, cut...), settleSend...), page: settle}
	}
	if g.o.Loaded(p.Frame.SecurityOrigin) || g.o.Loaded(p.Frame.URL) {
		return browserVerdict{send: append(append(decided, g.markLocked(t)...), settleSend...), page: settle}
	}
	send := append(append(decided, cut...), settleSend...)
	send = append(send, g.failedLoadFrameNavigatedLocked(t, m, p.Type, p.Frame.URL, p.Frame.UnreachableURL)...)
	return browserVerdict{forward: data, send: send, page: settle}
}

func (g *guard) failedLoadFrameNavigatedLocked(t *target, m *message, navType, frameURL, unreachable string) [][]byte {
	if t == nil {
		return nil
	}
	if chromeErrorDocument(frameURL) || unreachable != "" || navType == "BackForwardCacheRestore" || strings.HasPrefix(frameURL, "chrome-error:") {
		if g.errorProofOfLocked(t.id) != errorProofNone {
			g.resetFailedLoadProofLocked(t.id)
		}
	}
	nt := g.navTargets[t.id]
	if nt == nil {
		return nil
	}
	if (nt.confirm != nil && nt.confirm.done && nt.confirm.failed) || navType == "BackForwardCacheRestore" || unreachable != "" || strings.HasPrefix(frameURL, "chrome-error:") || protect.Inheriting(frameURL) || protect.Unparseable(frameURL) {
		g.beginConfirmLocked(nt)
	}
	return g.navEventLocked(nt, m)
}

func (g *guard) contextCreatedEvent(m *message, data []byte) (verdict browserVerdict) {
	var p struct {
		Context execContext `json:"context"`
	}
	_ = json.Unmarshal(m.Params, &p)
	var decided [][]byte
	if p.Context.AuxData.IsDefault {
		decided = g.noteChildFrameLocked(p.Context.AuxData.FrameID, p.Context.Origin)
		g.queueContextSettleLocked()
	}
	t := g.sessionTargetLocked(m.SessionID)
	settle := g.takeContextSettlePageLocked()
	settleSend := g.takeContextSettleSendLocked()
	defer func() {
		if t != nil {
			g.trackSoftLatchPageLocked(t.id)
		}
	}()
	switch {
	case t == nil:
		verdict = browserVerdict{forward: data, send: append(decided, settleSend...), page: settle}
		return
	case defaultContextProtected(g.o, p.Context, t):
		verdict = browserVerdict{send: append(append(decided, g.markContextLocked(t)...), settleSend...), page: settle}
		return
	case g.frameUnderProtectedLocked(p.Context.AuxData.FrameID):
		g.hideContextLocked(t.id, m.SessionID, p.Context)
		verdict = browserVerdict{send: append(append(decided, g.cutCastsLocked(t.id)...), settleSend...), page: settle}
		return
	case p.Context.AuxData.FrameID != "" && p.Context.AuxData.FrameID != t.id && !g.frameContextAncestrySettledLocked(p.Context.AuxData.FrameID, t.id):
		g.holdContextLocked(t.id, m.SessionID, p.Context, data)
		verdict = browserVerdict{send: append(append(decided, g.cutCastsLocked(t.id)...), settleSend...), page: settle}
		return
	case g.worldContextLocked(t.id, m.SessionID, p.Context):
		verdict = browserVerdict{send: append(append(decided, g.cutCastsLocked(t.id)...), settleSend...), page: settle}
		return
	}
	verdict = browserVerdict{forward: data, send: append(decided, settleSend...), page: settle}
	return
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
		if g.o.Loaded(u) || g.swProtected[reg] {
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
