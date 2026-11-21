package cdp

import (
	"encoding/json"
	"strings"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

var frameCarrierKept = map[string]bool{
	"id": true, "parentId": true, "loaderId": true, "url": true, "securityOrigin": true,
	"unreachableUrl": true, "mimeType": true, "secureContextType": true, "crossOriginIsolatedContextType": true,
}

type carrierScope struct {
	session  string
	target   string
	evidence bool
}

func (g *guard) scrubFrameCarriers(m map[string]any) bool {
	if !frameCarrierBlankEnabled() {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	session, _ := m["sessionId"].(string)
	sc := carrierScope{session: session, target: g.worldTargetLocked(session)}
	changed := false
	if res, ok := m["result"].(map[string]any); ok {
		if g.carrierResultLocked(res, &sc) {
			changed = true
		}
	}
	if p, ok := m["params"].(map[string]any); ok {
		method, _ := m["method"].(string)
		if g.carrierParamsLocked(method, p, &sc) {
			changed = true
		}
	}
	return changed
}

func (g *guard) carrierResultLocked(res map[string]any, sc *carrierScope) bool {
	changed := false
	if tree, ok := res["frameTree"].(map[string]any); ok {
		sc.evidence = g.carrierTreeEvidenceLocked(tree, 0)
		if g.carrierTreeLocked(tree, "", g.targetProtectedLocked(sc.target), false, true, sc, 0) {
			changed = true
		}
	}
	if entries, ok := res["entries"].([]any); ok {
		for _, e := range entries {
			if entry, ok := e.(map[string]any); ok && g.carrierHistoryEntryLocked(entry, sc) {
				changed = true
			}
		}
	}
	if info, ok := res["targetInfo"].(map[string]any); ok && g.carrierTargetInfoLocked(info) {
		changed = true
	}
	if list, ok := res["targetInfos"].([]any); ok {
		for _, item := range list {
			if info, ok := item.(map[string]any); ok && g.carrierTargetInfoLocked(info) {
				changed = true
			}
		}
	}
	return changed
}

func (g *guard) carrierParamsLocked(method string, p map[string]any, sc *carrierScope) bool {
	changed := false
	if info, ok := p["targetInfo"].(map[string]any); ok && g.carrierTargetInfoLocked(info) {
		changed = true
	}
	switch method {
	case "Page.frameNavigated", "Page.documentOpened":
		changed = g.carrierEventFrameLocked(p, sc) || changed
	case "Page.frameAttached":
		if frameCarrierChildEventsEnabled() && g.carrierStackLoaded(p["stack"], 0) {
			delete(p, "stack")
			changed = true
		}
	case "Page.windowOpen":
		changed = g.carrierWindowOpen(p) || changed
	case "Page.downloadWillBegin", "Browser.downloadWillBegin":
		if u, _ := p["url"].(string); g.o.Loaded(u) {
			if s, ok := p["suggestedFilename"].(string); ok && s != "" {
				p["suggestedFilename"], changed = "", true
			}
		}
	}
	return changed
}

func (g *guard) carrierEventFrameLocked(p map[string]any, sc *carrierScope) bool {
	f, ok := p["frame"].(map[string]any)
	if !ok {
		return false
	}
	parent, _ := f["parentId"].(string)
	if parent != "" && !frameCarrierChildEventsEnabled() {
		return false
	}
	if g.carrierEventFrameProtectedLocked(f, sc) {
		return blankFrameCarriers(f)
	}
	return parent != "" && g.carrierEventAncestorLocked(parent, sc) && blankCarrierName(f)
}

func (g *guard) carrierWindowOpen(p map[string]any) bool {
	u, _ := p["url"].(string)
	if !g.o.Loaded(u) {
		return false
	}
	changed := false
	if s, ok := p["windowName"].(string); ok && s != "" {
		p["windowName"], changed = "", true
	}
	if list, ok := p["windowFeatures"].([]any); ok && len(list) > 0 {
		p["windowFeatures"], changed = []any{}, true
	}
	return changed
}

func (g *guard) carrierStackLoaded(v any, depth int) bool {
	stack, ok := v.(map[string]any)
	if !ok || depth > maxFrameAncestors {
		return false
	}
	if frames, ok := stack["callFrames"].([]any); ok {
		for _, item := range frames {
			cf, _ := item.(map[string]any)
			if u, _ := cf["url"].(string); g.carrierLoaded(u) {
				return true
			}
		}
	}
	return g.carrierStackLoaded(stack["parent"], depth+1)
}

func (g *guard) carrierLoaded(raw string) bool {
	if raw == "" || !g.o.Loaded(raw) {
		return false
	}
	return !frameCarrierExtensionExcluded() || !g.o.ExtensionFrame(raw)
}

func (g *guard) carrierOwnProtected(f map[string]any) bool {
	u, _ := f["url"].(string)
	unreachable, _ := f["unreachableUrl"].(string)
	if g.carrierLoaded(u) || g.carrierLoaded(unreachable) {
		return true
	}
	origin, _ := f["securityOrigin"].(string)
	return frameCarrierOriginEnabled() && g.carrierLoaded(origin)
}

func carrierOpaqueOrigin(origin string) bool {
	return !frameCarrierOriginEnabled() || origin == "" || origin == "://" || origin == "null"
}

func carrierInherits(f map[string]any) bool {
	u, _ := f["url"].(string)
	origin, _ := f["securityOrigin"].(string)
	return protect.Inheriting(u) && carrierOpaqueOrigin(origin)
}

func (g *guard) carrierErrorFrameLocked(f map[string]any, sc *carrierScope) bool {
	u, _ := f["url"].(string)
	if !chromeErrorDocument(u) {
		return false
	}
	if unreachable, _ := f["unreachableUrl"].(string); unreachable == "" {
		return true
	}
	id, _ := f["id"].(string)
	return id != "" && id == sc.target && g.errorPageSessionLocked(sc.session)
}

func (g *guard) carrierTreeEvidenceLocked(node map[string]any, depth int) bool {
	if depth >= maxFrameAncestors && !frameCarrierDepthFailClosed() {
		return false
	}
	if f, ok := node["frame"].(map[string]any); ok && g.carrierOwnProtected(f) {
		return true
	}
	kids, _ := node["childFrames"].([]any)
	for _, k := range kids {
		if child, ok := k.(map[string]any); ok && g.carrierTreeEvidenceLocked(child, depth+1) {
			return true
		}
	}
	return false
}

func (g *guard) carrierTreeLocked(node map[string]any, parent string, parentProtected, ancestor, root bool, sc *carrierScope, depth int) bool {
	if depth >= maxFrameAncestors && !frameCarrierDepthFailClosed() {
		return false
	}
	f, _ := node["frame"].(map[string]any)
	protected := parentProtected
	changed := false
	if f != nil {
		id, _ := f["id"].(string)
		declared, _ := f["parentId"].(string)
		unknown := !root && declared != "" && declared != parent
		if unknown {
			ancestor = sc.evidence || g.carrierPageEvidenceLocked(sc.target)
		}
		switch {
		case g.carrierOwnProtected(f), g.carrierErrorFrameLocked(f, sc):
			protected = true
		case carrierInherits(f):
			if unknown {
				protected = ancestor
			}
		default:
			protected = false
		}
		if protected && blankFrameCarriers(f) {
			changed = true
		}
		if !protected && ancestor && frameCarrierAncestorNameEnabled() && blankCarrierName(f) {
			changed = true
		}
		parent = id
	}
	kids, _ := node["childFrames"].([]any)
	for _, k := range kids {
		if child, ok := k.(map[string]any); ok && g.carrierTreeLocked(child, parent, protected, ancestor || protected, false, sc, depth+1) {
			changed = true
		}
	}
	return changed
}

func (g *guard) carrierEventFrameProtectedLocked(f map[string]any, sc *carrierScope) bool {
	if g.carrierOwnProtected(f) || g.carrierErrorFrameLocked(f, sc) {
		return true
	}
	if !carrierInherits(f) {
		return false
	}
	parent, _ := f["parentId"].(string)
	if parent == "" {
		return g.targetProtectedLocked(sc.target)
	}
	protected, known := g.carrierParentLocked(parent, sc.target)
	if known {
		return protected
	}
	return g.carrierPageEvidenceLocked(sc.target)
}

func (g *guard) carrierParentLocked(parent, target string) (protected, known bool) {
	if g.carrierAncestorProtectedLocked(parent, target) {
		return true, true
	}
	_, child := g.childFrames[parent]
	_, tracked := g.targets[parent]
	return false, child || tracked || parent == target || g.frameParentOfLocked(parent) != ""
}

func (g *guard) carrierAncestorProtectedLocked(frame, target string) bool {
	seen := map[string]bool{}
	id := frame
	for depth := 0; id != "" && depth < maxFrameAncestors && !seen[id]; depth++ {
		seen[id] = true
		if g.carrierLoaded(g.childFrames[id]) {
			return true
		}
		if t := g.targets[id]; t != nil && !t.gone && t.protected && !g.extensionFrameTargetLocked(t) {
			return true
		}
		id = g.frameParentOfLocked(id)
	}
	if id == "" || !frameCarrierDepthFailClosed() {
		return false
	}
	return g.carrierPageEvidenceLocked(target)
}

func (g *guard) carrierEventAncestorLocked(parent string, sc *carrierScope) bool {
	if !frameCarrierAncestorNameEnabled() {
		return false
	}
	protected, known := g.carrierParentLocked(parent, sc.target)
	if known {
		return protected
	}
	return g.carrierPageEvidenceLocked(sc.target)
}

func (g *guard) carrierPageEvidenceLocked(target string) bool {
	if target == "" {
		return false
	}
	if g.targetProtectedLocked(target) || g.framesProtectedLocked(target) {
		return true
	}
	memo := map[string]carrierChain{}
	for id, raw := range g.childFrames {
		if !g.carrierLoaded(raw) {
			continue
		}
		if v := g.carrierChainLocked(id, target, memo); v != carrierChainElsewhere {
			return true
		}
	}
	return false
}

type carrierChain int8

const (
	carrierChainElsewhere carrierChain = iota
	carrierChainHere
	carrierChainUnresolved
)

func (g *guard) carrierChainLocked(id, target string, memo map[string]carrierChain) carrierChain {
	var path []string
	seen := map[string]bool{}
	verdict := carrierChainElsewhere
	for at := id; ; {
		if v, ok := memo[at]; ok {
			verdict = v
			break
		}
		if at == target {
			verdict = carrierChainHere
			break
		}
		if seen[at] || len(path) >= maxFrameAncestors && !frameCarrierDepthFailClosed() {
			if frameCarrierDepthFailClosed() {
				verdict = carrierChainUnresolved
			}
			break
		}
		if len(path) >= maxFrameAncestors && !frameCarrierEvidenceScoped() {
			verdict = carrierChainUnresolved
			break
		}
		seen[at] = true
		path = append(path, at)
		next := g.frameParentOfLocked(at)
		if next == "" {
			break
		}
		at = next
	}
	for _, p := range path {
		memo[p] = verdict
	}
	return verdict
}

func blankCarrierName(f map[string]any) bool {
	if s, ok := f["name"].(string); ok && s != "" {
		f["name"] = ""
		return true
	}
	return false
}

func blankFrameCarriers(f map[string]any) bool {
	changed := false
	for k, v := range f {
		if frameCarrierKept[k] {
			continue
		}
		if s, ok := v.(string); ok && s != "" {
			f[k], changed = "", true
		}
	}
	return changed
}

func (g *guard) carrierHistoryEntryLocked(entry map[string]any, sc *carrierScope) bool {
	u, _ := entry["url"].(string)
	typed, _ := entry["userTypedURL"].(string)
	protected := g.o.Loaded(u) || g.o.Loaded(typed) || chromeErrorDocument(u) && g.errorPageSessionLocked(sc.session)
	if !protected {
		return false
	}
	return blankCarrierFields(entry, "")
}

func (g *guard) carrierTargetInfoLocked(info map[string]any) bool {
	if !frameCarrierTargetInfoEnabled() {
		return false
	}
	u, _ := info["url"].(string)
	id, _ := info["targetId"].(string)
	typ, _ := info["type"].(string)
	protected := g.o.Loaded(u)
	if typ == "iframe" || !frameCarrierTargetTypeSplit() {
		protected = g.carrierLoaded(u)
	}
	if !protected && chromeErrorDocument(u) && id != "" {
		if nt := g.navTargets[id]; nt != nil && nt.errorURL != "" && g.o.Loaded(nt.errorURL) {
			protected = true
		}
	}
	if !protected {
		return false
	}
	keep := ""
	if u != "" {
		keep = redactURL(u)
	}
	return blankCarrierFields(info, keep)
}

func blankCarrierFields(obj map[string]any, keep string) bool {
	changed := false
	for k, v := range obj {
		s, ok := v.(string)
		if !ok || s == "" || s == keep || !failedLoadTitleField(strings.ToLower(k)) {
			continue
		}
		obj[k], changed = "", true
	}
	return changed
}

func (g *guard) mainFrameLoaded(url, origin string) bool {
	return !chromeErrorDocument(url) && (g.o.Loaded(url) || g.o.Loaded(origin))
}

func (g *guard) treeMainProtectedLocked(t *target, raw json.RawMessage) bool {
	var result struct {
		FrameTree *frameTreeNode `json:"frameTree"`
	}
	if t == nil || json.Unmarshal(raw, &result) != nil || result.FrameTree == nil || result.FrameTree.Frame.ID != t.id {
		return false
	}
	return g.mainFrameLoaded(result.FrameTree.Frame.URL, result.FrameTree.Frame.SecurityOrigin)
}

func (g *guard) documentOpenedEvent(m *message, data []byte) browserVerdict {
	var p struct {
		Frame struct {
			ID             string `json:"id"`
			ParentID       string `json:"parentId"`
			URL            string `json:"url"`
			SecurityOrigin string `json:"securityOrigin"`
		} `json:"frame"`
	}
	_ = json.Unmarshal(m.Params, &p)
	t := g.sessionTargetLocked(m.SessionID)
	if t == nil || p.Frame.ID == "" || p.Frame.ID != t.id || (p.Frame.ParentID != "" && t.typ != "iframe") {
		return browserVerdict{forward: data}
	}
	if g.mainFrameLoaded(p.Frame.URL, p.Frame.SecurityOrigin) {
		return browserVerdict{send: g.markLocked(t)}
	}
	return browserVerdict{forward: data}
}
