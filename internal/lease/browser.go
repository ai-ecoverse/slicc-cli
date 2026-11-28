package lease

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	guarded "github.com/ai-ecoverse/slicc-cli/internal/caps/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

type browserSession struct {
	c       *cdp.Client
	mu      sync.Mutex
	tabs    map[string]*tab
	cleanup sync.WaitGroup
	stopped bool
}
type tab struct {
	gate                   sync.Mutex
	target, session, world string
	unique                 string
	context                int
	generation             uint64
	changed                chan struct{}
	lost, cut, closing     bool
}

func openBrowser(ctx context.Context, endpoint string, o *protect.Origins) (*browserSession, error) {
	bs := &browserSession{tabs: map[string]*tab{}}
	c, e := guarded.OpenLocal(ctx, endpoint, o.Extra(), bs.observe)
	if e != nil {
		return nil, unsupported
	}
	bs.c = c
	return bs, nil
}
func (bs *browserSession) observe(e cdp.Event) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if e.Method == "Target.detachedFromTarget" && e.SessionID == "" {
		var p struct{ SessionID string }
		if json.Unmarshal(e.Params, &p) == nil {
			if t := bs.tabs[p.SessionID]; t != nil && !t.lost && !t.closing {
				bs.closeTabLocked(t, true)
			}
		}
		return
	}
	t := bs.tabs[e.SessionID]
	if t == nil {
		return
	}
	switch e.Method {
	case "Runtime.executionContextCreated":
		var p struct{ Context cdp.ExecutionContext }
		if json.Unmarshal(e.Params, &p) == nil && p.Context.Name == t.world && p.Context.AuxData.FrameID == t.target && p.Context.AuxData.Type == "isolated" && !p.Context.AuxData.IsDefault {
			t.unique = p.Context.UniqueID
			t.context = p.Context.ID
		}
		return
	case "Runtime.executionContextDestroyed":
		var p struct {
			ID int `json:"executionContextId"`
		}
		if json.Unmarshal(e.Params, &p) != nil || p.ID != t.context {
			return
		}
		t.unique = ""
	case "Runtime.executionContextsCleared":
		t.unique = ""
	case "Page.frameNavigated":
		var p struct{ Frame struct{ ID string } }
		if json.Unmarshal(e.Params, &p) != nil || p.Frame.ID != t.target {
			return
		}
	case "Page.navigatedWithinDocument", "Page.frameStartedNavigating", "Page.frameDetached":
		var p struct {
			ID string `json:"frameId"`
		}
		if json.Unmarshal(e.Params, &p) != nil || p.ID != t.target {
			return
		}
	default:
		return
	}
	t.generation++
	close(t.changed)
	t.changed = make(chan struct{})
}

type frame struct{ ID, ParentID, URL, URLFragment, SecurityOrigin, LoaderID, UnreachableURL string }

func (bs *browserSession) frame(ctx context.Context, t *tab) (frame, error) {
	bs.mu.Lock()
	target := t.target
	bs.mu.Unlock()
	var p struct{ FrameTree struct{ Frame frame } }
	e := bs.c.Call(ctx, t.session, "Page.getFrameTree", nil, &p)
	f := p.FrameTree.Frame
	if e != nil || target == "" || f.ID != target || f.ParentID != "" {
		return frame{}, denied
	}
	if f.UnreachableURL != "" {
		return f, denied
	}
	if f.LoaderID == "" {
		return frame{}, denied
	}
	f.URL += f.URLFragment
	return f, nil
}
func (bs *browserSession) create(ctx context.Context, t *tab, dest string) (err error) {
	if t.session != "" {
		return nil
	}
	defer func() {
		if err != nil {
			bs.closeTab(t, false)
		}
	}()
	var p struct{ TargetID string }
	if bs.c.Call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank", "background": false}, &p) != nil || p.TargetID == "" {
		return unsupported
	}
	t.target = p.TargetID
	s, e := bs.c.Attach(ctx, p.TargetID)
	if e != nil || s.ID == "" {
		return unsupported
	}
	t.session = s.ID
	t.world = "slicc-" + id()
	t.changed = make(chan struct{})
	bs.mu.Lock()
	bs.tabs[s.ID] = t
	bs.mu.Unlock()
	for _, m := range []string{"Page.enable", "Runtime.enable"} {
		if s.Call(ctx, m, nil, nil) != nil {
			return unsupported
		}
	}
	if s.Call(ctx, "Page.navigate", map[string]any{"url": dest}, nil) != nil {
		return unsupported
	}
	return nil
}
func (bs *browserSession) discard(t *tab) {
	t.gate.Lock()
	defer t.gate.Unlock()
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if t.target != "" {
		bs.closeTabLocked(t, false)
	}
}

func (bs *browserSession) closeTab(t *tab, cut bool) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.closeTabLocked(t, cut)
}

func (bs *browserSession) stopCleanup() {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.stopped = true
}

func (bs *browserSession) closeTabLocked(t *tab, cut bool) {
	if !t.lost {
		t.generation++
	}
	t.lost, t.cut = true, cut
	target := t.target
	if target == "" {
		return
	}
	t.target = ""
	delete(bs.tabs, t.session)
	if bs.stopped {
		return
	}
	t.closing = true
	bs.cleanup.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = bs.c.Call(ctx, "", "Target.closeTarget", map[string]any{"targetId": target}, nil)
	})
}

type stamp struct {
	f   frame
	gen uint64
}

func (bs *browserSession) stamp(ctx context.Context, t *tab) (stamp, error) {
	bs.mu.Lock()
	g := t.generation
	bs.mu.Unlock()
	f, e := bs.frame(ctx, t)
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if g != t.generation {
		return stamp{}, denied
	}
	return stamp{f, g}, e
}
func (st stamp) allowed(c claims, o *protect.Origins) bool {
	u, ok := safeURL(st.f.URL, o)
	return ok && st.f.SecurityOrigin == "https://"+u.Host() && matches(c.Allow, st.f.URL, o)
}

func (st stamp) refusedNavigation(previous stamp, allowed bool, readErr error) (bad, prompt bool) {
	if st.f.UnreachableURL != "" {
		return true, false
	}
	if readErr != nil || allowed || st.f.URL == "about:blank" {
		return false, false
	}
	prompt = st.f.URL != previous.f.URL
	return prompt || st.f.LoaderID != previous.f.LoaderID, prompt
}
func (bs *browserSession) world(ctx context.Context, t *tab) (string, error) {
	bs.mu.Lock()
	target := t.target
	bs.mu.Unlock()
	var w struct{ ExecutionContextID int }
	if bs.c.Call(ctx, t.session, "Page.createIsolatedWorld", map[string]any{"frameId": target, "worldName": t.world}, &w) != nil {
		return "", unsupported
	}
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if w.ExecutionContextID <= 0 || w.ExecutionContextID != t.context || t.unique == "" {
		return "", denied
	}
	return t.unique, nil
}

const snapshotBody = `let out=[],n=0,size=0;const walk=document.createTreeWalker(document.body,NodeFilter.SHOW_ELEMENT|NodeFilter.SHOW_TEXT,{acceptNode(x){if(x.nodeType===1 && (x.matches('script,style,noscript,template,object,embed,textarea,input,select') || !x.checkVisibility({visibilityProperty:true,opacityProperty:true})))return NodeFilter.FILTER_REJECT;return NodeFilter.FILTER_ACCEPT}});while(n++<2048 && size<8000 && walk.nextNode()){const x=walk.currentNode;if(x.nodeType===1){if(x.localName==='iframe')out.push('[frame]');continue}const v=x.substringData(0,512).trim();if(v){out.push(v);size+=v.length}}return {ok:true,text:out.join('\n').slice(0,8192)}`

func literal(v any) string { b, _ := json.Marshal(v); return string(b) }
func (bs *browserSession) action(ctx context.Context, t *tab, c claims, o *protect.Origins, op, url string, live func() bool) (map[string]any, stamp, error) {
	if !live() {
		return nil, stamp{}, retired
	}
	bs.mu.Lock()
	lost, cut := t.lost, t.cut
	bs.mu.Unlock()
	if cut {
		return nil, stamp{}, denied
	}
	if lost {
		return nil, stamp{}, retired
	}
	if op == "goto" {
		var ok bool
		if url, ok = canonical(c.Allow, url, o); !ok {
			return nil, stamp{}, denied
		}
	}
	if t.session == "" {
		if op != "goto" || !matches(c.Allow, url, o) {
			return nil, stamp{}, denied
		}
		if e := bs.create(ctx, t, url); e != nil {
			return nil, stamp{}, e
		}
		return bs.navigation(ctx, t, c, o, stamp{})
	}
	st, e := bs.stamp(ctx, t)
	if e != nil {
		return nil, stamp{}, e
	}
	if !st.allowed(c, o) {
		return nil, stamp{}, denied
	}
	w, e := bs.world(ctx, t)
	if e != nil {
		return nil, stamp{}, e
	}
	body := snapshotBody
	if op == "goto" {
		body = "location.assign(" + literal(url) + ");return {ok:true}"
	}
	expr := "(()=>{if(self!==top||self.origin!==" + literal(st.f.SecurityOrigin) + "||location.href!==" + literal(st.f.URL) + "||Date.now()>=" + literal(c.Exp*1000) + ")return null;" + body + "})()"
	bs.mu.Lock()
	same := t.generation == st.gen && t.unique == w
	bs.mu.Unlock()
	if !same || !live() {
		return nil, stamp{}, denied
	}
	var r struct {
		Result struct {
			Value struct {
				OK   bool
				Text string
			}
		}
	}
	if bs.c.Call(ctx, t.session, "Runtime.evaluate", map[string]any{"expression": expr, "uniqueContextId": w, "returnByValue": true, "timeout": 2000}, &r) != nil || !r.Result.Value.OK {
		return nil, stamp{}, denied
	}
	if op == "snapshot" {
		now, e := bs.stamp(ctx, t)
		if e != nil || st != now || !live() {
			return nil, stamp{}, denied
		}
		return map[string]any{"text": r.Result.Value.Text}, st, nil
	}
	return bs.navigation(ctx, t, c, o, st)
}
func (bs *browserSession) navigation(ctx context.Context, t *tab, c claims, o *protect.Origins, st stamp) (map[string]any, stamp, error) {
	knownBad := false
	for {
		bs.mu.Lock()
		changed := t.changed
		bs.mu.Unlock()
		now, e := bs.stamp(ctx, t)
		allowed := e == nil && now.allowed(c, o)
		if allowed && (now.f.LoaderID != st.f.LoaderID || now.f.URL != st.f.URL) {
			return map[string]any{}, now, nil
		}
		if allowed {
			knownBad = false
		}
		bad, prompt := now.refusedNavigation(st, allowed, e)
		knownBad = knownBad || bad
		if prompt {
			bs.closeTab(t, true)
			return nil, stamp{}, denied
		}
		select {
		case <-ctx.Done():
			if knownBad {
				bs.closeTab(t, true)
			}
			return nil, stamp{}, denied
		case <-changed:
		}
	}
}
