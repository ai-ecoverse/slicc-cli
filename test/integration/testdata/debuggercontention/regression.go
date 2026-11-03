package cdp

import (
	"encoding/binary"
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
	"github.com/ai-ecoverse/slicc-cli/test/integration/testdata/stage1"
	"github.com/coder/websocket"
)

type debuggerContentionPause struct{ wait func() }

var debuggerContentionHook atomic.Pointer[debuggerContentionPause]

func debuggerContentionAfterOrdinaryWrite(data []byte) {
	if strings.Contains(string(data), `"method":"Page.loadEventFired"`) {
		if hook := debuggerContentionHook.Load(); hook != nil {
			hook.wait()
		}
	}
}

func TestDebuggerContentionAfterBarrier(t *testing.T) {
	browser := stage1.NewBrowser(t)
	peer, server := stage1.Open(t, func(s *link.Stream) {
		if err := s.Accept(map[string]any{}); err != nil {
			t.Error(err)
		}
	}, "contention")
	client := stage1.NewClient(t, peer)
	conn, _, err := websocket.Dial(t.Context(), strings.Replace(browser.Server.URL, "http:", "ws:", 1)+"/devtools/browser/stage1", nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(MaxMessage)
	conn.SetReadLimit(MaxMessage)
	g := newGuard(protect.MustNew(nil), nil)
	g.targets["T"] = &target{id: "T", typ: "page", url: "https://ordinary.test/doc", document: "generation", documentRevision: 1}
	g.sessions["S"] = &session{id: "S", target: "T", typ: "page"}
	g.sessions["N"] = &session{id: "N", target: "T", typ: "page"}
	nt := &navTarget{target: "T", session: "N", done: true}
	g.navTargets["T"], g.navSessions["N"] = nt, nt
	g.shown["T"] = &shownSet{contexts: map[int64]*worldCtx{7: {unique: "context", defaultWorld: true, frame: "T"}}}
	g.noteDebuggerLocked(callKey{"S", 1}, &command{session: "S", method: "Debugger.enable"})
	state := g.debuggers["S"]
	state.enabled = true
	data := []byte(`{"method":"Debugger.scriptParsed","sessionId":"S","params":{"scriptId":"script","executionContextId":7,"url":"https://ordinary.test/script.js"}}`)
	var script message
	if err := json.Unmarshal(data, &script); err != nil {
		t.Fatal(err)
	}
	g.debuggerScriptLocked(&script, data)
	pause := &debuggerPause{state: state, generation: 1, deadline: time.Now().Add(5 * time.Second)}
	state.pause = pause
	g.holdDebuggerDataLocked(pause, []byte(`{"method":"Debugger.paused","sessionId":"S","params":{"callFrames":[{"callFrameId":"{\"injectedScriptId\":7,\"id\":0}","location":{"scriptId":"script"}}]}}`))
	rl := &relay{g: g, s: server, conn: conn, logf: func(string, ...any) {}, parked: map[parkKey][]parkedMessage{}, reader: &messageReader{reserve: func(n int) *link.Reservation { return server.ReserveAllocation(n, false) }}}
	pageDone, browserDone := make(chan struct{}), make(chan struct{})
	go func() { rl.toPage(); close(pageDone) }()
	go func() { rl.toBrowser(); close(browserDone) }()
	t.Cleanup(func() {
		_ = peer.Reset("ECANCELED")
		_ = conn.CloseNow()
		<-pageDone
		<-browserDone
		rl.reader.release()
	})
	tree := g.waitDebuggerPlacement(pause, func() message { return rl.debuggerRead(pause) }, server.Done())
	verdict := g.publishDebugger(pause, tree)
	defer releaseReservations(verdict.releases)
	if len(verdict.page) != 2 {
		t.Fatal("pause proof did not validate")
	}
	var frames []byte
	for _, record := range verdict.page {
		frames = append(frames, pageFrame(record)...)
	}
	reservation := server.ReserveAllocation(len(frames), false)
	if reservation == nil {
		t.Fatal("batch reservation failed")
	}
	defer reservation.Release()
	releaseReader := make(chan struct{})
	unlocked := make(chan struct{})
	var release, signal sync.Once
	unblockReader := func() { release.Do(func() { close(releaseReader) }) }
	defer unblockReader()
	debuggerContentionHook.Store(&debuggerContentionPause{wait: func() {
		signal.Do(func() { close(unlocked) })
		select {
		case <-releaseReader:
		case <-t.Context().Done():
		}
	}})
	defer debuggerContentionHook.Store(nil)
	blocked, releaseCredit := client.HoldNextRecord(t)
	padding, _ := json.Marshal(map[string]any{"method": "fixture.padding", "params": map[string]any{"data": strings.Repeat("x", link.Window-8192)}})
	framed := make([]byte, 4+len(padding))
	binary.BigEndian.PutUint32(framed, uint32(len(padding)))
	copy(framed[4:], padding)
	if _, err := server.Write(framed); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("credit did not stall")
	}
	browser.Emit(t, "forwardable")
	deadline := time.Now().Add(time.Second)
	for rl.pageMu.TryLock() {
		rl.pageMu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("ordinary write did not stall after the barrier")
		}
		runtime.Gosched()
	}
	browser.Emit(t, "foreign")
	attempts := make(chan [2]bool, 1)
	go func() { ok, failed := rl.tryDebuggerFrames(pause, frames, &tree); attempts <- [2]bool{ok, failed} }()
	var result [2]bool
	returned := false
	select {
	case result = <-attempts:
		returned = true
	case <-time.After(100 * time.Millisecond):
	}
	revoked := time.Now()
	releaseCredit()
	select {
	case <-unlocked:
	case <-time.After(time.Second):
		t.Fatal("ordinary write did not release pageMu")
	}
	if !returned {
		select {
		case result = <-attempts:
		case <-time.After(time.Second):
			t.Fatal("publication attempt did not return")
		}
	}
	retry := make(chan message, 1)
	go func() {
		retry <- g.waitDebuggerPlacement(pause, func() message { return rl.debuggerRead(pause) }, server.Done())
	}()
	unblockReader()
	if result[0] || result[1] {
		t.Errorf("contention published or failed instead of requiring another barrier: %v", result)
	}
	select {
	case <-browser.Resumes:
	case <-time.After(time.Second):
		t.Fatal("queued revocation did not resume promptly")
	}
	if time.Since(revoked) >= time.Second {
		t.Fatal("revocation waited for expiry")
	}
	select {
	case fresh := <-retry:
		if ok, _ := rl.tryDebuggerFrames(pause, frames, &fresh); ok {
			t.Error("fresh barrier published a revoked pause")
		}
	case <-time.After(time.Second):
		t.Fatal("fresh barrier did not observe queued revocation")
	}
	stage1.Result(t, client.Call(t, "Browser.getVersion", "", nil))
	for {
		select {
		case event := <-client.Events:
			if method, _ := event["method"].(string); strings.HasPrefix(method, "Debugger.") {
				t.Errorf("post-barrier contention published %s", method)
			}
		default:
			return
		}
	}
}
