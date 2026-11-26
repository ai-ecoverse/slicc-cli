package cdp

import (
	"encoding/json"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func childDocClearOnAdmit() bool { return false }

func childDocEvictMethodRecord() bool { return false }

func childDocMethodGate() bool { return true }

func childDocReplyGate() bool { return true }

func (g *guard) peekChildDocReplyLocked(m *message) (method string, snap childDocSnapshot, ok bool) {
	return "", childDocSnapshot{}, false
}

func (g *guard) snapshotChildDocLocked(session string) childDocSnapshot {
	return childDocSnapshot{session: session}
}

func applyChildDocOnSnapshot(data []byte, method string, snap childDocSnapshot, id *int64) (out []byte, drop bool, st *childDocStripState) {
	return data, false, nil
}

func childDocBytesReplaced(in, out []byte) bool {
	return len(out) > 0
}

func (g *guard) clearChildDocWorldLatchesLocked(pageID string) {}

func (g *guard) applyChildDocMarksLocked(session string, st *childDocStripState) [][]byte {
	return nil
}

func (g *guard) refreshChildDocMessage(m *message, out []byte) {}

type childDocSnapshot struct {
	session   string
	targetID  string
	targetURL string
}

const (
	childDocTightDepth     = 24
	childDocTightNodes     = 4096
	maxChildDocMethods     = 4096
	refusedChildDocMethods = "too many DOM tree commands are waiting for their replies to be scrubbed"
)

var childDocMethods = map[callKey]string{}

type childDocStripState struct {
	session           string
	targetID          string
	targetURL         string
	hitProtected      bool
	markFrames        []string
	markPage          bool
	removedNodeIDs    map[int64]struct{}
	removedBackendIDs map[int64]struct{}
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

func (g *guard) childDocOrdinary(documentURL, baseURL string, documentPresent, basePresent, parentOrdinary bool) bool {
	if !childDocClassifyFull() {
		return g.childDocOrdinaryLoadedOnly(documentURL, baseURL, documentPresent, basePresent)
	}
	if !childDocBothFields() {
		return g.childDocOrdinaryShortCircuit(documentURL, baseURL, parentOrdinary)
	}
	return g.childDocOrdinaryBoth(documentURL, baseURL, documentPresent, basePresent, parentOrdinary)
}

func (g *guard) childDocOrdinaryLoadedOnly(documentURL, baseURL string, documentPresent, basePresent bool) bool {
	u := ""
	if documentPresent {
		u = documentURL
	}
	if u == "" && basePresent {
		u = baseURL
	}
	if u == "" {
		return true
	}
	return !g.o.Loaded(u)
}

func (g *guard) childDocOrdinaryShortCircuit(documentURL, baseURL string, parentOrdinary bool) bool {
	for _, u := range []string{documentURL, baseURL} {
		if u != "" && protect.Inheriting(u) {
			return parentOrdinary
		}
	}
	u := documentURL
	if u == "" {
		u = baseURL
	}
	if u == "" {
		return false
	}
	if g.o.Loaded(documentURL) || g.o.Loaded(baseURL) || g.o.Loaded(u) {
		return false
	}
	if protect.Unparseable(documentURL) || protect.Unparseable(baseURL) || protect.Unparseable(u) {
		return false
	}
	return true
}

func (g *guard) childDocOrdinaryBoth(documentURL, baseURL string, documentPresent, basePresent, parentOrdinary bool) bool {
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
		if g.o.Loaded(f.u) || protect.Unparseable(f.u) {
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

func (g *guard) noteChildDocAdmitMeasureLocked(c *command) {
	if c == nil || c.method != "DOM.getDocument" {
		return
	}
	if t := g.sessionTargetLocked(c.session); t != nil {
		g.logChildDocAdmitMeasureLocked(t.id, false, "admit")
	}
}

func (g *guard) setPendingLocked(k callKey, kind callKind, method string) {
	g.pending[k] = kind
	delete(g.pendingMethods, k)
	_ = g.noteChildDocMethodLocked(k, method)
}

func (g *guard) clearPendingLocked(k callKey) callKind {
	kind := g.pending[k]
	delete(g.pending, k)
	delete(g.pendingMethods, k)
	return kind
}

func (g *guard) noteChildDocMethodLocked(k callKey, method string) string {
	if childDocClearOnAdmit() {
		delete(childDocMethods, k)
	}
	if !childDocCommandWalkMethods[method] {
		return ""
	}
	if _, exists := childDocMethods[k]; exists {
		childDocMethods[k] = method
		return ""
	}
	if len(childDocMethods) >= maxChildDocMethods {
		if !childDocEvictMethodRecord() {
			return refusedChildDocMethods
		}
		for old := range childDocMethods {
			delete(childDocMethods, old)
			break
		}
	}
	childDocMethods[k] = method
	return ""
}

func (g *guard) takeChildDocMethodLocked(k callKey) (string, bool) {
	if childDocMethods == nil {
		return "", false
	}
	method, ok := childDocMethods[k]
	if ok {
		delete(childDocMethods, k)
	}
	return method, ok
}

func (g *guard) dropChildDocMethodLocked(k callKey) {
	delete(childDocMethods, k)
	delete(g.pendingMethods, k)
}

func (g *guard) childDocShouldWalkLegacyLocked(m map[string]any) bool {
	if method, ok := m["method"].(string); ok && method != "" {
		if !childDocMethodGate() {
			return true
		}
		if !childDocEventsStrip() {
			return false
		}
		return childDocEventWalkMethods[method]
	}
	id, ok := childDocMessageID(m["id"])
	session, _ := m["sessionId"].(string)
	var method string
	var recorded bool
	if ok {
		method, recorded = g.takeChildDocMethodLocked(callKey{session, id})
	}
	if !childDocMethodGate() {
		return true
	}
	if !childDocReplyGate() {
		return true
	}
	if !ok || !recorded {
		return false
	}
	return childDocCommandWalkMethods[method]
}

func (g *guard) scrubChildDocuments(m map[string]any) (changed, refuse bool) {
	if !childDocStripEnabled() || m == nil {
		return false, false
	}
	if childDocStripBeforeHold() {
		return false, false
	}
	session, _ := m["sessionId"].(string)
	g.mu.Lock()
	walk := g.childDocShouldWalkLegacyLocked(m)
	g.mu.Unlock()
	if !walk {
		return false, false
	}
	return g.scrubChildDocumentsWalk(m, session)
}

func (g *guard) stripChildDocBrowserEventLocked(m *message, data []byte) (out []byte, drop, handled bool) {
	if m == nil || !childDocEventWalkMethods[m.Method] {
		return data, false, false
	}
	out, drop = g.applyChildDocStripLocked(data, m.Method, m.SessionID, m.ID)
	if drop {
		return nil, true, true
	}
	if err := json.Unmarshal(out, m); err != nil {
		return nil, true, true
	}
	return out, false, true
}

func (g *guard) stripChildDocReplyBeforeHoldLocked(m *message, data []byte, method string) (out []byte, drop, handled bool) {
	if !childDocStripBeforeHold() || method == "" || m == nil || m.Error != nil {
		return data, false, false
	}
	out, drop = g.applyChildDocStripLocked(data, method, m.SessionID, m.ID)
	if drop {
		return nil, true, true
	}
	var refreshed message
	if err := json.Unmarshal(out, &refreshed); err == nil {
		m.Result = refreshed.Result
		m.Error = refreshed.Error
	}
	return out, false, true
}

func (g *guard) applyChildDocStripLocked(data []byte, method, session string, id *int64) (out []byte, drop bool) {
	if !childDocStripEnabled() || method == "" {
		return data, false
	}
	if childDocEventWalkMethods[method] {
		if !childDocEventsStrip() {
			return data, false
		}
		if method == "DOM.childNodeInserted" && !childDocChildInsertedStrip() {
			return data, false
		}
	} else if !childDocCommandWalkMethods[method] {
		return data, false
	}
	m, ok := decodeObject(data)
	if !ok {
		if childDocDecodeFailOpen() {
			return data, false
		}
		if childDocEventWalkMethods[method] {
			return nil, true
		}
		if id != nil {
			return refusal(*id, session, refusedTarget), false
		}
		return nil, true
	}
	changed, refuse := g.scrubChildDocumentsWalk(m, session)
	if refuse {
		if childDocEventWalkMethods[method] {
			return nil, true
		}
		return childDocRefuseMessage(m, id, session), false
	}
	if !changed {
		return data, false
	}
	return encode(m), false
}

func (g *guard) scrubChildDocumentsWalk(m map[string]any, session string) (changed, refuse bool) {
	st := &childDocStripState{
		session:           session,
		removedNodeIDs:    map[int64]struct{}{},
		removedBackendIDs: map[int64]struct{}{},
	}
	stack := make([]childDocJob, 0, 64)
	if result, ok := m["result"]; ok {
		stack = append(stack, childDocJob{result, true, 0, childDocCarrierResult})
	}
	if params, ok := m["params"]; ok && childDocParamsStrip(m) {
		stack = append(stack, childDocJob{params, true, 0, childDocCarrierParams})
	}
	nodes := 0
	for len(stack) > 0 {
		job := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if childDocTightLimits() && (job.depth > childDocTightDepth || nodes > childDocTightNodes) {
			return changed, true
		}
		switch t := job.v.(type) {
		case map[string]any:
			nodes++
			if childDocTightLimits() && nodes > childDocTightNodes {
				return changed, true
			}
			if false {
				sink := 0
				for i := 0; i < nodes; i++ {
					sink += i
				}
				_ = sink
			}
			c, r, more := g.stripChildDocMapStep(t, job.parentOrdinary, job.depth, job.carrier, st)
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
	if c := g.purgeFlattenedChildDocs(m, st); c {
		changed = true
	}
	return changed, false
}

func (g *guard) purgeFlattenedChildDocs(m map[string]any, st *childDocStripState) bool {
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
	if childDocFlattenedPurgeDirectOnly() {
		if len(st.removedNodeIDs) == 0 && len(st.removedBackendIDs) == 0 {
			return false
		}
		return g.purgeFlattenedChildDocsDirectOnly(raw, result, st)
	}
	return g.purgeFlattenedChildDocsAncestry(raw, result, st)
}

func (g *guard) purgeFlattenedChildDocsDirectOnly(raw []any, result map[string]any, st *childDocStripState) bool {
	removedNodes := map[int64]struct{}{}
	for id := range st.removedNodeIDs {
		removedNodes[id] = struct{}{}
	}
	removedBackend := map[int64]struct{}{}
	for id := range st.removedBackendIDs {
		removedBackend[id] = struct{}{}
	}
	changed := false
	for {
		progress := false
		next := raw[:0:len(raw)]
		for _, item := range raw {
			keep, marked := g.flattenedNodeKeepDirect(item, st.session, removedNodes, removedBackend)
			if marked {
				changed = true
				progress = true
			}
			if keep {
				next = append(next, item)
			}
		}
		raw = next
		if !progress {
			break
		}
	}
	result["nodes"] = raw
	return changed
}

func (g *guard) purgeFlattenedChildDocsAncestry(raw []any, result map[string]any, st *childDocStripState) bool {
	n := len(raw)
	nodeID := make([]int64, n)
	backendID := make([]int64, n)
	hasNodeID := make([]bool, n)
	hasBackend := make([]bool, n)
	nested := make([][]int64, n)
	nodes := make([]map[string]any, n)
	byNodeID := map[int64]int{}
	children := map[int64][]int{}
	for i, item := range raw {
		node, ok := item.(map[string]any)
		if !ok || node == nil {
			continue
		}
		nodes[i] = node
		if id, ok := childDocMessageID(node["nodeId"]); ok {
			nodeID[i], hasNodeID[i] = id, true
			byNodeID[id] = i
		}
		if id, ok := childDocMessageID(node["backendNodeId"]); ok {
			backendID[i], hasBackend[i] = id, true
		}
		if id, ok := childDocMessageID(node["parentId"]); ok {
			children[id] = append(children[id], i)
		}
		nested[i] = childDocNestedNodeIDs(node)
	}
	removedNodes := map[int64]struct{}{}
	for id := range st.removedNodeIDs {
		removedNodes[id] = struct{}{}
	}
	removedBackend := map[int64]struct{}{}
	for id := range st.removedBackendIDs {
		removedBackend[id] = struct{}{}
	}
	dead := make([]bool, n)
	idQueue := make([]int64, 0, len(removedNodes)+n)
	seenID := map[int64]struct{}{}
	enqueueID := func(id int64) {
		if _, ok := seenID[id]; ok {
			return
		}
		seenID[id] = struct{}{}
		removedNodes[id] = struct{}{}
		idQueue = append(idQueue, id)
	}
	for id := range removedNodes {
		enqueueID(id)
	}
	markDead := func(i int) {
		if i < 0 || i >= n || dead[i] || nodes[i] == nil {
			return
		}
		dead[i] = true
		if hasNodeID[i] {
			enqueueID(nodeID[i])
		}
		if hasBackend[i] {
			removedBackend[backendID[i]] = struct{}{}
		}
		for _, nid := range nested[i] {
			enqueueID(nid)
		}
	}
	for i := 0; i < n; i++ {
		if nodes[i] == nil {
			continue
		}
		if hasNodeID[i] {
			if _, hit := removedNodes[nodeID[i]]; hit {
				markDead(i)
				continue
			}
		}
		if hasBackend[i] {
			if _, hit := removedBackend[backendID[i]]; hit {
				markDead(i)
				continue
			}
		}
		if g.flattenedProtectedDocument(nodes[i], st.session) {
			markDead(i)
		}
	}
	for len(idQueue) > 0 {
		id := idQueue[0]
		idQueue = idQueue[1:]
		if j, ok := byNodeID[id]; ok {
			markDead(j)
		}
		for _, j := range children[id] {
			markDead(j)
		}
	}
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

func childDocMarkRemoved(node map[string]any, removedNodes, removedBackend map[int64]struct{}) {
	if nid, ok := childDocMessageID(node["nodeId"]); ok {
		removedNodes[nid] = struct{}{}
	}
	if bid, ok := childDocMessageID(node["backendNodeId"]); ok {
		removedBackend[bid] = struct{}{}
	}
	if childDocFlattenedPurgeDirectOnly() {
		return
	}
	for _, id := range childDocNestedNodeIDs(node) {
		removedNodes[id] = struct{}{}
	}
}

func (g *guard) flattenedNodeKeepDirect(item any, session string, removedNodes, removedBackend map[int64]struct{}) (keep, marked bool) {
	node, ok := item.(map[string]any)
	if !ok || node == nil {
		return true, false
	}
	if id, ok := childDocMessageID(node["nodeId"]); ok {
		if _, hit := removedNodes[id]; hit {
			return false, true
		}
	}
	if id, ok := childDocMessageID(node["backendNodeId"]); ok {
		if _, hit := removedBackend[id]; hit {
			if nid, ok := childDocMessageID(node["nodeId"]); ok {
				removedNodes[nid] = struct{}{}
			}
			if bid, ok := childDocMessageID(node["backendNodeId"]); ok {
				removedBackend[bid] = struct{}{}
			}
			return false, true
		}
	}
	if pid, ok := childDocMessageID(node["parentId"]); ok {
		if _, hit := removedNodes[pid]; hit {
			if nid, ok := childDocMessageID(node["nodeId"]); ok {
				removedNodes[nid] = struct{}{}
			}
			if bid, ok := childDocMessageID(node["backendNodeId"]); ok {
				removedBackend[bid] = struct{}{}
			}
			return false, true
		}
	}
	if g.flattenedProtectedDocument(node, session) {
		if nid, ok := childDocMessageID(node["nodeId"]); ok {
			removedNodes[nid] = struct{}{}
		}
		if bid, ok := childDocMessageID(node["backendNodeId"]); ok {
			removedBackend[bid] = struct{}{}
		}
		return false, true
	}
	return true, false
}

func (g *guard) flattenedProtectedDocument(node map[string]any, session string) bool {
	if !childDocMapIsDocumentNode(node) || g.childDocReplyRootExemptLocked(node, session) {
		return false
	}
	docURL, docOK, docBad := childDocNodeURLField(node, "documentURL")
	baseURL, baseOK, baseBad := childDocNodeURLField(node, "baseURL")
	return docBad || baseBad || !g.childDocOrdinary(docURL, baseURL, docOK, baseOK, true)
}

func (g *guard) noteRemovedChildDoc(st *childDocStripState, doc map[string]any) {
	if st == nil || doc == nil {
		return
	}
	if id, ok := childDocMessageID(doc["nodeId"]); ok {
		st.removedNodeIDs[id] = struct{}{}
	}
	if id, ok := childDocMessageID(doc["backendNodeId"]); ok {
		st.removedBackendIDs[id] = struct{}{}
	}
	if childDocFlattenedPurgeDirectOnly() {
		return
	}
	for _, id := range childDocNestedNodeIDs(doc) {
		st.removedNodeIDs[id] = struct{}{}
	}
}

func (g *guard) childDocReplyRootExemptLocked(doc map[string]any, session string) bool {
	if doc == nil || !childDocMapIsDocumentNode(doc) {
		return false
	}
	t := g.sessionTargetLocked(session)
	if t == nil {
		return false
	}
	docURL, docOK, docBad := childDocNodeURLField(doc, "documentURL")
	baseURL, baseOK, baseBad := childDocNodeURLField(doc, "baseURL")
	if docBad || baseBad {
		return false
	}
	if (docOK || baseOK) && !g.childDocOrdinary(docURL, baseURL, docOK, baseOK, true) {
		return false
	}
	if frameID, ok := doc["frameId"].(string); ok && frameID != "" && frameID != t.id {
		return false
	}
	if docOK && docURL != "" && t.url != "" && !protect.Inheriting(docURL) {
		if g.o.Loaded(docURL) {
			return false
		}
	}
	return true
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

func (g *guard) stripChildDocMapStep(t map[string]any, parentOrdinary bool, depth int, carrier childDocCarrier, st *childDocStripState) (changed, refuse bool, more []childDocJob) {
	if childDocCaptureSnapshotRefuse() {
		if r := g.childDocCaptureSnapshotCheck(t, parentOrdinary); r {
			return false, true, nil
		}
	}
	if childDocGetSnapshotRefuse() {
		if r := g.childDocGetSnapshotCheck(t, parentOrdinary); r {
			return false, true, nil
		}
	}
	c, r, own := g.stripChildDocOwnStep(t, parentOrdinary, depth, st)
	changed = changed || c
	if r {
		return changed, true, nil
	}
	more = append(more, own...)
	r, nests := g.stripChildDocArrayNests(t, parentOrdinary, depth)
	if r {
		return changed, true, nil
	}
	more = append(more, nests...)
	r, tmpl := g.stripChildDocTemplate(t, parentOrdinary, depth)
	if r {
		return changed, true, nil
	}
	more = append(more, tmpl...)
	c, r, other := g.stripChildDocOtherJobs(t, parentOrdinary, depth, carrier, st)
	changed = changed || c
	if r {
		return changed, true, nil
	}
	more = append(more, other...)
	return changed, false, more
}

func (g *guard) stripChildDocOwnStep(t map[string]any, parentOrdinary bool, depth int, st *childDocStripState) (changed, refuse bool, more []childDocJob) {
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
		c, jobs := g.stripChildDocClassifyValue(t, docKey, doc, parentOrdinary, depth, docKey == "contentDocument", st)
		changed = changed || c
		more = append(more, jobs...)
	}
	return changed, false, more
}

func (g *guard) stripChildDocClassifyValue(t map[string]any, key string, doc map[string]any, parentOrdinary bool, depth int, dropFrame bool, st *childDocStripState) (changed bool, more []childDocJob) {
	docURL, docOK, docBad := childDocNodeURLField(doc, "documentURL")
	baseURL, baseOK, baseBad := childDocNodeURLField(doc, "baseURL")
	if !docBad && !baseBad && ((docOK && g.o.ExtensionFrame(docURL)) || (baseOK && g.o.ExtensionFrame(baseURL))) {
		return false, []childDocJob{{doc, parentOrdinary, depth + 1, childDocCarrierNone}}
	}
	if docBad || baseBad || !g.childDocOrdinary(docURL, baseURL, docOK, baseOK, parentOrdinary) {
		g.noteRemovedChildDoc(st, doc)
		delete(t, key)
		if dropFrame {
			delete(t, "frameId")
		}
		return true, nil
	}
	return false, []childDocJob{{doc, true, depth + 1, childDocCarrierNone}}
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

func (g *guard) stripChildDocArrayNests(t map[string]any, parentOrdinary bool, depth int) (refuse bool, more []childDocJob) {
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

func (g *guard) stripChildDocTemplate(t map[string]any, parentOrdinary bool, depth int) (refuse bool, more []childDocJob) {
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

func (g *guard) stripChildDocOtherJobs(t map[string]any, parentOrdinary bool, depth int, carrier childDocCarrier, st *childDocStripState) (changed, refuse bool, more []childDocJob) {
	session := ""
	if st != nil {
		session = st.session
	}
	for k, val := range t {
		if k == "contentDocument" || k == "importedDocument" || k == "documents" || k == "domNodes" || k == "templateContent" || childDocNestKey(k) {
			continue
		}
		if val == nil {
			continue
		}
		if (carrier == childDocCarrierResult || carrier == childDocCarrierParams) && k == "nodes" {
			more = append(more, childDocNodesJobs(val, parentOrdinary, depth)...)
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
		c, r, jobs := g.stripChildDocDocumentField(t, k, doc, parentOrdinary, depth, carrier, session, st)
		changed = changed || c
		if r {
			return changed, true, nil
		}
		more = append(more, jobs...)
	}
	return changed, false, more
}

func childDocNodesJobs(val any, parentOrdinary bool, depth int) []childDocJob {
	items, ok := val.([]any)
	if !ok {
		return nil
	}
	var more []childDocJob
	for i := len(items) - 1; i >= 0; i-- {
		if items[i] == nil {
			continue
		}
		more = append(more, childDocJob{items[i], parentOrdinary, depth + 1, childDocCarrierNone})
	}
	return more
}

func (g *guard) stripChildDocDocumentField(t map[string]any, k string, doc map[string]any, parentOrdinary bool, depth int, carrier childDocCarrier, session string, st *childDocStripState) (changed, refuse bool, more []childDocJob) {
	if (carrier == childDocCarrierResult || carrier == childDocCarrierParams) && (k == "root" || k == "node") {
		if g.childDocReplyRootExemptLocked(doc, session) {
			return false, false, []childDocJob{{doc, true, depth + 1, childDocCarrierNone}}
		}
		return false, true, nil
	}
	if childDocPathExempt(carrier, k) && carrier != childDocCarrierResult && carrier != childDocCarrierParams {
		return false, false, []childDocJob{{doc, true, depth + 1, childDocCarrierNone}}
	}
	c, jobs := g.stripChildDocClassifyValue(t, k, doc, parentOrdinary, depth, false, st)
	return c, false, jobs
}

func childDocPathExempt(carrier childDocCarrier, key string) bool {
	if childDocExemptByKeyAnywhere() {
		return key == "root" || key == "node"
	}
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

func (g *guard) childDocCaptureSnapshotCheck(t map[string]any, parentOrdinary bool) bool {
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
	if childDocIgnoreNonWireShapes() && !hasStrings {
		return false
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
		if childDocCaptureAbsentURLOK() && !docPresent && !basePresent {
			continue
		}
		if !docRefuse && !baseRefuse && g.childDocExtensionURLs(docURL, docPresent, baseURL, basePresent) {
			continue
		}
		if !g.childDocOrdinary(docURL, baseURL, docPresent, basePresent, parentOrdinary) {
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
		return "", false, !childDocCaptureURLTypeLax()
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

func (g *guard) childDocGetSnapshotCheck(t map[string]any, parentOrdinary bool) bool {
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
		if g.childDocGetSnapshotNodeCheck(nodes, node, parentOrdinary) {
			return true
		}
	}
	return false
}

func (g *guard) childDocGetSnapshotNodeCheck(nodes []any, node map[string]any, parentOrdinary bool) bool {
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
		if g.childDocExtensionURLs(docURL, docOK, baseURL, baseOK) {
			return false
		}
		if (!childDocCaptureAbsentURLOK() || docOK || baseOK) && !g.childDocOrdinary(docURL, baseURL, docOK, baseOK, parentOrdinary) {
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
	if childDocCaptureAbsentURLOK() && !docOK && !baseOK {
		return false
	}
	if g.childDocExtensionURLs(docURL, docOK, baseURL, baseOK) {
		return false
	}
	return !g.childDocOrdinary(docURL, baseURL, docOK, baseOK, parentOrdinary)
}

func (g *guard) childDocExtensionURLs(docURL string, docOK bool, baseURL string, baseOK bool) bool {
	return (docOK && g.o.ExtensionFrame(docURL)) || (baseOK && g.o.ExtensionFrame(baseURL))
}

func childDocResolveGetSnapshotURL(raw any) (s string, present, refuse bool) {
	if raw == nil {
		return "", false, false
	}
	if str, ok := raw.(string); ok {
		return str, true, false
	}
	return "", false, !childDocCaptureURLTypeLax()
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
	mid, ok := childDocMessageID(m["id"])
	if !ok {
		if id == nil {
			return nil
		}
		mid = *id
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
