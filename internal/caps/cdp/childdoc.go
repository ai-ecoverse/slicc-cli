package cdp

import (
	"bytes"
	"encoding/json"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

type childDocStripState struct {
	origins      *protect.Origins
	session      string
	markFrames   []string
	markPage     bool
	hitProtected bool
}

type childDocSnapshot struct {
	origins *protect.Origins
	session string
}

var childDocCommandWalkMethods = map[string]bool{
	"DOM.describeNode":                       true,
	"DOM.getDocument":                        true,
	"DOM.getFlattenedDocument":               true,
	"DOM.getNodesForSubtreeByStyle":          true,
	"DOM.getSearchResults":                   true,
	"DOM.pushNodesByBackendIdsToFrontend":    true,
	"DOM.querySelectorAll":                   true,
	"DOM.getTopLayerElements":                true,
	"DOM.getDetachedDomNodes":                true,
	"DOM.getQueryingDescendantsForContainer": true,
	"DOM.forceShowPopover":                   true,
	"DOM.getImplicitAnchorCandidates":        true,
	"DOMSnapshot.captureSnapshot":            true,
	"DOMSnapshot.getSnapshot":                true,
}

var childDocEventWalkMethods = map[string]bool{
	"DOM.childNodeInserted":       true,
	"DOM.distributedNodesUpdated": true,
	"DOM.pseudoElementAdded":      true,
	"DOM.setChildNodes":           true,
	"DOM.shadowRootPushed":        true,
}

var childDocNestKeys = []string{
	"children",
	"shadowRoots",
	"templateContent",
	"pseudoElements",
	"importedDocument",
	"distributedNodes",
}

var childDocArrayNestKeys = []string{
	"children",
	"shadowRoots",
	"pseudoElements",
	"distributedNodes",
}

var childDocOwnKeys = []string{"contentDocument", "importedDocument"}

type childDocCarrier uint8

const (
	childDocCarrierNone childDocCarrier = iota
	childDocCarrierResult
	childDocCarrierParams
)

type childDocJob struct {
	v              any
	parentOrdinary bool
	depth          int
	carrier        childDocCarrier
}

func childDocOrdinary(o *protect.Origins, documentURL, baseURL string, documentPresent, basePresent, parentOrdinary bool) bool {
	return childDocOrdinaryBoth(o, documentURL, baseURL, documentPresent, basePresent, parentOrdinary)
}

func childDocOrdinaryBoth(o *protect.Origins, documentURL, baseURL string, documentPresent, basePresent, parentOrdinary bool) bool {
	anyPresent := false
	allInheriting := true
	for _, f := range []struct {
		u       string
		present bool
	}{
		{documentURL, documentPresent},
		{baseURL, basePresent},
	} {
		if !f.present {
			continue
		}
		anyPresent = true
		if f.u == "" {
			return false
		}
		if protect.Inheriting(f.u) {
			continue
		}
		allInheriting = false
		if o.Loaded(f.u) || protect.Unparseable(f.u) {
			return false
		}
	}
	if !anyPresent {
		return false
	}
	if allInheriting {
		return parentOrdinary
	}
	return true
}

func (g *guard) setPendingLocked(k callKey, kind callKind, method string) {
	g.pending[k] = kind
	if g.pendingMethods == nil {
		g.pendingMethods = map[callKey]string{}
	}
	if childDocRecordPendingMethods() && childDocWalkCommands() && childDocCommandWalkMethods[method] {
		g.pendingMethods[k] = method
	} else {
		delete(g.pendingMethods, k)
	}
}

func (g *guard) clearPendingLocked(k callKey) callKind {
	kind := g.pending[k]
	delete(g.pending, k)
	delete(g.pendingMethods, k)
	return kind
}

func (g *guard) dropChildDocMethodLocked(k callKey) {
	delete(g.pendingMethods, k)
}

func (g *guard) scrubChildDocuments(m map[string]any) (changed, refuse bool) {
	if !childDocStripEnabled() || m == nil {
		return false, false
	}
	if childDocStripBeforeHold() {
		return false, false
	}
	session, _ := m["sessionId"].(string)
	st := newChildDocStripState(childDocSnapshot{session: session, origins: g.o})
	return scrubChildDocumentsWalk(m, st)
}

func newChildDocStripState(snap childDocSnapshot) *childDocStripState {
	return &childDocStripState{
		origins: snap.origins,
		session: snap.session,
	}
}

func (g *guard) snapshotChildDocLocked(session string) childDocSnapshot {
	return childDocSnapshot{session: session, origins: g.o}
}

func messageHasError(m *message) bool {
	if m == nil {
		return false
	}
	e := bytes.TrimSpace(m.Error)
	return len(e) > 0 && !bytes.Equal(e, []byte("null"))
}

func (g *guard) peekChildDocReplyLocked(m *message) (method string, snap childDocSnapshot, ok bool) {
	if m == nil || m.ID == nil || messageHasError(m) || !childDocStripBeforeHold() {
		return "", snap, false
	}
	k := callKey{m.SessionID, *m.ID}
	if !g.ownsClientAnswerLocked(k) {
		return "", snap, false
	}
	method = g.pendingMethods[k]
	if method == "" || !childDocWalkCommands() || !childDocCommandWalkMethods[method] {
		return "", snap, false
	}
	return method, g.snapshotChildDocLocked(m.SessionID), true
}

func applyChildDocOnSnapshot(data []byte, method string, snap childDocSnapshot, id *int64) (out []byte, drop bool, st *childDocStripState) {
	return applyChildDocStrip(data, method, snap, id)
}

func childDocBytesReplaced(in, out []byte) bool {
	if len(out) == 0 {
		return false
	}
	if len(in) == 0 || len(in) != len(out) {
		return true
	}
	return &in[0] != &out[0]
}

func (g *guard) stripChildDocReplyBeforeHoldLocked(m *message, data []byte, method string) (out []byte, drop, handled bool) {
	if !childDocStripBeforeHold() || method == "" || m == nil || messageHasError(m) {
		return data, false, false
	}
	out, drop = g.applyChildDocStripLocked(data, method, m.SessionID, m.ID)
	if drop {
		return nil, true, true
	}
	if childDocBytesReplaced(data, out) {
		g.refreshChildDocMessage(m, out)
	}
	return out, false, true
}

func (g *guard) refreshChildDocMessage(m *message, out []byte) {
	if m == nil {
		return
	}
	var refreshed message
	if err := json.Unmarshal(out, &refreshed); err == nil {
		m.Result = refreshed.Result
		m.Error = refreshed.Error
	}
}

func (g *guard) applyChildDocStripLocked(data []byte, method, session string, id *int64) (out []byte, drop bool) {
	out, drop, _ = applyChildDocStrip(data, method, childDocSnapshot{session: session, origins: g.o}, id)
	return out, drop
}

func applyChildDocStrip(data []byte, method string, snap childDocSnapshot, id *int64) (out []byte, drop bool, st *childDocStripState) {
	if !childDocStripEnabled() || method == "" {
		return data, false, nil
	}
	event := childDocEventWalkMethods[method]
	if event {
		if !childDocEventsStrip() {
			return data, false, nil
		}
		if method == "DOM.childNodeInserted" && !childDocChildInsertedStrip() {
			return data, false, nil
		}
	} else if !childDocWalkCommands() || !childDocCommandWalkMethods[method] {
		return data, false, nil
	}
	m, ok := decodeObject(data)
	if !ok {
		if id != nil {
			return refusal(*id, snap.session, refusedTarget), false, nil
		}
		return nil, true, nil
	}
	st = newChildDocStripState(snap)
	changed, refuse := scrubChildDocumentsWalk(m, st)
	if refuse {
		st.hitProtected = true
		if event {
			return nil, true, st
		}
		if refused := childDocRefuseMessage(m, id, snap.session); refused != nil {
			return refused, false, st
		}
		return nil, true, st
	}
	if !changed {
		return data, false, st
	}
	return encode(m), false, st
}

func scrubChildDocumentsWalk(m map[string]any, st *childDocStripState) (changed, refuse bool) {
	if st == nil {
		return false, false
	}
	stack := make([]childDocJob, 0, 64)
	if result, ok := m["result"]; ok {
		stack = append(stack, childDocJob{result, true, 0, childDocCarrierResult})
	}
	if params, ok := m["params"]; ok && childDocParamsStrip(m) {
		stack = append(stack, childDocJob{params, true, 0, childDocCarrierParams})
	}
	for len(stack) > 0 {
		job := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch t := job.v.(type) {
		case map[string]any:
			c, r, more := stripChildDocMapStep(t, job.parentOrdinary, job.depth, job.carrier, st)
			changed = changed || c
			if r {
				return changed, true
			}
			stack = append(stack, more...)
		case []any:
			for i := len(t) - 1; i >= 0; i-- {
				stack = append(stack, childDocJob{t[i], job.parentOrdinary, job.depth + 1, childDocCarrierNone})
			}
		}
	}
	if c := purgeFlattenedChildDocs(m, st); c {
		changed = true
	}
	return changed, false
}

func purgeFlattenedChildDocs(m map[string]any, st *childDocStripState) bool {
	if st == nil {
		return false
	}
	result, _ := m["result"].(map[string]any)
	if result == nil {
		return false
	}
	raw, ok := result["nodes"].([]any)
	if !ok || len(raw) == 0 {
		return false
	}
	return purgeFlattenedChildDocsAncestry(raw, result, st)
}

type flattenedPurgeIndex struct {
	nodeID     []int64
	backendID  []int64
	hasNodeID  []bool
	hasBackend []bool
	nested     [][]int64
	nodes      []map[string]any
	byNodeID   map[int64]int
	children   map[int64][]int
}

func buildFlattenedPurgeIndex(raw []any) flattenedPurgeIndex {
	n := len(raw)
	idx := flattenedPurgeIndex{
		nodeID: make([]int64, n), backendID: make([]int64, n),
		hasNodeID: make([]bool, n), hasBackend: make([]bool, n),
		nested: make([][]int64, n), nodes: make([]map[string]any, n),
		byNodeID: map[int64]int{}, children: map[int64][]int{},
	}
	for i, item := range raw {
		node, ok := item.(map[string]any)
		if !ok || node == nil {
			continue
		}
		idx.nodes[i] = node
		if id, ok := childDocMessageID(node["nodeId"]); ok {
			idx.nodeID[i], idx.hasNodeID[i] = id, true
			idx.byNodeID[id] = i
		}
		if id, ok := childDocMessageID(node["backendNodeId"]); ok {
			idx.backendID[i], idx.hasBackend[i] = id, true
		}
		if id, ok := childDocMessageID(node["parentId"]); ok {
			idx.children[id] = append(idx.children[id], i)
		}
		idx.nested[i] = childDocNestedNodeIDs(node)
	}
	return idx
}

func purgeFlattenedChildDocsAncestry(raw []any, result map[string]any, st *childDocStripState) bool {
	idx := buildFlattenedPurgeIndex(raw)
	dead := markFlattenedAncestryDead(idx, st)
	changed := false
	next := raw[:0:len(raw)]
	for i, item := range raw {
		if dead[i] {
			changed = true
			continue
		}
		next = append(next, item)
	}
	result["nodes"] = next
	return changed
}

type flattenedAncestryMark struct {
	idx            flattenedPurgeIndex
	removedNodes   map[int64]struct{}
	removedBackend map[int64]struct{}
	dead           []bool
	idQueue        []int64
	seenID         map[int64]struct{}
}

func newFlattenedAncestryMark(idx flattenedPurgeIndex) *flattenedAncestryMark {
	return &flattenedAncestryMark{
		idx:            idx,
		removedNodes:   map[int64]struct{}{},
		removedBackend: map[int64]struct{}{},
		dead:           make([]bool, len(idx.nodes)),
		seenID:         map[int64]struct{}{},
	}
}

func (m *flattenedAncestryMark) enqueue(id int64) {
	if _, ok := m.seenID[id]; ok {
		return
	}
	m.seenID[id] = struct{}{}
	m.removedNodes[id] = struct{}{}
	m.idQueue = append(m.idQueue, id)
}

func (m *flattenedAncestryMark) markDead(i int) {
	if i < 0 || i >= len(m.dead) || m.dead[i] || m.idx.nodes[i] == nil {
		return
	}
	m.dead[i] = true
	if m.idx.hasNodeID[i] {
		m.enqueue(m.idx.nodeID[i])
	}
	if m.idx.hasBackend[i] {
		m.removedBackend[m.idx.backendID[i]] = struct{}{}
	}
	for _, nid := range m.idx.nested[i] {
		m.enqueue(nid)
	}
}

func markFlattenedAncestryDead(idx flattenedPurgeIndex, st *childDocStripState) []bool {
	m := newFlattenedAncestryMark(idx)
	for i, node := range idx.nodes {
		if node == nil {
			continue
		}
		if idx.hasNodeID[i] {
			if _, hit := m.removedNodes[idx.nodeID[i]]; hit {
				m.markDead(i)
				continue
			}
		}
		if idx.hasBackend[i] {
			if _, hit := m.removedBackend[idx.backendID[i]]; hit {
				m.markDead(i)
				continue
			}
		}
		if flattenedProtectedDocument(node, st) {
			m.markDead(i)
		}
	}
	for len(m.idQueue) > 0 {
		id := m.idQueue[0]
		m.idQueue = m.idQueue[1:]
		if j, ok := idx.byNodeID[id]; ok {
			m.markDead(j)
		}
		for _, j := range idx.children[id] {
			m.markDead(j)
		}
	}
	return m.dead
}

func childDocNestedNodeIDs(node map[string]any) []int64 {
	if node == nil {
		return nil
	}
	var out []int64
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if id, ok := childDocMessageID(t["nodeId"]); ok {
				out = append(out, id)
			}
			if id, ok := childDocMessageID(t["backendNodeId"]); ok {
				out = append(out, id)
			}
			for _, key := range []string{"children", "shadowRoots", "pseudoElements", "distributedNodes", "contentDocument", "importedDocument", "templateContent"} {
				if raw, exists := t[key]; exists && raw != nil {
					walk(raw)
				}
			}
		case []any:
			for _, item := range t {
				walk(item)
			}
		}
	}
	for _, key := range []string{"children", "shadowRoots", "pseudoElements", "distributedNodes", "contentDocument", "importedDocument", "templateContent"} {
		if raw, exists := node[key]; exists && raw != nil {
			walk(raw)
		}
	}
	return out
}

func flattenedProtectedDocument(node map[string]any, st *childDocStripState) bool {
	if st == nil || !childDocMapIsDocumentNode(node) || childDocReplyRootExempt(node, st) {
		return false
	}
	docURL, docOK, docBad := childDocNodeURLField(node, "documentURL")
	baseURL, baseOK, baseBad := childDocNodeURLField(node, "baseURL")
	return docBad || baseBad || !childDocOrdinary(st.origins, docURL, baseURL, docOK, baseOK, true)
}

func childDocReplyRootExempt(doc map[string]any, st *childDocStripState) bool {
	if doc == nil || !childDocMapIsDocumentNode(doc) || st == nil {
		return false
	}
	docURL, docOK, docBad := childDocNodeURLField(doc, "documentURL")
	baseURL, baseOK, baseBad := childDocNodeURLField(doc, "baseURL")
	if docBad || baseBad {
		return false
	}
	if (docOK || baseOK) && !childDocOrdinary(st.origins, docURL, baseURL, docOK, baseOK, true) {
		return false
	}
	return true
}

func noteChildDocMark(st *childDocStripState, owner map[string]any) {
	if st == nil {
		return
	}
	st.hitProtected = true
	if owner != nil {
		if fid, ok := owner["frameId"].(string); ok && fid != "" {
			st.markFrames = append(st.markFrames, fid)
			return
		}
	}
	st.markPage = true
}

func (g *guard) clearChildDocWorldLatchesLocked(pageID string) {
	w := g.worlds[pageID]
	if w == nil {
		return
	}
	for _, wc := range w.contexts {
		if wc != nil {
			wc.protectedFrame = ""
		}
	}
}

func (g *guard) applyChildDocMarksLocked(session string, st *childDocStripState) (rebuild [][]byte) {
	if st == nil || (!st.hitProtected && len(st.markFrames) == 0 && !st.markPage) {
		return nil
	}
	page := g.sessionTargetLocked(session)
	if page == nil || page.gone {
		return nil
	}
	wasProtected := g.framesProtectedLocked(page.id)
	markPage := st.markPage
	for _, frameID := range st.markFrames {
		markPage = true
		if frameID == "" {
			continue
		}
		if t := g.targets[frameID]; t != nil && !t.gone {
			continue
		}
		w := g.worldsLocked(page.id)
		for _, wc := range w.contexts {
			if wc != nil && wc.frame == frameID {
				wc.protectedFrame = frameID
			}
		}
	}
	if markPage {
		page.childDocDOMBlocked = true
	}
	if markPage || len(st.markFrames) > 0 {
		g.logChildDocAdmitMeasureLocked(page.id, true, "childdoc-mark")
	} else if st.hitProtected {
		g.logChildDocAdmitMeasureLocked(page.id, true, "walk-refusal")
	}
	if !wasProtected && g.framesProtectedLocked(page.id) {
		rebuild = g.frameContentRebuildEventsLocked(page.id)
	}
	return rebuild
}

func childDocParamsStrip(m map[string]any) bool {
	if !childDocEventsStrip() {
		return false
	}
	method, _ := m["method"].(string)
	if method == "DOM.childNodeInserted" {
		return childDocChildInsertedStrip()
	}
	return true
}

func stripChildDocMapStep(t map[string]any, parentOrdinary bool, depth int, carrier childDocCarrier, st *childDocStripState) (changed, refuse bool, more []childDocJob) {
	if childDocCaptureSnapshotRefuse() {
		if r := childDocCaptureSnapshotCheck(st.origins, t, parentOrdinary); r {
			return false, true, nil
		}
	}
	if childDocGetSnapshotRefuse() {
		if r := childDocGetSnapshotCheck(st.origins, t, parentOrdinary); r {
			return false, true, nil
		}
	}
	c, r, own := stripChildDocOwnStep(t, parentOrdinary, depth, st)
	changed = changed || c
	if r {
		return changed, true, nil
	}
	more = append(more, own...)
	r, nests := stripChildDocArrayNests(t, parentOrdinary, depth)
	if r {
		return changed, true, nil
	}
	more = append(more, nests...)
	r, tmpl := stripChildDocTemplate(t, parentOrdinary, depth)
	if r {
		return changed, true, nil
	}
	more = append(more, tmpl...)
	c, r, other := stripChildDocOtherJobs(t, parentOrdinary, depth, carrier, st)
	changed = changed || c
	if r {
		return changed, true, nil
	}
	more = append(more, other...)
	return changed, false, more
}

func stripChildDocOwnStep(t map[string]any, parentOrdinary bool, depth int, st *childDocStripState) (changed, refuse bool, more []childDocJob) {
	for _, docKey := range childDocOwnKeys {
		if docKey == "importedDocument" && !childDocImportedStrip() {
			continue
		}
		raw, exists := t[docKey]
		if !exists || raw == nil {
			continue
		}
		doc, ok := raw.(map[string]any)
		if !ok {
			if childDocRefuseUnknown() {
				return changed, true, nil
			}
			continue
		}
		c, r, jobs := stripChildDocClassifyValue(t, doc, parentOrdinary, depth, st)
		changed = changed || c
		if r {
			return changed, true, nil
		}
		more = append(more, jobs...)
	}
	return changed, false, more
}

func stripChildDocClassifyValue(t map[string]any, doc map[string]any, parentOrdinary bool, depth int, st *childDocStripState) (changed, refuse bool, more []childDocJob) {
	docURL, docOK, docBad := childDocNodeURLField(doc, "documentURL")
	baseURL, baseOK, baseBad := childDocNodeURLField(doc, "baseURL")
	if !docBad && !baseBad && childDocExtensionURLs(st.origins, docURL, docOK, baseURL, baseOK) {
		return false, false, []childDocJob{{doc, parentOrdinary, depth + 1, childDocCarrierNone}}
	}
	if docBad || baseBad || !childDocOrdinary(st.origins, docURL, baseURL, docOK, baseOK, parentOrdinary) {
		noteChildDocMark(st, t)
		return false, true, nil
	}
	return false, false, []childDocJob{{doc, true, depth + 1, childDocCarrierNone}}
}

func childDocNodeURLField(doc map[string]any, key string) (s string, present, badType bool) {
	raw, exists := doc[key]
	if !exists || raw == nil {
		return "", false, false
	}
	s, ok := raw.(string)
	if !ok {
		return "", true, true
	}
	return s, true, false
}

func stripChildDocArrayNests(t map[string]any, parentOrdinary bool, depth int) (refuse bool, more []childDocJob) {
	for _, key := range childDocArrayNestKeys {
		if key == "shadowRoots" && !childDocShadowRecurse() {
			continue
		}
		if key == "distributedNodes" && !childDocDistributedWalk() {
			continue
		}
		if key == "pseudoElements" && !childDocTemplatePseudo() {
			continue
		}
		raw, exists := t[key]
		if !exists || raw == nil {
			continue
		}
		items, ok := raw.([]any)
		if !ok {
			if childDocRefuseUnknown() {
				return true, nil
			}
			continue
		}
		for i := len(items) - 1; i >= 0; i-- {
			if items[i] == nil {
				continue
			}
			more = append(more, childDocJob{items[i], parentOrdinary, depth + 1, childDocCarrierNone})
		}
	}
	return false, more
}

func stripChildDocTemplate(t map[string]any, parentOrdinary bool, depth int) (refuse bool, more []childDocJob) {
	if !childDocTemplatePseudo() {
		return false, nil
	}
	raw, exists := t["templateContent"]
	if !exists || raw == nil {
		return false, nil
	}
	doc, ok := raw.(map[string]any)
	if !ok {
		if childDocRefuseUnknown() {
			return true, nil
		}
		return false, nil
	}
	return false, []childDocJob{{doc, parentOrdinary, depth + 1, childDocCarrierNone}}
}

func stripChildDocOtherJobs(t map[string]any, parentOrdinary bool, depth int, carrier childDocCarrier, st *childDocStripState) (changed, refuse bool, more []childDocJob) {
	for k, val := range t {
		if k == "contentDocument" || k == "importedDocument" || k == "documents" || k == "domNodes" || k == "templateContent" || childDocNestKey(k) {
			continue
		}
		if val == nil {
			continue
		}
		if (carrier == childDocCarrierResult || carrier == childDocCarrierParams) && k == "nodes" {
			jobs, bad := childDocNodesJobs(val, parentOrdinary, depth)
			if bad {
				return changed, true, nil
			}
			more = append(more, jobs...)
			continue
		}
		doc, ok := val.(map[string]any)
		if !ok {
			more = append(more, childDocJob{val, parentOrdinary, depth + 1, childDocCarrierNone})
			continue
		}
		if !childDocMapIsDocumentNode(doc) {
			more = append(more, childDocJob{doc, parentOrdinary, depth + 1, childDocCarrierNone})
			continue
		}
		c, r, jobs := stripChildDocDocumentField(t, k, doc, parentOrdinary, depth, carrier, st)
		changed = changed || c
		if r {
			return changed, true, nil
		}
		more = append(more, jobs...)
	}
	return changed, false, more
}

func childDocNodesJobs(val any, parentOrdinary bool, depth int) (more []childDocJob, refuse bool) {
	items, ok := val.([]any)
	if !ok {
		return nil, childDocRefuseUnknown()
	}
	for i := len(items) - 1; i >= 0; i-- {
		if items[i] == nil {
			continue
		}
		more = append(more, childDocJob{items[i], parentOrdinary, depth + 1, childDocCarrierNone})
	}
	return more, false
}

func stripChildDocDocumentField(t map[string]any, k string, doc map[string]any, parentOrdinary bool, depth int, carrier childDocCarrier, st *childDocStripState) (changed, refuse bool, more []childDocJob) {
	if (carrier == childDocCarrierResult || carrier == childDocCarrierParams) && (k == "root" || k == "node") {
		if childDocReplyRootExempt(doc, st) {
			return false, false, []childDocJob{{doc, true, depth + 1, childDocCarrierNone}}
		}
		noteChildDocMark(st, nil)
		return false, true, nil
	}
	if childDocPathExempt(carrier, k) && carrier != childDocCarrierResult && carrier != childDocCarrierParams {
		return false, false, []childDocJob{{doc, true, depth + 1, childDocCarrierNone}}
	}
	return stripChildDocClassifyValue(t, doc, parentOrdinary, depth, st)
}

func childDocPathExempt(carrier childDocCarrier, key string) bool {
	switch carrier {
	case childDocCarrierResult:
		return key == "root" || key == "node"
	case childDocCarrierParams:
		return key == "node"
	default:
		return false
	}
}

func childDocMapIsDocumentNode(doc map[string]any) bool {
	if name, ok := doc["nodeName"].(string); ok && name == "#document" {
		return true
	}
	if n, ok := childDocIndex(doc["nodeType"]); ok && n == 9 {
		return true
	}
	return false
}

func childDocNestKey(k string) bool {
	for _, nest := range childDocNestKeys {
		if k == nest {
			return true
		}
	}
	return false
}

func childDocCaptureSnapshotCheck(o *protect.Origins, t map[string]any, parentOrdinary bool) bool {
	rawDocs, hasDocs := t["documents"]
	if !hasDocs || rawDocs == nil {
		return false
	}
	docs, ok := rawDocs.([]any)
	if !ok {
		return childDocRefuseUnknown()
	}
	if len(docs) == 0 {
		return false
	}
	rawStrings, hasStringsKey := t["strings"]
	hasStrings := false
	var stringsTable []any
	if hasStringsKey && rawStrings != nil {
		stringsTable, ok = rawStrings.([]any)
		if !ok {
			return true
		}
		hasStrings = true
	}
	for _, item := range docs {
		if item == nil {
			return childDocRefuseUnknown()
		}
		doc, ok := item.(map[string]any)
		if !ok {
			return childDocRefuseUnknown()
		}
		docURL, docPresent, docRefuse := childDocResolveCaptureURL(stringsTable, hasStrings, doc["documentURL"])
		if docRefuse {
			return childDocStringIndexStrict()
		}
		baseURL, basePresent, baseRefuse := childDocResolveCaptureURL(stringsTable, hasStrings, doc["baseURL"])
		if baseRefuse {
			return childDocStringIndexStrict()
		}
		if !docRefuse && !baseRefuse && childDocExtensionURLs(o, docURL, docPresent, baseURL, basePresent) {
			continue
		}
		if !childDocOrdinary(o, docURL, baseURL, docPresent, basePresent, parentOrdinary) {
			return true
		}
	}
	return false
}

func childDocResolveCaptureURL(strings []any, hasStrings bool, raw any) (s string, present, refuse bool) {
	if raw == nil {
		return "", false, false
	}
	if str, ok := raw.(string); ok {
		return str, true, false
	}
	n, ok := childDocIndex(raw)
	if !ok {
		return "", false, true
	}
	if !hasStrings {
		return "", false, true
	}
	if n < 0 {
		return "", false, false
	}
	if n >= len(strings) {
		return "", false, true
	}
	str, ok := strings[n].(string)
	if !ok {
		return "", false, true
	}
	return str, true, false
}

func childDocGetSnapshotCheck(o *protect.Origins, t map[string]any, parentOrdinary bool) bool {
	raw, has := t["domNodes"]
	if !has || raw == nil {
		return false
	}
	nodes, ok := raw.([]any)
	if !ok {
		return childDocRefuseUnknown()
	}
	for _, item := range nodes {
		if item == nil {
			return childDocRefuseUnknown()
		}
		node, ok := item.(map[string]any)
		if !ok {
			return childDocRefuseUnknown()
		}
		if childDocGetSnapshotNodeCheck(o, nodes, node, parentOrdinary) {
			return true
		}
	}
	return false
}

func childDocGetSnapshotNodeCheck(o *protect.Origins, nodes []any, node map[string]any, parentOrdinary bool) bool {
	_, hasDoc := node["documentURL"]
	_, hasBase := node["baseURL"]
	if hasDoc || hasBase || childDocMapIsDocumentNode(node) {
		docURL, docOK, docRefuse := childDocResolveGetSnapshotURL(node["documentURL"])
		if docRefuse {
			return true
		}
		baseURL, baseOK, baseRefuse := childDocResolveGetSnapshotURL(node["baseURL"])
		if baseRefuse {
			return true
		}
		if childDocExtensionURLs(o, docURL, docOK, baseURL, baseOK) {
			return false
		}
		if !childDocOrdinary(o, docURL, baseURL, docOK, baseOK, parentOrdinary) {
			return true
		}
	}
	rawIdx, hasIdx := node["contentDocumentIndex"]
	if !hasIdx || rawIdx == nil {
		return false
	}
	n, ok := childDocIndex(rawIdx)
	if !ok || n < 0 || n >= len(nodes) {
		return true
	}
	child, ok := nodes[n].(map[string]any)
	if !ok {
		return true
	}
	docURL, docOK, docRefuse := childDocResolveGetSnapshotURL(child["documentURL"])
	if docRefuse {
		return true
	}
	baseURL, baseOK, baseRefuse := childDocResolveGetSnapshotURL(child["baseURL"])
	if baseRefuse {
		return true
	}
	if childDocExtensionURLs(o, docURL, docOK, baseURL, baseOK) {
		return false
	}
	return !childDocOrdinary(o, docURL, baseURL, docOK, baseOK, parentOrdinary)
}

func childDocExtensionURLs(o *protect.Origins, docURL string, docOK bool, baseURL string, baseOK bool) bool {
	return (docOK && o.ExtensionFrame(docURL)) || (baseOK && o.ExtensionFrame(baseURL))
}

func childDocResolveGetSnapshotURL(raw any) (s string, present, refuse bool) {
	if raw == nil {
		return "", false, false
	}
	if str, ok := raw.(string); ok {
		return str, true, false
	}
	return "", false, true
}

func childDocIndex(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

func childDocRefuseMessage(m map[string]any, id *int64, session string) []byte {
	if id != nil {
		return refusal(*id, session, refusedTarget)
	}
	mid, ok := childDocMessageID(m["id"])
	if !ok {
		return nil
	}
	if s, _ := m["sessionId"].(string); s != "" {
		session = s
	}
	return refusal(mid, session, refusedTarget)
}

func childDocMessageID(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}
