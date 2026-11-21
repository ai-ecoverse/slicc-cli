package cdp

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	vsTarget   = "VS-TARGET"
	vsSession  = "S-VS"
	vsAttachID = 50
	vsReadID   = 51
	vsSecret   = "SECRET-FROM-VAULT-DOCUMENT"
	vsOrdinary = "https://ordinary.test/ok"
)

func vsGuard(t *testing.T) *guard {
	t.Helper()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, nil)
	g.relaySession = "RELAY"
	return g
}

func vsRelay(method, url string) []byte {
	return encode(map[string]any{
		"sessionId": "RELAY",
		"method":    method,
		"params": map[string]any{
			"targetInfo": map[string]any{"targetId": vsTarget, "type": "page", "url": url, "browserContextId": "CTX"},
		},
	})
}

func vsAttachedEvent(url string) []byte {
	return encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          vsSession,
			"waitingForDebugger": false,
			"targetInfo":         map[string]any{"targetId": vsTarget, "type": "page", "url": url, "browserContextId": "CTX"},
		},
	})
}

func vsAttachReply() []byte {
	return encode(map[string]any{"id": vsAttachID, "result": map[string]any{"sessionId": vsSession}})
}

func vsReadReply() []byte {
	return encode(map[string]any{
		"id":        vsReadID,
		"sessionId": vsSession,
		"result":    map[string]any{"result": map[string]any{"type": "string", "value": vsSecret}},
	})
}

func vsTargetInfoAnswer(id int64, url string) []byte {
	return encode(map[string]any{
		"id":        id,
		"sessionId": "RELAY",
		"result": map[string]any{
			"targetInfo": map[string]any{"targetId": vsTarget, "type": "page", "url": url, "browserContextId": "CTX"},
		},
	})
}

func vsOutbound(raw []byte) (id int64, method, session string) {
	var m struct {
		ID        *int64 `json:"id"`
		Method    string `json:"method"`
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(raw, &m)
	if m.ID != nil {
		id = *m.ID
	}
	return id, m.Method, m.SessionID
}

func vsWatchReady(g *guard) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ready := make(chan struct{})
	close(ready)
	g.navTargets[vsTarget] = &navTarget{target: vsTarget, ready: ready, done: true}
}

type vsOutcome struct {
	clientPayloads []string
	attachRefused  bool
	attachReplied  bool
	sawAttachEvent bool
	readForwarded  bool
	readRefused    bool
	readDelivered  bool
	detachSeen     bool
	refusal        string
}

func (o *vsOutcome) take(v browserVerdict) {
	if v.forward != nil {
		o.clientPayloads = append(o.clientPayloads, string(v.forward))
	}
	for _, b := range v.page {
		o.clientPayloads = append(o.clientPayloads, string(b))
	}
}

func (o *vsOutcome) summarize() {
	for _, p := range o.clientPayloads {
		switch {
		case strings.Contains(p, `"id":50`) && strings.Contains(p, `"error"`):
			o.attachRefused = true
		case strings.Contains(p, `"id":50`) && strings.Contains(p, `"result"`):
			o.attachReplied = true
		case strings.Contains(p, "attachedToTarget") && strings.Contains(p, vsSession):
			o.sawAttachEvent = true
		case strings.Contains(p, "detachedFromTarget") && strings.Contains(p, vsSession):
			o.detachSeen = true
		case strings.Contains(p, vsSecret):
			o.readDelivered = true
		}
	}
}

func vsDrain(g *guard, o *vsOutcome, v browserVerdict, live string) {
	o.take(v)
	for _, raw := range v.send {
		id, method, session := vsOutbound(raw)
		if method == "Target.getTargetInfo" && session == "RELAY" {
			vsDrain(g, o, g.fromBrowser(vsTargetInfoAnswer(id, live)), live)
		}
	}
}

func vsRead(g *guard, o *vsOutcome, live string) {
	pv := g.fromCommand(&command{id: vsReadID, method: "Runtime.evaluate", session: vsSession, params: map[string]any{"expression": "document.body.innerText"}}, vsReadID, true)
	if pv.reply != nil {
		o.readRefused = true
		o.clientPayloads = append(o.clientPayloads, string(pv.reply))
		o.refusal = string(pv.reply)
		return
	}
	if pv.wait != nil {
		o.readRefused = true
		return
	}
	if pv.forward != nil {
		o.readForwarded = true
		vsDrain(g, o, g.fromBrowser(vsReadReply()), live)
	}
}

type vsScenario struct {
	announced string
	watched   bool
	lateArm   bool
}

func vsRun(t *testing.T, order int, live string, s vsScenario) vsOutcome {
	t.Helper()
	announced := s.announced
	if announced == "" {
		announced = "about:blank"
	}
	g := vsGuard(t)
	var o vsOutcome
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", announced)), live)
	if s.watched && !s.lateArm {
		vsWatchReady(g)
	}
	pv := g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
	if pv.reply != nil {
		o.clientPayloads = append(o.clientPayloads, string(pv.reply))
		o.summarize()
		return o
	}
	vsDrain(g, &o, g.fromBrowser(vsAttachedEvent(announced)), live)
	if s.lateArm {
		vsWatchReady(g)
	}
	switch order {
	case 1:
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetInfoChanged", live)), live)
		vsDrain(g, &o, g.fromBrowser(vsAttachReply()), live)
		vsRead(g, &o, live)
	case 2:
		vsDrain(g, &o, g.fromBrowser(vsAttachReply()), live)
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetInfoChanged", live)), live)
		vsRead(g, &o, live)
	default:
		vsDrain(g, &o, g.fromBrowser(vsAttachReply()), live)
		vsRead(g, &o, live)
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetInfoChanged", live)), live)
	}
	o.summarize()
	return o
}

func vsProtectedURLs() []string {
	return []string{
		"view-source:https://vault.sliccy.ai/doc",
		"view-source:https://seven.sliccy.ai/",
		"blob:https://vault.sliccy.ai/0c1d2e3f-4a5b-6c7d-8e9f-001122334455",
		"filesystem:https://vault.sliccy.ai/temporary/x.html",
		"view-source:blob:https://vault.sliccy.ai/0c1d2e3f-4a5b-6c7d-8e9f-001122334455",
	}
}

func vsCheck(t *testing.T, order int, live string, s vsScenario) {
	t.Helper()
	o := vsRun(t, order, live, s)
	t.Logf("announced=%s lateArm=%t watched=%t order %d %s: attachRefused=%t attachReplied=%t sawAttachEvent=%t readForwarded=%t readRefused=%t readDelivered=%t detachSeen=%t refusal=%s", s.announced, s.lateArm, s.watched, order, live, o.attachRefused, o.attachReplied, o.sawAttachEvent, o.readForwarded, o.readRefused, o.readDelivered, o.detachSeen, o.refusal)
	if o.readDelivered {
		t.Errorf("order %d %s: the client READ content of a protected document through a session the guard admitted while it still held a stale URL: %v", order, live, o.clientPayloads)
	}
	if !o.attachRefused && (o.attachReplied || o.sawAttachEvent) {
		t.Errorf("order %d %s: the attach to a target that is really protected was admitted and visible to the client (refused=%t replied=%t event=%t detach=%t)", order, live, o.attachRefused, o.attachReplied, o.sawAttachEvent, o.detachSeen)
	}
}

func vsOrderName(prefix string, url string, order int) string {
	return prefix + url + "/order" + string(rune('0'+order))
}

func TestViewSourceAttachRacesTargetInfoChanged(t *testing.T) {
	for _, watched := range []bool{false, true} {
		for _, url := range vsProtectedURLs() {
			for order := 1; order <= 3; order++ {
				watched, url, order := watched, url, order
				name := "unwatched/"
				if watched {
					name = "watched/"
				}
				t.Run(vsOrderName(name, url, order), func(t *testing.T) {
					vsCheck(t, order, url, vsScenario{watched: watched})
				})
			}
		}
	}
}

func TestViewSourceAttachLoadedBeforeNavArmed(t *testing.T) {
	for _, url := range vsProtectedURLs() {
		for order := 1; order <= 3; order++ {
			url, order := url, order
			t.Run(vsOrderName("lateArm/", url, order), func(t *testing.T) {
				vsCheck(t, order, url, vsScenario{lateArm: true})
			})
		}
	}
}

func TestViewSourceAttachOrdinaryRedirectToProtected(t *testing.T) {
	for _, watched := range []bool{false, true} {
		for _, url := range vsProtectedURLs() {
			for order := 1; order <= 3; order++ {
				watched, url, order := watched, url, order
				name := "unwatched/"
				if watched {
					name = "watched/"
				}
				t.Run(vsOrderName("fromOrdinary/"+name, url, order), func(t *testing.T) {
					vsCheck(t, order, url, vsScenario{announced: vsOrdinary, watched: watched})
				})
			}
		}
	}
}

func TestViewSourceAttachControlOrdinaryIsReadable(t *testing.T) {
	cases := []vsScenario{
		{watched: true},
		{lateArm: true},
		{announced: vsOrdinary, watched: true},
		{announced: vsOrdinary, lateArm: true},
	}
	for _, s := range cases {
		for order := 1; order <= 3; order++ {
			s, order := s, order
			o := vsRun(t, order, vsOrdinary, s)
			if o.attachRefused || !o.readDelivered {
				t.Errorf("announced=%s lateArm=%t watched=%t order %d: an ordinary target must stay attachable and readable in this model (refused=%t read=%t payloads=%v)", s.announced, s.lateArm, s.watched, order, o.attachRefused, o.readDelivered, o.clientPayloads)
			}
		}
	}
}

const (
	vsSession2  = "S-VS-2"
	vsAttachID2 = 60
	vsReadID2   = 61
)

func vsAttachedEventSession(url, session string) []byte {
	return encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          session,
			"waitingForDebugger": false,
			"targetInfo":         map[string]any{"targetId": vsTarget, "type": "page", "url": url, "browserContextId": "CTX"},
		},
	})
}

func vsAttachReplyID(id int64, session string) []byte {
	return encode(map[string]any{"id": id, "result": map[string]any{"sessionId": session}})
}

func vsReadOn(g *guard, o *vsOutcome, live string, id int64, session string) {
	pv := g.fromCommand(&command{id: id, method: "Runtime.evaluate", session: session, params: map[string]any{"expression": "document.body.innerText"}}, id, true)
	if pv.reply != nil {
		o.readRefused = true
		o.clientPayloads = append(o.clientPayloads, string(pv.reply))
		o.refusal = string(pv.reply)
		return
	}
	if pv.wait != nil {
		o.readRefused = true
		return
	}
	if pv.forward != nil {
		o.readForwarded = true
		reply := encode(map[string]any{
			"id":        id,
			"sessionId": session,
			"result":    map[string]any{"result": map[string]any{"type": "string", "value": vsSecret}},
		})
		vsDrain(g, o, g.fromBrowser(reply), live)
	}
}

func vsDrainAnswer(g *guard, o *vsOutcome, v browserVerdict, answer func(id int64) string) (asked []int64) {
	o.take(v)
	for _, raw := range v.send {
		id, method, session := vsOutbound(raw)
		if method != "Target.getTargetInfo" || session != "RELAY" {
			continue
		}
		asked = append(asked, id)
		url := answer(id)
		if url == "" {
			continue
		}
		more := vsDrainAnswer(g, o, g.fromBrowser(vsTargetInfoAnswer(id, url)), answer)
		asked = append(asked, more...)
	}
	return asked
}

func vsPendingConfirm(g *guard, session string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, dc := range g.docInflight {
		if dc != nil && !dc.done && dc.holds[session] != nil {
			return true
		}
	}
	return false
}

func vsDocFailedCount(g *guard) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.docFailed)
}

func vsDocConfirmCount(g *guard) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.docInflight)
}

func vsAttachIDKey(attachID int64) string {
	return fmt.Sprintf(`"id":%d`, attachID)
}

func vsSecondAdmitted(payloads []string, session string, attachID int64) (event, reply, read bool) {
	aid := vsAttachIDKey(attachID)
	for _, p := range payloads {
		if strings.Contains(p, "attachedToTarget") && strings.Contains(p, session) {
			event = true
		}
		if strings.Contains(p, aid) && strings.Contains(p, `"result"`) && strings.Contains(p, session) {
			reply = true
		}
		if strings.Contains(p, vsSecret) && strings.Contains(p, session) {
			read = true
		}
	}
	return event, reply, read
}

func vsCheckSecondRefused(t *testing.T, payloads []string, session string, attachID int64, confirmed bool) {
	t.Helper()
	event, reply, read := vsSecondAdmitted(payloads, session, attachID)
	if read {
		t.Errorf("second session %s read a protected document", session)
	}
	if event || reply {
		t.Errorf("second session %s was client-visible for a protected live document (event=%t reply=%t confirmed=%t); a racing attach must wait for or reuse the same confirmation, never skip it", session, event, reply, confirmed)
	}
	if !confirmed {
		t.Errorf("second session %s never waited on a Target.getTargetInfo confirmation for the shared target", session)
	}
}

func TestViewSourceAttachSecondSessionMustConfirm(t *testing.T) {
	for _, url := range vsProtectedURLs() {
		for order := 1; order <= 3; order++ {
			for _, s1Pass := range []bool{false, true} {
				url, order, s1Pass := url, order, s1Pass
				name := "s1fail/"
				if s1Pass {
					name = "s1pass/"
				}
				t.Run(vsOrderName(name, url, order), func(t *testing.T) {
					g := vsGuard(t)
					var o vsOutcome
					live := url
					vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), live)
					vsWatchReady(g)
					_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
					_ = g.fromCommand(&command{id: vsAttachID2, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID2, true)
					s1Answer := live
					if s1Pass {
						s1Answer = vsOrdinary
					}
					var held []int64
					holdAnswers := func(id int64) string {
						held = append(held, id)
						return ""
					}
					answerIDs := func(ids []int64, url string) {
						for _, id := range ids {
							vsDrainAnswer(g, &o, g.fromBrowser(vsTargetInfoAnswer(id, url)), func(int64) string { return url })
						}
					}
					failIDs := func(ids []int64) {
						for _, id := range ids {
							vsDrainAnswer(g, &o, g.fromBrowser(encode(map[string]any{
								"id":        id,
								"sessionId": "RELAY",
								"error":     map[string]any{"code": -32000, "message": "No target with given id found"},
							})), func(int64) string { return "" })
						}
					}
					takeHeld := func() []int64 {
						ids := append([]int64(nil), held...)
						held = nil
						return ids
					}
					s2Confirmed := false
					noteS2 := func() {
						if vsPendingConfirm(g, vsSession2) || len(held) > 0 {
							s2Confirmed = true
						}
					}
					runS1 := func() {
						vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEventSession("about:blank", vsSession)), holdAnswers)
						vsDrainAnswer(g, &o, g.fromBrowser(vsAttachReplyID(vsAttachID, vsSession)), holdAnswers)
						ids := takeHeld()
						if s1Pass {
							answerIDs(ids, s1Answer)
						} else {
							failIDs(ids)
						}
					}
					runS2EventReply := func() {
						vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEventSession("about:blank", vsSession2)), holdAnswers)
						noteS2()
						vsDrainAnswer(g, &o, g.fromBrowser(vsAttachReplyID(vsAttachID2, vsSession2)), holdAnswers)
						noteS2()
						ids := takeHeld()
						if len(ids) > 0 {
							s2Confirmed = true
						}
						answerIDs(ids, live)
					}
					switch order {
					case 1:
						vsDrainAnswer(g, &o, g.fromBrowser(vsRelay("Target.targetInfoChanged", live)), holdAnswers)
						answerIDs(takeHeld(), live)
						runS1()
						runS2EventReply()
					case 2:
						runS1()
						vsDrainAnswer(g, &o, g.fromBrowser(vsRelay("Target.targetInfoChanged", live)), holdAnswers)
						answerIDs(takeHeld(), live)
						runS2EventReply()
					default:
						vsDrainAnswer(g, &o, g.fromBrowser(vsAttachReplyID(vsAttachID2, vsSession2)), holdAnswers)
						noteS2()
						s2Pending := takeHeld()
						if len(s2Pending) > 0 {
							s2Confirmed = true
						}
						runS1()
						vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEventSession("about:blank", vsSession2)), holdAnswers)
						noteS2()
						s2Pending = append(s2Pending, takeHeld()...)
						if len(s2Pending) > 0 {
							s2Confirmed = true
						}
						answerIDs(s2Pending, live)
					}
					vsReadOn(g, &o, live, vsReadID2, vsSession2)
					vsCheckSecondRefused(t, o.clientPayloads, vsSession2, vsAttachID2, s2Confirmed)
				})
			}
		}
	}
}

func TestViewSourceAttachDocAttachClearedOnFailedAttach(t *testing.T) {
	g := vsGuard(t)
	baseFailed := vsDocFailedCount(g)
	baseConfirm := vsDocConfirmCount(g)
	for i := 0; i < 32; i++ {
		id := int64(1000 + i)
		tid := fmt.Sprintf("MISSING-%d", i)
		pv := g.fromCommand(&command{id: id, method: "Target.attachToTarget", params: map[string]any{"targetId": tid, "flatten": true}}, id, true)
		if pv.reply != nil {
			continue
		}
		_ = g.fromBrowser(encode(map[string]any{
			"id":    id,
			"error": map[string]any{"code": -32000, "message": "No target with given id found"},
		}))
	}
	if vsDocFailedCount(g) != baseFailed {
		t.Errorf("docFailed grew from %d to %d after failed attaches", baseFailed, vsDocFailedCount(g))
	}
	if vsDocConfirmCount(g) != baseConfirm {
		t.Errorf("docInflight grew from %d to %d after failed attaches", baseConfirm, vsDocConfirmCount(g))
	}
}

func TestViewSourceAttachConfirmMustSettleWithoutAnswer(t *testing.T) {
	t.Setenv(HoldTimeoutEnv, "5ms")
	g := vsGuard(t)
	var o vsOutcome
	_ = g.fromBrowser(vsRelay("Target.targetCreated", "about:blank"))
	vsWatchReady(g)
	_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
	v := g.fromBrowser(vsAttachedEvent("about:blank"))
	o.take(v)
	var confirmID int64
	for _, raw := range v.send {
		id, method, session := vsOutbound(raw)
		if method == "Target.getTargetInfo" && session == "RELAY" {
			confirmID = id
		}
	}
	if confirmID == 0 {
		t.Fatal("expected a confirmation getTargetInfo")
	}
	_ = g.fromBrowser(vsAttachReply())
	if !vsPendingConfirm(g, vsSession) {
		t.Fatal("confirm should still be pending before an answer")
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !vsPendingConfirm(g, vsSession) && vsDocConfirmCount(g) == 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if vsPendingConfirm(g, vsSession) || vsDocConfirmCount(g) > 0 {
		t.Errorf("confirm left pending when getTargetInfo never arrives; must fail closed on the hold deadline")
	}
}

func TestViewSourceAttachConfirmEmptyRelayFailsClosed(t *testing.T) {
	g := vsGuard(t)
	_ = g.fromBrowser(vsRelay("Target.targetCreated", "about:blank"))
	vsWatchReady(g)
	g.relaySession = ""
	var o vsOutcome
	_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
	v := g.fromBrowser(vsAttachedEvent("about:blank"))
	o.take(v)
	asked := false
	for _, raw := range v.send {
		_, method, session := vsOutbound(raw)
		if method == "Target.getTargetInfo" && session == "RELAY" {
			asked = true
		}
	}
	o.take(g.fromBrowser(vsAttachReply()))
	if asked {
		t.Fatal("empty relaySession must not send getTargetInfo")
	}
	if vsPendingConfirm(g, vsSession) || vsDocConfirmCount(g) > 0 {
		t.Errorf("empty relaySession left a pending confirm; must fail closed immediately")
	}
	event, reply, _ := vsSecondAdmitted(o.clientPayloads, vsSession, vsAttachID)
	if event || reply {
		t.Errorf("empty relaySession still made the attach client-visible (event=%t reply=%t)", event, reply)
	}
}

func TestViewSourceAttachConfirmTargetDestroyedFailsClosed(t *testing.T) {
	g := vsGuard(t)
	_ = g.fromBrowser(vsRelay("Target.targetCreated", "about:blank"))
	vsWatchReady(g)
	_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
	_ = g.fromBrowser(vsAttachedEvent("about:blank"))
	_ = g.fromBrowser(vsAttachReply())
	if !vsPendingConfirm(g, vsSession) {
		t.Fatal("expected a pending confirm before destroy")
	}
	_ = g.fromBrowser(encode(map[string]any{"sessionId": "RELAY", "method": "Target.targetDestroyed", "params": map[string]any{"targetId": vsTarget}}))
	if vsPendingConfirm(g, vsSession) || vsDocConfirmCount(g) > 0 {
		t.Errorf("confirm left pending after the target was destroyed; must fail closed and prune")
	}
}

func TestViewSourceAttachConfirmClientGoneFailsClosed(t *testing.T) {
	g := vsGuard(t)
	_ = g.fromBrowser(vsRelay("Target.targetCreated", "about:blank"))
	vsWatchReady(g)
	_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
	_ = g.fromBrowser(vsAttachedEvent("about:blank"))
	_ = g.fromBrowser(vsAttachReply())
	if !vsPendingConfirm(g, vsSession) {
		t.Fatal("expected a pending confirm")
	}
	g.mu.Lock()
	g.forgetSessionLocked(vsSession)
	g.mu.Unlock()
	if vsPendingConfirm(g, vsSession) || vsDocConfirmCount(g) > 0 || vsDocFailedCount(g) > 0 {
		t.Errorf("confirm maps left residue after the client session was forgotten (confirms=%d failed=%d pending=%t)", vsDocConfirmCount(g), vsDocFailedCount(g), vsPendingConfirm(g, vsSession))
	}
}

func TestViewSourceAttachNavAfterConfirmStillCut(t *testing.T) {
	g := vsGuard(t)
	var o vsOutcome
	_ = g.fromBrowser(vsRelay("Target.targetCreated", "about:blank"))
	vsWatchReady(g)
	_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
	vsDrain(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), vsOrdinary)
	vsDrain(g, &o, g.fromBrowser(vsAttachReply()), vsOrdinary)
	if o.attachRefused {
		t.Fatalf("ordinary confirm must admit the session: %v", o.clientPayloads)
	}
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetInfoChanged", "view-source:https://vault.sliccy.ai/doc")), "view-source:https://vault.sliccy.ai/doc")
	vsRead(g, &o, "view-source:https://vault.sliccy.ai/doc")
	o.summarize()
	if o.readDelivered {
		t.Errorf("a navigation to a protected document after confirmation must still be cut before the first content command: %v", o.clientPayloads)
	}
	if !o.readRefused {
		t.Errorf("expected the first content command after a protected navigation to be refused")
	}
}

func vsErrorPageShapes() []struct {
	name string
	live string
} {
	return []struct {
		name string
		live string
	}{
		{"C8.protected-error-page", "https://seven.sliccy.ai/doc"},
		{"C8.protected-error-page.query", "https://seven.sliccy.ai/doc?q=1"},
		{"C8.protected-error-page.fragment", "https://seven.sliccy.ai/doc#frag"},
		{"C8.protected-document-restore", "https://seven.sliccy.ai/doc"},
		{"C8.protected-error-page-sticky", "https://seven.sliccy.ai/doc"},
		{"C8.protected-error-page-sticky.query", "https://seven.sliccy.ai/doc?q=1"},
		{"C8.protected-error-page-sticky.fragment", "https://seven.sliccy.ai/doc#frag"},
	}
}

func vsArmErrorPage(g *guard, live string, sticky bool) {
	ready := make(chan struct{})
	close(ready)
	nt := &navTarget{
		target:      vsTarget,
		ready:       ready,
		done:        true,
		documentURL: "chrome-error://chromewebdata/",
		errorURL:    live,
	}
	if sticky {
		nt.historyLocked = true
	}
	g.navTargets[vsTarget] = nt
	g.setErrorProofLocked(vsTarget, errorProofOrdinary)
}

func TestViewSourceAttachMutationProbe(t *testing.T) {
	if os.Getenv("SLICC_DOC_CONFIRM_MUTATION") == "" {
		t.Skip("overlay mutation only")
	}
	url := "view-source:https://vault.sliccy.ai/doc"
	o := vsRun(t, 3, url, vsScenario{watched: true})
	if o.attachRefused || !o.readDelivered {
		t.Fatalf("mutation: with confirmation stubbed the watched order-3 attach must still admit and deliver the read (refused=%t delivered=%t payloads=%v)", o.attachRefused, o.readDelivered, o.clientPayloads)
	}
}

func TestViewSourceAttachErrorPageReportingProtectedURLAdmitted(t *testing.T) {
	for _, shape := range vsErrorPageShapes() {
		shape := shape
		t.Run(shape.name, func(t *testing.T) {
			g := vsGuard(t)
			var o vsOutcome
			announced := "chrome-error://chromewebdata/"
			vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", announced)), shape.live)
			vsArmErrorPage(g, shape.live, strings.Contains(shape.name, "sticky") || strings.Contains(shape.name, "restore"))
			_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
			vsDrain(g, &o, g.fromBrowser(vsAttachedEvent(announced)), shape.live)
			vsDrain(g, &o, g.fromBrowser(vsAttachReply()), shape.live)
			o.summarize()
			if o.attachRefused {
				t.Fatalf("%s: error-page attach reporting %s must stay admitted (main attachRedacted path), got refused payloads=%v", shape.name, shape.live, o.clientPayloads)
			}
			if !o.attachReplied && !o.sawAttachEvent {
				t.Fatalf("%s: expected a client-visible attach: %v", shape.name, o.clientPayloads)
			}
			for _, p := range o.clientPayloads {
				if strings.Contains(p, "attachedToTarget") && strings.Contains(p, shape.live) {
					t.Errorf("%s: protected URL leaked in attach event: %s", shape.name, p)
				}
			}
		})
	}
}

func vsCountRelayGetTargetInfo(send [][]byte) (n int, ids []int64) {
	for _, raw := range send {
		id, method, session := vsOutbound(raw)
		if method == "Target.getTargetInfo" && session == "RELAY" {
			n++
			ids = append(ids, id)
		}
	}
	return n, ids
}

func TestViewSourceAttachOneConfirmGatesEventAndReply(t *testing.T) {
	for _, order := range []string{"event-then-reply", "reply-then-event"} {
		order := order
		t.Run(order, func(t *testing.T) {
			g := vsGuard(t)
			var o vsOutcome
			var asked int
			vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), vsOrdinary)
			vsWatchReady(g)
			_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
			hold := func(v browserVerdict) {
				o.take(v)
				n, ids := vsCountRelayGetTargetInfo(v.send)
				asked += n
				for _, id := range ids {
					more := g.fromBrowser(vsTargetInfoAnswer(id, vsOrdinary))
					o.take(more)
					extra, _ := vsCountRelayGetTargetInfo(more.send)
					asked += extra
					for _, p := range more.page {
						o.clientPayloads = append(o.clientPayloads, string(p))
					}
				}
				for _, p := range v.page {
					o.clientPayloads = append(o.clientPayloads, string(p))
				}
			}
			switch order {
			case "event-then-reply":
				hold(g.fromBrowser(vsAttachedEvent("about:blank")))
				hold(g.fromBrowser(vsAttachReply()))
			default:
				hold(g.fromBrowser(vsAttachReply()))
				hold(g.fromBrowser(vsAttachedEvent("about:blank")))
			}
			o.summarize()
			if asked != 1 {
				t.Fatalf("%s: expected exactly one relay Target.getTargetInfo, got %d payloads=%v", order, asked, o.clientPayloads)
			}
			if o.attachRefused || !o.attachReplied || !o.sawAttachEvent {
				t.Fatalf("%s: both event and reply must release after the single pass (refused=%t replied=%t event=%t payloads=%v)", order, o.attachRefused, o.attachReplied, o.sawAttachEvent, o.clientPayloads)
			}
		})
	}
}

func TestViewSourceAttachErrorPageWithoutProofPreArm(t *testing.T) {
	live := "https://seven.sliccy.ai/doc"
	g := vsGuard(t)
	var o vsOutcome
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), live)
	vsWatchReady(g)
	_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
	vsDrain(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), live)
	vsDrain(g, &o, g.fromBrowser(vsAttachReply()), live)
	o.summarize()
	if o.attachRefused {
		t.Fatalf("recovery attach without pre-armed proof must admit when getTargetInfo names the protected unreachable URL (main redaction path): %v", o.clientPayloads)
	}
	if !o.attachReplied && !o.sawAttachEvent {
		t.Fatalf("expected a client-visible attach: %v", o.clientPayloads)
	}
}

func TestViewSourceAttachConfirmEarlyExpireRearms(t *testing.T) {
	t.Setenv(HoldTimeoutEnv, "50ms")
	g := vsGuard(t)
	_ = g.fromBrowser(vsRelay("Target.targetCreated", "about:blank"))
	vsWatchReady(g)
	_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
	_ = g.fromBrowser(vsAttachedEvent("about:blank"))
	_ = g.fromBrowser(vsAttachReply())
	if !vsPendingConfirm(g, vsSession) {
		t.Fatal("expected a pending confirm")
	}
	g.mu.Lock()
	dc := g.docInflight[vsTarget]
	if dc == nil {
		g.mu.Unlock()
		t.Fatal("missing inflight confirm")
	}
	dc.deadline = time.Now().Add(80 * time.Millisecond)
	dc.start = time.Now()
	g.mu.Unlock()
	g.expireDocConfirm(vsTarget)
	if !vsPendingConfirm(g, vsSession) {
		t.Fatal("early expire must re-arm until the fixed deadline, not fail closed")
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !vsPendingConfirm(g, vsSession) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if vsPendingConfirm(g, vsSession) || vsDocConfirmCount(g) > 0 {
		t.Errorf("confirm must fail closed once the fixed deadline elapses")
	}
}

func TestViewSourceAttachDocFailedClearedOnDestroy(t *testing.T) {
	g := vsGuard(t)
	_ = g.fromBrowser(vsRelay("Target.targetCreated", "about:blank"))
	vsWatchReady(g)
	_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
	_ = g.fromBrowser(vsAttachedEvent("about:blank"))
	_ = g.fromBrowser(vsAttachReply())
	g.mu.Lock()
	dc := g.docInflight[vsTarget]
	if dc == nil {
		g.mu.Unlock()
		t.Fatal("expected pending confirm")
	}
	_ = g.failDocConfirmLocked(dc)
	if !g.docFailed[vsSession] {
		g.mu.Unlock()
		t.Fatal("expected docFailed after refuse")
	}
	g.mu.Unlock()
	_ = g.fromBrowser(encode(map[string]any{"sessionId": "RELAY", "method": "Target.targetDestroyed", "params": map[string]any{"targetId": vsTarget}}))
	g.mu.Lock()
	failed := g.docFailed[vsSession]
	hidden := g.hidden[vsSession]
	g.mu.Unlock()
	if failed || hidden {
		t.Errorf("docFailed/hidden must clear on target destroy when detach never arrives (failed=%t hidden=%t)", failed, hidden)
	}
}

const vsObserveSession = "S-OBSERVE"
const vsNavSession = "S-NAV"

func vsNavFrameNavigated(url, origin string) []byte {
	if origin == "" {
		origin = "://"
	}
	return encode(map[string]any{
		"sessionId": vsNavSession,
		"method":    "Page.frameNavigated",
		"params": map[string]any{
			"frame": map[string]any{"id": vsTarget, "url": url, "securityOrigin": origin},
		},
	})
}

func vsDrainNav(g *guard, o *vsOutcome, v browserVerdict, live string) {
	o.take(v)
	for _, raw := range v.send {
		id, method, session := vsOutbound(raw)
		switch {
		case method == "Target.getTargetInfo" && session == "RELAY":
			vsDrainNav(g, o, g.fromBrowser(vsTargetInfoAnswer(id, live)), live)
		case method == "Target.attachToTarget" && session == "RELAY":
			reply := encode(map[string]any{"id": id, "sessionId": "RELAY", "result": map[string]any{"sessionId": vsNavSession}})
			vsDrainNav(g, o, g.fromBrowser(reply), live)
		case method == "Target.detachFromTarget":
			vsDrainNav(g, o, g.fromBrowser(encode(map[string]any{"id": id, "sessionId": session, "result": map[string]any{}})), live)
		case method == "Fetch.enable" || method == "Network.enable" || method == "Network.setBypassServiceWorker" || method == "Runtime.enable" || method == "Page.enable" || method == "Page.getNavigationHistory":
			vsDrainNav(g, o, g.fromBrowser(encode(map[string]any{"id": id, "sessionId": session, "result": map[string]any{}})), live)
		}
	}
}

func TestDocConfirmObserveNeverChangesVerdict(t *testing.T) {
	g := vsGuard(t)
	var o vsOutcome
	vsDrainNav(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), "about:blank")
	v := g.fromBrowser(vsAttachedEvent("about:blank"))
	vsDrainNav(g, &o, v, "about:blank")
	vsDrainNav(g, &o, g.fromBrowser(vsNavFrameNavigated("about:blank", "://")), "about:blank")
	o.summarize()
	detachSent := false
	for _, raw := range v.send {
		_, method, _ := vsOutbound(raw)
		if method == "Target.detachFromTarget" {
			detachSent = true
		}
	}
	g.mu.Lock()
	client := g.sessions[vsSession] != nil
	observe := g.sessions[vsObserveSession] != nil
	failed := g.docFailed[vsSession]
	nSessions := len(g.sessions)
	logs := append([]string(nil), g.docObserveLog...)
	g.mu.Unlock()
	_ = nSessions
	t.Logf("observe-never-changes client=%t observeSession=%t sessions=%d failed=%t logs=%d detachSent=%t", client, observe, nSessions, failed, len(logs), detachSent)
	if !client {
		t.Fatal("client session must admit like main")
	}
	if observe {
		t.Fatal("observe must not create a proof session")
	}
	if failed || o.attachRefused {
		t.Fatal("observe must not refuse or latch the client attach")
	}
	if detachSent {
		t.Fatal("source-2 observe must not emit Target.detachFromTarget")
	}
	if len(logs) == 0 {
		t.Fatal("expected DIAG document-confirm would-admit|would-refuse log after main-frame commit")
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "DIAG document-confirm would-") {
		t.Fatalf("missing greppable observe log: %q", joined)
	}
	if strings.Contains(joined, "https://") || strings.Contains(joined, "vault.") || strings.Contains(joined, "seven.") {
		t.Fatalf("observe log must not carry URLs or hosts: %q", joined)
	}
}

func TestDocConfirmObserveFilterMustNotClaimNavguardSession(t *testing.T) {
	g := vsGuard(t)
	var o vsOutcome
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), "about:blank")
	v := g.fromBrowser(vsAttachedEvent("about:blank"))
	o.take(v)
	var attachIDs []int64
	for _, raw := range v.send {
		id, method, session := vsOutbound(raw)
		if method == "Target.attachToTarget" && session == "RELAY" {
			attachIDs = append(attachIDs, id)
		}
	}
	if len(attachIDs) == 0 {
		t.Fatal("expected touchLocked attachToTarget")
	}
	navSession := vsNavSession
	o.take(g.fromBrowser(encode(map[string]any{"id": attachIDs[0], "sessionId": "RELAY", "result": map[string]any{"sessionId": navSession}})))
	if len(attachIDs) > 1 {
		o.take(g.fromBrowser(encode(map[string]any{"id": attachIDs[1], "sessionId": "RELAY", "result": map[string]any{"sessionId": vsObserveSession}})))
	}
	att := map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          navSession,
			"waitingForDebugger": false,
			"targetInfo":         map[string]any{"targetId": vsTarget, "type": "page", "url": "about:blank", "browserContextId": "CTX"},
		},
	}
	o.take(g.fromBrowser(encode(att)))
	g.mu.Lock()
	claimed := g.docObserveSessions[navSession]
	navOK := g.navSessions[navSession] != nil
	filtered := g.docConfirmObserveFilterEventLocked(navSession, "")
	g.mu.Unlock()
	t.Logf("claimed=%q navSessions=%t filter=%t attaches=%d", claimed, navOK, filtered, len(attachIDs))
	if !navOK {
		t.Fatal("navguard session must remain registered")
	}
	if claimed != "" || filtered {
		t.Fatalf("observe filter must not claim navguard session (claimed=%q filter=%t)", claimed, filtered)
	}
}

func TestDocConfirmObserveMustNotEmitProofDetach(t *testing.T) {
	g := vsGuard(t)
	var o vsOutcome
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), "about:blank")
	v := g.fromBrowser(vsAttachedEvent("about:blank"))
	o.take(v)
	attachN := 0
	var walk func(browserVerdict)
	walk = func(ver browserVerdict) {
		for _, raw := range ver.send {
			id, method, session := vsOutbound(raw)
			if method == "Target.detachFromTarget" {
				t.Fatalf("proof must not emit Target.detachFromTarget session=%s", session)
			}
			if method == "Page.getFrameTree" && session == vsObserveSession {
				t.Fatal("source-2 observe must not Page.getFrameTree on a proof session")
			}
			switch {
			case method == "Target.attachToTarget" && session == "RELAY":
				attachN++
				sid := vsNavSession
				if attachN > 1 {
					sid = vsObserveSession
				}
				walk(g.fromBrowser(encode(map[string]any{"id": id, "sessionId": "RELAY", "result": map[string]any{"sessionId": sid}})))
				if sid == vsObserveSession {
					walk(g.fromBrowser(encode(map[string]any{
						"method": "Target.attachedToTarget",
						"params": map[string]any{
							"sessionId":          sid,
							"waitingForDebugger": false,
							"targetInfo":         map[string]any{"targetId": vsTarget, "type": "page", "url": "about:blank", "browserContextId": "CTX"},
						},
					})))
				}
			case method == "Page.getFrameTree" && session != "":
				origin := "://"
				walk(g.fromBrowser(encode(map[string]any{
					"id": id, "sessionId": session,
					"result": map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": vsTarget, "url": "about:blank", "securityOrigin": origin}}},
				})))
			case method == "Fetch.enable" || method == "Network.enable" || method == "Network.setBypassServiceWorker" || method == "Runtime.enable" || method == "Page.enable" || method == "Page.getNavigationHistory":
				walk(g.fromBrowser(encode(map[string]any{"id": id, "sessionId": session, "result": map[string]any{}})))
			}
		}
	}
	walk(v)
}

func TestDocConfirmObserveTabAttachPopupHoldResume(t *testing.T) {
	g := vsGuard(t)
	g.targets["OPENER"] = &target{id: "OPENER", typ: "page", url: "https://ordinary.test/"}
	g.sessions["S-OPENER"] = &session{id: "S-OPENER", target: "OPENER", typ: "page"}
	_ = g.fromBrowser(encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-TAB-POP",
			"waitingForDebugger": true,
			"targetInfo":         map[string]any{"targetId": "TAB-POP", "type": "tab", "url": "", "openerId": "OPENER"},
		},
	}))
	if g.sessions["S-TAB-POP"] == nil || g.held["S-TAB-POP"] != nil {
		t.Fatal("popup tab must be announced so the client can arm tab-level setAutoAttach")
	}
	v := g.fromBrowser(encode(map[string]any{
		"sessionId": "S-TAB-POP",
		"method":    "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-PAGE-POP",
			"waitingForDebugger": true,
			"targetInfo":         map[string]any{"targetId": "PAGE-POP", "type": "page", "url": "", "openerId": "OPENER"},
		},
	}))
	var o vsOutcome
	o.take(v)
	if strings.Contains(strings.Join(o.clientPayloads, ""), "attachedToTarget") {
		t.Fatalf("empty-URL page child must stay held: %v", o.clientPayloads)
	}
	h := g.held["S-PAGE-POP"]
	if h == nil {
		t.Fatal("empty-URL popup page child was not held")
	}
	if !h.resumed {
		t.Fatal("held popup page child must be guard-resumed at hold time (#880)")
	}
	resume := 0
	for _, raw := range v.send {
		_, method, session := vsOutbound(raw)
		if method == "Runtime.runIfWaitingForDebugger" && session == "S-PAGE-POP" {
			resume++
		}
	}
	if resume != 1 {
		t.Fatalf("hold must resume page child exactly once; got %d", resume)
	}
	rel := g.fromBrowser(encode(map[string]any{
		"sessionId": "RELAY",
		"method":    "Target.targetInfoChanged",
		"params":    map[string]any{"targetInfo": map[string]any{"targetId": "PAGE-POP", "type": "page", "url": "about:blank", "openerId": "OPENER"}},
	}))
	o.take(rel)
	g.mu.Lock()
	path := g.docObservePath["PAGE-POP"]
	g.mu.Unlock()
	if path != "F" {
		t.Fatalf("held release must note observe path F; got %q", path)
	}
}

func TestDocConfirmObserveFilterMutantLeaks(t *testing.T) {
	if os.Getenv("SLICC_DOC_CONFIRM_OBSERVE_FILTER_MUTANT") == "" {
		t.Skip("observe filter mutant only")
	}
	g := vsGuard(t)
	var o vsOutcome
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), "about:blank")
	v := g.fromBrowser(vsAttachedEvent("about:blank"))
	o.take(v)
	var attachIDs []int64
	for _, raw := range v.send {
		id, method, session := vsOutbound(raw)
		if method == "Target.attachToTarget" && session == "RELAY" {
			attachIDs = append(attachIDs, id)
		}
	}
	if len(attachIDs) < 2 {
		t.Fatal("sessionful mutant must dual-attach")
	}
	o.take(g.fromBrowser(encode(map[string]any{"id": attachIDs[0], "sessionId": "RELAY", "result": map[string]any{"sessionId": vsNavSession}})))
	o.take(g.fromBrowser(encode(map[string]any{"id": attachIDs[1], "sessionId": "RELAY", "result": map[string]any{"sessionId": vsObserveSession}})))
	o.take(g.fromBrowser(encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          vsObserveSession,
			"waitingForDebugger": false,
			"targetInfo":         map[string]any{"targetId": vsTarget, "type": "page", "url": "about:blank", "browserContextId": "CTX"},
		},
	})))
	g.mu.Lock()
	observe := g.sessions[vsObserveSession] != nil
	nSessions := len(g.sessions)
	g.mu.Unlock()
	t.Logf("filter-mutant observeSession=%t sessions=%d", observe, nSessions)
	if !observe && nSessions < 2 {
		t.Fatal("filter mutant must leak the observe session into the sessions map")
	}
}
