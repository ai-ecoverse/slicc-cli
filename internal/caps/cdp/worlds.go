package cdp

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	refusedWorld     = "that execution context belongs to a browser extension"
	refusedUnexposed = "that execution context is not exposed"
	refusedHeap      = "HeapProfiler reads the page's whole renderer, browser extensions' worlds included"
	refusedBreak     = "a breakpoint condition that could run in a browser extension's world needs a url naming the page's own script"
	maxWorlds        = 4096
)

var heapOK = map[string]bool{"HeapProfiler.enable": true, "HeapProfiler.disable": true, "HeapProfiler.collectGarbage": true, "HeapProfiler.getHeapObjectId": true}

type worldCtx struct {
	defaultWorld   bool
	frame          string
	protectedFrame string
	unique         string
	name           string
	placeSeq       uint64
	seen           map[string]bool
	scripts        []string
}

type worldSet struct {
	contexts map[int64]*worldCtx
	scripts  map[string]int64
	full     bool
}

type shownSet struct {
	contexts map[int64]*worldCtx
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

type heldContextKey struct {
	target string
	id     int64
}

type heldContext struct {
	target  string
	session string
	context execContext
	data    []byte
	at      time.Time
}

type frameWorldWait struct {
	frame   string
	page    string
	ch      chan struct{}
	at      time.Time
	expired bool
}

type worldEpoch struct {
	timer   *time.Timer
	until   time.Time
	expired bool
}

const maxHeldContexts = 256

func (g *guard) isolatedWorldReadyLocked(frame, page string) (ready, refuse bool) {
	if frame == "" || page == "" || frame == page {
		return true, false
	}
	if g.frameUnderProtectedLocked(frame) {
		return true, true
	}
	if g.ancestryDisputedFull || g.ancestryDisputedOverflow[page] || g.ancestryDisputed[frame] {
		return true, true
	}
	if g.frameContextAncestrySettledLocked(frame, page) {
		return true, false
	}
	if w := g.frameWorldWaits[frame]; w != nil && w.expired {
		return true, true
	}
	if e := g.worldEpochs[page]; e != nil && e.expired && time.Now().Before(e.until) {
		return true, true
	}
	return false, false
}

func (g *guard) isolatedWorldWaitLocked(c *command) (<-chan struct{}, [][]byte) {
	if c == nil || c.method != "Page.createIsolatedWorld" {
		return nil, nil
	}
	t := g.sessionTargetLocked(c.session)
	frame := c.str("frameId")
	if t == nil || frame == "" || frame == t.id {
		return nil, nil
	}
	ready, _ := g.isolatedWorldReadyLocked(frame, t.id)
	if ready {
		return nil, nil
	}
	w := g.frameWorldWaits[frame]
	if w != nil {
		if w.expired {
			return nil, nil
		}
		return w.ch, nil
	}
	send := g.worldEpochLocked(t.id, c.session)
	w = &frameWorldWait{frame: frame, page: t.id, ch: make(chan struct{}), at: time.Now()}
	g.frameWorldWaits[frame] = w
	g.logf("cdp: holding Page.createIsolatedWorld until frame ancestry settles frame=%s", frame)
	g.diagE7Disposition("HELD", "Page.createIsolatedWorld", c.session, c.id, "frame-ancestry-settle frame="+frame)
	return w.ch, send
}

func (g *guard) worldEpochLocked(page, session string) [][]byte {
	now := time.Now()
	for id, e := range g.worldEpochs {
		if e.expired && !now.Before(e.until) {
			delete(g.worldEpochs, id)
		}
	}
	if e := g.worldEpochs[page]; e != nil {
		return nil
	}
	hold := holdTimeout()
	e := &worldEpoch{}
	g.worldEpochs[page] = e
	e.timer = time.AfterFunc(hold, func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.worldEpochs[page] != e || e.expired {
			return
		}
		e.expired = true
		e.until = time.Now().Add(hold)
		g.logf("cdp: frame ancestry did not settle in time; refusing isolated worlds for unclassified frames page=%s", page)
		for _, w := range g.frameWorldWaits {
			if w != nil && w.page == page && !w.expired {
				w.expired = true
				g.closeFrameWorldWaitLocked(w)
			}
		}
	})
	return [][]byte{g.callbackLocked(session, "Page.getFrameTree", nil, func(m message) [][]byte {
		if m.Error == nil {
			g.noteFrameTreeResultLocked(page, m.Result)
		}
		return nil
	})}
}

func (g *guard) settleWorldEpochLocked(page string) {
	e := g.worldEpochs[page]
	if e == nil || e.expired {
		return
	}
	for _, w := range g.frameWorldWaits {
		if w != nil && w.page == page {
			return
		}
	}
	e.timer.Stop()
	delete(g.worldEpochs, page)
}

func (g *guard) closeFrameWorldWaitLocked(w *frameWorldWait) {
	if w == nil {
		return
	}
	select {
	case <-w.ch:
	default:
		close(w.ch)
	}
}

func (g *guard) resolveFrameWorldWaitsLocked() {
	pages := map[string]bool{}
	for frame, w := range g.frameWorldWaits {
		if w == nil {
			delete(g.frameWorldWaits, frame)
			continue
		}
		ready, _ := g.isolatedWorldReadyLocked(frame, w.page)
		if !ready && !w.expired {
			continue
		}
		delete(g.frameWorldWaits, frame)
		g.closeFrameWorldWaitLocked(w)
		pages[w.page] = true
	}
	for page := range pages {
		g.settleWorldEpochLocked(page)
	}
}

func (g *guard) createIsolatedWorldRule(c *command) string {
	t := g.sessionTargetLocked(c.session)
	frame := c.str("frameId")
	if t == nil || frame == "" || frame == t.id {
		return ""
	}
	ready, refuse := g.isolatedWorldReadyLocked(frame, t.id)
	if !ready {
		return ""
	}
	if refuse {
		return refusedUnexposed
	}
	return ""
}

func (g *guard) frameContextAncestrySettledLocked(frame, page string) bool {
	if frame == "" || frame == page {
		return true
	}
	if g.ancestryDisputedFull || g.ancestryDisputedOverflow[page] || g.ancestryDisputed[frame] {
		return false
	}
	seen := map[string]bool{}
	id := frame
	for depth := 0; id != "" && depth < maxFrameAncestors; depth++ {
		if seen[id] || id == page {
			return id == page
		}
		seen[id] = true
		settled, cont := g.frameContextStepSettledLocked(id)
		if settled || !cont {
			return settled
		}
		parent := g.frameParentOfLocked(id)
		if parent == "" {
			return false
		}
		id = parent
	}
	return false
}

func (g *guard) frameContextStepSettledLocked(id string) (settled, cont bool) {
	if t := g.targets[id]; t != nil && !t.gone {
		if t.typ == "page" || t.typ == "tab" || t.protected {
			return true, false
		}
		if t.typ == "iframe" && (g.iframeAncestryUnknownLocked(t) || g.targetHeldLocked(t.id) || g.targetUnconfirmedLocked(t.id)) {
			return false, false
		}
		if !g.creatorClassifiedLocked(id) && protect.Inheriting(t.url) {
			return false, false
		}
		return false, true
	}
	if g.ancestryDisputed[id] {
		return false, false
	}
	ok, known := g.localFrameClassifiedLocked(id)
	if !known || !ok {
		return false, false
	}
	if g.creatorProtectedLocked(id) {
		return true, false
	}
	return false, true
}

func (g *guard) holdContextLocked(target, session string, c execContext, data []byte) {
	if target == "" || c.ID == 0 || c.AuxData.FrameID == "" || c.AuxData.FrameID == target {
		return
	}
	key := heldContextKey{target: target, id: c.ID}
	if prev := g.heldContexts[key]; prev != nil {
		prev.session = session
		prev.context = c
		prev.data = data
		return
	}
	if len(g.heldContexts) >= maxHeldContexts {
		g.logf("cdp: too many execution contexts wait for frame ancestry; refusing one")
		g.hideContextLocked(target, session, c)
		return
	}
	g.heldContexts[key] = &heldContext{target: target, session: session, context: c, data: data, at: time.Now()}
}

func (g *guard) contextHeldLocked(target string, id int64) bool {
	return g.heldContexts[heldContextKey{target: target, id: id}] != nil
}

func (g *guard) forgetHeldContextLocked(target string, id int64) {
	delete(g.heldContexts, heldContextKey{target: target, id: id})
}

func (g *guard) forgetHeldContextsLocked(target, session string) {
	for key, h := range g.heldContexts {
		if key.target == target && (session == "" || h.session == session) {
			delete(g.heldContexts, key)
		}
	}
}

func (g *guard) queueContextSettleLocked() {
	g.contextSettlePage = append(g.contextSettlePage, g.settleHeldContextsLocked()...)
}

func (g *guard) takeContextSettlePageLocked() [][]byte {
	out := g.contextSettlePage
	g.contextSettlePage = nil
	return out
}

func (g *guard) takeContextSettleSendLocked() [][]byte {
	out := g.contextSettleSend
	g.contextSettleSend = nil
	return out
}

func (g *guard) settleHeldContextsLocked() [][]byte {
	deadline := holdTimeout()
	now := time.Now()
	var page [][]byte
	for key, h := range g.heldContexts {
		expired := now.Sub(h.at) >= deadline
		if !expired && !g.frameContextAncestrySettledLocked(h.context.AuxData.FrameID, h.target) {
			continue
		}
		delete(g.heldContexts, key)
		if expired || g.frameUnderProtectedLocked(h.context.AuxData.FrameID) {
			g.hideContextLocked(h.target, h.session, h.context)
			continue
		}
		if g.worldContextLocked(h.target, h.session, h.context) {
			continue
		}
		if h.data != nil {
			page = append(page, h.data)
		}
	}
	page = append(page, g.hideShownUnderProtectedLocked()...)
	g.resolveFrameWorldWaitsLocked()
	var soft []string
	for target := range g.softLatched {
		soft = append(soft, target)
	}
	for _, target := range soft {
		g.trackSoftLatchPageLocked(target)
	}
	return page
}

func (g *guard) destroyShownContextLocked(target, session string, c execContext) [][]byte {
	if target == "" || c.ID == 0 {
		return nil
	}
	params := map[string]any{"executionContextId": c.ID}
	if c.UniqueID != "" {
		params["executionContextUniqueId"] = c.UniqueID
	}
	ev := map[string]any{"method": "Runtime.executionContextDestroyed", "params": params}
	if session != "" {
		ev["sessionId"] = session
	}
	return [][]byte{encode(ev)}
}

func (g *guard) hideShownUnderProtectedLocked() [][]byte {
	type hit struct {
		target, session string
		context         execContext
	}
	var hits []hit
	for target, s := range g.shown {
		if s == nil {
			continue
		}
		for id, sc := range s.contexts {
			if sc == nil || sc.frame == "" || !g.frameUnderProtectedLocked(sc.frame) {
				continue
			}
			session := ""
			for sid := range sc.seen {
				session = sid
				break
			}
			c := execContext{ID: id, UniqueID: sc.unique}
			c.AuxData.IsDefault = sc.defaultWorld
			c.AuxData.FrameID = sc.frame
			hits = append(hits, hit{target: target, session: session, context: c})
		}
	}
	var page [][]byte
	cut := map[string]bool{}
	g.latchHiddenUnderProtectedLocked(cut)
	for _, h := range hits {
		g.hideContextLocked(h.target, h.session, h.context)
		page = append(page, g.destroyShownContextLocked(h.target, h.session, h.context)...)
		if h.target != "" {
			cut[h.target] = true
		}
	}
	for target := range cut {
		g.contextSettleSend = append(g.contextSettleSend, g.cutCastsLocked(target)...)
	}
	return page
}

func (g *guard) noteFrameTreeResultLocked(target string, raw json.RawMessage) {
	var result struct {
		FrameTree *frameTreeNode `json:"frameTree"`
	}
	if target == "" || json.Unmarshal(raw, &result) != nil || result.FrameTree == nil || result.FrameTree.Frame.ID != target {
		return
	}
	budget := maxFrameTreeNodes
	g.walkFrameTreeLocked(result.FrameTree, target, "", 0, &budget)
	g.queueContextSettleLocked()
	g.trackSoftLatchPageLocked(target)
}

const maxFrameTreeNodes = 4096

type frameTreeNode struct {
	Frame struct {
		ID             string `json:"id"`
		ParentID       string `json:"parentId"`
		URL            string `json:"url"`
		SecurityOrigin string `json:"securityOrigin"`
		UnreachableURL string `json:"unreachableUrl"`
	} `json:"frame"`
	ChildFrames []*frameTreeNode `json:"childFrames"`
}

func (g *guard) walkFrameTreeLocked(node *frameTreeNode, root, parent string, depth int, budget *int) {
	if node == nil || node.Frame.ID == "" || depth >= maxFrameAncestors || *budget <= 0 {
		return
	}
	*budget--
	id := node.Frame.ID
	pid := node.Frame.ParentID
	if parent != "" && pid != "" && pid != parent {
		g.markAncestryDisputedLocked(id, root)
		return
	}
	if pid == "" {
		pid = parent
	}
	if pid != "" {
		if known := g.frameParentOfLocked(id); known != "" && known != pid {
			g.markAncestryDisputedLocked(id, root)
			return
		}
		send, _ := g.noteFrameParentLocked(id, pid)
		g.contextSettleSend = append(g.contextSettleSend, send...)
	}
	raw := node.Frame.URL
	if node.Frame.UnreachableURL != "" {
		raw = node.Frame.UnreachableURL
	} else if protect.Inheriting(raw) {
		raw = node.Frame.SecurityOrigin
	}
	g.contextSettleSend = append(g.contextSettleSend, g.noteChildFrameLocked(id, raw)...)
	for _, child := range node.ChildFrames {
		g.walkFrameTreeLocked(child, root, id, depth+1, budget)
	}
}

func (g *guard) worldsLocked(target string) *worldSet {
	w := g.worlds[target]
	if w == nil {
		w = &worldSet{contexts: map[int64]*worldCtx{}, scripts: map[string]int64{}}
		g.worlds[target] = w
	}
	return w
}

func (g *guard) showContextLocked(target, session string, c execContext) {
	s := g.shown[target]
	if s == nil {
		s = &shownSet{contexts: map[int64]*worldCtx{}}
		g.shown[target] = s
	}
	if sc := s.contexts[c.ID]; sc != nil && sc.unique == c.UniqueID {
		sc.seen[session] = true
		sc.defaultWorld = c.AuxData.IsDefault
		if c.AuxData.FrameID != "" {
			sc.frame = c.AuxData.FrameID
		}
		return
	}
	g.unshowLocked(target, c.ID)
	if s.full || len(s.contexts) >= g.maxWorlds {
		if !s.full {
			g.logf("cdp: a target has more execution contexts than slicc tracks; refusing every newer context there until its contexts are cleared")
		}
		s.full = true
		return
	}
	s.contexts[c.ID] = &worldCtx{defaultWorld: c.AuxData.IsDefault, frame: c.AuxData.FrameID, unique: c.UniqueID, placeSeq: g.docPlaceSeq[target], seen: map[string]bool{session: true}}
	if c.UniqueID != "" {
		g.shownUniques[c.UniqueID] = target
	}
}

func (g *guard) unshowLocked(target string, id int64) {
	s := g.shown[target]
	if s == nil || s.contexts[id] == nil {
		return
	}
	if u := s.contexts[id].unique; u != "" {
		delete(g.shownUniques, u)
	}
	delete(s.contexts, id)
}

func (g *guard) shownGoneLocked(target string, id int64, unique string) {
	if t, ok := g.shownUniques[unique]; ok && unique != "" {
		target = t
	}
	s := g.shown[target]
	if s == nil {
		return
	}
	for cid, sc := range s.contexts {
		if unique != "" && sc.unique == unique {
			id = cid
		}
	}
	g.unshowLocked(target, id)
}

func (g *guard) hideContextLocked(target, session string, c execContext) {
	g.unshowLocked(target, c.ID)
	w := g.worldsLocked(target)
	frame := ""
	if c.AuxData.FrameID != target {
		if c.AuxData.FrameID == "" {
			w.full = true
		} else if g.contextLatchesLocked(c) {
			frame = c.AuxData.FrameID
			g.logf("cdp: a hidden execution context locks its page's frame content target=%s frame=%s default=%v", target, frame, c.AuxData.IsDefault)
		}
	}
	if wc := w.contexts[c.ID]; wc != nil {
		wc.seen[session] = true
		wc.defaultWorld = c.AuxData.IsDefault
		if c.Name != "" {
			wc.name = c.Name
		}
		if c.AuxData.FrameID != "" {
			wc.frame = c.AuxData.FrameID
		}
		if frame != "" {
			wc.protectedFrame = frame
		}
		return
	}
	if len(w.contexts) >= g.maxWorlds {
		if !w.full {
			g.logf("cdp: a target has more extension worlds than slicc tracks; refusing every command there that names an execution context")
		}
		w.full = true
		return
	}
	w.contexts[c.ID] = &worldCtx{defaultWorld: c.AuxData.IsDefault, frame: c.AuxData.FrameID, unique: c.UniqueID, name: c.Name, seen: map[string]bool{session: true}, protectedFrame: frame}
	if c.UniqueID != "" {
		g.worldUniques[c.UniqueID] = target
	}
}

func (g *guard) contextLatchesLocked(c execContext) bool {
	if c.AuxData.IsDefault || g.frameUnderProtectedLocked(c.AuxData.FrameID) {
		return true
	}
	return g.o.Loaded(c.Origin) && !protect.ExtensionScheme(c.Origin)
}

func (g *guard) frameDefaultSeenLocked(frame string) bool {
	if frame == "" {
		return false
	}
	for _, w := range g.worlds {
		if w == nil {
			continue
		}
		for _, wc := range w.contexts {
			if wc != nil && wc.defaultWorld && wc.frame == frame {
				return true
			}
		}
	}
	for _, s := range g.shown {
		if s == nil {
			continue
		}
		for _, sc := range s.contexts {
			if sc != nil && sc.defaultWorld && sc.frame == frame {
				return true
			}
		}
	}
	for _, h := range g.heldContexts {
		if h != nil && h.context.AuxData.IsDefault && h.context.AuxData.FrameID == frame {
			return true
		}
	}
	return false
}

func (g *guard) frameWorldEvidenceLocked(frame string) bool {
	if frame == "" {
		return false
	}
	if g.frameDefaultSeenLocked(frame) {
		return true
	}
	if raw := g.childFrames[frame]; raw != "" && !protect.Inheriting(raw) {
		return true
	}
	if t := g.targets[frame]; t != nil && !t.gone && t.url != "" && !protect.Inheriting(t.url) {
		return true
	}
	return false
}

func (g *guard) unknownNonDefaultWorldProtectsLocked(target string) bool {
	if target == "" {
		return false
	}
	if w := g.worlds[target]; w != nil {
		for _, wc := range w.contexts {
			if wc == nil || wc.defaultWorld || wc.frame == "" || wc.frame == target {
				continue
			}
			if !g.frameWorldEvidenceLocked(wc.frame) {
				return true
			}
		}
	}
	for _, h := range g.heldContexts {
		if h == nil || h.target != target || h.context.AuxData.IsDefault {
			continue
		}
		frame := h.context.AuxData.FrameID
		if frame == "" || frame == target {
			continue
		}
		if !g.frameWorldEvidenceLocked(frame) {
			return true
		}
	}
	return false
}

func (g *guard) latchHiddenUnderProtectedLocked(cut map[string]bool) {
	for target, w := range g.worlds {
		if w == nil {
			continue
		}
		for _, wc := range w.contexts {
			if wc == nil || wc.protectedFrame != "" || wc.frame == "" || wc.frame == target || !g.frameUnderProtectedLocked(wc.frame) {
				continue
			}
			wc.protectedFrame = wc.frame
			cut[target] = true
			g.logf("cdp: a hidden execution context locks its page's frame content once its frame is protected target=%s frame=%s", target, wc.frame)
		}
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
	if s := g.shown[target]; s != nil {
		for id := range s.contexts {
			g.unshowLocked(target, id)
		}
		delete(g.shown, target)
	}
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
	if g.extensionProbeWorldName(c.Name) {
		g.hideContextLocked(target, session, c)
		return true
	}
	if g.frameUnderProtectedLocked(c.AuxData.FrameID) {
		g.hideContextLocked(target, session, c)
		return true
	}
	if !g.o.Loaded(c.Origin) || (c.AuxData.IsDefault && c.AuxData.FrameID == target) {
		if w := g.worlds[target]; w != nil {
			g.forgetContextLocked(w, c.ID)
		}
		g.showContextLocked(target, session, c)
		return false
	}
	g.hideContextLocked(target, session, c)
	return true
}

func (g *guard) frameUnderProtectedLocked(frame string) bool {
	if frame == "" {
		return false
	}
	seen := map[string]bool{}
	id := frame
	for depth := 0; id != "" && depth < maxFrameAncestors; depth++ {
		if seen[id] {
			return false
		}
		seen[id] = true
		if g.creatorProtectedLocked(id) {
			return true
		}
		if t := g.targets[id]; t != nil && !t.gone && t.protected {
			return true
		}
		id = g.frameParentOfLocked(id)
	}
	return false
}

func (g *guard) contextGoneLocked(target, session string, id int64, unique string) bool {
	g.shownGoneLocked(target, id, unique)
	g.forgetHeldContextLocked(target, id)
	if t, ok := g.worldUniques[unique]; ok && unique != "" {
		target = t
	}
	w := g.worlds[target]
	if w == nil {
		g.forgetExtensionProbeWorldContextLocked(session, id)
		return false
	}
	if unique != "" {
		for cid, wc := range w.contexts {
			if wc.unique == unique {
				id = cid
			}
		}
	}
	g.forgetExtensionProbeWorldContextLocked(session, id)
	if w.contexts[id] == nil {
		return false
	}
	g.forgetContextLocked(w, id)
	return true
}

func (g *guard) contextsClearedLocked(target, session string) {
	g.forgetHeldContextsLocked(target, session)
	if s := g.shown[target]; s != nil {
		for id, sc := range s.contexts {
			if sc.seen[session] {
				g.unshowLocked(target, id)
			}
		}
		if len(s.contexts) < g.maxWorlds {
			s.full = false
		}
	}
	g.forgetExtensionProbeWorldSessionLocked(session)
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

func (g *guard) extensionFrameContentLocked(c *command, target string) string {
	if reason := g.extensionGateReasonLocked(c, target); reason != "" {
		return reason
	}
	if name, _ := c.params["worldName"].(string); c.method == "Page.createIsolatedWorld" && g.extensionProbeWorldName(name) {
		return refusedTarget
	}
	if frame := c.str("frameId"); frame != "" {
		if t := g.targets[frame]; g.extensionFrameTargetLocked(t) || g.extensionFrameIDLocked(frame) {
			if c.method == "DOM.getFrameOwner" && g.extensionFrameOnPageLocked(frame, target) {
				return ""
			}
			return refusedTarget
		}
	}
	return ""
}

func (g *guard) frameContentCheckLocked(c *command, target string) string {
	if reason := g.extensionFrameContentLocked(c, target); reason != "" {
		return reason
	}
	if !g.framesProtectedLocked(target) {
		return ""
	}
	if w := g.worlds[target]; w != nil {
		for _, context := range w.contexts {
			if c.str("frameId") != "" && c.str("frameId") == context.protectedFrame && !g.extensionFrameIDLocked(context.protectedFrame) {
				return refusedTarget
			}
		}
	}
	if frame := c.str("frameId"); frame != "" {
		if t := g.targets[frame]; t != nil && !t.gone && t.typ == "iframe" && (t.protected || g.targetLoadedLocked(t)) && !g.extensionFrameTargetLocked(t) {
			return refusedTarget
		}
	}
	domain, verb, _ := strings.Cut(c.method, ".")
	if (domain == "DOM" || domain == "DOMSnapshot" || domain == "Accessibility" || domain == "Input") && verb != "enable" && verb != "disable" {
		return refusedTarget
	}
	switch c.method {
	case "Page.getFrameTree", "Page.getResourceTree", "Page.getResourceContent", "Page.captureSnapshot", "Page.printToPDF":
		g.diagE7Disposition("REWRITTEN", c.method, c.session, c.id, "frameContentCheck framesProtected refusal")
		return refusedTarget
	}
	if framePaintMethods[c.method] {
		return refusedTarget
	}
	return ""
}

func (g *guard) worldCheckLocked(c *command) string {
	if reason := g.debuggerCheckLocked(c); reason != "" {
		return reason
	}
	t := g.sessionTargetLocked(c.session)
	if t != nil {
		if reason := g.frameContentCheckLocked(c, t.id); reason != "" {
			return reason
		}
	}
	if t != nil && navTypes[t.typ] && strings.HasPrefix(c.method, "HeapProfiler.") && !heapOK[c.method] {
		return refusedHeap
	}
	if t != nil && navTypes[t.typ] && c.method == "Debugger.setBreakpointByUrl" && c.str("condition") != "" && c.str("url") == "" {
		return refusedBreak
	}
	if u, ok := c.params["uniqueContextId"].(string); ok && t != nil {
		if _, hidden := g.worldUniques[u]; hidden {
			return refusedWorld
		}
		if shown, ok := g.shownUniques[u]; !ok || shown != t.id {
			return refusedUnexposed
		}
	}
	if t != nil {
		if reason := g.numericContextLocked(t.id, c.params); reason != "" {
			return reason
		}
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

func (g *guard) numericContextLocked(target string, params map[string]any) string {
	for _, k := range []string{"contextId", "executionContextId"} {
		n, ok := params[k].(json.Number)
		if !ok {
			continue
		}
		id, err := n.Int64()
		switch {
		case err != nil:
		case g.contextHiddenLocked(target, id):
			return refusedWorld
		case g.contextHeldLocked(target, id):
			return refusedUnexposed
		case g.shown[target] == nil || g.shown[target].contexts[id] == nil:
			return refusedUnexposed
		}
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

func (g *guard) navWorldLocked(nt *navTarget, m *message) [][]byte {
	if nt.target == "" {
		return nil
	}
	switch m.Method {
	case "Runtime.executionContextCreated":
		var p struct {
			Context execContext `json:"context"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return g.cutCastsLocked(nt.target)
		}
		var send [][]byte
		if p.Context.AuxData.IsDefault && p.Context.AuxData.FrameID != nt.target {
			send = g.noteChildFrameLocked(p.Context.AuxData.FrameID, p.Context.Origin)
			g.queueContextSettleLocked()
		}
		g.worldContextLocked(nt.target, m.SessionID, p.Context)
		g.trackSoftLatchPageLocked(nt.target)
		return append(send, g.cutCastsLocked(nt.target)...)
	case "Runtime.executionContextDestroyed":
		var p struct {
			ID       int64  `json:"executionContextId"`
			UniqueID string `json:"executionContextUniqueId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		g.contextGoneLocked(nt.target, m.SessionID, p.ID, p.UniqueID)
	case "Runtime.executionContextsCleared":
		g.contextsClearedLocked(nt.target, m.SessionID)
	}
	return nil
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
	if g.contextGoneLocked(g.worldTargetLocked(m.SessionID), m.SessionID, p.ID, p.UniqueID) {
		return browserVerdict{}
	}
	return browserVerdict{forward: data}
}

func (g *guard) contextsClearedEvent(m *message, data []byte) browserVerdict {
	g.contextsClearedLocked(g.worldTargetLocked(m.SessionID), m.SessionID)
	return browserVerdict{forward: data}
}

func (g *guard) contextShownLocked(target string, id int64) bool {
	s := g.shown[target]
	return s != nil && s.contexts[id] != nil
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
	if m.Method == "Runtime.bindingCalled" && !g.contextShownLocked(target, p.ID) {
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
	g.noteScriptLocked(m)
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
		if (u != "" && g.o.URL(u)) || g.scrubStacksLocked(w, e) || g.fromWorldLocked(g.worldTargetLocked(m.SessionID), e) {
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
	for _, s := range g.shown {
		for _, sc := range s.contexts {
			delete(sc.seen, id)
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
	reservation *link.Reservation
	m           message
	data        []byte
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
	for k := range g.navChains {
		if k.session == target {
			delete(g.navChains, k)
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
				if id, ok := val.(string); ok && (requestIDKeys[k] || strings.HasSuffix(k, "RequestId")) && g.worldRequestKnownLocked(target, id) {
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
			delete(g.navChains, g.navSeenOrder[0])
			g.navSeenOrder = g.navSeenOrder[1:]
		}
	}
	if m.Method == "Network.requestWillBeSent" {
		g.chainLocked(k, p)
	}
	return g.releasePausesLocked(k)
}

func (g *guard) releasePausesLocked(k requestKey) browserVerdict {
	held := g.heldPauses[k]
	for _, h := range held {
		g.pendingRelease = append(g.pendingRelease, h.reservation)
	}
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

func (g *guard) expirePause(k requestKey) (page, send [][]byte, releases []*link.Reservation) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.heldPauses[k]) > 0 {
		g.logf("cdp: handed out a paused request the navigation guard never reported")
	}
	v := g.releasePausesLocked(k)
	return v.page, v.send, g.takeReleasesLocked()
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
	reservation, ok := g.holdReservation(data)
	if !ok {
		return browserVerdict{}
	}
	g.heldPauses[k] = append(g.heldPauses[k], heldPause{m: *m, data: data, reservation: reservation})
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
	method, params := continueWorld(m.Method, id, p)
	msg, _ := g.newCallLocked(m.SessionID, method, params, false)
	return browserVerdict{send: [][]byte{msg}}
}

func continueWorld(event, id string, p map[string]any) (string, map[string]any) {
	method, params := "Fetch.continueRequest", map[string]any{"requestId": id}
	switch event {
	case "Network.requestIntercepted":
		method, params = "Network.continueInterceptedRequest", map[string]any{"interceptionId": id}
		if _, ok := p["authChallenge"]; ok {
			params["authChallengeResponse"] = map[string]any{"response": "Default"}
		}
	case "Fetch.authRequired":
		method = "Fetch.continueWithAuth"
		params["authChallengeResponse"] = map[string]any{"response": "Default"}
	}
	return method, params
}

func (g *guard) worldPauseLocked(m *message, p map[string]any) ([]byte, bool) {
	id, other := pausedIDs(m.Method, p)
	t := g.worldTargetLocked(m.SessionID)
	auth := m.Method == "Fetch.authRequired" && g.requestMarkedLocked(m.SessionID, id)
	if !auth && !g.worldRequestKnownLocked(t, other) && !g.worldRequestKnownLocked(t, id) {
		return nil, false
	}
	g.dropHandedLocked(requestKey{m.SessionID, id})
	g.markRequestLocked(m.SessionID, id)
	g.markRequestLocked(m.SessionID, other)
	method, params := continueWorld(m.Method, id, p)
	g.logf("cdp: continued a request a browser extension's world made without showing it to the page")
	msg, _ := g.newCallLocked(m.SessionID, method, params, false)
	return msg, true
}
