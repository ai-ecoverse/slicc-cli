package cdp

import (
	"encoding/json"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

type contextMark struct {
	loader    string
	baseline  bool
	committed bool
}

func defaultContextOf(c execContext, t *target) bool {
	return t != nil && c.AuxData.IsDefault && c.AuxData.FrameID != "" && c.AuxData.FrameID == t.id
}

func contextOriginProtected(o *protect.Origins, origin string) bool {
	if protect.OpaqueContextOrigin(origin) {
		return false
	}
	return o.Loaded(origin) || protect.AboutLookalike(origin)
}

func defaultContextProtected(o *protect.Origins, c execContext, t *target) bool {
	return defaultContextOf(c, t) && contextOriginProtected(o, c.Origin)
}

func ordinaryContextOrigin(o *protect.Origins, origin string) bool {
	return origin != "" && !protect.OpaqueContextOrigin(origin) && origin != "null" && !contextOriginProtected(o, origin)
}

func (g *guard) markContextLocked(t *target) [][]byte {
	mark := &contextMark{}
	nt := g.navTargets[t.id]
	if nt != nil && nt.loaderID != "" {
		mark.loader, mark.baseline = nt.loaderID, true
	}
	t.contextMark = mark
	g.logf("cdp: a target's default context marks it protected target=%s baseline=%t", t.id, mark.baseline)
	send := g.markLocked(t)
	if !mark.baseline && nt != nil && nt.session != "" {
		send = append(send, g.callbackLocked(nt.session, "Page.getFrameTree", nil, func(m message) [][]byte {
			var res struct {
				FrameTree struct {
					Frame struct {
						ID       string `json:"id"`
						LoaderID string `json:"loaderId"`
					} `json:"frame"`
				} `json:"frameTree"`
			}
			if m.Error == nil && json.Unmarshal(m.Result, &res) == nil && t.contextMark == mark {
				g.contextMarkBaselineLocked(t, res.FrameTree.Frame.ID, res.FrameTree.Frame.LoaderID)
			}
			return nil
		}))
	}
	return send
}

func (g *guard) contextMarkBaselineLocked(t *target, frame, loader string) {
	if t == nil || t.contextMark == nil || t.contextMark.baseline || frame != t.id || loader == "" {
		return
	}
	t.contextMark.loader, t.contextMark.baseline = loader, true
}

func forgetContextMark(t *target) {
	t.contextMark = nil
}

func forgetDefaultOrigin(nt *navTarget) {
	nt.defaultOrigin, nt.defaultContext, nt.defaultUnique, nt.defaultLoader = "", 0, "", ""
}

func defaultOriginNavLocked(nt *navTarget, t *target, m *message) (string, bool) {
	switch m.Method {
	case "Runtime.executionContextCreated":
		var p struct {
			Context execContext `json:"context"`
		}
		if json.Unmarshal(m.Params, &p) != nil || !defaultContextOf(p.Context, t) {
			return "", false
		}
		nt.defaultOrigin, nt.defaultContext, nt.defaultUnique, nt.defaultLoader = p.Context.Origin, p.Context.ID, p.Context.UniqueID, nt.loaderID
		return p.Context.Origin, true
	case "Runtime.executionContextDestroyed":
		var p struct {
			ID       int64  `json:"executionContextId"`
			UniqueID string `json:"executionContextUniqueId"`
		}
		if json.Unmarshal(m.Params, &p) == nil && (p.UniqueID != "" && p.UniqueID == nt.defaultUnique || p.ID != 0 && p.ID == nt.defaultContext) {
			forgetDefaultOrigin(nt)
		}
	case "Runtime.executionContextsCleared":
		forgetDefaultOrigin(nt)
	case "Page.frameNavigated":
		var p navCommitParams
		if json.Unmarshal(m.Params, &p) == nil && p.Frame.ID == t.id && (nt.defaultLoader == "" || nt.defaultLoader != p.Frame.LoaderID) {
			forgetDefaultOrigin(nt)
		}
	}
	return "", false
}

func (g *guard) contextMarkNavLocked(nt *navTarget, m *message) [][]byte {
	t := g.targets[nt.target]
	if t == nil || nt.session == "" || m.SessionID != nt.session {
		return nil
	}
	origin, created := defaultOriginNavLocked(nt, t, m)
	if t.contextMark == nil {
		if created && contextOriginProtected(g.o, origin) && !g.targetLoadedLocked(t) {
			return g.markContextLocked(t)
		}
		return nil
	}
	switch m.Method {
	case "Page.frameNavigated":
		var p navCommitParams
		if json.Unmarshal(m.Params, &p) != nil || p.Frame.ID != t.id {
			return nil
		}
		if p.Frame.LoaderID == "" {
			t.contextMark.committed = false
			return nil
		}
		if !t.contextMark.baseline {
			g.contextMarkBaselineLocked(t, p.Frame.ID, p.Frame.LoaderID)
			return nil
		}
		if p.Frame.LoaderID != t.contextMark.loader {
			t.contextMark.loader = p.Frame.LoaderID
			t.contextMark = nil
			t.protected = g.decide(t, t.protected)
			return g.redecideWorkersLocked(t.id)
			g.logf("cdp: a new document committed on a target held by its default-context mark target=%s", t.id)
		}
	case "Runtime.executionContextCreated":
		var p struct {
			Context execContext `json:"context"`
		}
		if json.Unmarshal(m.Params, &p) != nil || !defaultContextOf(p.Context, t) {
			return nil
		}
		if contextOriginProtected(g.o, p.Context.Origin) {
			t.contextMark.committed = false
			return nil
		}
		if !t.contextMark.committed || !ordinaryContextOrigin(g.o, p.Context.Origin) {
			return nil
		}
		t.contextMark = nil
		t.protected = g.decide(t, t.protected)
		g.logf("cdp: a new document cleared a target's default-context mark target=%s protected=%t", t.id, t.protected)
		return g.redecideWorkersLocked(t.id)
	}
	return nil
}
