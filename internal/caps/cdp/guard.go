package cdp

import (
	"bytes"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	refusedTarget  = "target is on seven's origin"
	refusedCookies = "cookies on seven's origin"
	refusedLost    = "slicc lost track of the browser's targets"
	refusedPaused  = "slicc didn't pass this paused request to the page, so it could be seven's"
	refusedParse   = "slicc can't parse this URL, so it could be a protected page"
	LostGrace      = 2 * time.Second
	maxTombstones  = 4096
	maxRefused     = 4096
	maxMarked      = 65536
	relayIDBase    = 1 << 30
)

type callKey struct {
	session string
	id      int64
}

type callKind int

const (
	kindPlain callKind = iota
	kindTargets
	kindTargetInfo
	kindCookies
	kindBrowserSession
	kindContext
	kindCreate
	kindAttach
	kindStreams
)

type relayCall struct {
	ch chan message
	fn func(m message) [][]byte
}

type target struct {
	id          string
	typ         string
	url         string
	opener      string
	openerFrame string
	ctx         string
	protected   bool
	gone        bool
}

type session struct {
	id      string
	target  string
	parent  string
	typ     string
	refused bool
}

type guard struct {
	mu   sync.Mutex
	o    *protect.Origins
	logf func(format string, args ...any)

	relaySession      string
	lost              bool
	targets           map[string]*target
	tombstones        []string
	sessions          map[string]*session
	hidden            map[string]bool
	refused           map[string]bool
	refusedOrder      []string
	browserSessions   map[string]bool
	ownContexts       map[string]bool
	pending           map[callKey]callKind
	relayCalls        map[callKey]*relayCall
	nextID            int64
	marked            map[string]map[string]bool
	markedCount       int
	saturated         map[string]bool
	swProtected       map[string]bool
	navTargets        map[string]*navTarget
	navSessions       map[string]*navTarget
	unknown           map[string]*navTarget
	gone              map[string]bool
	goneOrder         []string
	held              map[string]*heldSession
	explicit          map[string]int
	attachCalls       map[callKey]string
	patterns          []any
	heldExtras        map[requestKey][]heldExtra
	heldExtraCount    int
	seen              map[requestKey]reqLevel
	seenOrder         []requestKey
	handed            map[requestKey]int
	handedOrder       []handedPause
	pauses            int
	continues         map[callKey]handedPause
	bodyCalls         map[callKey]bodyCall
	heldAnswers       map[callKey]*heldAnswer
	streams           map[string]bool
	streamOrder       []string
	bounds            guardBounds
	worlds            map[string]*worldSet
	worldUniques      map[string]string
	shown             map[string]*shownSet
	shownUniques      map[string]string
	worldRequests     map[requestKey]bool
	worldRequestCount map[string]int
	worldRequestsFull map[string]bool
	navSeen           map[requestKey]bool
	navSeenOrder      []requestKey
	heldPauses        map[requestKey][]heldPause
	heldPauseCount    int
	maxWorlds         int
	answered          time.Time
	probing           bool
}

func newGuard(o *protect.Origins, logf func(format string, args ...any)) *guard {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	g := &guard{
		o:                 o,
		logf:              logf,
		targets:           map[string]*target{},
		sessions:          map[string]*session{},
		hidden:            map[string]bool{},
		refused:           map[string]bool{},
		browserSessions:   map[string]bool{},
		ownContexts:       map[string]bool{},
		pending:           map[callKey]callKind{},
		relayCalls:        map[callKey]*relayCall{},
		nextID:            relayIDBase,
		marked:            map[string]map[string]bool{},
		saturated:         map[string]bool{},
		swProtected:       map[string]bool{},
		navTargets:        map[string]*navTarget{},
		navSessions:       map[string]*navTarget{},
		unknown:           map[string]*navTarget{},
		gone:              map[string]bool{},
		held:              map[string]*heldSession{},
		explicit:          map[string]int{},
		attachCalls:       map[callKey]string{},
		heldExtras:        map[requestKey][]heldExtra{},
		seen:              map[requestKey]reqLevel{},
		handed:            map[requestKey]int{},
		continues:         map[callKey]handedPause{},
		bodyCalls:         map[callKey]bodyCall{},
		heldAnswers:       map[callKey]*heldAnswer{},
		streams:           map[string]bool{},
		worlds:            map[string]*worldSet{},
		worldUniques:      map[string]string{},
		shown:             map[string]*shownSet{},
		shownUniques:      map[string]string{},
		worldRequests:     map[requestKey]bool{},
		worldRequestCount: map[string]int{},
		worldRequestsFull: map[string]bool{},
		navSeen:           map[requestKey]bool{},
		heldPauses:        map[requestKey][]heldPause{},
		maxWorlds:         maxWorlds,
		bounds:            boundsFromEnv(),
	}
	g.patterns = g.documentPatterns()
	return g
}

type message struct {
	ID        *int64          `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     json.RawMessage `json:"error,omitempty"`
}

type targetInfo struct {
	TargetID         string `json:"targetId"`
	Type             string `json:"type"`
	URL              string `json:"url"`
	OpenerID         string `json:"openerId"`
	OpenerFrameID    string `json:"openerFrameId"`
	BrowserContextID string `json:"browserContextId"`
}

func encode(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimRight(b.Bytes(), "\n")
}

func refusal(id int64, sessionID, reason string) []byte {
	m := map[string]any{"id": id, "error": map[string]any{"code": -32000, "message": "slicc: refused: " + reason}}
	if sessionID != "" {
		m["sessionId"] = sessionID
	}
	return encode(m)
}

func (g *guard) newCallLocked(sessionID, method string, params any, wait bool) ([]byte, *relayCall) {
	for {
		g.nextID++
		k := callKey{sessionID, g.nextID}
		if _, busy := g.pending[k]; busy {
			continue
		}
		c := &relayCall{}
		if wait {
			c.ch = make(chan message, 1)
		}
		g.relayCalls[k] = c
		m := map[string]any{"id": g.nextID, "method": method}
		if params != nil {
			m["params"] = params
		}
		if sessionID != "" {
			m["sessionId"] = sessionID
		}
		return encode(m), c
	}
}

func (g *guard) call(sessionID, method string, params any) ([]byte, *relayCall) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.newCallLocked(sessionID, method, params, true)
}

func (g *guard) cancel(c *relayCall) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for k, v := range g.relayCalls {
		if v == c {
			delete(g.relayCalls, k)
		}
	}
}

func (g *guard) setRelaySession(id string) {
	g.mu.Lock()
	g.relaySession = id
	g.mu.Unlock()
}

func (g *guard) openerState(t *target) protect.Opener {
	if t.openerFrame != "" {
		if f, ok := g.targets[t.openerFrame]; ok && f.protected {
			return protect.OpenerProtected
		}
	}
	if t.opener == "" {
		return protect.NoOpener
	}
	op, ok := g.targets[t.opener]
	switch {
	case !ok:
		return protect.OpenerUnknown
	case op.protected:
		return protect.OpenerProtected
	default:
		return protect.OpenerUnprotected
	}
}

func (g *guard) decide(t *target, previous bool) bool {
	if t.typ == "browser" {
		return false
	}
	return g.o.TargetOf(t.url, t.typ, previous, g.openerState(t))
}

func (g *guard) record(info targetInfo) *target {
	t, ok := g.targets[info.TargetID]
	if !ok {
		t = &target{id: info.TargetID}
		g.targets[info.TargetID] = t
	}
	t.typ, t.url, t.opener, t.openerFrame, t.ctx = info.Type, info.URL, info.OpenerID, info.OpenerFrameID, info.BrowserContextID
	return t
}

func (g *guard) observeLocked(info targetInfo) [][]byte {
	if info.TargetID == "" {
		return nil
	}
	t := g.record(info)
	t.gone = false
	prev := t.protected
	t.protected = g.decide(t, prev)
	if t.protected && !prev {
		return g.protectLocked(t)
	}
	return nil
}

func (g *guard) observeAll(infos []targetInfo) {
	g.mu.Lock()
	defer g.mu.Unlock()
	fresh := map[string]bool{}
	for _, info := range infos {
		if info.TargetID == "" {
			continue
		}
		if _, ok := g.targets[info.TargetID]; !ok {
			fresh[info.TargetID] = true
		}
		g.record(info)
	}
	done := map[string]bool{}
	var resolve func(t *target)
	resolve = func(t *target) {
		if done[t.id] {
			return
		}
		done[t.id] = true
		if op, ok := g.targets[t.opener]; ok && fresh[op.id] {
			resolve(op)
		}
		t.protected = g.decide(t, t.protected)
	}
	for id := range fresh {
		resolve(g.targets[id])
	}
}

func (g *guard) markLocked(t *target) [][]byte {
	if t.protected {
		return nil
	}
	t.protected = true
	return g.protectLocked(t)
}

func (g *guard) protectLocked(t *target) [][]byte {
	g.logf("cdp: a target moved onto a protected origin; detaching its sessions")
	var out [][]byte
	for _, s := range g.sessions {
		if s.target != t.id || s.refused {
			continue
		}
		s.refused = true
		g.addRefusedLocked(s.id)
		msg, _ := g.newCallLocked(s.parent, "Target.detachFromTarget", map[string]any{"sessionId": s.id}, false)
		out = append(out, msg)
	}
	return out
}

func (g *guard) addRefusedLocked(id string) {
	if g.refused[id] {
		return
	}
	g.refused[id] = true
	g.refusedOrder = append(g.refusedOrder, id)
	if len(g.refusedOrder) > maxRefused {
		delete(g.refused, g.refusedOrder[0])
		g.refusedOrder = g.refusedOrder[1:]
	}
}

func (g *guard) destroyedLocked(id string) {
	g.navForgetLocked(id)
	g.dropHeldLocked(id)
	g.forgetWorldsLocked(id)
	g.forgetWorldRequestsLocked(id)
	t, ok := g.targets[id]
	if !ok || t.gone {
		return
	}
	t.gone = true
	g.tombstones = append(g.tombstones, id)
	for len(g.tombstones) > maxTombstones {
		old := g.tombstones[0]
		g.tombstones = g.tombstones[1:]
		if o, ok := g.targets[old]; ok && o.gone {
			delete(g.targets, old)
		}
	}
}

func (g *guard) forgetSessionLocked(id string) {
	g.goneLocked(id)
	g.forgetWorldSessionLocked(id)
	delete(g.sessions, id)
	delete(g.browserSessions, id)
	g.markedCount -= len(g.marked[id])
	delete(g.marked, id)
	delete(g.saturated, id)
	g.forgetExtrasLocked(id)
	for k := range g.pending {
		if k.session == id {
			delete(g.pending, k)
			delete(g.bodyCalls, k)
			g.attachDoneLocked(k)
		}
	}
}

func (g *guard) attachDoneLocked(k callKey) {
	t, ok := g.attachCalls[k]
	if !ok {
		return
	}
	delete(g.attachCalls, k)
	if g.explicit[t]--; g.explicit[t] <= 0 {
		delete(g.explicit, t)
	}
}

func (g *guard) markRequestLocked(sessionID, requestID string) {
	if requestID == "" {
		return
	}
	m := g.marked[sessionID]
	if m == nil {
		m = map[string]bool{}
		g.marked[sessionID] = m
	}
	if m[requestID] {
		return
	}
	if g.markedCount >= maxMarked {
		g.saturated[sessionID] = true
		return
	}
	m[requestID] = true
	g.markedCount++
}

func (g *guard) requestMarkedLocked(sessionID, requestID string) bool {
	return g.saturated[sessionID] || g.marked[sessionID][requestID] || g.worldRequestKnownLocked(g.worldTargetLocked(sessionID), requestID)
}

func (g *guard) targetProtectedLocked(id string) bool {
	t, ok := g.targets[id]
	return ok && t.protected
}

func (g *guard) sessionTargetLocked(id string) *target {
	if s, ok := g.sessions[id]; ok {
		return g.targets[s.target]
	}
	return nil
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
