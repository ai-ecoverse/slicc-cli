package cdp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	esTarget     = "ES-TARGET"
	esSession    = "S-ES"
	esNavSession = "S-ES-NAV"
	esAttachID   = 70
	esReadID     = 71
	esSecret     = "SECRET-FROM-PROTECTED-DOCUMENT"
	esOrdinary   = "https://ordinary.test/existing"
	esOrdinaryB  = "https://ordinary.test/other"
)

type esOrder int

const (
	esLearnBeforeCmd esOrder = iota + 1
	esLearnBeforeReply
	esReplyBeforeLearn
	esNeverLearn
	esCmdBeforeCommit
)

func (o esOrder) String() string {
	switch o {
	case esLearnBeforeCmd:
		return "learn-before-cmd"
	case esLearnBeforeReply:
		return "learn-before-reply"
	case esReplyBeforeLearn:
		return "reply-before-learn"
	case esNeverLearn:
		return "never-learn"
	case esCmdBeforeCommit:
		return "cmd-before-commit"
	default:
		return fmt.Sprintf("order-%d", int(o))
	}
}

type esEventKind int

const (
	esEventsBoth esEventKind = iota
	esEventsFrameOnly
	esEventsInfoOnly
)

func (k esEventKind) String() string {
	switch k {
	case esEventsBoth:
		return "frame+info"
	case esEventsFrameOnly:
		return "frameNavigated"
	case esEventsInfoOnly:
		return "targetInfoChanged"
	default:
		return "events"
	}
}

func esGuard(t *testing.T) *guard {
	t.Helper()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, nil)
	g.relaySession = "RELAY"
	return g
}

func esEncode(v any) []byte { return encode(v) }

func esRelay(method, url string) []byte {
	return esEncode(map[string]any{
		"sessionId": "RELAY",
		"method":    method,
		"params": map[string]any{
			"targetInfo": map[string]any{"targetId": esTarget, "type": "page", "url": url, "browserContextId": "CTX"},
		},
	})
}

func esAttached(url string) []byte {
	return esEncode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          esSession,
			"waitingForDebugger": false,
			"targetInfo":         map[string]any{"targetId": esTarget, "type": "page", "url": url, "browserContextId": "CTX"},
		},
	})
}

func esAttachReply() []byte {
	return esEncode(map[string]any{"id": esAttachID, "result": map[string]any{"sessionId": esSession}})
}

func esTargetInfoAnswer(id int64, url string) []byte {
	return esEncode(map[string]any{
		"id":        id,
		"sessionId": "RELAY",
		"result": map[string]any{
			"targetInfo": map[string]any{"targetId": esTarget, "type": "page", "url": url, "browserContextId": "CTX"},
		},
	})
}

func esFrameNavigated(url, loader string) []byte {
	origin := "://"
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		origin = url
		if i := strings.Index(url[8:], "/"); i >= 0 {
			origin = url[:8+i]
		}
	}
	return esEncode(map[string]any{
		"sessionId": esNavSession,
		"method":    "Page.frameNavigated",
		"params": map[string]any{
			"frame": map[string]any{
				"id":             esTarget,
				"loaderId":       loader,
				"url":            url,
				"securityOrigin": origin,
				"mimeType":       "text/html",
			},
			"type": "Navigation",
		},
	})
}

func esOutbound(raw []byte) (id int64, method, session string) {
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

func esArmNav(g *guard, documentURL string) {
	ready := make(chan struct{})
	close(ready)
	nt := &navTarget{
		target:      esTarget,
		session:     esNavSession,
		ready:       ready,
		done:        true,
		documentURL: documentURL,
	}
	g.navTargets[esTarget] = nt
	g.navSessions[esNavSession] = nt
}

type esOutcome struct {
	payloads  []string
	refused   bool
	forwarded bool
	delivered bool
	refusal   string
	mechanism string
	chromeErr bool
	waited    bool
}

func (o *esOutcome) take(v browserVerdict) {
	if v.forward != nil {
		o.payloads = append(o.payloads, string(v.forward))
	}
	for _, b := range v.page {
		o.payloads = append(o.payloads, string(b))
	}
}

func esIsReadReply(p string) bool {
	return strings.Contains(p, fmt.Sprintf(`"id":%d`, esReadID))
}

func (o *esOutcome) notePayloadsFor(live string) {
	for _, p := range o.payloads {
		if !esIsReadReply(p) {
			continue
		}
		if strings.Contains(p, `"error"`) && strings.Contains(p, "slicc: refused:") {
			o.refused = true
			o.refusal = p
			switch {
			case strings.Contains(p, refusedOutlived):
				o.mechanism = "outlived"
			case strings.Contains(p, refusedHistory):
				o.mechanism = "conformance/historyLocked"
			case strings.Contains(p, refusedTarget):
				if o.mechanism == "" {
					o.mechanism = "sessionRefused/protect"
				}
			case strings.Contains(p, "Page.captureSnapshot"):
				o.mechanism = "methodRules/captureSnapshot"
			default:
				if o.mechanism == "" {
					o.mechanism = "checkLocked"
				}
			}
			continue
		}
		if strings.Contains(p, "Cannot find context") {
			o.chromeErr = true
			if o.mechanism == "" {
				o.mechanism = "chrome-context-destroyed"
			}
			continue
		}
		if strings.Contains(p, esSecret) {
			o.delivered = true
			continue
		}
		if live != "" && live != esOrdinary && live != esOrdinaryB && strings.Contains(p, live) {
			o.delivered = true
		}
	}
}

func esDrain(g *guard, o *esOutcome, v browserVerdict, answerURL string) {
	o.take(v)
	for _, raw := range v.send {
		id, method, session := esOutbound(raw)
		if method == "Target.getTargetInfo" && session == "RELAY" {
			esDrain(g, o, g.fromBrowser(esTargetInfoAnswer(id, answerURL)), answerURL)
		}
	}
}

func esAdmit(t *testing.T, g *guard) *esOutcome {
	t.Helper()
	var o esOutcome
	esDrain(g, &o, g.fromBrowser(esRelay("Target.targetCreated", esOrdinary)), esOrdinary)
	esArmNav(g, esOrdinary)
	pv := g.fromCommand(&command{id: esAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": esTarget, "flatten": true}}, esAttachID, true)
	if pv.reply != nil {
		o.payloads = append(o.payloads, string(pv.reply))
		o.refused = true
		o.mechanism = "attach-refused"
		return &o
	}
	esDrain(g, &o, g.fromBrowser(esAttached(esOrdinary)), esOrdinary)
	esDrain(g, &o, g.fromBrowser(esAttachReply()), esOrdinary)
	g.mu.Lock()
	passed := g.docPassed[esSession]
	sess := g.sessions[esSession]
	g.mu.Unlock()
	if sess == nil || !passed {
		t.Fatalf("existing session not admitted: sess=%v passed=%t payloads=%v", sess != nil, passed, o.payloads)
	}
	return &o
}

type esCommand struct {
	label  string
	method string
	params map[string]any
	pinned bool
}

func esCommands() []esCommand {
	return []esCommand{
		{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "document.body.innerText"}},
		{label: "Runtime.evaluate+contextId", method: "Runtime.evaluate", params: map[string]any{"expression": "1", "contextId": json.Number("1")}, pinned: true},
		{label: "DOM.getDocument", method: "DOM.getDocument", params: map[string]any{}},
		{label: "DOM.getOuterHTML", method: "DOM.getOuterHTML", params: map[string]any{"nodeId": json.Number("1")}},
		{label: "DOMSnapshot.captureSnapshot", method: "DOMSnapshot.captureSnapshot", params: map[string]any{"computedStyleWhitelist": []any{}}},
		{label: "Page.captureScreenshot", method: "Page.captureScreenshot", params: map[string]any{}},
		{label: "Page.printToPDF", method: "Page.printToPDF", params: map[string]any{}},
		{label: "Page.captureSnapshot", method: "Page.captureSnapshot", params: map[string]any{}},
		{label: "Page.getResourceContent", method: "Page.getResourceContent", params: map[string]any{"frameId": esTarget, "url": esOrdinary}},
		{label: "Page.getResourceTree", method: "Page.getResourceTree", params: map[string]any{}},
		{label: "Accessibility.getFullAXTree", method: "Accessibility.getFullAXTree", params: map[string]any{}},
		{label: "Accessibility.getPartialAXTree", method: "Accessibility.getPartialAXTree", params: map[string]any{"nodeId": json.Number("1")}},
		{label: "Accessibility.queryAXTree", method: "Accessibility.queryAXTree", params: map[string]any{"nodeId": json.Number("1")}},
		{label: "Input.dispatchMouseEvent", method: "Input.dispatchMouseEvent", params: map[string]any{"type": "mousePressed", "x": 1, "y": 1, "button": "left", "clickCount": 1}},
		{label: "Input.dispatchKeyEvent", method: "Input.dispatchKeyEvent", params: map[string]any{"type": "keyDown", "key": "a"}},
	}
}

func esProtectedURLs() []string {
	return []string{
		"view-source:https://vault.sliccy.ai/doc",
		"view-source:https://seven.sliccy.ai/",
		"blob:https://vault.sliccy.ai/0c1d2e3f-4a5b-6c7d-8e9f-001122334455",
		"filesystem:https://vault.sliccy.ai/temporary/x.html",
		"view-source:blob:https://vault.sliccy.ai/0c1d2e3f-4a5b-6c7d-8e9f-001122334455",
	}
}

func esContentReply(cmd esCommand, id int64, live string, committed bool) []byte {
	secret := committed && (strings.Contains(live, "vault.sliccy.ai") || strings.Contains(live, "seven.sliccy.ai"))
	if cmd.pinned && committed {
		return esEncode(map[string]any{
			"id":        id,
			"sessionId": esSession,
			"error":     map[string]any{"code": -32000, "message": "Cannot find context with specified id"},
		})
	}
	body := "ordinary"
	if secret {
		body = esSecret
	}
	switch cmd.method {
	case "Runtime.evaluate":
		return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{"result": map[string]any{"type": "string", "value": body}}})
	case "DOM.getDocument":
		url := esOrdinary
		if committed {
			url = live
		}
		return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{"root": map[string]any{"nodeId": 1, "documentURL": url, "nodeName": "HTML"}}})
	case "DOM.getOuterHTML":
		html := "<html></html>"
		if secret {
			html = "<html>" + esSecret + "</html>"
		}
		return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{"outerHTML": html}})
	case "DOMSnapshot.captureSnapshot":
		if secret {
			return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{"documents": []any{map[string]any{"documentURL": live}}, "strings": []any{esSecret}}})
		}
		return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{"documents": []any{}, "strings": []any{}}})
	case "Page.captureScreenshot", "Page.printToPDF":
		data := "iVBORw0KGgo="
		if secret {
			data = "marker-" + esSecret
		}
		return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{"data": data}})
	case "Page.captureSnapshot":
		doc := "snapshot"
		if secret {
			doc = "snapshot " + esSecret
		}
		return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{"document": doc}})
	case "Page.getResourceContent":
		content := "body"
		if secret {
			content = "body " + esSecret
		}
		return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{"content": content, "base64Encoded": false}})
	case "Page.getResourceTree":
		url := esOrdinary
		if committed {
			url = live
		}
		return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": esTarget, "url": url}, "resources": []any{}}}})
	case "Accessibility.getFullAXTree", "Accessibility.getPartialAXTree", "Accessibility.queryAXTree":
		nodes := []any{}
		if secret {
			nodes = []any{map[string]any{"name": map[string]any{"value": "ax " + esSecret}}}
		}
		return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{"nodes": nodes}})
	case "Input.dispatchMouseEvent", "Input.dispatchKeyEvent":
		return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{}})
	default:
		return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{}})
	}
}

func esDeliverLearn(g *guard, o *esOutcome, live string, kind esEventKind) {
	switch kind {
	case esEventsFrameOnly:
		esDrain(g, o, g.fromBrowser(esFrameNavigated(live, "L1")), live)
	case esEventsInfoOnly:
		esDrain(g, o, g.fromBrowser(esRelay("Target.targetInfoChanged", live)), live)
	default:
		esDrain(g, o, g.fromBrowser(esFrameNavigated(live, "L1")), live)
		esDrain(g, o, g.fromBrowser(esRelay("Target.targetInfoChanged", live)), live)
	}
}

func esRunCell(t *testing.T, cmd esCommand, live string, order esOrder, kind esEventKind) esOutcome {
	t.Helper()
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	committed := order != esCmdBeforeCommit
	id := int64(esReadID)
	issue := func() {
		pv := g.fromCommand(&command{id: id, method: cmd.method, session: esSession, params: cmd.params}, id, true)
		if pv.reply != nil {
			o.refused = true
			o.payloads = append(o.payloads, string(pv.reply))
			o.refusal = string(pv.reply)
			switch {
			case strings.Contains(o.refusal, refusedHistory):
				o.mechanism = "conformance/historyLocked"
			case strings.Contains(o.refusal, refusedTarget):
				o.mechanism = "check/sessionRefused"
			case strings.Contains(o.refusal, "Page.captureSnapshot"):
				o.mechanism = "methodRules/captureSnapshot"
			default:
				o.mechanism = "checkLocked"
			}
			return
		}
		if pv.wait != nil {
			o.waited = true
			o.refused = true
			o.mechanism = "held-wait"
			return
		}
		if pv.forward != nil {
			o.forwarded = true
			reply := esContentReply(cmd, id, live, committed)
			esDrain(g, &o, g.fromBrowser(reply), live)
		}
	}
	switch order {
	case esLearnBeforeCmd:
		esDeliverLearn(g, &o, live, kind)
		issue()
	case esLearnBeforeReply:
		pv := g.fromCommand(&command{id: id, method: cmd.method, session: esSession, params: cmd.params}, id, true)
		if pv.reply != nil {
			o.refused = true
			o.payloads = append(o.payloads, string(pv.reply))
			o.refusal = string(pv.reply)
			o.mechanism = "checkLocked"
			break
		}
		if pv.wait != nil {
			o.waited = true
			o.refused = true
			o.mechanism = "held-wait"
			break
		}
		if pv.forward != nil {
			o.forwarded = true
			esDeliverLearn(g, &o, live, kind)
			reply := esContentReply(cmd, id, live, true)
			esDrain(g, &o, g.fromBrowser(reply), live)
		}
	case esReplyBeforeLearn:
		issue()
		esDeliverLearn(g, &o, live, kind)
	case esNeverLearn:
		issue()
	case esCmdBeforeCommit:
		committed = false
		issue()
		esDeliverLearn(g, &o, live, kind)
	}
	o.notePayloadsFor(live)
	if o.delivered && o.mechanism == "" {
		o.mechanism = "none/stale-cache"
	}
	if !o.delivered && !o.refused && o.chromeErr {
		o.refused = true
	}
	if !o.delivered && !o.refused && o.forwarded && (cmd.method == "Input.dispatchMouseEvent" || cmd.method == "Input.dispatchKeyEvent") {
		o.mechanism = "empty-result"
	}
	if !o.delivered && !o.refused && o.forwarded && o.mechanism == "" {
		o.mechanism = "ordinary-or-empty"
	}
	return o
}

type esCell struct {
	command   string
	order     string
	events    string
	url       string
	red       bool
	mechanism string
	refusal   string
}

func esColor(o esOutcome) (red bool) {
	return o.delivered
}

func TestExistingSessionProtectedNavigationCells(t *testing.T) {
	var reds []esCell
	var greens int
	for _, cmd := range esCommands() {
		for _, live := range esProtectedURLs() {
			for _, order := range []esOrder{esLearnBeforeCmd, esLearnBeforeReply, esReplyBeforeLearn, esNeverLearn, esCmdBeforeCommit} {
				for _, kind := range []esEventKind{esEventsBoth, esEventsFrameOnly, esEventsInfoOnly} {
					cmd, live, order, kind := cmd, live, order, kind
					name := fmt.Sprintf("%s/%s/%s/%s", cmd.label, order, kind, live)
					t.Run(name, func(t *testing.T) {
						o := esRunCell(t, cmd, live, order, kind)
						cell := esCell{command: cmd.label, order: order.String(), events: kind.String(), url: live, red: esColor(o), mechanism: o.mechanism, refusal: o.refusal}
						t.Logf("cmd=%s order=%s events=%s url=%s red=%t mechanism=%s refused=%t delivered=%t forwarded=%t", cmd.label, order, kind, live, cell.red, o.mechanism, o.refused, o.delivered, o.forwarded)
						if cell.red {
							reds = append(reds, cell)
							t.Errorf("RED: client received protected document content via %s (%s/%s) mechanism=%s", cmd.label, order, kind, o.mechanism)
						} else {
							greens++
						}
						if order == esCmdBeforeCommit && o.delivered {
							t.Errorf("cmd-before-commit must not deliver protected content")
						}
					})
				}
			}
		}
	}
	t.Logf("existing-session matrix: greens≈%d (see subtests); red count logged on failure", greens)
	_ = reds
}

func TestExistingSessionOrdinaryStaysReadable(t *testing.T) {
	for _, cmd := range esCommands() {
		if cmd.method == "Page.captureSnapshot" {
			continue
		}
		for _, order := range []esOrder{esLearnBeforeCmd, esLearnBeforeReply, esReplyBeforeLearn, esNeverLearn, esCmdBeforeCommit} {
			for _, kind := range []esEventKind{esEventsBoth} {
				cmd, order, kind := cmd, order, kind
				t.Run(fmt.Sprintf("%s/%s", cmd.label, order), func(t *testing.T) {
					o := esRunCell(t, cmd, esOrdinaryB, order, kind)
					if o.delivered && strings.Contains(strings.Join(o.payloads, ""), esSecret) {
						t.Fatalf("ordinary control leaked secret marker")
					}
					if order == esLearnBeforeCmd || order == esLearnBeforeReply || order == esReplyBeforeLearn || order == esNeverLearn || order == esCmdBeforeCommit {
						if o.refused && o.mechanism != "methodRules/captureSnapshot" && !cmd.pinned {
							if order == esLearnBeforeCmd || order == esLearnBeforeReply {
								if strings.Contains(o.refusal, "vault") || strings.Contains(o.refusal, "seven") {
									t.Fatalf("ordinary navigation must not refuse as protected: %s", o.refusal)
								}
							}
						}
					}
					if (order == esNeverLearn || order == esCmdBeforeCommit || order == esReplyBeforeLearn) && !cmd.pinned && !o.forwarded && !o.refused {
						t.Fatalf("ordinary command should forward or refuse cleanly: %+v", o)
					}
				})
			}
		}
	}
}

func TestExistingSessionOwnTabErrorPageRecovery(t *testing.T) {
	shapes := []struct {
		name   string
		live   string
		sticky bool
	}{
		{"C8.protected-error-page", "https://seven.sliccy.ai/doc", false},
		{"C8.protected-error-page-sticky", "https://seven.sliccy.ai/doc", true},
		{"own-tab.chrome-error", "https://vault.sliccy.ai/doc", false},
	}
	for _, shape := range shapes {
		shape := shape
		t.Run(shape.name, func(t *testing.T) {
			g := esGuard(t)
			announced := "chrome-error://chromewebdata/"
			var o esOutcome
			esDrain(g, &o, g.fromBrowser(esRelay("Target.targetCreated", announced)), shape.live)
			ready := make(chan struct{})
			close(ready)
			nt := &navTarget{
				target:      esTarget,
				session:     esNavSession,
				ready:       ready,
				done:        true,
				documentURL: announced,
				errorURL:    shape.live,
			}
			if shape.sticky {
				nt.historyLocked = true
			}
			g.navTargets[esTarget] = nt
			g.navSessions[esNavSession] = nt
			g.setErrorProofLocked(esTarget, errorProofOrdinary)
			pv := g.fromCommand(&command{id: esAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": esTarget, "flatten": true}}, esAttachID, true)
			if pv.reply != nil && strings.Contains(string(pv.reply), "slicc: refused:") {
				t.Fatalf("%s: recovery attach must stay admitted: %s", shape.name, pv.reply)
			}
			esDrain(g, &o, g.fromBrowser(esAttached(announced)), shape.live)
			esDrain(g, &o, g.fromBrowser(esAttachReply()), shape.live)
			attachRefused := false
			attachVisible := false
			for _, p := range o.payloads {
				if strings.Contains(p, `"id":70`) && strings.Contains(p, `"error"`) {
					attachRefused = true
				}
				if strings.Contains(p, "attachedToTarget") && strings.Contains(p, esSession) {
					attachVisible = true
				}
				if strings.Contains(p, `"id":70`) && strings.Contains(p, `"result"`) {
					attachVisible = true
				}
				if strings.Contains(p, "attachedToTarget") && strings.Contains(p, shape.live) {
					t.Errorf("%s: protected URL leaked in attach event: %s", shape.name, p)
				}
			}
			if attachRefused {
				t.Fatalf("%s: error-page attach reporting protected unreachable URL must stay admitted", shape.name)
			}
			if !attachVisible {
				t.Fatalf("%s: expected client-visible attach with redaction: %v", shape.name, o.payloads)
			}
		})
	}
}

func TestExistingSessionMatrixSummary(t *testing.T) {
	type key struct{ command, order, events string }
	red := map[key][]string{}
	mech := map[key]string{}
	for _, cmd := range esCommands() {
		for _, live := range esProtectedURLs() {
			for _, order := range []esOrder{esLearnBeforeCmd, esLearnBeforeReply, esReplyBeforeLearn, esNeverLearn, esCmdBeforeCommit} {
				for _, kind := range []esEventKind{esEventsBoth, esEventsFrameOnly, esEventsInfoOnly} {
					o := esRunCell(t, cmd, live, order, kind)
					k := key{cmd.label, order.String(), kind.String()}
					if esColor(o) {
						red[k] = append(red[k], live)
						mech[k] = o.mechanism
					} else if _, ok := mech[k]; !ok {
						mech[k] = o.mechanism
					}
				}
			}
		}
	}
	var lines []string
	for _, cmd := range esCommands() {
		for _, order := range []esOrder{esLearnBeforeCmd, esLearnBeforeReply, esReplyBeforeLearn, esNeverLearn, esCmdBeforeCommit} {
			for _, kind := range []esEventKind{esEventsBoth, esEventsFrameOnly, esEventsInfoOnly} {
				k := key{cmd.label, order.String(), kind.String()}
				if urls, ok := red[k]; ok {
					lines = append(lines, fmt.Sprintf("RED\t%s\t%s\t%s\tn=%d\t%s\tmechanism=%s", cmd.label, order, kind, len(urls), urls[0], mech[k]))
				} else {
					lines = append(lines, fmt.Sprintf("GREEN\t%s\t%s\t%s\tmechanism=%s", cmd.label, order, kind, mech[k]))
				}
			}
		}
	}
	t.Log("\n" + strings.Join(lines, "\n"))
	if len(red) > 0 {
		t.Errorf("existing-session red cell classes: %d (see log table)", len(red))
	}
}
