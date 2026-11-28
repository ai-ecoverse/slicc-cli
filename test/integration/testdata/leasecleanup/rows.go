package lease

import (
	"context"
	"encoding/json"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
	"github.com/coder/websocket"
)

const cleanupAllowed = "https://billing.example/start"
const cleanupForeign = "https://foreign.example/landing"
const cleanupError = "chrome-error://chromewebdata/"

type cleanupPlan struct {
	landing, fail, close                          string
	pause, enter, frameRead, closeGate, writeDone chan struct{}
	pipe                                          net.Conn
	child, opaque                                 bool
}

type cleanupChrome struct {
	*fakeChrome
	plan          cleanupPlan
	mu            sync.Mutex
	attempts      []string
	counts        map[string]int
	open          map[string]bool
	locations     map[string]string
	landed        bool
	missingCloses int
	firstClose    time.Time
}

func (f *cleanupChrome) event(method, session string, params any) {
	if method == "Target.targetDestroyed" {
		fields, _ := params.(map[string]any)
		id, _ := fields["targetId"].(string)
		f.mu.Lock()
		delete(f.open, id)
		f.mu.Unlock()
	}
	f.push(map[string]any{"method": method, "sessionId": session, "params": params})
}

func (f *cleanupChrome) Close(status websocket.StatusCode, reason string) error {
	f.mu.Lock()
	pipe := f.plan.pipe
	f.mu.Unlock()
	if pipe != nil {
		_ = pipe.Close()
	}
	return f.fakeChrome.Close(status, reason)
}

func (f *cleanupChrome) answer(id int64, result any) {
	f.push(map[string]any{"id": id, "result": result})
}

func (f *cleanupChrome) fail(id int64, msg string) {
	f.push(map[string]any{"id": id, "error": map[string]any{"code": -32000, "message": msg}})
}

func (f *cleanupChrome) Write(ctx context.Context, kind websocket.MessageType, data []byte) error {
	var m struct {
		ID      int64  `json:"id"`
		Method  string `json:"method"`
		Session string `json:"sessionId"`
		Params  struct {
			Target     string `json:"targetId"`
			Expression string `json:"expression"`
		} `json:"params"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	f.mu.Lock()
	p, landed := f.plan, f.landed
	f.mu.Unlock()
	if m.Method == "Target.closeTarget" && p.closeGate != nil {
		select {
		case <-p.closeGate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	f.counts[m.Method]++
	count := f.counts[m.Method]
	if m.Method == "Target.createTarget" {
		f.open["T1"] = true
	}
	if m.Method == "Target.closeTarget" {
		if len(f.attempts) == 0 {
			f.firstClose = time.Now()
		}
		f.attempts = append(f.attempts, m.Params.Target)
	}
	f.mu.Unlock()
	if m.Method == p.fail {
		f.fail(m.ID, "scripted setup failure")
		return nil
	}
	switch m.Method {
	case "Target.getTargets":
		targets := make([]map[string]any, 0, 3)
		f.mu.Lock()
		for _, id := range []string{"decoy", "popup", "T1"} {
			if f.open[id] {
				targets = append(targets, map[string]any{"targetId": id, "type": "page", "url": f.locations[id]})
			}
		}
		f.mu.Unlock()
		f.answer(m.ID, map[string]any{"targetInfos": targets})
		return nil
	case "Page.getFrameTree":
		if p.frameRead != nil {
			defer func() {
				select {
				case p.frameRead <- struct{}{}:
				default:
				}
			}()
		}
		if p.child {
			f.fail(m.ID, "slicc: refused: target is on seven's origin")
			return nil
		}
		f.fakeChrome.mu.Lock()
		current := f.url
		f.fakeChrome.mu.Unlock()
		if p.opaque && landed {
			f.fakeChrome.mu.Lock()
			result := f.frame()
			f.fakeChrome.mu.Unlock()
			result["frameTree"].(map[string]any)["frame"].(map[string]any)["securityOrigin"] = "null"
			f.answer(m.ID, result)
			return nil
		}
		if current == cleanupError {
			f.answer(m.ID, map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "T1", "url": cleanupError, "loaderId": "error-loader", "unreachableUrl": cleanupForeign}}})
			return nil
		}
	case "Page.navigate", "Runtime.evaluate":
		assign := assignLiteral.FindStringSubmatch(m.Params.Expression) != nil
		if p.pause != nil && (m.Method == "Page.navigate" || assign) {
			close(p.enter)
			select {
			case <-p.pause:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if p.landing != "" && (m.Method == "Page.navigate" || assign) {
			f.fakeChrome.mu.Lock()
			f.navigate(p.landing)
			f.fakeChrome.mu.Unlock()
			f.mu.Lock()
			f.locations["T1"] = p.landing
			f.landed = true
			f.mu.Unlock()
			f.event("Page.frameNavigated", "S1", map[string]any{"frame": map[string]any{"id": "T1"}})
			f.event("Target.targetInfoChanged", "", map[string]any{"targetInfo": map[string]any{"targetId": "decoy", "openerId": "T1", "url": p.landing}})
			if assign {
				f.answer(m.ID, map[string]any{"result": map[string]any{"value": map[string]any{"ok": true}}})
			} else {
				f.answer(m.ID, map[string]any{"frameId": "popup", "loaderId": "loader"})
			}
			return nil
		}
	case "Target.closeTarget":
		if p.pipe != nil {
			close(p.enter)
			defer close(p.writeDone)
			if deadline, ok := ctx.Deadline(); ok {
				if err := p.pipe.SetWriteDeadline(deadline); err != nil {
					return err
				}
			}
			_, err := p.pipe.Write(data)
			return err
		}
		if p.enter != nil && p.close == "hang" && count == 1 {
			close(p.enter)
		}
		if p.close == "hang" {
			return nil
		}
		if p.close == "drop" {
			return f.Close(websocket.StatusGoingAway, "scripted disconnect")
		}
		if p.close == "error" {
			f.fail(m.ID, "scripted close failure")
			return nil
		}
		f.mu.Lock()
		existed := f.open[m.Params.Target]
		delete(f.open, m.Params.Target)
		if !existed {
			f.missingCloses++
		}
		f.mu.Unlock()
		if !existed {
			f.push(map[string]any{"id": m.ID, "error": map[string]any{"code": -32602, "message": "No target with given id found"}})
			return nil
		}
		f.event("Target.detachedFromTarget", "", map[string]any{"sessionId": "S1", "targetId": m.Params.Target})
		f.answer(m.ID, map[string]any{"success": true})
		return nil
	}
	return f.fakeChrome.Write(ctx, kind, data)
}

type cleanupFixture struct {
	b *Broker
	a *active
	f *cleanupChrome
}

func newCleanupFixture(t *testing.T) cleanupFixture {
	t.Helper()
	o, err := protect.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &cleanupChrome{fakeChrome: newFakeChrome(), counts: map[string]int{}, open: map[string]bool{"decoy": true, "popup": true}, locations: map[string]string{"decoy": cleanupForeign, "popup": "https://opener.example/"}}
	bs := &browserSession{tabs: map[string]*tab{}}
	bs.c = cdp.NewClient(f, bs.observe)
	return newCleanupBroker(t, o, bs, f)
}

func newCleanupBroker(t *testing.T, o *protect.Origins, bs *browserSession, f *cleanupChrome) cleanupFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	a := &active{claims: claims{ID: "lease", Exp: time.Now().Add(time.Minute).Unix(), CNF: confirmation{"holder"}, Allow: billing}, tab: new(tab), ctx: ctx, cancel: cancel}
	a.timer = time.NewTimer(time.Hour)
	b := &Broker{bs: bs, origins: o, active: map[string]*active{a.ID: a}}
	t.Cleanup(b.Close)
	return cleanupFixture{b, a, f}
}

type cleanupResult struct {
	err     error
	payload []byte
}

func (f cleanupFixture) action(t *testing.T, op, path string) cleanupResult {
	t.Helper()
	args := json.RawMessage(`{}`)
	if op == "goto" {
		args = json.RawMessage(literal(map[string]string{"url": path}))
	}
	var r cleanupResult
	r.err = f.b.action(t.Context(), f.a, op, args, func(payload any) bool { r.payload, _ = json.Marshal(payload); return true })
	return r
}

func cleanupRefusal(t *testing.T, r cleanupResult, want error) {
	t.Helper()
	if r.err != want || len(r.payload) != 0 {
		t.Fatalf("refusal got %v payload %q, want %v and zero payload", r.err, r.payload, want)
	}
	if got := failure(r.err, 1); got["reason"] != want.Error() || got["result"] != nil {
		t.Fatalf("refusal envelope: %v", got)
	}
}

func (f cleanupFixture) set(p cleanupPlan) {
	f.f.mu.Lock()
	defer f.f.mu.Unlock()
	f.f.plan = p
	f.f.landed = false
	if p.landing != "" {
		f.f.locations["decoy"] = p.landing
	}
	if p.close == "gone" {
		delete(f.f.open, "T1")
	}
}

func (f cleanupFixture) prime(t *testing.T) {
	t.Helper()
	if r := f.action(t, "goto", cleanupAllowed); r.err != nil || len(r.payload) == 0 {
		t.Fatalf("setup goto: %+v", r)
	}
}

func (f cleanupFixture) recorded(t *testing.T, want []string, open bool) {
	t.Helper()
	f.b.retiring.Wait()
	f.b.bs.cleanup.Wait()
	f.f.mu.Lock()
	defer f.f.mu.Unlock()
	if !slices.Equal(f.f.attempts, want) || f.f.open["T1"] != open || !f.f.open["decoy"] || !f.f.open["popup"] {
		t.Fatalf("attempts=%v want=%v, open=%v want recorded open=%t, both decoys must survive", f.f.attempts, want, f.f.open, open)
	}
}

func (f cleanupFixture) revoke(t *testing.T) {
	t.Helper()
	if e := f.b.Revoke(f.a.ID); e != nil {
		t.Fatal(e)
	}
	f.b.retiring.Wait()
	f.b.bs.cleanup.Wait()
}

func cleanupPhase(t *testing.T) { t.Log("cleanup phase reached") }

func cleanupSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture signal timed out")
	}
}

func (f cleanupFixture) sendEvent(t *testing.T, method, session string, params any) {
	t.Helper()
	sub := f.b.bs.c.Subscribe()
	defer sub.Close()
	f.f.event(method, session, params)
	select {
	case ev := <-sub.Events():
		if ev.Method != method || ev.SessionID != session {
			t.Fatalf("fixture event %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatalf("callback stalled on %s", method)
	}
}

func (f cleanupFixture) timedNavigation(t *testing.T, p cleanupPlan, deadline bool) {
	t.Helper()
	f.set(p)
	type timedResult struct {
		cleanupResult
		elapsed time.Duration
	}
	results := make(chan timedResult, 1)
	cleanupPhase(t)
	start := time.Now()
	go func() {
		r := f.action(t, "goto", cleanupAllowed)
		results <- timedResult{r, time.Since(start)}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	got := cleanupReceive(t, ctx, results)
	cleanupRefusal(t, got.cleanupResult, denied)
	f.recorded(t, []string{"T1"}, false)
	f.f.mu.Lock()
	closeElapsed := f.f.firstClose.Sub(start)
	f.f.mu.Unlock()
	if deadline && closeElapsed < 5*time.Second {
		t.Fatalf("close reached the browser before the action deadline: %v", closeElapsed)
	}
	if deadline && (got.elapsed < 5*time.Second || got.elapsed > 8*time.Second) {
		t.Fatalf("refusal took %v; main waited for the 5 s action deadline", got.elapsed)
	}
	if !deadline && got.elapsed >= 3*time.Second {
		t.Fatalf("refusal took %v; main returned promptly on a foreign URL change", got.elapsed)
	}
	t.Logf("holder refusal after %v (main used deadline=%t)", got.elapsed, deadline)
}

var leaseCleanupObservations sync.Map

type cleanupObservation struct{ allowed, prompt bool }

func leaseCleanupNavigationObserved(tab *tab, bad, prompt, allowed bool) {
	if bad || allowed {
		if hook, ok := leaseCleanupObservations.Load(tab); ok {
			select {
			case hook.(chan cleanupObservation) <- cleanupObservation{allowed, prompt}:
			default:
			}
		}
	}
}

func (f cleanupFixture) navigateAfterBad(t *testing.T, plan cleanupPlan, next string) cleanupResult {
	t.Helper()
	f.set(plan)
	observed := make(chan cleanupObservation, 8)
	leaseCleanupObservations.Store(f.a.tab, observed)
	defer leaseCleanupObservations.Delete(f.a.tab)
	result := make(chan cleanupResult, 1)
	start := time.Now()
	cleanupPhase(t)
	go func() { result <- f.action(t, "goto", cleanupAllowed) }()
	observationContext, cancelObservations := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancelObservations()
	if first := cleanupReceive(t, observationContext, observed); first.allowed || first.prompt {
		t.Fatal("fixture did not reach a deferred bad observation")
	}
	f.set(cleanupPlan{})
	f.f.fakeChrome.mu.Lock()
	f.f.navigate(next)
	f.f.fakeChrome.mu.Unlock()
	f.sendEvent(t, "Page.frameNavigated", "S1", map[string]any{"frame": map[string]any{"id": "T1"}})
	if next == cleanupForeign {
		for !cleanupReceive(t, observationContext, observed).prompt {
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
	defer cancel()
	got := cleanupReceive(t, ctx, result)
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Fatalf("navigation did not resolve on the later commit before its deadline: %v", elapsed)
	}
	return got
}

func TestLeaseCleanupRows(t *testing.T) {
	for _, op := range []string{"R1CreateRedirect", "R2AssignRedirect"} {
		for _, row := range []struct{ name, url string }{{"Foreign", cleanupForeign}, {"ErrorPage", cleanupError}} {
			t.Run(op+row.name, func(t *testing.T) {
				t.Parallel()
				f := newCleanupFixture(t)
				if op == "R2AssignRedirect" {
					f.prime(t)
				}
				f.timedNavigation(t, cleanupPlan{landing: row.url}, row.url == cleanupError)
				f.recorded(t, []string{"T1"}, false)
				reads := f.f.counts["Page.getFrameTree"]
				cleanupRefusal(t, f.action(t, "goto", cleanupAllowed), denied)
				if f.f.counts["Page.getFrameTree"] != reads {
					t.Fatal("lost tab sent another frame read")
				}
				if len(f.b.List().Active) != 1 {
					t.Fatal("scope loss retired the lease")
				}
				f.revoke(t)
				f.recorded(t, []string{"T1"}, false)
			})
		}
	}
	for _, method := range []string{"Target.attachToTarget", "Page.enable", "Runtime.enable", "Page.navigate"} {
		t.Run("R3CreateFailure-"+method, func(t *testing.T) {
			f := newCleanupFixture(t)
			f.set(cleanupPlan{fail: method})
			cleanupPhase(t)
			cleanupRefusal(t, f.action(t, "goto", cleanupAllowed), unsupported)
			f.recorded(t, []string{"T1"}, false)
			cleanupRefusal(t, f.action(t, "goto", cleanupAllowed), retired)
			f.revoke(t)
			f.recorded(t, []string{"T1"}, false)
		})
	}
	for _, mode := range []string{"error", "gone", "drop"} {
		t.Run("R4CloseFailure-"+mode, func(t *testing.T) {
			f := newCleanupFixture(t)
			f.prime(t)
			f.set(cleanupPlan{landing: cleanupForeign, close: mode})
			cleanupPhase(t)
			cleanupRefusal(t, f.action(t, "goto", cleanupAllowed), denied)
			f.recorded(t, []string{"T1"}, mode != "gone")
			f.revoke(t)
			f.recorded(t, []string{"T1"}, mode != "gone")
		})
	}
	t.Run("R5RecordedTargetOnly", func(t *testing.T) {
		f := newCleanupFixture(t)
		f.prime(t)
		f.set(cleanupPlan{landing: cleanupForeign})
		f.sendEvent(t, "Target.targetCreated", "", map[string]any{"targetInfo": map[string]any{"targetId": "popup", "openerId": "decoy", "url": cleanupForeign}})
		f.sendEvent(t, "Page.frameNavigated", "S1", map[string]any{"frame": map[string]any{"id": "popup"}})
		cleanupPhase(t)
		cleanupRefusal(t, f.action(t, "goto", cleanupAllowed), denied)
		f.recorded(t, []string{"T1"}, false)
	})
	t.Run("R6UnknownTimeoutKeepsTab", func(t *testing.T) {
		t.Parallel()
		f := newCleanupFixture(t)
		f.set(cleanupPlan{landing: "about:blank"})
		cleanupPhase(t)
		start := time.Now()
		cleanupRefusal(t, f.action(t, "goto", cleanupAllowed), denied)
		if time.Since(start) < 5*time.Second {
			t.Fatal("action did not reach browser timeout")
		}
		f.recorded(t, nil, true)
	})
	t.Run("R7ExpiryDoesNotRepeatClose", func(t *testing.T) {
		f := newCleanupFixture(t)
		f.set(cleanupPlan{landing: cleanupForeign})
		cleanupPhase(t)
		cleanupRefusal(t, f.action(t, "goto", cleanupAllowed), denied)
		f.b.mu.Lock()
		f.a.timer.Stop()
		expired := make(chan struct{})
		f.a.timer = time.AfterFunc(time.Second, func() { _ = f.b.Revoke(f.a.ID); close(expired) })
		f.b.mu.Unlock()
		cleanupSignal(t, expired)
		f.recorded(t, []string{"T1"}, false)
	})
	t.Run("R8ConcurrentActionBusy", func(t *testing.T) {
		f := newCleanupFixture(t)
		p := cleanupPlan{landing: cleanupForeign, enter: make(chan struct{}), pause: make(chan struct{})}
		f.set(p)
		result := make(chan cleanupResult, 1)
		go func() { result <- f.action(t, "goto", cleanupAllowed) }()
		cleanupSignal(t, p.enter)
		cleanupPhase(t)
		cleanupRefusal(t, f.action(t, "snapshot", ""), busy)
		f.recorded(t, nil, true)
		close(p.pause)
		cleanupRefusal(t, <-result, denied)
		f.b.bs.mu.Lock()
		lost := f.a.tab.cut
		f.b.bs.mu.Unlock()
		if !lost {
			t.Fatal("scope loss was not recorded before the refusal")
		}
		f.recorded(t, []string{"T1"}, false)
	})
	for _, order := range []string{"DetachFirst", "DestroyFirst", "CloseBeforeDestroy"} {
		t.Run("R10UserClose"+order, func(t *testing.T) {
			f := newCleanupFixture(t)
			f.prime(t)
			p := cleanupPlan{}
			if order == "DetachFirst" {
				p.closeGate = make(chan struct{})
			}
			f.set(p)
			cleanupPhase(t)
			destroy := func() { f.sendEvent(t, "Target.targetDestroyed", "", map[string]any{"targetId": "T1"}) }
			if order == "DestroyFirst" {
				destroy()
			}
			f.sendEvent(t, "Target.detachedFromTarget", "", map[string]any{"sessionId": "S1", "targetId": "decoy"})
			if order == "DetachFirst" {
				destroy()
				close(p.closeGate)
			}
			f.recorded(t, []string{"T1"}, false)
			if order == "CloseBeforeDestroy" {
				destroy()
			}
			wantMissing := 1
			if order == "CloseBeforeDestroy" {
				wantMissing = 0
			}
			if f.f.missingCloses != wantMissing {
				t.Fatalf("missing-target replies: %d, want %d", f.f.missingCloses, wantMissing)
			}
			cleanupRefusal(t, f.action(t, "snapshot", ""), denied)
			f.revoke(t)
			f.recorded(t, []string{"T1"}, false)
		})
	}
	t.Run("R11CrashAlone", func(t *testing.T) {
		f := newCleanupFixture(t)
		f.prime(t)
		cleanupPhase(t)
		f.sendEvent(t, "Inspector.targetCrashed", "S1", map[string]any{})
		f.recorded(t, nil, true)
	})
	t.Run("R12AllowedNavigation", func(t *testing.T) {
		f := newCleanupFixture(t)
		f.prime(t)
		cleanupPhase(t)
		if r := f.action(t, "goto", "https://billing.example/next"); r.err != nil {
			t.Fatal(r.err)
		}
		f.recorded(t, nil, true)
	})
	t.Run("R13OwnDiscard", func(t *testing.T) {
		f := newCleanupFixture(t)
		f.prime(t)
		cleanupPhase(t)
		f.revoke(t)
		f.recorded(t, []string{"T1"}, false)
	})
	t.Run("R14CallbackDoesNotBlock", func(t *testing.T) {
		f := newCleanupFixture(t)
		f.prime(t)
		p := cleanupPlan{close: "hang", enter: make(chan struct{})}
		f.set(p)
		cleanupPhase(t)
		f.sendEvent(t, "Target.detachedFromTarget", "", map[string]any{"sessionId": "S1"})
		cleanupSignal(t, p.enter)
		f.sendEvent(t, "Fixture.marker", "", map[string]any{})
		cleanupRefusal(t, f.action(t, "snapshot", ""), denied)
		f.recorded(t, []string{"T1"}, true)
	})
	t.Run("R15bChildDetachDoesNotCloseParent", func(t *testing.T) {
		f := newCleanupFixture(t)
		f.prime(t)
		f.set(cleanupPlan{child: true})
		baseline := f.action(t, "snapshot", "")
		cleanupRefusal(t, baseline, denied)
		f.recorded(t, nil, true)
		cleanupPhase(t)
		f.sendEvent(t, "Target.detachedFromTarget", "", map[string]any{"sessionId": "child", "targetId": "T1"})
		got := f.action(t, "snapshot", "")
		cleanupRefusal(t, got, baseline.err)
		if !slices.Equal(got.payload, baseline.payload) {
			t.Fatal("holder payload differs from baseline")
		}
		f.recorded(t, nil, true)
	})

	t.Run("R16ShutdownRejectsLateCleanup", func(t *testing.T) {
		t.Parallel()
		f := newCleanupFixture(t)
		f.prime(t)
		p := cleanupPlan{close: "hang", enter: make(chan struct{})}
		f.set(p)
		bs := f.b.bs
		late := &tab{target: "late-target", session: "late-session"}
		bs.mu.Lock()
		bs.tabs[late.session] = late
		bs.mu.Unlock()

		done := make(chan struct{})
		func() {
			f.a.tab.gate.Lock()
			defer f.a.tab.gate.Unlock()
			go func() { f.b.Close(); close(done) }()
			cleanupSignal(t, f.a.ctx.Done())
			bs.mu.Lock()
			stopped := bs.stopped
			bs.mu.Unlock()
			if stopped {
				t.Fatal("shutdown stopped scheduling while the active tab still needed retirement")
			}
		}()
		cleanupSignal(t, p.enter)
		cleanupPhase(t)
		deadline := time.NewTimer(time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			bs.mu.Lock()
			stopped := bs.stopped
			bs.mu.Unlock()
			if stopped {
				break
			}
			select {
			case <-tick.C:
			case <-deadline.C:
				t.Fatal("shutdown did not reach its browser cleanup wait")
			}
		}
		select {
		case <-done:
			t.Fatal("shutdown returned while its first close was outstanding")
		default:
		}
		f.sendEvent(t, "Target.detachedFromTarget", "", map[string]any{"sessionId": late.session})
		bs.mu.Lock()
		cleared := late.target == "" && late.lost && late.cut && bs.tabs[late.session] == nil
		bs.mu.Unlock()
		if !cleared {
			t.Fatal("shutdown did not clear the late detached tab")
		}
		cleanupSignal(t, done)
		select {
		case <-bs.c.Done():
		default:
			t.Fatal("shutdown returned with its CDP client open")
		}
		f.recorded(t, []string{"T1"}, true)
	})
	t.Run("R17CutNavigationKeepsTimeout", func(t *testing.T) {
		t.Parallel()
		f := newCleanupFixture(t)
		p := cleanupPlan{landing: "about:blank", frameRead: make(chan struct{}, 1)}
		f.set(p)
		start := time.Now()
		results := make(chan cleanupResult, 1)
		go func() { results <- f.action(t, "goto", cleanupAllowed) }()
		cleanupSignal(t, p.frameRead)
		cleanupPhase(t)
		f.sendEvent(t, "Target.detachedFromTarget", "", map[string]any{"sessionId": "S1"})
		f.recorded(t, []string{"T1"}, false)
		ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
		defer cancel()
		cleanupRefusal(t, cleanupReceive(t, ctx, results), denied)
		if elapsed := time.Since(start); elapsed < 5*time.Second {
			t.Fatalf("detach returned from navigation before the action deadline: %v", elapsed)
		}
	})

	t.Run("R18SameURLNewOpaqueDocument", func(t *testing.T) {
		t.Parallel()
		f := newCleanupFixture(t)
		f.prime(t)
		f.timedNavigation(t, cleanupPlan{landing: cleanupAllowed, opaque: true}, true)
		f.recorded(t, []string{"T1"}, false)
		cleanupRefusal(t, f.action(t, "snapshot", ""), denied)
		f.revoke(t)
		f.recorded(t, []string{"T1"}, false)
	})
	t.Run("R19ShutdownAbortsStalledCloseWrite", func(t *testing.T) {
		t.Parallel()
		f := newCleanupFixture(t)
		f.prime(t)
		writer, peer := net.Pipe()
		defer writer.Close()
		defer peer.Close()
		p := cleanupPlan{pipe: writer, enter: make(chan struct{}), writeDone: make(chan struct{})}
		f.set(p)
		done := make(chan struct{})
		start := time.Now()
		go func() { f.b.Close(); close(done) }()
		cleanupSignal(t, p.enter)
		cleanupPhase(t)
		select {
		case <-done:
			if time.Since(start) > 4500*time.Millisecond {
				t.Fatal("shutdown exceeded its close wait plus scheduling tolerance")
			}
		case <-time.After(4500 * time.Millisecond):
			_ = peer.Close()
			cleanupSignal(t, done)
			t.Fatal("shutdown waited for a write on a pipe with no reader")
		}
		select {
		case <-p.writeDone:
		default:
			t.Fatal("cleanup writer outlived broker shutdown")
		}
		f.recorded(t, []string{"T1"}, true)
	})
	for _, row := range []struct {
		name string
		plan cleanupPlan
		next string
	}{
		{"R20ErrorPageRecoversToAllowed", cleanupPlan{landing: cleanupError}, "https://billing.example/recovered"},
		{"R21SameURLOpaqueReloadRecovers", cleanupPlan{landing: cleanupAllowed, opaque: true}, cleanupAllowed},
		{"R22ErrorPageThenForeignRefusesPromptly", cleanupPlan{landing: cleanupError}, cleanupForeign},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f := newCleanupFixture(t)
			f.prime(t)
			got := f.navigateAfterBad(t, row.plan, row.next)
			if row.next == cleanupForeign {
				cleanupRefusal(t, got, denied)
				f.recorded(t, []string{"T1"}, false)
				f.revoke(t)
				f.recorded(t, []string{"T1"}, false)
				return
			}
			if got.err != nil || string(got.payload) != "{}" {
				t.Fatalf("main accepted the later allowed commit: got %v payload=%s", got.err, got.payload)
			}
			f.recorded(t, nil, true)
		})
	}

	t.Run("R23RestoredOriginalAllowedDocumentKeepsTab", func(t *testing.T) {
		t.Parallel()
		f := newCleanupFixture(t)
		f.prime(t)
		f.f.fakeChrome.mu.Lock()
		originalURL, originalLoader := f.f.url, f.f.loader
		f.f.fakeChrome.mu.Unlock()

		f.set(cleanupPlan{landing: cleanupError})
		observed := make(chan cleanupObservation, 8)
		leaseCleanupObservations.Store(f.a.tab, observed)
		defer leaseCleanupObservations.Delete(f.a.tab)
		results := make(chan cleanupResult, 1)
		start := time.Now()
		cleanupPhase(t)
		go func() { results <- f.action(t, "goto", cleanupAllowed) }()
		ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
		defer cancel()
		if first := cleanupReceive(t, ctx, observed); first.allowed || first.prompt {
			t.Fatal("fixture did not first observe an error page with a deferred refusal")
		}

		f.set(cleanupPlan{})
		f.f.fakeChrome.mu.Lock()
		f.f.url, f.f.loader = originalURL, originalLoader
		f.f.fakeChrome.mu.Unlock()
		f.sendEvent(t, "Page.frameNavigated", "S1", map[string]any{"frame": map[string]any{"id": "T1"}, "type": "BackForwardCacheRestore"})
		for !cleanupReceive(t, ctx, observed).allowed {
		}
		cleanupRefusal(t, cleanupReceive(t, ctx, results), denied)
		if elapsed := time.Since(start); elapsed < 5*time.Second {
			t.Fatalf("restoring the original loader returned before the action deadline: %v", elapsed)
		}
		f.recorded(t, nil, true)
		f.b.bs.mu.Lock()
		recorded := f.a.tab.target == "T1" && !f.a.tab.lost && f.b.bs.tabs["S1"] == f.a.tab
		f.b.bs.mu.Unlock()
		if !recorded {
			t.Fatal("the restored target is no longer recorded for its lease")
		}
	})

}
