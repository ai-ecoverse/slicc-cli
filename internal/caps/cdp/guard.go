package cdp

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

const (
	refusedTarget       = "target is on seven's origin"
	refusedCookies      = "cookies on seven's origin"
	refusedLost         = "slicc lost track of the browser's targets"
	refusedPaused       = "slicc didn't pass this paused request to the page, so it could be seven's"
	refusedRedirect     = "that URL redirects to seven's origin"
	refusedReplayXHR    = "Network.replayXHR follows redirects inside the browser, so it could send a request to seven's pages or the vault"
	refusedLoadResource = "Network.loadNetworkResource follows redirects inside the browser, so it could load seven's pages"
	LostGrace           = 2 * time.Second
	maxTombstones       = 4096
	maxOpenedSeen       = 4096
	OpenedSeenBoundEnv  = "SLICC_CDP_OPENED_SEEN"
	maxRefused          = 4096
	maxMarked           = 65536
	relayIDBase         = 1879048192
	maxRetiredCalls     = 4096
	retiredCallAge      = time.Minute
)

var refusedParse = strings.TrimPrefix(protect.UnparseableRefusal(), "slicc: refused: ")

type callKey struct {
	session string
	id      int64
}

func admittedScriptKey(session, identifier string) string {
	return session + "\x00" + identifier
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
	kindPaint
	kindScript
)

type relayCall struct {
	ch chan message
	fn func(m message) [][]byte
}

type target struct {
	id               string
	typ              string
	url              string
	opener           string
	parentFrame      string
	openerFrame      string
	ctx              string
	protected        bool
	shown            bool
	gone             bool
	document         string
	documentRevision uint64
	framePage        *target
	frameDocument    string
	frameOwner       *frameOwnership
}

type session struct {
	id      string
	target  string
	parent  string
	typ     string
	refused bool
	waiting bool
}

type guard struct {
	reserveHold     func([]byte) (*link.Reservation, bool)
	reserveBytes    func(int) (*link.Reservation, bool)
	pendingRelease  []*link.Reservation
	mu              sync.Mutex
	o               *protect.Origins
	logf            func(format string, args ...any)
	scheduleInitial func(*navTarget)

	relaySession          string
	documentsOnly         bool
	verifiesInput         bool
	lost                  bool
	hostTarget            func() string
	targets               map[string]*target
	tombstones            []string
	opened                map[string]bool
	openedOrder           []string
	listed                map[string]bool
	touchSeq              int
	touchedAt             map[string]int
	pendingTouch          map[callKey]touchReservation
	touchedOrder          []string
	openedSeenAt          map[string]int
	openedSeenOrder       []string
	openedIneligible      map[string]bool
	openedWait            map[string]string
	sessions              map[string]*session
	hidden                map[string]bool
	hiddenOwner           map[string]string
	attachParent          string
	refused               map[string]bool
	refusedOrder          []string
	browserSessions       map[string]bool
	ownContexts           map[string]bool
	pending               map[callKey]callKind
	relayCalls            map[callKey]*relayCall
	retiredCalls          map[callKey]time.Time
	nextID                int64
	marked                map[string]map[string]bool
	markedCount           int
	saturated             map[string]bool
	swProtected           map[string]bool
	navTargets            map[string]*navTarget
	navSessions           map[string]*navTarget
	unknown               map[string]*navTarget
	keptURLLatch          map[string]bool
	gone                  map[string]bool
	goneOrder             []string
	held                  map[string]*heldSession
	childFrames           map[string]string
	frameParents          map[string]string
	frameParentsFull      bool
	ancestryDisputed      map[string]bool
	blockedChildren       map[string]string
	explicit              map[string]int
	attachCalls           map[callKey]string
	patterns              []any
	heldExtras            map[requestKey][]heldExtra
	heldExtraCount        int
	seen                  map[requestKey]reqLevel
	seenOrder             []requestKey
	handed                map[requestKey]int
	handedOrder           []handedPause
	pauses                int
	pauseLinks            map[requestKey]requestKey
	clientRequests        map[requestKey]*clientRequestState
	clientCalls           map[callKey]clientMark
	continues             map[callKey]handedPause
	bodyCalls             map[callKey]bodyCall
	heldAnswers           map[callKey]*heldAnswer
	streams               map[string]bool
	streamOrder           []string
	bounds                guardBounds
	reads                 readClock
	frameProbeAttempts    map[string]int
	frameProbed           map[string]bool
	frames                map[string]map[string]bool
	worlds                map[string]*worldSet
	worldUniques          map[string]string
	shown                 map[string]*shownSet
	shownUniques          map[string]string
	worldRequests         map[requestKey]bool
	worldRequestCount     map[string]int
	worldRequestsFull     map[string]bool
	navSeen               map[requestKey]bool
	navSeenOrder          []requestKey
	navChains             map[requestKey][]string
	knownRedirects        map[string]*assetSet
	tainted               map[string]*assetSet
	clean                 map[string]*assetSet
	assets                map[string]*assetSet
	heldPauses            map[requestKey][]heldPause
	heldPauseCount        int
	commitMarks           map[callKey]commitMark
	maxWorlds             int
	casting               map[string]bool
	castCut               map[string]bool
	debuggers             map[string]*debuggerState
	debuggerReplies       map[callKey]debuggerReply
	debuggerBytes         int
	debuggerLimit         int
	heldExtensionPaints   map[callKey]*heldExtensionPaint
	pendingPage           [][]byte
	paintMethods          map[callKey]string
	extensionShownCleared bool
	extensionProbeWorlds  map[string]bool
	extensionProbeCache   map[extensionWorldKey]*extensionProbeWorld
	extensionFrameURLs    map[string]map[string]string
	extensionCaptureDirty map[callKey]bool
	extensionOwnerCalls   map[callKey]string
	extensionOwnerReplies map[callKey]int
	extensionPaintGen     uint64
	versionCalls          map[callKey]bool
	versionReplies        map[callKey]int
	scripts               map[string]bool
}

func newGuard(o *protect.Origins, logf func(format string, args ...any)) *guard {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	g := &guard{
		o:                     o,
		logf:                  logf,
		knownRedirects:        map[string]*assetSet{},
		targets:               map[string]*target{},
		opened:                map[string]bool{},
		listed:                map[string]bool{},
		touchedAt:             map[string]int{},
		pendingTouch:          map[callKey]touchReservation{},
		openedSeenAt:          map[string]int{},
		openedIneligible:      map[string]bool{},
		openedWait:            map[string]string{},
		sessions:              map[string]*session{},
		hidden:                map[string]bool{},
		hiddenOwner:           map[string]string{},
		refused:               map[string]bool{},
		browserSessions:       map[string]bool{},
		ownContexts:           map[string]bool{},
		pending:               map[callKey]callKind{},
		relayCalls:            map[callKey]*relayCall{},
		retiredCalls:          map[callKey]time.Time{},
		nextID:                relayIDBase,
		marked:                map[string]map[string]bool{},
		saturated:             map[string]bool{},
		swProtected:           map[string]bool{},
		navTargets:            map[string]*navTarget{},
		navSessions:           map[string]*navTarget{},
		unknown:               map[string]*navTarget{},
		keptURLLatch:          map[string]bool{},
		gone:                  map[string]bool{},
		held:                  map[string]*heldSession{},
		childFrames:           map[string]string{},
		frameParents:          map[string]string{},
		ancestryDisputed:      map[string]bool{},
		blockedChildren:       map[string]string{},
		explicit:              map[string]int{},
		attachCalls:           map[callKey]string{},
		heldExtras:            map[requestKey][]heldExtra{},
		seen:                  map[requestKey]reqLevel{},
		handed:                map[requestKey]int{},
		pauseLinks:            map[requestKey]requestKey{},
		clientRequests:        map[requestKey]*clientRequestState{},
		clientCalls:           map[callKey]clientMark{},
		continues:             map[callKey]handedPause{},
		bodyCalls:             map[callKey]bodyCall{},
		heldAnswers:           map[callKey]*heldAnswer{},
		streams:               map[string]bool{},
		frameProbeAttempts:    map[string]int{},
		frameProbed:           map[string]bool{},
		frames:                map[string]map[string]bool{},
		worlds:                map[string]*worldSet{},
		worldUniques:          map[string]string{},
		shown:                 map[string]*shownSet{},
		shownUniques:          map[string]string{},
		worldRequests:         map[requestKey]bool{},
		worldRequestCount:     map[string]int{},
		worldRequestsFull:     map[string]bool{},
		navSeen:               map[requestKey]bool{},
		navChains:             map[requestKey][]string{},
		tainted:               map[string]*assetSet{},
		clean:                 map[string]*assetSet{},
		assets:                map[string]*assetSet{},
		heldPauses:            map[requestKey][]heldPause{},
		commitMarks:           map[callKey]commitMark{},
		casting:               map[string]bool{},
		castCut:               map[string]bool{},
		debuggers:             map[string]*debuggerState{},
		debuggerReplies:       map[callKey]debuggerReply{},
		debuggerLimit:         debuggerBudgetFromEnv(),
		paintMethods:          map[callKey]string{},
		heldExtensionPaints:   map[callKey]*heldExtensionPaint{},
		extensionProbeCache:   map[extensionWorldKey]*extensionProbeWorld{},
		extensionCaptureDirty: map[callKey]bool{},
		scripts:               map[string]bool{},
		maxWorlds:             maxWorlds,
		bounds:                boundsFromEnv(),
	}
	g.patterns = g.protectedRequestPatterns()
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
	ParentFrameID    string `json:"parentFrameId"`
	ParentID         string `json:"parentId"`
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

func missingTarget(id int64, sessionID string) []byte {
	m := map[string]any{"id": id, "error": map[string]any{"code": -32602, "message": "No target with given id found"}}
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
			g.retireCallLocked(k, time.Now())
		}
	}
}

func (g *guard) expireRetiredLocked(now time.Time) {
	for k, at := range g.retiredCalls {
		if now.Sub(at) >= retiredCallAge {
			delete(g.retiredCalls, k)
		}
	}
}

func (g *guard) retireCallLocked(k callKey, now time.Time) {
	g.expireRetiredLocked(now)
	if len(g.retiredCalls) >= maxRetiredCalls {
		var oldest callKey
		var at time.Time
		for key, retired := range g.retiredCalls {
			if at.IsZero() || retired.Before(at) {
				oldest, at = key, retired
			}
		}
		delete(g.retiredCalls, oldest)
	}
	g.retiredCalls[k] = now
}

func (g *guard) setRelaySession(id string) {
	g.mu.Lock()
	g.relaySession = id
	g.mu.Unlock()
}

func (g *guard) setHostTarget(fn func() string) {
	g.mu.Lock()
	g.hostTarget = fn
	g.mu.Unlock()
}

func (g *guard) hostTargetLocked(id string) bool {
	if id == "" || g.hostTarget == nil {
		return false
	}
	return g.hostTarget() == id
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
	if (workerTypes[t.typ] || t.typ == "iframe") && previous {
		return true
	}
	if g.workerFromProtectedCreatorLocked(t) {
		return true
	}
	if g.iframeFromProtectedAncestorLocked(t) {
		return true
	}
	return g.o.TargetWith(t.url, t.typ, previous, g.openerState(t), g.evidenceLocked(t))
}

const maxFrameAncestors = 64

func (g *guard) frameParentOfLocked(id string) string {
	if id == "" {
		return ""
	}
	if t := g.targets[id]; t != nil && t.parentFrame != "" {
		return t.parentFrame
	}
	return g.frameParents[id]
}

func (g *guard) noteFrameParentLocked(id, parent string) bool {
	if id == "" || parent == "" || id == parent {
		return false
	}
	if prev, ok := g.frameParents[id]; ok {
		return prev == parent
	}
	if len(g.frameParents) >= 4096 {
		g.frameParentsFull = true
		return false
	}
	g.frameParents[id] = parent
	if t := g.targets[id]; t != nil && t.parentFrame == "" {
		t.parentFrame = parent
	}
	return true
}

func (g *guard) forgetFrameParentLocked(id string) {
	if id == "" {
		return
	}
	delete(g.frameParents, id)
	delete(g.ancestryDisputed, id)
	if g.frameParentsFull && len(g.frameParents) < 4096 {
		g.frameParentsFull = false
	}
}

func (g *guard) pruneFrameParentsLocked(id string) {
	if id == "" {
		return
	}
	var drop []string
	for child, parent := range g.frameParents {
		if child == id || parent == id {
			drop = append(drop, child)
		}
	}
	for _, child := range drop {
		g.forgetFrameParentLocked(child)
	}
}

func (g *guard) frameParentChainReachesLocked(start, owner string) bool {
	if start == "" || owner == "" {
		return false
	}
	seen := map[string]bool{}
	id := start
	for depth := 0; id != "" && depth < maxFrameAncestors; depth++ {
		if id == owner {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		id = g.frameParentOfLocked(id)
	}
	return false
}

func (g *guard) iframeAncestorStartLocked(t *target) string {
	if t == nil {
		return ""
	}
	if t.parentFrame != "" {
		return t.parentFrame
	}
	return g.frameParents[t.id]
}

func (g *guard) noteIframeAttachParentLocked(info targetInfo) {
	owner := g.sessionOwnerLocked(g.attachParent)
	if owner == nil {
		return
	}
	pf := parentFrameOf(info)
	if pf != "" && pf != owner.id {
		if _, ok := g.frameParents[pf]; ok {
			if !g.frameParentChainReachesLocked(pf, owner.id) {
				g.ancestryDisputed[info.TargetID] = true
			}
		} else if !g.noteFrameParentLocked(pf, owner.id) {
			g.ancestryDisputed[info.TargetID] = true
		}
	}
	parent := pf
	if parent == "" {
		parent = owner.id
	}
	if !g.noteFrameParentLocked(info.TargetID, parent) && g.frameParents[info.TargetID] != parent {
		g.ancestryDisputed[info.TargetID] = true
	}
}

func (g *guard) iframeParentFromFramesLocked(t *target) *target {
	if t == nil || t.id == "" {
		return nil
	}
	for pageID, frames := range g.frames {
		if frames == nil || !frames[t.id] {
			continue
		}
		if p := g.targets[pageID]; p != nil && !p.gone && (p.typ == "page" || p.typ == "tab" || p.typ == "iframe") {
			return p
		}
	}
	return nil
}

func (g *guard) iframeParentTargetLocked(t *target) *target {
	if t == nil {
		return nil
	}
	if g.attachParent != "" {
		if owner := g.sessionOwnerLocked(g.attachParent); owner != nil {
			return owner
		}
	}
	for _, h := range g.held {
		if h == nil || h.target != t.id {
			continue
		}
		if owner := g.sessionOwnerLocked(h.parent); owner != nil {
			return owner
		}
	}
	if p := g.targets[t.parentFrame]; p != nil && !p.gone && (p.typ == "page" || p.typ == "tab" || p.typ == "iframe") {
		return p
	}
	if page := g.iframePageLocked(t); page != nil {
		return page
	}
	if t.framePage != nil && !t.framePage.gone {
		return t.framePage
	}
	if p := g.iframeParentFromFramesLocked(t); p != nil {
		return p
	}
	seen := map[string]bool{t.id: true}
	id := g.iframeAncestorStartLocked(t)
	for depth := 0; id != "" && depth < maxFrameAncestors; depth++ {
		if seen[id] {
			break
		}
		seen[id] = true
		if p := g.targets[id]; p != nil && !p.gone && (p.typ == "page" || p.typ == "tab" || p.typ == "iframe") {
			return p
		}
		id = g.frameParentOfLocked(id)
	}
	return nil
}

func (g *guard) iframeAncestryClassifiedLocked(t *target) bool {
	if t == nil {
		return false
	}
	if parent := g.iframeParentTargetLocked(t); parent != nil {
		return g.creatorClassifiedLocked(parent.id)
	}
	seen := map[string]bool{t.id: true}
	id := g.iframeAncestorStartLocked(t)
	for depth := 0; id != "" && depth < maxFrameAncestors; depth++ {
		if seen[id] {
			return false
		}
		seen[id] = true
		if g.creatorClassifiedLocked(id) {
			return true
		}
		id = g.frameParentOfLocked(id)
	}
	return false
}

func (g *guard) iframeBrowserRootedLocked(t *target) bool {
	if t == nil || t.typ != "iframe" {
		return false
	}
	if g.iframeAncestorStartLocked(t) != "" || g.iframeParentTargetLocked(t) != nil {
		return false
	}
	if g.attachParent != "" {
		return g.browserSessions[g.attachParent] || g.sessionOwnerLocked(g.attachParent) == nil
	}
	for _, h := range g.held {
		if h != nil && h.target == t.id && h.typ == "iframe" {
			return h.parent == "" || g.browserSessions[h.parent]
		}
	}
	return true
}

func (g *guard) iframeErrorPageURLLocked(t *target) bool {
	return t != nil && strings.HasPrefix(strings.ToLower(t.url), "chrome-error:")
}

func (g *guard) iframeAncestryUnknownLocked(t *target) bool {
	if t == nil || t.gone || t.typ != "iframe" || t.protected || g.explicit[t.id] > 0 {
		return false
	}
	if g.extensionFrameTargetLocked(t) {
		return false
	}
	if g.frameParentsFull {
		return true
	}
	if g.attachParent != "" {
		return g.sessionOwnerLocked(g.attachParent) == nil
	}
	if g.iframeParentTargetLocked(t) != nil {
		return false
	}
	if g.iframeAncestorStartLocked(t) != "" {
		return !g.iframeAncestryClassifiedLocked(t)
	}
	return true
}

func (g *guard) iframeFromProtectedAncestorLocked(t *target) bool {
	if t == nil || t.typ != "iframe" {
		return false
	}
	if g.ancestryDisputed[t.id] {
		return true
	}
	if parent := g.iframeParentTargetLocked(t); parent != nil && parent.protected {
		return true
	}
	seen := map[string]bool{t.id: true}
	id := g.iframeAncestorStartLocked(t)
	for depth := 0; id != "" && depth < maxFrameAncestors; depth++ {
		if seen[id] {
			return false
		}
		seen[id] = true
		if g.creatorProtectedLocked(id) {
			return true
		}
		id = g.frameParentOfLocked(id)
	}
	return false
}

func (g *guard) iframeAncestorPendingLocked(t *target) bool {
	if t == nil || t.typ != "iframe" {
		return false
	}
	seen := map[string]bool{t.id: true}
	id := g.iframeAncestorStartLocked(t)
	for depth := 0; id != "" && depth < maxFrameAncestors; depth++ {
		if seen[id] {
			break
		}
		seen[id] = true
		if g.targetHeldLocked(id) || g.targetUnconfirmedLocked(id) {
			return true
		}
		id = g.frameParentOfLocked(id)
	}
	if parent := g.iframeParentTargetLocked(t); parent != nil {
		return g.targetHeldLocked(parent.id) || g.targetUnconfirmedLocked(parent.id)
	}
	return false
}

func (g *guard) iframeHasAncestorLocked(t *target, ancestor string) bool {
	if t == nil || ancestor == "" {
		return false
	}
	if parent := g.iframeParentTargetLocked(t); parent != nil && parent.id == ancestor {
		return true
	}
	if t.parentFrame == ancestor || g.frameParents[t.id] == ancestor {
		return true
	}
	seen := map[string]bool{t.id: true}
	id := g.iframeAncestorStartLocked(t)
	for depth := 0; id != "" && depth < maxFrameAncestors; depth++ {
		if seen[id] {
			return false
		}
		if id == ancestor {
			return true
		}
		seen[id] = true
		id = g.frameParentOfLocked(id)
	}
	return false
}

func (g *guard) iframeUnresolvedLocked(t *target) bool {
	if t == nil || t.gone || t.typ != "iframe" || t.protected || g.explicit[t.id] > 0 {
		return false
	}
	if g.extensionFrameTargetLocked(t) {
		return false
	}
	if g.iframeAncestorPendingLocked(t) {
		return true
	}
	if g.iframeAncestryUnknownLocked(t) {
		if g.iframeBrowserRootedLocked(t) && g.iframeErrorPageURLLocked(t) {
			return false
		}
		return true
	}
	if g.attachParent != "" {
		owner := g.sessionOwnerLocked(g.attachParent)
		if owner == nil {
			return true
		}
		return g.targetUnconfirmedLocked(owner.id)
	}
	if !g.iframeHeldLocked(t.id) {
		return false
	}
	parent := g.iframeParentTargetLocked(t)
	if parent == nil {
		return !g.iframeBrowserRootedLocked(t) || !g.iframeErrorPageURLLocked(t)
	}
	return g.targetUnconfirmedLocked(parent.id)
}

func (g *guard) iframePendingLocked(t *target) bool {
	if t == nil || t.gone || t.typ != "iframe" || t.protected || g.explicit[t.id] > 0 {
		return false
	}
	if g.extensionFrameTargetLocked(t) {
		return t.url == ""
	}
	if g.iframeUnresolvedLocked(t) {
		return true
	}
	return t.url == ""
}

func (g *guard) iframeHeldLocked(id string) bool {
	for _, h := range g.held {
		if h.target == id && h.typ == "iframe" {
			return true
		}
	}
	return false
}

func (g *guard) targetHeldLocked(id string) bool {
	for _, h := range g.held {
		if h.target == id {
			return true
		}
	}
	return false
}

func (g *guard) iframeConcealedLocked(id string) bool {
	t := g.targets[id]
	if t != nil && t.typ != "iframe" && !g.iframeHeldLocked(id) {
		return false
	}
	if t == nil && !g.iframeHeldLocked(id) {
		return false
	}
	if g.extensionFrameTargetLocked(t) {
		return false
	}
	return g.iframeHeldLocked(id) || g.iframeUnresolvedLocked(t) || g.iframeAncestryUnknownLocked(t)
}

func (g *guard) targetBlockedLocked(id string) bool {
	return id != "" && (g.targetProtectedLocked(id) || g.targetHeldLocked(id) || g.targetUnconfirmedLocked(id))
}

func (g *guard) iframeFollowRefusedLocked(sessionID string) bool {
	s := g.sessions[sessionID]
	if s == nil {
		return false
	}
	t := g.targets[s.target]
	if t == nil || t.gone || t.typ != "iframe" {
		return false
	}
	if t.protected || g.iframeFromProtectedAncestorLocked(t) || g.iframeAncestorPendingLocked(t) {
		return true
	}
	if g.iframeAncestryUnknownLocked(t) && (!g.iframeBrowserRootedLocked(t) || !g.iframeErrorPageURLLocked(t)) {
		return true
	}
	chain := g.iframePageLocked(t)
	if chain == nil {
		chain = g.iframeParentTargetLocked(t)
	}
	if chain != nil && (chain.protected || g.targetBlockedLocked(chain.id)) {
		return true
	}
	if g.extensionFrameTargetLocked(t) || s.parent == "" || g.browserSessions[s.parent] {
		return false
	}
	if g.held[s.parent] != nil || g.hidden[s.parent] {
		return true
	}
	owner := g.sessionOwnerLocked(s.parent)
	if owner == nil {
		return false
	}
	return g.targetBlockedLocked(owner.id) || (chain != nil && owner.id != chain.id)
}

func (g *guard) workerFromProtectedCreatorLocked(t *target) bool {
	if t == nil || !workerTypes[t.typ] {
		return false
	}
	if g.creatorProtectedLocked(t.openerFrame) || g.creatorProtectedLocked(t.opener) || g.creatorProtectedLocked(t.parentFrame) {
		return true
	}
	parent := g.attachParent
	if parent != "" && (g.hidden[parent] || g.held[parent] != nil) {
		return true
	}
	if owner := g.sessionOwnerLocked(parent); owner != nil && owner.protected {
		return true
	}
	return strings.HasPrefix(strings.ToLower(t.url), "data:") && !g.workerCreatorUnprotectedLocked(t)
}

func parentFrameOf(info targetInfo) string {
	if info.ParentFrameID != "" {
		return info.ParentFrameID
	}
	return info.ParentID
}

func (g *guard) noteChildFrameLocked(id, raw string) [][]byte {
	if id == "" || protect.Inheriting(raw) || raw == "null" || raw == "://" {
		return nil
	}
	if prev, ok := g.childFrames[id]; ok {
		if g.o.Loaded(prev) && !g.o.Loaded(raw) {
			return nil
		}
	} else if len(g.childFrames) >= 4096 {
		return nil
	}
	g.childFrames[id] = raw
	return g.redecideWorkersLocked(id)
}

func (g *guard) forgetChildFrameLocked(id string) {
	if id == "" {
		return
	}
	delete(g.childFrames, id)
}

func (g *guard) redecideWorkersLocked(creator string) [][]byte {
	if creator == "" {
		return nil
	}
	var send [][]byte
	for _, t := range g.targets {
		if t == nil || t.gone || !workerTypes[t.typ] {
			continue
		}
		if t.opener != creator && t.openerFrame != creator && t.parentFrame != creator {
			continue
		}
		prev := t.protected
		t.protected = g.decide(t, prev)
		if t.protected && !prev {
			send = append(send, g.protectLocked(t)...)
		}
	}
	return append(send, g.redecideIframesLocked(creator)...)
}

func (g *guard) redecideIframesLocked(creator string) [][]byte {
	if creator == "" {
		return nil
	}
	var send [][]byte
	for _, t := range g.targets {
		if t == nil || t.gone || t.typ != "iframe" {
			continue
		}
		if !g.iframeHasAncestorLocked(t, creator) {
			continue
		}
		prev := t.protected
		t.protected = g.decide(t, prev)
		if t.protected && !prev {
			send = append(send, g.protectLocked(t)...)
		}
	}
	return send
}

func (g *guard) workerUnshownLocked(id string) bool {
	t := g.targets[id]
	return t != nil && !t.gone && workerTypes[t.typ] && !t.shown
}

func (g *guard) noteWorkerShownLocked(info targetInfo) {
	if !workerTypes[info.Type] && info.Type != "iframe" {
		return
	}
	if t := g.targets[info.TargetID]; t != nil {
		t.shown = true
	}
}

func (g *guard) iframeUnshownLocked(id string) bool {
	t := g.targets[id]
	return t != nil && !t.gone && t.typ == "iframe" && !t.shown
}

func (g *guard) creatorProtectedLocked(id string) bool {
	if id == "" {
		return false
	}
	if g.targetProtectedLocked(id) {
		return true
	}
	return g.o.Loaded(g.childFrames[id])
}

func (g *guard) creatorClassifiedLocked(id string) bool {
	if id == "" {
		return false
	}
	if t := g.targets[id]; t != nil && !t.gone && (t.protected || !protect.Inheriting(t.url)) {
		return true
	}
	raw := g.childFrames[id]
	return raw != "" && !protect.Inheriting(raw)
}

func (g *guard) workerCreatorClassifiedLocked(t *target) bool {
	if t == nil {
		return false
	}
	if t.openerFrame != "" {
		return g.creatorClassifiedLocked(t.openerFrame)
	}
	if t.parentFrame != "" {
		return g.creatorClassifiedLocked(t.parentFrame)
	}
	return t.opener != "" && g.creatorClassifiedLocked(t.opener)
}

func (g *guard) workerUnresolvedLocked(t *target) bool {
	if t == nil || t.gone || !workerTypes[t.typ] || t.protected || g.explicit[t.id] > 0 {
		return false
	}
	if g.o.URL(t.url) || protect.Unparseable(t.url) {
		return false
	}
	if t.openerFrame != "" && !g.creatorClassifiedLocked(t.openerFrame) {
		return true
	}
	if t.parentFrame != "" && !g.creatorClassifiedLocked(t.parentFrame) {
		return true
	}
	if t.opener != "" && !g.creatorClassifiedLocked(t.opener) {
		return true
	}
	return protect.Inheriting(t.url) && !g.workerCreatorClassifiedLocked(t)
}

func (g *guard) workerHeldLocked(id string) bool {
	for _, h := range g.held {
		if h.target == id && workerTypes[h.typ] {
			return true
		}
	}
	return false
}

func (g *guard) workerConcealedLocked(id string) bool {
	t := g.targets[id]
	if t != nil && !workerTypes[t.typ] && !g.workerHeldLocked(id) {
		return false
	}
	if t == nil && !g.workerHeldLocked(id) {
		return false
	}
	return g.workerHeldLocked(id) || g.workerUnresolvedLocked(t)
}

func (g *guard) workerCreatorUnprotectedLocked(t *target) bool {
	if t.openerFrame != "" || t.parentFrame != "" {
		return false
	}
	c := g.targets[t.opener]
	return t.opener != "" && c != nil && !c.gone && !c.protected
}

func (g *guard) sessionOwnerLocked(id string) *target {
	if id == "" {
		return nil
	}
	if s := g.sessions[id]; s != nil {
		return g.targets[s.target]
	}
	if h := g.held[id]; h != nil {
		return g.targets[h.target]
	}
	return g.targets[g.hiddenOwner[id]]
}

func (g *guard) record(info targetInfo) *target {
	t, ok := g.targets[info.TargetID]
	if !ok {
		t = &target{id: info.TargetID}
		g.targets[info.TargetID] = t
	}
	if nt := g.navTargets[t.id]; nt != nil && (t.url != info.URL || t.typ != info.Type) {
		nt.revision++
	}
	if t.url != info.URL {
		t.frameOwner = nil
		delete(g.frameProbeAttempts, t.id)
		delete(g.frameProbed, t.id)
		delete(g.frames, t.id)
	}
	parent := parentFrameOf(info)
	if parent == "" {
		parent = t.parentFrame
	}
	if parent == "" {
		parent = g.frameParents[info.TargetID]
	}
	if parent != "" {
		g.noteFrameParentLocked(info.TargetID, parent)
	}
	if info.Type == "iframe" && (t.parentFrame != parent || t.url != info.URL) {
		for id, page := range g.targets {
			if page.typ == "page" && page.ctx == info.BrowserContextID {
				delete(g.frameProbeAttempts, id)
				delete(g.frameProbed, id)
			}
		}
	}
	if t.parentFrame != parent || t.ctx != info.BrowserContextID {
		t.framePage, t.frameDocument, t.frameOwner = nil, "", nil
	}
	t.parentFrame = parent
	t.typ, t.url, t.opener, t.openerFrame, t.ctx = info.Type, info.URL, info.OpenerID, info.OpenerFrameID, info.BrowserContextID
	g.bindIframeDocumentLocked(t)
	return t
}

func (g *guard) observeLocked(info targetInfo) [][]byte {
	if info.TargetID == "" {
		return nil
	}
	t := g.record(info)
	t.gone = false
	prev := t.protected
	if nt := g.navTargets[t.id]; !prev && navTypes[t.typ] && nt != nil && !nt.failed && g.o.Loaded(t.url) && !protect.Unparseable(t.url) {
		nt.historyLocked = true
		g.beginConfirmLocked(nt)
		return g.confirmTargetLocked(nt)
	}
	t.protected = g.decide(t, prev)
	var send [][]byte
	if t.protected && !prev {
		send = append(send, g.protectLocked(t)...)
	}
	send = append(send, g.redecideWorkersLocked(t.id)...)
	if g.extensionFrameTargetLocked(t) {
		if page := g.iframePageLocked(t); page != nil {
			send = append(send, g.cutCastsLocked(page.id)...)
		}
		send = append(send, g.cutFrameCastsLocked()...)
	}
	return send
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
		if !g.opened[info.TargetID] {
			g.listed[info.TargetID] = true
			g.forgetOpenedSeenLocked(info.TargetID)
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
		for _, id := range []string{t.opener, t.openerFrame, t.parentFrame} {
			if op, ok := g.targets[id]; ok && fresh[op.id] {
				resolve(op)
			}
		}
		t.protected = g.decide(t, t.protected)
	}
	for id := range fresh {
		resolve(g.targets[id])
	}
}

func (g *guard) markLocked(t *target) [][]byte {
	var send [][]byte
	if !t.protected {
		t.protected = true
		send = append(send, g.protectLocked(t)...)
	}
	return append(send, g.redecideWorkersLocked(t.id)...)
}

func (g *guard) protectLocked(t *target) [][]byte {
	g.logf("cdp: a target moved onto a protected origin; detaching its sessions target=%s", t.id)
	var out [][]byte
	for _, s := range g.sessions {
		if s.target != t.id || s.refused {
			continue
		}
		s.refused = true
		g.addRefusedLocked(s.id)
		g.logf("cdp: detaching a session on a protected origin target=%s session=%s parent=%s waiting=%t", t.id, s.id, s.parent, s.waiting)
		if s.waiting {
			out = append(out, g.resumeLocked(s.id, t.id))
		}
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
	g.clearBlockedChildrenLocked(id)
	delete(g.frameProbeAttempts, id)
	delete(g.frameProbed, id)
	delete(g.frames, id)
	g.forgetChildFrameLocked(id)
	g.pruneFrameParentsLocked(id)
	g.forgetExtensionFrameURLLocked("", id)
	g.forgetExtensionProbeWorldsForTargetLocked(id)
	g.navForgetLocked(id)
	g.dropHeldLocked(id)
	g.forgetWorldsLocked(id)
	g.forgetWorldRequestsLocked(id)
	g.forgetOpenedSeenLocked(id)
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
	g.forgetCastLocked(id)
	g.forgetExtensionProbeWorldSessionLocked(id)
	g.forgetExtensionOwnerCallsLocked(id)
	g.forgetVersionCallsLocked(id)
	g.releaseExtensionPaintsForSessionLocked(id)
	delete(g.sessions, id)
	delete(g.browserSessions, id)
	g.markedCount -= len(g.marked[id])
	delete(g.marked, id)
	delete(g.saturated, id)
	delete(g.assets, id)
	g.forgetExtrasLocked(id)
	for k := range g.pending {
		if k.session == id {
			delete(g.pending, k)
			delete(g.paintMethods, k)
			delete(g.commitMarks, k)
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

func releaseReservations(list []*link.Reservation) {
	for _, r := range list {
		r.Release()
	}
}

func (g *guard) holdReservation(data []byte) (*link.Reservation, bool) {
	if g.reserveHold == nil {
		return nil, true
	}
	return g.reserveHold(data)
}

func (g *guard) holdBytes(n int) (*link.Reservation, bool) {
	if g.reserveBytes == nil {
		return nil, true
	}
	return g.reserveBytes(n)
}

func (g *guard) takeReleasesLocked() []*link.Reservation {
	list := g.pendingRelease
	g.pendingRelease = nil
	return list
}

func (g *guard) releaseAllocations() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, list := range g.heldExtras {
		for _, h := range list {
			h.reservation.Release()
		}
	}
	for _, h := range g.heldAnswers {
		h.reservation.Release()
	}
	for _, list := range g.heldPauses {
		for _, h := range list {
			h.reservation.Release()
		}
	}
	for _, s := range g.debuggers {
		g.dropDebuggerLocked(s)
	}
	g.releaseHeldExtensionPaintLocked()
	clear(g.heldExtras)
	clear(g.heldAnswers)
	clear(g.heldPauses)
	releaseReservations(g.takeReleasesLocked())
}
