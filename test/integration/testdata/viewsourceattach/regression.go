package cdp

import (
	"strings"
	"testing"

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

func vsRead(g *guard, o *vsOutcome) {
	pv := g.fromCommand(&command{id: vsReadID, method: "Runtime.evaluate", session: vsSession, params: map[string]any{"expression": "document.body.innerText"}}, vsReadID, true)
	if pv.reply != nil {
		o.readRefused = true
		o.clientPayloads = append(o.clientPayloads, string(pv.reply))
		o.refusal = string(pv.reply)
		return
	}
	if pv.forward != nil {
		o.readForwarded = true
		o.take(g.fromBrowser(vsReadReply()))
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
	g.fromBrowser(vsRelay("Target.targetCreated", announced))
	if s.watched && !s.lateArm {
		vsWatchReady(g)
	}
	pv := g.fromCommand(&command{id: vsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": vsTarget, "flatten": true}}, vsAttachID, true)
	if pv.reply != nil {
		o.clientPayloads = append(o.clientPayloads, string(pv.reply))
		o.summarize()
		return o
	}
	o.take(g.fromBrowser(vsAttachedEvent(announced)))
	if s.lateArm {
		vsWatchReady(g)
	}
	switch order {
	case 1:
		o.take(g.fromBrowser(vsRelay("Target.targetInfoChanged", live)))
		o.take(g.fromBrowser(vsAttachReply()))
		vsRead(g, &o)
	case 2:
		o.take(g.fromBrowser(vsAttachReply()))
		o.take(g.fromBrowser(vsRelay("Target.targetInfoChanged", live)))
		vsRead(g, &o)
	default:
		o.take(g.fromBrowser(vsAttachReply()))
		vsRead(g, &o)
		o.take(g.fromBrowser(vsRelay("Target.targetInfoChanged", live)))
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
