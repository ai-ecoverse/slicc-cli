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

const vsProofSession = "S-DOC-PROOF"

func vsFrameTreeAnswer(id int64, session, url string) []byte {
	return vsFrameTreeAnswerOrigin(id, session, url, "")
}

func vsFrameTreeAnswerOrigin(id int64, session, url, origin string) []byte {
	if origin == "" {
		lower := strings.ToLower(url)
		switch {
		case url == "" || lower == "about:blank" || strings.HasPrefix(lower, "about:blank"):
			origin = "://"
		case strings.HasPrefix(lower, "chrome-error:"):
			origin = "://"
		default:
			origin = url
		}
	}
	frame := map[string]any{"id": vsTarget, "url": url, "securityOrigin": origin}
	return encode(map[string]any{
		"id":        id,
		"sessionId": session,
		"result": map[string]any{
			"frameTree": map[string]any{
				"frame": frame,
			},
		},
	})
}

func vsProofAttachAnswer(id int64) []byte {
	return encode(map[string]any{"id": id, "sessionId": "RELAY", "result": map[string]any{"sessionId": vsProofSession}})
}

func vsDrain(g *guard, o *vsOutcome, v browserVerdict, live string) {
	o.take(v)
	for _, raw := range v.send {
		id, method, session := vsOutbound(raw)
		switch {
		case method == "Target.getTargetInfo" && session == "RELAY":
			vsDrain(g, o, g.fromBrowser(vsTargetInfoAnswer(id, live)), live)
		case method == "Target.attachToTarget" && session == "RELAY":
			vsDrain(g, o, g.fromBrowser(vsProofAttachAnswer(id)), live)
		case method == "Page.getFrameTree" && session == vsProofSession:
			vsDrain(g, o, g.fromBrowser(vsFrameTreeAnswer(id, session, live)), live)
		case method == "Target.detachFromTarget":
			vsDrain(g, o, g.fromBrowser(encode(map[string]any{"id": id, "sessionId": session, "result": map[string]any{}})), live)
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

func vsTitleRead(g *guard, o *vsOutcome, live string) (refused bool) {
	const titleID = int64(52)
	pv := g.fromCommand(&command{id: titleID, method: "Runtime.evaluate", session: vsSession, params: map[string]any{"expression": "document.title"}}, titleID, true)
	if pv.reply != nil {
		o.clientPayloads = append(o.clientPayloads, string(pv.reply))
		return true
	}
	if pv.wait != nil {
		return true
	}
	if pv.forward != nil {
		vsDrain(g, o, g.fromBrowser(encode(map[string]any{
			"id": titleID, "sessionId": vsSession,
			"result": map[string]any{"result": map[string]any{"type": "string", "value": "vault-title"}},
		})), live)
	}
	return false
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
	return vsDrainAnswerOrigin(g, o, v, func(id int64) (string, string) {
		return answer(id), ""
	})
}

func vsDrainAnswerOrigin(g *guard, o *vsOutcome, v browserVerdict, answer func(id int64) (url, origin string)) (asked []int64) {
	o.take(v)
	for _, raw := range v.send {
		id, method, session := vsOutbound(raw)
		switch {
		case method == "Target.getTargetInfo" && session == "RELAY":
			asked = append(asked, id)
			url, _ := answer(id)
			if url == "" {
				continue
			}
			more := vsDrainAnswerOrigin(g, o, g.fromBrowser(vsTargetInfoAnswer(id, url)), answer)
			asked = append(asked, more...)
		case method == "Target.attachToTarget" && session == "RELAY":
			asked = append(asked, id)
			more := vsDrainAnswerOrigin(g, o, g.fromBrowser(vsProofAttachAnswer(id)), answer)
			asked = append(asked, more...)
		case method == "Page.getFrameTree" && session == vsProofSession:
			asked = append(asked, id)
			url, origin := answer(id)
			more := vsDrainAnswerOrigin(g, o, g.fromBrowser(vsFrameTreeAnswerOrigin(id, session, url, origin)), answer)
			asked = append(asked, more...)
		case method == "Target.detachFromTarget":
			more := vsDrainAnswerOrigin(g, o, g.fromBrowser(encode(map[string]any{"id": id, "sessionId": session, "result": map[string]any{}})), answer)
			asked = append(asked, more...)
		}
	}
	return asked
}

func vsCountRelayGetTargetInfo(send [][]byte) (n int, ids []int64) {
	for _, raw := range send {
		id, method, session := vsOutbound(raw)
		if method == "Target.getTargetInfo" && session == "RELAY" {
			n++
			ids = append(ids, id)
		}
		if method == "Target.attachToTarget" && session == "RELAY" {
			n++
			ids = append(ids, id)
		}
		if method == "Page.getFrameTree" && session == vsProofSession {
			n++
			ids = append(ids, id)
		}
	}
	return n, ids
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
	asked := false
	for _, raw := range v.send {
		_, method, session := vsOutbound(raw)
		if method == "Target.attachToTarget" && session == "RELAY" {
			asked = true
		}
		if method == "Target.getTargetInfo" && session == "RELAY" {
			asked = true
		}
	}
	if !asked {
		t.Fatal("expected a document proof ask (relay attach or getTargetInfo)")
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
		t.Errorf("confirm left pending when the proof read never arrives; must fail closed on the hold deadline")
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

func TestViewSourceAttachOneConfirmGatesEventAndReply(t *testing.T) {
	for _, order := range []string{"event-then-reply", "reply-then-event"} {
		order := order
		t.Run(order, func(t *testing.T) {
			g := vsGuard(t)
			var o vsOutcome
			vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), vsOrdinary)
			vsWatchReady(g)
			_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
			var asked []int64
			switch order {
			case "event-then-reply":
				asked = vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), func(int64) string { return vsOrdinary })
				asked = append(asked, vsDrainAnswer(g, &o, g.fromBrowser(vsAttachReply()), func(int64) string { return vsOrdinary })...)
			default:
				asked = vsDrainAnswer(g, &o, g.fromBrowser(vsAttachReply()), func(int64) string { return vsOrdinary })
				asked = append(asked, vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), func(int64) string { return vsOrdinary })...)
			}
			o.summarize()
			if len(asked) == 0 {
				t.Fatalf("%s: expected a document proof read", order)
			}
			if o.attachRefused || !o.attachReplied || !o.sawAttachEvent {
				t.Fatalf("%s: both event and reply must release after the single pass (refused=%t replied=%t event=%t payloads=%v asked=%d)", order, o.attachRefused, o.attachReplied, o.sawAttachEvent, o.clientPayloads, len(asked))
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
	if !o.attachRefused && (o.attachReplied || o.sawAttachEvent) {
		t.Fatalf("without errorProofOrdinary provenance, a proof read naming a protected URL must refuse: %v", o.clientPayloads)
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
	_ = g.refuseDocConfirmLocked(dc, true)
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

const (
	vsHTTPVault     = "view-source:http://vault.sliccy.ai:36309/doc?n=c8-close-protected-view-source"
	vsHTTPVaultBare = "http://vault.sliccy.ai:36309/doc?n=c8-close-protected-view-source"
	vsHTTPSVault    = "view-source:https://vault.sliccy.ai/doc"
)

func vsRunConfirmAnswer(t *testing.T, announced, confirmURL string, watched bool) (o vsOutcome, confirmAsked bool) {
	t.Helper()
	g := vsGuard(t)
	proofURL := confirmURL
	if confirmURL == "" || confirmURL == "about:blank" || confirmURL == vsOrdinary {
		proofURL = vsHTTPVault
	}
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", announced)), proofURL)
	if watched {
		vsWatchReady(g)
	}
	_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
	asked := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent(announced)), func(int64) string { return proofURL })
	asked = append(asked, vsDrainAnswer(g, &o, g.fromBrowser(vsAttachReply()), func(int64) string { return proofURL })...)
	confirmAsked = len(asked) > 0
	vsRead(g, &o, proofURL)
	o.summarize()
	return o, confirmAsked
}

func TestViewSourceAttachHypothesisARelayLagAdmitsBlank(t *testing.T) {
	for _, watched := range []bool{false, true} {
		for _, stale := range []string{"about:blank", vsOrdinary, ""} {
			watched, stale := watched, stale
			name := "unwatched/"
			if watched {
				name = "watched/"
			}
			if stale == "" {
				name += "empty"
			} else {
				name += stale
			}
			t.Run(name, func(t *testing.T) {
				o, asked := vsRunConfirmAnswer(t, "about:blank", stale, watched)
				t.Logf("hyp-a watched=%t confirmURL=%q asked=%t attachRefused=%t attachReplied=%t sawAttachEvent=%t readDelivered=%t", watched, stale, asked, o.attachRefused, o.attachReplied, o.sawAttachEvent, o.readDelivered)
				if !asked {
					t.Fatalf("hyp-a: expected a document proof read (relay attach + Page.getFrameTree)")
				}
				if !o.attachRefused && (o.attachReplied || o.sawAttachEvent) {
					t.Errorf("hyp-a: frame-tree proof must refuse when the live document is protected view-source (stale getTargetInfo would have been %q); admitted refused=%t replied=%t event=%t read=%t", stale, o.attachRefused, o.attachReplied, o.sawAttachEvent, o.readDelivered)
				}
			})
		}
	}
}

func TestViewSourceAttachHypothesisBHTTPViewSourceForms(t *testing.T) {
	forms := []string{
		vsHTTPVault,
		"view-source:http://vault.sliccy.ai/doc",
		"view-source:http://seven.sliccy.ai/",
		vsHTTPSVault,
	}
	for _, form := range forms {
		form := form
		t.Run(form, func(t *testing.T) {
			o, err := protect.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			loaded := o.Loaded(form)
			live := (&guard{o: o}).docConfirmLiveProtectedDocumentLocked(form)
			t.Logf("hyp-b Loaded=%t liveProtectedWrapper=%t", loaded, live)
			if !loaded {
				t.Errorf("hyp-b: Loaded(%q)=false; http view-source of a protected host must count as loaded", form)
			}
			if !live {
				t.Errorf("hyp-b: docConfirmLiveProtectedDocumentLocked(%q)=false; unconfirmed carve-out would miss this form", form)
			}
			out, asked := vsRunConfirmAnswer(t, "about:blank", form, true)
			t.Logf("hyp-b asked=%t attachRefused=%t attachReplied=%t sawAttachEvent=%t readDelivered=%t", asked, out.attachRefused, out.attachReplied, out.sawAttachEvent, out.readDelivered)
			if !asked {
				t.Fatalf("hyp-b: expected confirmation getTargetInfo")
			}
			if !out.attachRefused && (out.attachReplied || out.sawAttachEvent) {
				t.Errorf("hyp-b RED: confirm answered the http/https view-source form and still admitted (refused=%t replied=%t event=%t)", out.attachRefused, out.attachReplied, out.sawAttachEvent)
			}
			if out.readDelivered {
				t.Errorf("hyp-b: read delivered vault content through an admitted session")
			}
		})
	}
	t.Run("bare-http-vault-not-wrapper", func(t *testing.T) {
		o, err := protect.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		if !o.Loaded(vsHTTPVaultBare) {
			t.Fatalf("bare http vault must be Loaded")
		}
		if (&guard{o: o}).docConfirmLiveProtectedDocumentLocked(vsHTTPVaultBare) {
			t.Fatalf("bare http vault must NOT match the live-wrapper carve-out (prefix view-source/blob/filesystem only)")
		}
	})
}

func TestViewSourceAttachHypothesisCFirstSightRace(t *testing.T) {
	t.Run("attach-before-targetInfoChanged-confirm-fresh", func(t *testing.T) {
		o, asked := vsRunConfirmAnswer(t, "about:blank", vsHTTPVault, true)
		t.Logf("hyp-c fresh asked=%t attachRefused=%t attachReplied=%t sawAttachEvent=%t readDelivered=%t", asked, o.attachRefused, o.attachReplied, o.sawAttachEvent, o.readDelivered)
		if !asked {
			t.Fatalf("hyp-c: expected confirmation")
		}
		if !o.attachRefused && (o.attachReplied || o.sawAttachEvent) {
			t.Errorf("hyp-c RED: first sight about:blank, no targetInfoChanged before attach, but confirm returned the live view-source http form and still admitted")
		}
	})
	t.Run("attach-before-targetInfoChanged-confirm-stale-blank", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), vsHTTPVault)
		vsWatchReady(g)
		_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
		asked := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), func(int64) string { return "about:blank" })
		asked = append(asked, vsDrainAnswer(g, &o, g.fromBrowser(vsAttachReply()), func(int64) string { return "about:blank" })...)
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetInfoChanged", vsHTTPVault)), vsHTTPVault)
		vsRead(g, &o, vsHTTPVault)
		o.summarize()
		t.Logf("hyp-c stale asked=%d attachRefused=%t attachReplied=%t sawAttachEvent=%t readDelivered=%t", len(asked), o.attachRefused, o.attachReplied, o.sawAttachEvent, o.readDelivered)
		if len(asked) == 0 {
			t.Fatalf("hyp-c: expected confirmation")
		}
		vsExpectAdmittedAttach(t, "hyp-c-stale-blank-ordinary-opaque", o)
	})
	t.Run("targetInfoChanged-before-attach", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), vsHTTPVault)
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetInfoChanged", vsHTTPVault)), vsHTTPVault)
		vsWatchReady(g)
		pv := g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
		if pv.reply != nil {
			o.clientPayloads = append(o.clientPayloads, string(pv.reply))
			o.summarize()
			t.Logf("hyp-c prelearned immediate reply attachRefused=%t", o.attachRefused)
			if !o.attachRefused {
				t.Errorf("hyp-c: target already showing protected view-source before attach must refuse immediately")
			}
			return
		}
		asked := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent(vsHTTPVault)), func(int64) string { return vsHTTPVault })
		asked = append(asked, vsDrainAnswer(g, &o, g.fromBrowser(vsAttachReply()), func(int64) string { return vsHTTPVault })...)
		o.summarize()
		t.Logf("hyp-c prelearned asked=%d attachRefused=%t attachReplied=%t sawAttachEvent=%t", len(asked), o.attachRefused, o.attachReplied, o.sawAttachEvent)
		if !o.attachRefused && (o.attachReplied || o.sawAttachEvent) {
			t.Errorf("hyp-c RED: pre-learned view-source http still admitted through confirm")
		}
	})
}

func vsAttachedEventWait(url string, waiting bool) []byte {
	return encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          vsSession,
			"waitingForDebugger": waiting,
			"targetInfo": map[string]any{
				"targetId":         vsTarget,
				"type":             "page",
				"url":              url,
				"browserContextId": "CTX",
				"attached":         true,
				"openerId":         "",
				"canAccessOpener":  false,
			},
		},
	})
}

func vsSessionPresent(g *guard) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sessions[vsSession] != nil
}

func vsConfirmAsked(send [][]byte) bool {
	for _, raw := range send {
		_, method, session := vsOutbound(raw)
		if method == "Target.getTargetInfo" && session == "RELAY" {
			return true
		}
		if method == "Page.getFrameTree" && session == vsProofSession {
			return true
		}
	}
	return false
}

func vsProofAttachAsked(send [][]byte) bool {
	for _, raw := range send {
		var m struct {
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
			Params    struct {
				TargetID string `json:"targetId"`
			} `json:"params"`
		}
		_ = json.Unmarshal(raw, &m)
		if m.Method == "Target.attachToTarget" && m.SessionID == "RELAY" && m.Params.TargetID == vsTarget {
			return true
		}
	}
	return false
}

func vsClientVisible(o *vsOutcome) bool {
	return o.attachReplied || o.sawAttachEvent || o.readDelivered
}

func vsExpectAdmittedAttach(t *testing.T, name string, o vsOutcome) {
	t.Helper()
	if o.attachRefused {
		t.Fatalf("%s: expected admitted attach, got refuse payloads=%v", name, o.clientPayloads)
	}
	if !o.attachReplied && !o.sawAttachEvent {
		t.Fatalf("%s: expected client-visible attach (reply or event), got %v", name, o.clientPayloads)
	}
}

func vsCreateThroughGuard(t *testing.T, g *guard, o *vsOutcome, url string) {
	t.Helper()
	const createID = int64(41)
	pv := g.fromCommand(&command{id: createID, method: "Target.createTarget", params: map[string]any{"url": url}}, createID, true)
	if pv.reply != nil {
		t.Fatalf("Target.createTarget refused: %s", pv.reply)
	}
	vsDrain(g, o, g.fromBrowser(vsRelay("Target.targetCreated", url)), url)
	vsDrain(g, o, g.fromBrowser(encode(map[string]any{"id": createID, "result": map[string]any{"targetId": vsTarget}})), url)
	g.mu.Lock()
	touched := g.touchedAt[vsTarget] > 0
	nav := g.navTargets[vsTarget] != nil
	g.mu.Unlock()
	if !touched && !nav {
		t.Fatal("createTarget through the guard must leave touchedAt or navTargets receipt")
	}
}

func TestViewSourceAttachPathDAutoAttachNoConfirm(t *testing.T) {
	t.Run("foreign-blank-announce-needs-proof", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), vsHTTPVault)
		asked := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), func(int64) string { return vsHTTPVault })
		o.summarize()
		t.Logf("path-D blank announce asked=%d session=%t sawEvent=%t", len(asked), vsSessionPresent(g), o.sawAttachEvent)
		if len(asked) == 0 {
			t.Fatalf("path-D: foreign blank auto-attach must enter document proof")
		}
		if vsSessionPresent(g) || vsClientVisible(&o) {
			t.Errorf("path-D: foreign blank announce with protected live document must not leave a client page session (session=%t event=%t)", vsSessionPresent(g), o.sawAttachEvent)
		}
	})
	t.Run("foreign-ordinary-announce-live-control", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", vsOrdinary)), vsOrdinary)
		v := g.fromBrowser(vsAttachedEvent(vsOrdinary))
		g.mu.Lock()
		needs := g.needsDocConfirmLocked(vsTarget, vsSession, "page", vsOrdinary)
		inflight := len(g.docInflight)
		g.mu.Unlock()
		asked := vsDrainAnswer(g, &o, v, func(int64) string { return vsOrdinary })
		o.summarize()
		t.Logf("path-D ordinary announce needs=%t inflight=%d drainAsked=%d session=%t event=%t foreignOrdinaryProof=%t", needs, inflight, len(asked), vsSessionPresent(g), o.sawAttachEvent, docConfirmForeignOrdinaryProofEnabled())
		if docConfirmForeignOrdinaryProofEnabled() {
			if !needs && inflight == 0 && len(asked) == 0 {
				t.Fatalf("path-D: foreignOrdinaryProof=true must run document proof for live ordinary http(s) foreign announce")
			}
		} else if needs || inflight != 0 {
			t.Fatalf("path-D: stock foreignOrdinaryProof=false must skip document proof for live ordinary http(s) foreign announce")
		}
		if !vsSessionPresent(g) && !o.sawAttachEvent {
			t.Errorf("path-D ordinary control: live ordinary announce must admit (session=%t event=%t)", vsSessionPresent(g), o.sawAttachEvent)
		}
	})
	t.Run("protected-announce-hidden", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", vsHTTPVault)), vsHTTPVault)
		v := g.fromBrowser(vsAttachedEvent(vsHTTPVault))
		asked := vsConfirmAsked(v.send)
		o.take(v)
		for _, p := range v.page {
			o.clientPayloads = append(o.clientPayloads, string(p))
		}
		o.summarize()
		t.Logf("path-D protected announce asked=%t session=%t event=%t", asked, vsSessionPresent(g), o.sawAttachEvent)
		if vsSessionPresent(g) || o.sawAttachEvent {
			t.Errorf("path-D: protected announce URL must hit attachedShouldHideLocked (no client session); session=%t event=%t asked=%t", vsSessionPresent(g), o.sawAttachEvent, asked)
		}
	})
	t.Run("waiting-debugger-race-after-blank-announce", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), vsHTTPVault)
		asked := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEventWait("about:blank", true)), func(int64) string { return vsHTTPVault })
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetInfoChanged", vsHTTPVault)), vsHTTPVault)
		vsRead(g, &o, vsHTTPVault)
		o.summarize()
		t.Logf("path-D wait-race asked=%d session=%t readDelivered=%t", len(asked), vsSessionPresent(g), o.readDelivered)
		if len(asked) == 0 {
			t.Fatalf("path-D wait-race: blank announce must enter document proof")
		}
		if vsSessionPresent(g) || o.sawAttachEvent || o.readDelivered {
			t.Errorf("path-D: waitingForDebugger blank announce must not admit a client session before proof (session=%t event=%t read=%t)", vsSessionPresent(g), o.sawAttachEvent, o.readDelivered)
		}
	})
}

func TestViewSourceAttachPathKEventBeforeExplicit(t *testing.T) {
	g := vsGuard(t)
	var o vsOutcome
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), vsHTTPVault)
	askedEvent := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), func(int64) string { return vsHTTPVault })
	sessionAfterEvent := vsSessionPresent(g)
	_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
	more := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachReply()), func(int64) string { return vsHTTPVault })
	o.summarize()
	t.Logf("path-K eventAsked=%d sessionAfterEvent=%t replyAsked=%d session=%t", len(askedEvent), sessionAfterEvent, len(more), vsSessionPresent(g))
	if len(askedEvent) == 0 {
		t.Fatalf("path-K: blank auto-attach event before explicit must enter document proof")
	}
	if sessionAfterEvent || o.sawAttachEvent || vsSessionPresent(g) {
		t.Errorf("path-K: blank event before explicit must not leave a client page session (sessionAfterEvent=%t event=%t session=%t)", sessionAfterEvent, o.sawAttachEvent, vsSessionPresent(g))
	}
}

func TestViewSourceAttachPathCUnknownTarget(t *testing.T) {
	g := vsGuard(t)
	var o vsOutcome
	_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
	asked := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), func(int64) string { return vsHTTPVault })
	asked = append(asked, vsDrainAnswer(g, &o, g.fromBrowser(vsAttachReply()), func(int64) string { return vsHTTPVault })...)
	vsRead(g, &o, vsHTTPVault)
	o.summarize()
	t.Logf("path-C asked=%d session=%t refused=%t replied=%t event=%t readDelivered=%t", len(asked), vsSessionPresent(g), o.attachRefused, o.attachReplied, o.sawAttachEvent, o.readDelivered)
	if !o.attachRefused && (vsClientVisible(&o) || vsSessionPresent(g) || o.readDelivered) {
		t.Errorf("path-C RED: attach to a target not yet in g.targets yielded a client-visible page session or content read (session=%t refused=%t replied=%t event=%t read=%t)", vsSessionPresent(g), o.attachRefused, o.attachReplied, o.sawAttachEvent, o.readDelivered)
	}
}

func TestViewSourceAttachPathFEmptyHoldSettle(t *testing.T) {
	t.Run("hold-on-blank-empty", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "")), vsHTTPVault)
		v := g.fromBrowser(vsAttachedEvent(""))
		o.take(v)
		g.mu.Lock()
		held := g.held[vsSession] != nil
		g.mu.Unlock()
		t.Logf("path-F hold-on-empty held=%t session=%t", held, vsSessionPresent(g))
		if vsSessionPresent(g) {
			t.Fatalf("path-F: empty-URL auto-attach must hold (#850), not create a session yet")
		}
		if !held {
			t.Fatalf("path-F: expected held session on empty announce")
		}
	})
	t.Run("stale-ordinary-settle-live-wrapper", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "")), vsHTTPVault)
		_ = g.fromBrowser(vsAttachedEvent(""))
		if vsSessionPresent(g) {
			t.Fatalf("path-F: empty-URL auto-attach must hold (#850), not create a session yet")
		}
		asked := vsDrainAnswer(g, &o, g.fromBrowser(vsRelay("Target.targetInfoChanged", vsOrdinary)), func(int64) string { return vsHTTPVault })
		o.summarize()
		t.Logf("path-F stale-settle asked=%d session=%t event=%t", len(asked), vsSessionPresent(g), o.sawAttachEvent)
		if len(asked) == 0 {
			t.Fatalf("path-F: settle must re-confirm with a fresh proof read")
		}
		if vsSessionPresent(g) || o.sawAttachEvent {
			t.Errorf("path-F: settle proof of protected live document must not admit (session=%t event=%t)", vsSessionPresent(g), o.sawAttachEvent)
		}
	})
	t.Run("protected-settle-hidden", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "")), vsHTTPVault)
		_ = g.fromBrowser(vsAttachedEvent(""))
		rel := g.fromBrowser(vsRelay("Target.targetInfoChanged", vsHTTPVault))
		o.take(rel)
		for _, p := range rel.page {
			o.clientPayloads = append(o.clientPayloads, string(p))
		}
		o.summarize()
		t.Logf("path-F protected-settle session=%t event=%t", vsSessionPresent(g), o.sawAttachEvent)
		if vsSessionPresent(g) || o.sawAttachEvent {
			t.Errorf("path-F: protected settle must hide (no client session); session=%t event=%t", vsSessionPresent(g), o.sawAttachEvent)
		}
	})
	t.Run("ordinary-settle-control", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "")), vsOrdinary)
		_ = g.fromBrowser(vsAttachedEvent(""))
		asked := vsDrainAnswer(g, &o, g.fromBrowser(vsRelay("Target.targetInfoChanged", vsOrdinary)), func(int64) string { return vsOrdinary })
		o.summarize()
		t.Logf("path-F ordinary-settle asked=%d session=%t event=%t", len(asked), vsSessionPresent(g), o.sawAttachEvent)
		if len(asked) == 0 {
			t.Fatalf("path-F ordinary: settle must re-confirm then admit")
		}
		if !vsSessionPresent(g) && !o.sawAttachEvent {
			t.Errorf("path-F ordinary control: proof of ordinary settle must admit (session=%t event=%t)", vsSessionPresent(g), o.sawAttachEvent)
		}
	})
	t.Run("settle-proof-read-fails-refuse-without-latch", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "")), vsOrdinary)
		_ = g.fromBrowser(vsAttachedEvent(""))
		asked := vsDrainAnswer(g, &o, g.fromBrowser(vsRelay("Target.targetInfoChanged", vsOrdinary)), func(int64) string { return "" })
		o.summarize()
		t.Logf("path-F proof-fail asked=%d session=%t event=%t", len(asked), vsSessionPresent(g), o.sawAttachEvent)
		if vsSessionPresent(g) || o.sawAttachEvent {
			t.Errorf("path-F: failed settle proof must refuse without latching a client session (session=%t event=%t)", vsSessionPresent(g), o.sawAttachEvent)
		}
		g.mu.Lock()
		failed := g.docFailed[vsSession]
		g.mu.Unlock()
		if failed {
			t.Errorf("path-F: proof-read failure must refuse without latching docFailed")
		}
	})
}

func TestViewSourceAttachPathBEChromeErrorSkipWithoutProvenance(t *testing.T) {
	t.Run("chrome-error-alone-skips-confirm", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		announced := "chrome-error://chromewebdata/"
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", announced)), announced)
		vsWatchReady(g)
		g.mu.Lock()
		proof := g.errorProofOfLocked(vsTarget)
		opened := g.opened[vsTarget]
		g.mu.Unlock()
		if proof == errorProofOrdinary || opened {
			t.Fatalf("path-BE: must not carry own-tab provenance (errorProofOrdinary / opened); got proof=%v opened=%t", proof, opened)
		}
		pv := g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
		if pv.reply != nil {
			o.clientPayloads = append(o.clientPayloads, string(pv.reply))
			o.summarize()
			t.Fatalf("path-BE: attach command refused before reply path (payloads=%v)", o.clientPayloads)
		}
		asked := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachReply()), func(int64) string { return announced })
		asked = append(asked, vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent(announced)), func(int64) string { return announced })...)
		o.summarize()
		t.Logf("path-BE chrome-error-alone reply-before-event asked=%d session=%t refused=%t replied=%t event=%t evidence=none(foreign)", len(asked), vsSessionPresent(g), o.attachRefused, o.attachReplied, o.sawAttachEvent)
		if len(asked) == 0 && !o.attachRefused && (vsClientVisible(&o) || vsSessionPresent(g)) {
			t.Errorf("path-BE: chrome-error URL alone must not skip proof without errorProofOrdinary; session admitted")
		}
		if !o.attachRefused && (vsClientVisible(&o) || vsSessionPresent(g)) {
			t.Errorf("path-BE: attach without own-tab provenance must not admit (proof=%v asked=%d)", proof, len(asked))
		}
	})
	t.Run("blankness-alone-not-provenance", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), vsHTTPVault)
		g.mu.Lock()
		proof := g.errorProofOfLocked(vsTarget)
		touched := g.touchedAt[vsTarget] > 0
		opened := g.opened[vsTarget]
		g.mu.Unlock()
		if proof != errorProofNone || touched || opened {
			t.Fatalf("blankness mutant: want foreign (no errorProof/touchedAt/opened), got proof=%v touched=%t opened=%t", proof, touched, opened)
		}
		_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
		asked := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), func(int64) string { return "about:blank" })
		asked = append(asked, vsDrainAnswer(g, &o, g.fromBrowser(vsAttachReply()), func(int64) string { return "about:blank" })...)
		o.summarize()
		t.Logf("path-BE blankness-alone asked=%d refused=%t replied=%t event=%t", len(asked), o.attachRefused, o.attachReplied, o.sawAttachEvent)
		if len(asked) == 0 && !o.attachRefused && vsClientVisible(&o) {
			t.Errorf("path-BE RED: blankness alone skipped confirm without createTarget receipt / opened / errorProof; foreign blank must not be exempt")
		}
	})
	t.Run("own-tab-recovery-with-errorProofOrdinary", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		announced := "chrome-error://chromewebdata/"
		live := "https://seven.sliccy.ai/doc"
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", announced)), live)
		vsArmErrorPage(g, live, false)
		g.mu.Lock()
		proof := g.errorProofOfLocked(vsTarget)
		g.mu.Unlock()
		if proof != errorProofOrdinary {
			t.Fatalf("control provenance: want errorProofOrdinary (own-tab #842 failed-URL proof on errorProof map; unreachableUrl=%s), got %v", live, proof)
		}
		_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
		vsDrain(g, &o, g.fromBrowser(vsAttachedEvent(announced)), live)
		vsDrain(g, &o, g.fromBrowser(vsAttachReply()), live)
		o.summarize()
		vsExpectAdmittedAttach(t, "path-BE-control-errorProofOrdinary", o)
	})
}

func TestViewSourceAttachPathGFailedLoadSettle(t *testing.T) {
	t.Run("hold-on-chrome-error-pending", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		announced := "chrome-error://chromewebdata/"
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", announced)), vsOrdinary)
		v := g.fromBrowser(vsAttachedEvent(announced))
		o.take(v)
		g.mu.Lock()
		held := g.held[vsSession] != nil
		proof := g.errorProofOfLocked(vsTarget)
		g.mu.Unlock()
		t.Logf("path-G hold-on-error held=%t session=%t proof=%v", held, vsSessionPresent(g), proof)
		if vsSessionPresent(g) {
			t.Fatalf("path-G: chrome-error auto-attach without errorProofOrdinary must hold for failed-load proof, not admit")
		}
		if !held && proof == errorProofNone {
			t.Fatalf("path-G: expected failed-load hold or pending proof on chrome-error announce")
		}
	})
	t.Run("stale-ordinary-settle-live-wrapper", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		announced := "chrome-error://chromewebdata/"
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", announced)), vsHTTPVault)
		_ = g.fromBrowser(vsAttachedEvent(announced))
		g.mu.Lock()
		g.setErrorProofLocked(vsTarget, errorProofNone)
		if tgt := g.targets[vsTarget]; tgt != nil {
			tgt.url = vsOrdinary
		}
		held := g.held[vsSession]
		g.mu.Unlock()
		if held == nil {
			t.Fatalf("path-G stale: expected held session before settle release")
		}
		g.mu.Lock()
		rel := g.deliverHeldLocked(held)
		g.mu.Unlock()
		asked := vsDrainAnswer(g, &o, rel, func(int64) string { return vsHTTPVault })
		o.summarize()
		t.Logf("path-G stale-settle asked=%d session=%t event=%t", len(asked), vsSessionPresent(g), o.sawAttachEvent)
		if len(asked) == 0 {
			t.Fatalf("path-G: settle must re-confirm with a fresh proof read")
		}
		if vsSessionPresent(g) || o.sawAttachEvent {
			t.Errorf("path-G: settle proof of protected live document must not admit (session=%t event=%t)", vsSessionPresent(g), o.sawAttachEvent)
		}
	})
	t.Run("protected-settle-hidden", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		announced := "chrome-error://chromewebdata/"
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", announced)), vsHTTPVault)
		_ = g.fromBrowser(vsAttachedEvent(announced))
		g.mu.Lock()
		g.setErrorProofLocked(vsTarget, errorProofProtected)
		held := g.held[vsSession]
		g.mu.Unlock()
		if held == nil {
			t.Fatalf("path-G protected: expected held session")
		}
		g.mu.Lock()
		part := g.hideHeldPageAndTabsLocked(held, true, "failed-load protected")
		delete(g.held, vsSession)
		g.mu.Unlock()
		o.take(part)
		o.summarize()
		t.Logf("path-G protected-settle session=%t event=%t", vsSessionPresent(g), o.sawAttachEvent)
		if vsSessionPresent(g) || o.sawAttachEvent {
			t.Errorf("path-G: protected failed-load settle must hide (no client session); session=%t event=%t", vsSessionPresent(g), o.sawAttachEvent)
		}
	})
	t.Run("ordinary-error-proof-release", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		announced := "chrome-error://chromewebdata/"
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", announced)), vsOrdinary)
		vsArmErrorPage(g, vsOrdinary, false)
		_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
		asked := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent(announced)), func(int64) string { return announced })
		asked = append(asked, vsDrainAnswer(g, &o, g.fromBrowser(vsAttachReply()), func(int64) string { return announced })...)
		o.summarize()
		t.Logf("path-G ordinary-proof asked=%d refused=%t replied=%t event=%t evidence=errorProofOrdinary", len(asked), o.attachRefused, o.attachReplied, o.sawAttachEvent)
		if o.attachRefused {
			t.Errorf("path-G: own-tab ordinary errorProofOrdinary recovery must stay admitted (provenance: errorProof map)")
		}
		if !o.attachReplied && !o.sawAttachEvent {
			t.Errorf("path-G ordinary control: expected client-visible attach after errorProofOrdinary")
		}
	})
	t.Run("proof-read-fails-refuse-without-latch", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		announced := "chrome-error://chromewebdata/"
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", announced)), vsOrdinary)
		_ = g.fromBrowser(vsAttachedEvent(announced))
		g.mu.Lock()
		g.setErrorProofLocked(vsTarget, errorProofFailed)
		held := g.held[vsSession]
		g.mu.Unlock()
		if held == nil {
			t.Fatalf("path-G proof-fail: expected held session")
		}
		g.mu.Lock()
		part := g.hideHeldPageAndTabsLocked(held, true, "failed-load proof failed")
		delete(g.held, vsSession)
		g.mu.Unlock()
		o.take(part)
		o.summarize()
		t.Logf("path-G proof-fail session=%t event=%t", vsSessionPresent(g), o.sawAttachEvent)
		if vsSessionPresent(g) || o.sawAttachEvent {
			t.Errorf("path-G: proof-read failure must refuse/hide without latching a client page session (session=%t event=%t)", vsSessionPresent(g), o.sawAttachEvent)
		}
	})
}

func TestViewSourceAttachProvenanceControls(t *testing.T) {
	t.Run("client-createTarget-receipt", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		vsCreateThroughGuard(t, g, &o, "about:blank")
		g.mu.Lock()
		touched := g.touchedAt[vsTarget] > 0 || g.navTargets[vsTarget] != nil
		g.mu.Unlock()
		if !touched {
			t.Fatal("provenance: createTarget through the guard must leave touch/navTargets receipt; missing ⇒ foreign")
		}
		_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
		asked := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), func(int64) string { return "about:blank" })
		asked = append(asked, vsDrainAnswer(g, &o, g.fromBrowser(vsAttachReply()), func(int64) string { return "about:blank" })...)
		_ = asked
		o.summarize()
		vsExpectAdmittedAttach(t, "provenance-createTarget", o)
	})
	t.Run("missing-provenance-defaults-foreign", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), vsHTTPVault)
		g.mu.Lock()
		touched := g.touchedAt[vsTarget] > 0
		opened := g.opened[vsTarget]
		proof := g.errorProofOfLocked(vsTarget)
		g.mu.Unlock()
		if touched || opened || proof != errorProofNone {
			t.Fatalf("foreign fixture contaminated: touched=%t opened=%t proof=%v", touched, opened, proof)
		}
		asked := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), func(int64) string { return vsHTTPVault })
		o.summarize()
		if len(asked) == 0 {
			t.Fatalf("missing provenance defaults to foreign; blank auto-attach must enter document proof")
		}
		if vsSessionPresent(g) || o.sawAttachEvent {
			t.Errorf("missing provenance (no createTarget receipt, no opened, no errorProofOrdinary) defaults to foreign; must not leave a client session (session=%t event=%t)", vsSessionPresent(g), o.sawAttachEvent)
		}
	})
}

func TestViewSourceAttachMutationDropProof(t *testing.T) {
	if os.Getenv("SLICC_DOC_CONFIRM_DROP_PROOF") == "" {
		t.Skip("overlay mutation only")
	}
	url := "view-source:https://vault.sliccy.ai/doc"
	o := vsRun(t, 3, url, vsScenario{watched: true})
	if o.attachRefused || !o.readDelivered {
		t.Fatalf("drop-proof mutant: covering askDocConfirm pass-without-read must admit vault view-source (refused=%t delivered=%t)", o.attachRefused, o.readDelivered)
	}
}

func TestViewSourceAttachMutationLatchOnFailure(t *testing.T) {
	if os.Getenv("SLICC_DOC_CONFIRM_LATCH_ON_FAILURE") == "" {
		t.Skip("overlay mutation only")
	}
	g := vsGuard(t)
	var o vsOutcome
	announced := "view-source:https://ordinary.test/x"
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", announced)), announced)
	asked := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent(announced)), func(int64) string { return announced })
	if len(asked) == 0 {
		t.Fatal("latch-on-failure mutant: expected document proof ask")
	}
	g.mu.Lock()
	failed := g.docFailed[vsSession]
	g.mu.Unlock()
	if !failed {
		t.Fatal("latch-on-failure mutant: covering refuseDocConfirm latch path must set docFailed on non-ordinary proof")
	}
}

func TestViewSourceAttachMutationExemptBlankness(t *testing.T) {
	if os.Getenv("SLICC_DOC_CONFIRM_EXEMPT_BLANKNESS") == "" {
		t.Skip("overlay mutation only")
	}
	g := vsGuard(t)
	var o vsOutcome
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), "about:blank")
	v := g.fromBrowser(vsAttachedEvent("about:blank"))
	o.take(v)
	for _, p := range v.page {
		o.clientPayloads = append(o.clientPayloads, string(p))
	}
	o.summarize()
	if vsConfirmAsked(v.send) {
		t.Fatal("exempt-blankness mutant: covering URLNeedsProof blank skip must not ask document proof for foreign about:blank")
	}
	if !vsSessionPresent(g) && !o.sawAttachEvent {
		t.Fatal("exempt-blankness mutant: foreign blank must announce without proof under the covering skip")
	}
}

func TestViewSourceAttachMutationDropProvenance(t *testing.T) {
	if os.Getenv("SLICC_DOC_CONFIRM_DROP_PROVENANCE") == "" {
		t.Skip("overlay mutation only")
	}
	g := vsGuard(t)
	var o vsOutcome
	vsCreateThroughGuard(t, g, &o, "about:blank")
	_ = g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
	asked := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), func(int64) string { return "about:blank" })
	asked = append(asked, vsDrainAnswer(g, &o, g.fromBrowser(vsAttachReply()), func(int64) string { return "about:blank" })...)
	if len(asked) == 0 {
		t.Fatal("drop-provenance mutant: covering OwnedLocked skip must force proof even after createTarget receipt")
	}
}

func TestViewSourceAttachMutationDropSettleEmpty(t *testing.T) {
	if os.Getenv("SLICC_DOC_CONFIRM_DROP_SETTLE_EMPTY") == "" {
		t.Skip("overlay mutation only")
	}
	g := vsGuard(t)
	var o vsOutcome
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "")), vsOrdinary)
	_ = g.fromBrowser(vsAttachedEvent(""))
	rel := g.fromBrowser(vsRelay("Target.targetInfoChanged", vsOrdinary))
	o.take(rel)
	for _, p := range rel.page {
		o.clientPayloads = append(o.clientPayloads, string(p))
	}
	o.summarize()
	if vsConfirmAsked(rel.send) {
		t.Fatal("drop-settle-empty mutant: covering settleDocConfirm empty-hold skip must admit without proof ask")
	}
	if !vsSessionPresent(g) && !o.sawAttachEvent {
		t.Fatal("drop-settle-empty mutant: empty-hold settle must admit without re-confirm")
	}
}

func TestViewSourceAttachMutationDropSettleFailed(t *testing.T) {
	if os.Getenv("SLICC_DOC_CONFIRM_DROP_SETTLE_FAILED") == "" {
		t.Skip("overlay mutation only")
	}
	g := vsGuard(t)
	var o vsOutcome
	announced := "chrome-error://chromewebdata/"
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", announced)), vsOrdinary)
	_ = g.fromBrowser(vsAttachedEvent(announced))
	g.mu.Lock()
	g.setErrorProofLocked(vsTarget, errorProofFailed)
	if tgt := g.targets[vsTarget]; tgt != nil {
		tgt.url = vsOrdinary
	}
	held := g.held[vsSession]
	g.mu.Unlock()
	if held == nil {
		t.Fatal("drop-settle-failed mutant: expected held chrome-error session")
	}
	g.mu.Lock()
	rel := g.deliverHeldLocked(held)
	g.mu.Unlock()
	o.take(rel)
	for _, p := range rel.page {
		o.clientPayloads = append(o.clientPayloads, string(p))
	}
	o.summarize()
	if vsConfirmAsked(rel.send) {
		t.Fatal("drop-settle-failed mutant: covering settleDocConfirm failed-load skip must admit without proof ask")
	}
	if !vsSessionPresent(g) && !o.sawAttachEvent {
		t.Fatal("drop-settle-failed mutant: failed-load settle must admit without re-confirm")
	}
}

func TestViewSourceAttachMutationForeignOrdinary(t *testing.T) {
	if os.Getenv("SLICC_DOC_CONFIRM_FOREIGN_ORDINARY") == "" {
		t.Skip("overlay mutation only")
	}
	if !docConfirmForeignOrdinaryProofEnabled() {
		t.Fatal("proof-on mutant must flip docConfirmForeignOrdinaryProofEnabled to true")
	}
	g := vsGuard(t)
	var o vsOutcome
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", vsOrdinary)), vsOrdinary)
	asked := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent(vsOrdinary)), func(int64) string { return vsOrdinary })
	o.summarize()
	if len(asked) == 0 {
		t.Fatal("proof-on mutant: covering ForeignOrdinaryProof=true must run document proof for live ordinary http(s)")
	}
	if !vsSessionPresent(g) && !o.sawAttachEvent {
		t.Fatal("proof-on mutant: proof of ordinary must admit")
	}
}

func TestViewSourceAttachMutationDropDK(t *testing.T) {
	if os.Getenv("SLICC_DOC_CONFIRM_DROP_DK") == "" {
		t.Skip("overlay mutation only")
	}
	g := vsGuard(t)
	var o vsOutcome
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), "about:blank")
	v := g.fromBrowser(vsAttachedEvent("about:blank"))
	o.take(v)
	for _, p := range v.page {
		o.clientPayloads = append(o.clientPayloads, string(p))
	}
	o.summarize()
	if vsConfirmAsked(v.send) {
		t.Fatal("drop-DK mutant: covering ForeignNeedsProof D/K gate must skip proof for foreign blank")
	}
	if !vsSessionPresent(g) && !o.sawAttachEvent {
		t.Fatal("drop-DK mutant: foreign blank must announce when D/K coverage is dropped")
	}
}

func vsResidualOrdinaryThenProtected(t *testing.T, g *guard) (contentRefused, titleRefused bool, o vsOutcome) {
	t.Helper()
	protected := vsHTTPVault
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", vsOrdinary)), protected)
	if docConfirmForeignOrdinaryProofEnabled() {
		asked := vsDrainAnswer(g, &o, g.fromBrowser(vsAttachedEvent(vsOrdinary)), func(int64) string { return protected })
		if len(asked) == 0 {
			t.Fatal("residual: foreign ordinary announce must enter proof when foreignOrdinaryProof=true")
		}
	} else {
		v := g.fromBrowser(vsAttachedEvent(vsOrdinary))
		o.take(v)
		for _, p := range v.page {
			o.clientPayloads = append(o.clientPayloads, string(p))
		}
		_ = g.fromBrowser(vsRelay("Target.targetInfoChanged", protected))
	}
	o.summarize()
	vsRead(g, &o, protected)
	contentRefused = o.readRefused
	titleRefused = vsTitleRead(g, &o, protected)
	return contentRefused, titleRefused, o
}

func TestViewSourceAttachResidualOrdinaryAnnounceThenProtectedBeforeCommand(t *testing.T) {
	g := vsGuard(t)
	if docConfirmForeignOrdinaryProofEnabled() {
		contentRefused, titleRefused, o := vsResidualOrdinaryThenProtected(t, g)
		t.Logf("residual proof-on session=%t event=%t contentRefused=%t titleRefused=%t", vsSessionPresent(g), o.sawAttachEvent, contentRefused, titleRefused)
		if vsSessionPresent(g) || o.sawAttachEvent {
			t.Errorf("residual proof-on: proof of protected wrapper before first command must not leave a client page session")
		}
		if !contentRefused {
			t.Errorf("residual proof-on: content read as first command must refuse after protected commit during proof")
		}
		if !titleRefused {
			t.Errorf("residual proof-on: title read as first command must refuse after protected commit during proof")
		}
		return
	}
	var o vsOutcome
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", vsOrdinary)), vsOrdinary)
	v := g.fromBrowser(vsAttachedEvent(vsOrdinary))
	o.take(v)
	for _, p := range v.page {
		o.clientPayloads = append(o.clientPayloads, string(p))
	}
	o.summarize()
	t.Logf("stock residual lighter session=%t event=%t", vsSessionPresent(g), o.sawAttachEvent)
	if !vsSessionPresent(g) && !o.sawAttachEvent {
		t.Fatal("stock residual (lighter): ordinary announce without proof must admit — documents the #863 hole")
	}
	vsWatchReady(g)
	vsRead(g, &o, vsOrdinary)
	titleRefused := vsTitleRead(g, &o, vsOrdinary)
	if o.readRefused || titleRefused {
		t.Fatal("stock residual (lighter) without #863: content+title as first command after ordinary announce must land — hole the proof-on mutant closes")
	}
}

func TestViewSourceAttachMutationProofOnResidualClosesHole(t *testing.T) {
	if os.Getenv("SLICC_DOC_CONFIRM_FOREIGN_ORDINARY") == "" {
		t.Skip("overlay mutation only")
	}
	if !docConfirmForeignOrdinaryProofEnabled() {
		t.Fatal("proof-on residual mutant requires foreignOrdinaryProof=true")
	}
	g := vsGuard(t)
	contentRefused, titleRefused, o := vsResidualOrdinaryThenProtected(t, g)
	t.Logf("proof-on residual session=%t event=%t contentRefused=%t titleRefused=%t", vsSessionPresent(g), o.sawAttachEvent, contentRefused, titleRefused)
	if vsSessionPresent(g) || o.sawAttachEvent {
		t.Fatal("proof-on residual: must not leave a client page session")
	}
	if !contentRefused || !titleRefused {
		t.Fatal("proof-on residual: content+title must refuse after protected commit during proof")
	}
}

func TestViewSourceAttachMutationLighterResidualDocumentsHole(t *testing.T) {
	if os.Getenv("SLICC_DOC_CONFIRM_LIGHTER_ORDINARY") == "" {
		t.Skip("overlay mutation only")
	}
	if docConfirmForeignOrdinaryProofEnabled() {
		t.Fatal("lighter residual mutant requires foreignOrdinaryProof=false")
	}
	g := vsGuard(t)
	var o vsOutcome
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", vsOrdinary)), vsOrdinary)
	v := g.fromBrowser(vsAttachedEvent(vsOrdinary))
	o.take(v)
	for _, p := range v.page {
		o.clientPayloads = append(o.clientPayloads, string(p))
	}
	o.summarize()
	if !vsSessionPresent(g) && !o.sawAttachEvent {
		t.Fatal("lighter residual: ordinary announce without proof must admit")
	}
	vsWatchReady(g)
	vsRead(g, &o, vsOrdinary)
	titleRefused := vsTitleRead(g, &o, vsOrdinary)
	t.Logf("lighter residual hole session=%t contentRefused=%t titleRefused=%t readForwarded=%t", vsSessionPresent(g), o.readRefused, titleRefused, o.readForwarded)
	if o.readRefused || titleRefused {
		t.Fatal("lighter residual without #863: content+title as first command after ordinary announce must land")
	}
}

func TestViewSourceAttachBlankForeignThreeWay(t *testing.T) {
	t.Run("a-blank-ordinary-opaque-origin-admits", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		var logs []string
		g.logf = func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), "about:blank")
		asked := vsDrainAnswerOrigin(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), func(int64) (string, string) { return "about:blank", "://" })
		o.summarize()
		t.Logf("blank-a asked=%d session=%t event=%t refusedOrdinary=%d refusedBlank=%d", len(asked), vsSessionPresent(g), o.sawAttachEvent, g.docRefusedOrdinary, g.docRefusedBlank)
		if len(asked) == 0 {
			t.Fatal("blank-a: foreign blank announce must enter document proof")
		}
		if !vsSessionPresent(g) && !o.sawAttachEvent {
			t.Fatal("blank-a: about:blank + opaque origin must admit")
		}
		if g.docRefusedOrdinary != 0 || g.docRefusedBlank != 0 {
			t.Fatalf("blank-a availability: refused-ordinary=%d refused-blank=%d", g.docRefusedOrdinary, g.docRefusedBlank)
		}
	})
	t.Run("b-blank-protected-origin-latches", func(t *testing.T) {
		g := vsGuard(t)
		var o vsOutcome
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), "about:blank")
		asked := vsDrainAnswerOrigin(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), func(int64) (string, string) {
			return "about:blank", "https://vault.sliccy.ai"
		})
		o.summarize()
		g.mu.Lock()
		failed := g.docFailed[vsSession]
		g.mu.Unlock()
		t.Logf("blank-b asked=%d session=%t event=%t latched=%t", len(asked), vsSessionPresent(g), o.sawAttachEvent, failed)
		if len(asked) == 0 {
			t.Fatal("blank-b: foreign blank announce must enter document proof")
		}
		if vsSessionPresent(g) || o.sawAttachEvent {
			t.Fatal("blank-b: blank + protected origin must not admit")
		}
		if !failed {
			t.Fatal("blank-b: protected origin must latch refuse")
		}
	})
	t.Run("c-transition-park-then-refuse-without-latch", func(t *testing.T) {
		t.Setenv("SLICC_CDP_HOLD_TIMEOUT", "30ms")
		g := vsGuard(t)
		var o vsOutcome
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), "about:blank")
		asked := vsDrainAnswerOrigin(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), func(int64) (string, string) { return "", "" })
		if len(asked) == 0 {
			t.Fatal("blank-c: foreign blank announce must enter document proof")
		}
		g.mu.Lock()
		pending := g.docInflight[vsTarget] != nil && !g.docInflight[vsTarget].done
		transition := pending && g.docInflight[vsTarget].transition
		g.mu.Unlock()
		if !pending || !transition {
			t.Fatalf("blank-c: empty frame tree must park transition (pending=%t transition=%t)", pending, transition)
		}
		if vsSessionPresent(g) || o.sawAttachEvent {
			t.Fatal("blank-c: transition park must not admit")
		}
		deadline := time.Now().Add(200 * time.Millisecond)
		for time.Now().Before(deadline) {
			g.mu.Lock()
			pending = g.docInflight[vsTarget] != nil && !g.docInflight[vsTarget].done
			failed := g.docFailed[vsSession]
			g.mu.Unlock()
			if !pending {
				if failed {
					t.Fatal("blank-c: hold-bound refuse must not latch")
				}
				if vsSessionPresent(g) {
					t.Fatal("blank-c: hold-bound refuse must never admit")
				}
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("blank-c: expected hold-bound refuse without latch")
	})
}

func TestViewSourceAttachMutationRefuseBlankOrdinary(t *testing.T) {
	if os.Getenv("SLICC_DOC_CONFIRM_REFUSE_BLANK_ORDINARY") == "" {
		t.Skip("overlay mutation only")
	}
	if docConfirmAdmitBlankOrdinaryEnabled() {
		t.Fatal("refuse-blank-ordinary mutant must flip admitBlankOrdinary to false")
	}
	g := vsGuard(t)
	var o vsOutcome
	vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), "about:blank")
	asked := vsDrainAnswerOrigin(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), func(int64) (string, string) { return "about:blank", "://" })
	o.summarize()
	if len(asked) == 0 {
		t.Fatal("refuse-blank mutant: expected proof ask")
	}
	if vsSessionPresent(g) || o.sawAttachEvent {
		t.Fatal("refuse-blank mutant: covering admitBlankOrdinary=false must refuse blank+opaque")
	}
	if g.docRefusedBlank == 0 {
		t.Fatal("refuse-blank mutant: availability refused-blank must increment")
	}
}

func TestViewSourceAttachAvailabilityGate(t *testing.T) {
	var logs []string
	logf := func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }
	gOrd := vsGuard(t)
	gOrd.logf = logf
	var oOrd vsOutcome
	vsDrain(gOrd, &oOrd, gOrd.fromBrowser(vsRelay("Target.targetCreated", vsOrdinary)), vsOrdinary)
	v := gOrd.fromBrowser(vsAttachedEvent(vsOrdinary))
	oOrd.take(v)
	for _, p := range v.page {
		oOrd.clientPayloads = append(oOrd.clientPayloads, string(p))
	}
	oOrd.summarize()
	if !vsSessionPresent(gOrd) && !oOrd.sawAttachEvent {
		t.Fatal("availability: ordinary lighter admit required")
	}
	gBlank := vsGuard(t)
	gBlank.logf = logf
	var oBlank vsOutcome
	vsDrain(gBlank, &oBlank, gBlank.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), "about:blank")
	asked := vsDrainAnswerOrigin(gBlank, &oBlank, gBlank.fromBrowser(vsAttachedEvent("about:blank")), func(int64) (string, string) { return "about:blank", "://" })
	if len(asked) == 0 {
		t.Fatal("availability: blank ordinary must proof")
	}
	if !vsSessionPresent(gBlank) && !oBlank.sawAttachEvent {
		t.Fatal("availability: blank ordinary admit required")
	}
	t.Logf("DOC_CONFIRM_AVAILABILITY refused-ordinary=%d refused-blank=%d", gOrd.docRefusedOrdinary+gBlank.docRefusedOrdinary, gOrd.docRefusedBlank+gBlank.docRefusedBlank)
	if gOrd.docRefusedOrdinary != 0 || gBlank.docRefusedOrdinary != 0 || gOrd.docRefusedBlank != 0 || gBlank.docRefusedBlank != 0 {
		t.Fatalf("availability gate must stay 0 on ordinary+blank admits; ordinary=%d/%d blank=%d/%d", gOrd.docRefusedOrdinary, gBlank.docRefusedOrdinary, gOrd.docRefusedBlank, gBlank.docRefusedBlank)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "refused-ordinary=0") || !strings.Contains(joined, "refused-blank=0") {
		t.Fatalf("availability permanent log missing: %q", joined)
	}
}

func TestViewSourceAttachOrdinaryForeignProofCost(t *testing.T) {
	const rounds = 11
	samples := make([]time.Duration, 0, rounds)
	for i := 0; i < rounds; i++ {
		g := vsGuard(t)
		var o vsOutcome
		start := time.Now()
		vsDrain(g, &o, g.fromBrowser(vsRelay("Target.targetCreated", "about:blank")), "about:blank")
		asked := vsDrainAnswerOrigin(g, &o, g.fromBrowser(vsAttachedEvent("about:blank")), func(int64) (string, string) { return "about:blank", "://" })
		took := time.Since(start)
		if len(asked) == 0 {
			t.Fatalf("proof-cost round %d: expected blank foreign proof ask", i)
		}
		samples = append(samples, took)
	}
	sorted := append([]time.Duration(nil), samples...)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j] < sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	median := sorted[len(sorted)/2]
	p90 := sorted[(len(sorted)*9)/10]
	max := sorted[len(sorted)-1]
	t.Logf("DOC_CONFIRM_PROOF_COST kind=foreign-blank-announce n=%d median=%s p90=%s max=%s samples=%v", len(samples), median.Round(time.Microsecond), p90.Round(time.Microsecond), max.Round(time.Microsecond), samples)
	t.Logf("DOC_CONFIRM_PROOF_COST kind=foreign-ordinary-announce note=lighter-default-off stock skips proof; proof-on mutant covers residual; Chrome stock popup/tab scrape \"DIAG document-confirm proof round took\"")
	t.Logf("DOC_CONFIRM_PROOF_COST table blank-min-set median=%s p90=%s max=%s ordinary=lighter-default", median.Round(time.Microsecond), p90.Round(time.Microsecond), max.Round(time.Microsecond))
}
