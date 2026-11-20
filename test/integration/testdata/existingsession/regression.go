package cdp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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
	return esFrameNavigatedUnreachable(url, loader, "")
}

func esFrameNavigatedUnreachable(url, loader, unreachable string) []byte {
	origin := "://"
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		origin = url
		if i := strings.Index(url[8:], "/"); i >= 0 {
			origin = url[:8+i]
		}
	}
	frame := map[string]any{
		"id":             esTarget,
		"loaderId":       loader,
		"url":            url,
		"securityOrigin": origin,
		"mimeType":       "text/html",
	}
	if unreachable != "" {
		frame["unreachableUrl"] = unreachable
	}
	return esEncode(map[string]any{
		"sessionId": esNavSession,
		"method":    "Page.frameNavigated",
		"params": map[string]any{
			"frame": frame,
			"type":  "Navigation",
		},
	})
}

func esWindowTaintThenOrdinaryPostRead(t *testing.T, g *guard, learn func(*esOutcome)) {
	t.Helper()
	var o esOutcome
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "document.body.innerText"}}
	pv := g.fromCommand(&command{id: int64(esReadID), method: cmd.method, session: esSession, params: cmd.params}, int64(esReadID), true)
	if pv.forward == nil {
		t.Fatalf("admit must forward: reply=%s", pv.reply)
	}
	g.mu.Lock()
	before := g.docIdentityTaint[esTarget]
	g.mu.Unlock()
	held := g.fromBrowser(esContentReply(cmd, int64(esReadID), esOrdinaryB, true))
	o.take(held)
	var infoID int64
	for _, raw := range held.send {
		id, method, session := esOutbound(raw)
		if method == "Target.getTargetInfo" && session == "RELAY" {
			infoID = id
		}
	}
	if infoID == 0 {
		t.Fatal("expected identity post-read getTargetInfo while the reply is held")
	}
	learn(&o)
	g.mu.Lock()
	after := g.docIdentityTaint[esTarget]
	g.mu.Unlock()
	if after == before {
		t.Fatalf("learn must bump docIdentityTaint (before=%d after=%d)", before, after)
	}
	if esSessionCut(g) {
		t.Fatalf("learn during the hold must not cut before identity settle (payloads=%v)", o.payloads)
	}
	esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, esOrdinaryB)), esOrdinaryB)
	o.notePayloadsFor(esOrdinaryB)
	joined := strings.Join(o.payloads, "")
	if strings.Contains(joined, "committed Chrome's error page for a protected URL while this command ran") {
		t.Fatalf("window taint must cut via identity taint, not outlived: payloads=%v", o.payloads)
	}
	if o.delivered {
		t.Fatalf("window taint must drop the reply even when post-read is ordinary: payloads=%v", o.payloads)
	}
	if !esSessionCut(g) {
		t.Fatalf("window taint must cut the session (refused=%t payloads=%v)", o.refused, o.payloads)
	}
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
		switch {
		case method == "Target.getTargetInfo" && session == "RELAY":
			esDrain(g, o, g.fromBrowser(esTargetInfoAnswer(id, answerURL)), answerURL)
		case method == "Page.getFrameTree" && (session == esNavSession || session == "RELAY"):
			loader := "L-POST"
			if nt := g.navTargets[esTarget]; nt != nil && nt.loaderID != "" {
				loader = nt.loaderID
			}
			esDrain(g, o, g.fromBrowser(esFrameTreeAnswer(id, session, answerURL, loader)), answerURL)
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

const (
	esRetriableMark = "Execution context was destroyed"
	esHoldCap       = 256
	esReadID2       = 72
	esReadID3       = 73
)

func esSessionCut(g *guard) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.sessions[esSession]
	return s == nil || s.refused || g.refused[esSession]
}

func esHasRetriable(o esOutcome) bool {
	if strings.Contains(o.refusal, esRetriableMark) {
		return true
	}
	for _, p := range o.payloads {
		if strings.Contains(p, esRetriableMark) {
			return true
		}
	}
	return false
}

func esFrameTreeAnswer(id int64, session, url, loader string) []byte {
	return esEncode(map[string]any{
		"id":        id,
		"sessionId": session,
		"result": map[string]any{
			"frameTree": map[string]any{
				"frame": map[string]any{
					"id":             esTarget,
					"loaderId":       loader,
					"url":            url,
					"securityOrigin": "://",
					"mimeType":       "text/html",
				},
			},
		},
	})
}

func esIssueContent(g *guard, o *esOutcome, id int64, cmd esCommand, live string, committed bool) {
	pv := g.fromCommand(&command{id: id, method: cmd.method, session: esSession, params: cmd.params}, id, true)
	if pv.reply != nil {
		o.refused = true
		o.payloads = append(o.payloads, string(pv.reply))
		o.refusal = string(pv.reply)
		return
	}
	if pv.wait != nil {
		o.waited = true
		return
	}
	if pv.forward != nil {
		o.forwarded = true
		reply := esContentReply(cmd, id, live, committed)
		esDrain(g, o, g.fromBrowser(reply), live)
	}
}

func TestExistingSessionBaselineUpdatesOnOrdinaryNavigation(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	esDeliverLearn(g, &o, esOrdinaryB, esEventsBoth)
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	esIssueContent(g, &o, int64(esReadID), cmd, esOrdinaryB, true)
	o.notePayloadsFor(esOrdinaryB)
	if o.delivered && strings.Contains(strings.Join(o.payloads, ""), esSecret) {
		t.Fatalf("ordinary baseline update must not deliver protected content")
	}
	if esSessionCut(g) {
		t.Fatalf("ordinary→ordinary must not cut the session")
	}
	if o.refused && !esHasRetriable(o) && strings.Contains(o.refusal, "slicc: refused:") {
		t.Fatalf("after the guard learns an ordinary commit, admit baseline must move with it so a later read is not a hard refuse: %s", o.refusal)
	}
	g.mu.Lock()
	base, ok := g.docBaselines[esTarget]
	g.mu.Unlock()
	if !ok || base.url != esOrdinaryB {
		t.Fatalf("after ordinary navigation learn+read, baseline must advance to %q: ok=%t url=%q", esOrdinaryB, ok, base.url)
	}
}

func TestExistingSessionOutcomeAProtectedCuts(t *testing.T) {
	for _, live := range esProtectedURLs() {
		live := live
		t.Run(live, func(t *testing.T) {
			g := esGuard(t)
			_ = esAdmit(t, g)
			var o esOutcome
			cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "document.body.innerText"}}
			esIssueContent(g, &o, int64(esReadID), cmd, live, true)
			o.notePayloadsFor(live)
			if o.delivered {
				t.Fatalf("outcome (a): never-learn must drop protected content before the client sees it: %v", o.payloads)
			}
			if !esSessionCut(g) {
				t.Fatalf("outcome (a): protected or unaccountable identity must cut the session (refused=%t mechanism=%s)", o.refused, o.mechanism)
			}
		})
	}
}

func TestExistingSessionOutcomeBOrdinaryIdentityChangeAcceptsAndAdvancesBaseline(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	pv := g.fromCommand(&command{id: int64(esReadID), method: cmd.method, session: esSession, params: cmd.params}, int64(esReadID), true)
	if pv.reply != nil {
		t.Fatalf("admit on ordinary must forward: %s", pv.reply)
	}
	if pv.forward == nil {
		t.Fatalf("expected forward")
	}
	esDrain(g, &o, g.fromBrowser(esContentReply(cmd, int64(esReadID), esOrdinaryB, true)), esOrdinaryB)
	o.notePayloadsFor(esOrdinaryB)
	if esSessionCut(g) {
		t.Fatalf("outcome (b): ordinary identity change with a clean window must not cut")
	}
	if esHasRetriable(o) {
		t.Fatalf("outcome (b): ordinary→ordinary with no protected watch in the window must accept, not retriable: payloads=%v", o.payloads)
	}
	if !o.delivered && !strings.Contains(strings.Join(o.payloads, ""), fmt.Sprintf(`"id":%d`, esReadID)) {
		t.Fatalf("outcome (b): expected the held reply to be released to the client: payloads=%v", o.payloads)
	}
	g.mu.Lock()
	base, ok := g.docBaselines[esTarget]
	g.mu.Unlock()
	if !ok || base.url != esOrdinaryB {
		t.Fatalf("outcome (b): baseline must advance to the post-read ordinary identity: ok=%t url=%q", ok, base.url)
	}
	pv2 := g.fromCommand(&command{id: int64(esReadID2), method: cmd.method, session: esSession, params: cmd.params}, int64(esReadID2), true)
	if pv2.reply != nil {
		t.Fatalf("outcome (b): session must remain usable after accept+baseline advance: %s", pv2.reply)
	}
}

func TestExistingSessionWindowTaintProtectedEventCutsDespiteOrdinaryPostRead(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	esWindowTaintThenOrdinaryPostRead(t, g, func(o *esOutcome) {
		esDeliverLearn(g, o, esProtectedURLs()[0], esEventsFrameOnly)
	})
}

func TestExistingSessionWindowTaintUnparseableURLCutsDespiteOrdinaryPostRead(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	esWindowTaintThenOrdinaryPostRead(t, g, func(o *esOutcome) {
		esDrain(g, o, g.fromBrowser(esFrameNavigated("view-source:!!!not-a-url!!!", "L-BAD")), "view-source:!!!not-a-url!!!")
	})
}

func TestExistingSessionWindowTaintEmptyToWrappedUnresolvableCutsDespiteOrdinaryPostRead(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	esWindowTaintThenOrdinaryPostRead(t, g, func(o *esOutcome) {
		esDrain(g, o, g.fromBrowser(esFrameNavigated("blob:", "L-WRAP")), "blob:")
	})
}

func TestExistingSessionWindowTaintChromeErrorProtectedFailedURLDoesNotTaint(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	pv := g.fromCommand(&command{id: int64(esReadID), method: cmd.method, session: esSession, params: cmd.params}, int64(esReadID), true)
	if pv.forward == nil {
		t.Fatalf("admit must forward: reply=%s", pv.reply)
	}
	g.mu.Lock()
	before := g.docIdentityTaint[esTarget]
	g.mu.Unlock()
	failed := "https://seven.sliccy.ai/doc"
	held := g.fromBrowser(esContentReply(cmd, int64(esReadID), esOrdinary, true))
	o.take(held)
	var infoID int64
	for _, raw := range held.send {
		id, method, session := esOutbound(raw)
		if method == "Target.getTargetInfo" && session == "RELAY" {
			infoID = id
		}
	}
	if infoID == 0 {
		t.Fatal("expected identity post-read getTargetInfo while the trigger reply is held")
	}
	esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable("chrome-error://chromewebdata/", "L-ERR", failed)), failed)
	g.mu.Lock()
	after := g.docIdentityTaint[esTarget]
	g.mu.Unlock()
	if after != before {
		t.Fatalf("chrome-error for a blocked protected URL must not bump identity taint (blocked≠document): before=%d after=%d", before, after)
	}
	esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, "chrome-error://chromewebdata/")), "chrome-error://chromewebdata/")
	if esSessionCut(g) {
		t.Fatalf("chrome-error protected unreachable must not cut via identity taint")
	}
	if esHasRetriable(o) || strings.Contains(strings.Join(o.payloads, ""), "slicc: refused:") {
		t.Fatalf("trigger held across a protected error-page commit must stay OK: payloads=%v", o.payloads)
	}
}

func TestExistingSessionWindowTaintChromeErrorOrdinaryFailedURLDoesNotTaint(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	pv := g.fromCommand(&command{id: int64(esReadID), method: cmd.method, session: esSession, params: cmd.params}, int64(esReadID), true)
	if pv.forward == nil {
		t.Fatalf("admit must forward: reply=%s", pv.reply)
	}
	g.mu.Lock()
	before := g.docIdentityTaint[esTarget]
	g.mu.Unlock()
	failed := esOrdinary + "/missing"
	esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable("chrome-error://chromewebdata/", "L-ERR", failed)), failed)
	g.mu.Lock()
	after := g.docIdentityTaint[esTarget]
	g.mu.Unlock()
	if after != before {
		t.Fatalf("chrome-error ordinary failed URL must not bump identity taint: before=%d after=%d", before, after)
	}
	if esSessionCut(g) {
		t.Fatalf("chrome-error with an ordinary failed URL must not cut the session")
	}
}

func TestExistingSessionOutlivedProtectedErrorPageCommitKeepsTrigger(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	shot := esCommand{label: "Page.captureScreenshot", method: "Page.captureScreenshot", params: map[string]any{}}
	trig := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	pvShot := g.fromCommand(&command{id: int64(esReadID2), method: shot.method, session: esSession, params: shot.params}, int64(esReadID2), true)
	if pvShot.forward == nil {
		t.Fatalf("screenshot admit must forward: reply=%s", pvShot.reply)
	}
	pvTrig := g.fromCommand(&command{id: int64(esReadID), method: trig.method, session: esSession, params: trig.params}, int64(esReadID), true)
	if pvTrig.forward == nil {
		t.Fatalf("trigger admit must forward: reply=%s", pvTrig.reply)
	}
	failed := "https://seven.sliccy.ai/doc"
	held := g.fromBrowser(esContentReply(trig, int64(esReadID), esOrdinary, true))
	o.take(held)
	var infoID int64
	for _, raw := range held.send {
		id, method, session := esOutbound(raw)
		if method == "Target.getTargetInfo" && session == "RELAY" {
			infoID = id
		}
	}
	if infoID == 0 {
		t.Fatal("expected identity post-read getTargetInfo while the trigger reply is held")
	}
	esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable("chrome-error://chromewebdata/", "L-ERR", failed)), failed)
	esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, "chrome-error://chromewebdata/")), "chrome-error://chromewebdata/")
	trigSeen, trigRefused := false, false
	for _, p := range o.payloads {
		if !strings.Contains(p, fmt.Sprintf(`"id":%d`, esReadID)) {
			continue
		}
		trigSeen = true
		if strings.Contains(p, "slicc: refused:") {
			trigRefused = true
		}
	}
	if !trigSeen || trigRefused {
		t.Fatalf("stock outlived fixture: trigger evaluate must stay OK after protected error-page commit: payloads=%v", o.payloads)
	}
	esDrain(g, &o, g.fromBrowser(esContentReply(shot, int64(esReadID2), esOrdinary, true)), esOrdinary)
	shotOK, shotOutlived := false, false
	for _, p := range o.payloads {
		if !strings.Contains(p, fmt.Sprintf(`"id":%d`, esReadID2)) {
			continue
		}
		if strings.Contains(p, "committed Chrome's error page for a protected URL while this command ran") {
			shotOutlived = true
		} else if !strings.Contains(p, "slicc: refused:") {
			shotOK = true
		}
	}
	if shotOK || !shotOutlived {
		t.Fatalf("stock outlived fixture: delayed screenshot must be refused with outlived text: payloads=%v", o.payloads)
	}
}

const esErrPageSecret = "ERRTOK9f3a2b"

func esChromeErrorFailedURL() string {
	return "https://seven.sliccy.ai/doc/" + esErrPageSecret + "/x?tok=" + esErrPageSecret
}

func esErrorPageContentReply(cmd esCommand, id int64, failed string) []byte {
	switch cmd.method {
	case "DOM.getDocument":
		return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{"root": map[string]any{"nodeId": 1, "documentURL": "chrome-error://chromewebdata/", "nodeName": "HTML", "children": []any{map[string]any{"nodeValue": failed}}}}})
	case "DOM.getOuterHTML":
		return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{"outerHTML": "<html><body>This site can’t be reached " + failed + "</body></html>"}})
	case "Page.captureScreenshot":
		return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{"data": "shot-" + failed}})
	case "Accessibility.getFullAXTree":
		return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{"nodes": []any{map[string]any{"name": map[string]any{"value": failed}}}}})
	default:
		return esEncode(map[string]any{"id": id, "sessionId": esSession, "result": map[string]any{"value": failed}})
	}
}

func esHoldThenSettleChromeError(t *testing.T, g *guard, o *esOutcome, cmd esCommand, id int64, reply []byte, eventBeforeReply bool, neverLearn bool) {
	t.Helper()
	pv := g.fromCommand(&command{id: id, method: cmd.method, session: esSession, params: cmd.params}, id, true)
	if pv.forward == nil {
		t.Fatalf("admit must forward: reply=%s", pv.reply)
	}
	failed := esChromeErrorFailedURL()
	if eventBeforeReply {
		esDrain(g, o, g.fromBrowser(esFrameNavigatedUnreachable("chrome-error://chromewebdata/", "L-ERR", failed)), failed)
	}
	held := g.fromBrowser(reply)
	o.take(held)
	var infoID int64
	for _, raw := range held.send {
		askID, method, session := esOutbound(raw)
		if method == "Target.getTargetInfo" && session == "RELAY" {
			infoID = askID
		}
	}
	if infoID == 0 {
		t.Fatal("expected identity post-read getTargetInfo while reply is held")
	}
	if !neverLearn && !eventBeforeReply {
		esDrain(g, o, g.fromBrowser(esFrameNavigatedUnreachable("chrome-error://chromewebdata/", "L-ERR", failed)), failed)
	}
	esDrain(g, o, g.fromBrowser(esTargetInfoAnswer(infoID, "chrome-error://chromewebdata/")), "chrome-error://chromewebdata/")
}

func TestExistingSessionChromeErrorReplyBeforeEventDuringHoldDelivers(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	esHoldThenSettleChromeError(t, g, &o, cmd, int64(esReadID), esContentReply(cmd, int64(esReadID), esOrdinary, true), false, false)
	if esSessionCut(g) {
		t.Fatal("reply-before-event must not cut")
	}
	if esHasRetriable(o) || strings.Contains(strings.Join(o.payloads, ""), "slicc: refused:") {
		t.Fatalf("reply-before-event with commit during hold must deliver: payloads=%v", o.payloads)
	}
	if !strings.Contains(strings.Join(o.payloads, ""), fmt.Sprintf(`"id":%d`, esReadID)) {
		t.Fatalf("expected delivered trigger reply: payloads=%v", o.payloads)
	}
}

func TestExistingSessionChromeErrorReplyAfterEventRefusesRetriable(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	esHoldThenSettleChromeError(t, g, &o, cmd, int64(esReadID), esContentReply(cmd, int64(esReadID), esOrdinary, true), true, false)
	if esSessionCut(g) {
		t.Fatal("reply-after-event must not cut")
	}
	if !esHasRetriable(o) {
		t.Fatalf("reply-after-event must retriable-refuse: payloads=%v", o.payloads)
	}
}

func TestExistingSessionChromeErrorNeverLearnSecretRefused(t *testing.T) {
	failed := esChromeErrorFailedURL()
	if !strings.Contains(failed, esErrPageSecret) || strings.Count(failed, esErrPageSecret) < 2 {
		t.Fatal("failed URL must carry the secret in both path and query")
	}
	cmds := []esCommand{
		{label: "DOM.getDocument", method: "DOM.getDocument", params: map[string]any{}},
		{label: "DOM.getOuterHTML", method: "DOM.getOuterHTML", params: map[string]any{"nodeId": json.Number("1")}},
		{label: "Page.captureScreenshot", method: "Page.captureScreenshot", params: map[string]any{}},
		{label: "Accessibility.getFullAXTree", method: "Accessibility.getFullAXTree", params: map[string]any{}},
	}
	for i, cmd := range cmds {
		t.Run(cmd.label, func(t *testing.T) {
			g := esGuard(t)
			_ = esAdmit(t, g)
			var o esOutcome
			id := int64(esReadID + i)
			reply := esErrorPageContentReply(cmd, id, failed)
			if !strings.Contains(string(reply), esErrPageSecret) {
				t.Fatal("fixture reply must carry the error-page secret")
			}
			esHoldThenSettleChromeError(t, g, &o, cmd, id, reply, false, true)
			joined := strings.Join(o.payloads, "")
			if esSessionCut(g) {
				t.Fatal("never-learn chrome-error must not cut")
			}
			if !esHasRetriable(o) {
				t.Fatalf("never-learn chrome-error must retriable-refuse: payloads=%v", o.payloads)
			}
			if strings.Contains(joined, esErrPageSecret) {
				t.Fatalf("never-learn chrome-error must not deliver error-page secret bytes: payloads=%v", o.payloads)
			}
		})
	}
}

func TestExistingSessionHistoryQuarantinedTreesRedactNotRefuseResourceTree(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	g.mu.Lock()
	if nt := g.navTargets[esTarget]; nt != nil {
		nt.historyLocked = true
		nt.protectedHistory = map[string]string{"1": refusedHistory}
	}
	g.mu.Unlock()
	origin := "https://seven.sliccy.ai"
	marker := origin + "/path-marker?query-marker"
	framePV := g.fromCommand(&command{id: int64(esReadID), method: "Page.getFrameTree", session: esSession, params: map[string]any{}}, int64(esReadID), true)
	if framePV.reply != nil || framePV.forward == nil {
		t.Fatalf("getFrameTree must remain a navigation control under history quarantine: reply=%s", framePV.reply)
	}
	resPV := g.fromCommand(&command{id: int64(esReadID2), method: "Page.getResourceTree", session: esSession, params: map[string]any{}}, int64(esReadID2), true)
	if resPV.reply != nil {
		t.Fatalf("getResourceTree must not be history-refused (scrub-only; was removed from navigationControls in 4aab87ca): %s", resPV.reply)
	}
	if resPV.forward == nil {
		t.Fatalf("getResourceTree must forward under history quarantine")
	}
	raw := esEncode(map[string]any{
		"id": int64(esReadID2), "sessionId": esSession,
		"result": map[string]any{"frameTree": map[string]any{
			"frame":     map[string]any{"id": esTarget, "url": marker, "urlFragment": "#fragment-marker", "securityOrigin": origin},
			"resources": []any{map[string]any{"url": origin + "/script-marker.js?query-marker#fragment-marker", "type": "Script"}},
		}},
	})
	scrubbed := string(g.scrub(raw))
	if strings.Contains(scrubbed, "marker") {
		t.Fatalf("scrub must origin-redact protected tree URLs: %s", scrubbed)
	}
	if !strings.Contains(scrubbed, origin) {
		t.Fatalf("scrub must keep origin: %s", scrubbed)
	}
	scriptPV := g.fromCommand(&command{id: int64(esReadID3), method: "Runtime.evaluate", session: esSession, params: map[string]any{"expression": "true"}}, int64(esReadID3), true)
	if scriptPV.reply == nil || !strings.Contains(string(scriptPV.reply), refusedHistory) {
		t.Fatalf("evaluate under history quarantine must refuse with history text: %s", scriptPV.reply)
	}
}

func TestExistingSessionSilentOrdinaryProtectedOrdinaryNoEvent_KnownResidual(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "document.body.innerText"}}
	esIssueContent(g, &o, int64(esReadID), cmd, esOrdinary, true)
	joined := strings.Join(o.payloads, "")
	if esSessionCut(g) || strings.Contains(joined, "slicc: refused:") || esHasRetriable(o) {
		t.Fatalf("KnownResidual documents that a clean-window ordinary post-read ACCEPTS the held reply (ordinary→protected→ordinary with no frameNavigated/targetInfoChanged). cut=%t payloads=%v — if this becomes a refuse/cut, update this KnownResidual test and the exposure statement deliberately; do not auto-green or auto-skip it", esSessionCut(g), o.payloads)
	}
	if !strings.Contains(joined, fmt.Sprintf(`"id":%d`, esReadID)) {
		t.Fatalf("KnownResidual (pinned green-as-documented, like PageBeforeTabProtectedHide_KnownResidual): asserts the documented residual that a silent ordinary→protected→ordinary hop (view-source:/blob:/filesystem: only) can still supply the body when no watch event reaches the guard; failure means the residual closed or the accept path broke — update this test and the exposure statement deliberately")
	}
}

func TestExistingSessionIndependentHoldsAndCoalescing(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	live := esProtectedURLs()[0]
	var forwards []struct {
		id  int64
		raw []byte
	}
	for _, id := range []int64{esReadID, esReadID2, esReadID3} {
		pv := g.fromCommand(&command{id: id, method: cmd.method, session: esSession, params: cmd.params}, id, true)
		if pv.reply != nil {
			t.Fatalf("parallel admits must not refuse before post-read: id=%d %s", id, pv.reply)
		}
		if pv.forward == nil {
			t.Fatalf("expected forward id=%d", id)
		}
		forwards = append(forwards, struct {
			id  int64
			raw []byte
		}{id, esContentReply(cmd, id, live, true)})
	}
	for _, f := range forwards {
		esDrain(g, &o, g.fromBrowser(f.raw), live)
	}
	var clientBefore int
	for _, p := range o.payloads {
		if strings.Contains(p, esSecret) {
			clientBefore++
		}
	}
	if clientBefore > 0 {
		t.Fatalf("independent holds: no covered reply may reach the client before a post-read taken after that reply: leaked=%d", clientBefore)
	}
	refused := false
	for _, p := range o.payloads {
		if strings.Contains(p, "slicc: refused:") || strings.Contains(p, esRetriableMark) {
			refused = true
		}
	}
	if esSessionCut(g) && clientBefore == 0 && !refused {
		t.Fatalf("holds must settle via post-read, not a silent cut")
	}
}

func TestExistingSessionHoldCapRetriableNoCut(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	live := esOrdinary
	var o esOutcome
	var sawRetriable bool
	for i := 0; i < esHoldCap+8; i++ {
		id := int64(1000 + i)
		pv := g.fromCommand(&command{id: id, method: cmd.method, session: esSession, params: cmd.params}, id, true)
		if pv.reply != nil {
			if strings.Contains(string(pv.reply), esRetriableMark) {
				sawRetriable = true
			}
			continue
		}
		if pv.forward == nil {
			continue
		}
		o.take(g.fromBrowser(esContentReply(cmd, id, live, true)))
		if esSessionCut(g) {
			t.Fatalf("hold-cap trip must be retriable without cutting the session")
		}
	}
	for _, p := range o.payloads {
		if strings.Contains(p, esRetriableMark) {
			sawRetriable = true
		}
		if strings.Contains(p, esSecret) {
			t.Fatalf("cap trip must not forward a held protected result")
		}
	}
	if !sawRetriable {
		t.Fatalf("exceeding the per-session hold cap (%d) must fail closed with a retriable refusal", esHoldCap)
	}
}

func TestExistingSessionIdentityAbsentCases(t *testing.T) {
	t.Run("empty-empty-ordinary-blank-passes", func(t *testing.T) {
		g := esGuard(t)
		var o esOutcome
		esDrain(g, &o, g.fromBrowser(esRelay("Target.targetCreated", "about:blank")), "about:blank")
		esArmNav(g, "about:blank")
		pv := g.fromCommand(&command{id: esAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": esTarget, "flatten": true}}, esAttachID, true)
		if pv.reply != nil && strings.Contains(string(pv.reply), "slicc: refused:") {
			t.Fatalf("blank attach must admit: %s", pv.reply)
		}
		esDrain(g, &o, g.fromBrowser(esAttached("about:blank")), "about:blank")
		esDrain(g, &o, g.fromBrowser(esAttachReply()), "about:blank")
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		esIssueContent(g, &o, int64(esReadID), cmd, "about:blank", false)
		if esSessionCut(g) {
			t.Fatalf("unchanged empty identity on inheriting blank must not cut")
		}
	})
	t.Run("unreadable-identity-fail-closed", func(t *testing.T) {
		o := esRunCell(t, esCommand{label: "DOM.getDocument", method: "DOM.getDocument", params: map[string]any{}}, esProtectedURLs()[0], esNeverLearn, esEventsBoth)
		if o.delivered {
			t.Fatalf("missing/unreadable post-read identity must not deliver protected content")
		}
	})
}

func TestExistingSessionChromeErrorSkipsIdentityCheck(t *testing.T) {
	g := esGuard(t)
	announced := "chrome-error://chromewebdata/"
	live := "https://seven.sliccy.ai/doc"
	var o esOutcome
	esDrain(g, &o, g.fromBrowser(esRelay("Target.targetCreated", announced)), live)
	ready := make(chan struct{})
	close(ready)
	nt := &navTarget{
		target:      esTarget,
		session:     esNavSession,
		ready:       ready,
		done:        true,
		documentURL: announced,
		errorURL:    live,
	}
	g.navTargets[esTarget] = nt
	g.navSessions[esNavSession] = nt
	g.setErrorProofLocked(esTarget, errorProofOrdinary)
	_ = g.fromCommand(&command{id: esAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": esTarget, "flatten": true}}, esAttachID, true)
	esDrain(g, &o, g.fromBrowser(esAttached(announced)), live)
	esDrain(g, &o, g.fromBrowser(esAttachReply()), live)
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	esIssueContent(g, &o, int64(esReadID), cmd, announced, false)
	o.notePayloadsFor(live)
	if o.delivered && strings.Contains(strings.Join(o.payloads, ""), esSecret) {
		t.Fatalf("chrome-error path must not expose protected content")
	}
}

func TestExistingSessionMainFrameOnly(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	child := "ES-IFRAME"
	esDrain(g, &o, g.fromBrowser(esEncode(map[string]any{
		"sessionId": esNavSession,
		"method":    "Page.frameNavigated",
		"params": map[string]any{
			"frame": map[string]any{
				"id":             child,
				"parentId":       esTarget,
				"loaderId":       "L-IFRAME",
				"url":            "view-source:https://vault.sliccy.ai/iframe",
				"securityOrigin": "://",
			},
			"type": "Navigation",
		},
	})), "view-source:https://vault.sliccy.ai/iframe")
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	esIssueContent(g, &o, int64(esReadID), cmd, esOrdinary, false)
	o.notePayloadsFor(esOrdinary)
	if o.delivered && strings.Contains(strings.Join(o.payloads, ""), esSecret) {
		t.Fatalf("iframe navigation must not by itself leak through a main-frame content command")
	}
}

func TestExistingSessionPostReadOnRelayOrWatchNotClient(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	live := esProtectedURLs()[0]
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	pv := g.fromCommand(&command{id: int64(esReadID), method: cmd.method, session: esSession, params: cmd.params}, int64(esReadID), true)
	if pv.forward == nil && pv.reply == nil {
		t.Fatalf("expected admit")
	}
	var clientPost bool
	var guardPost bool
	inspect := func(send [][]byte) {
		for _, raw := range send {
			_, method, session := esOutbound(raw)
			if method != "Page.getFrameTree" && method != "Target.getTargetInfo" {
				continue
			}
			switch session {
			case esSession:
				clientPost = true
			case "RELAY", esNavSession:
				guardPost = true
			}
		}
	}
	inspect(pv.send)
	if pv.forward != nil {
		v := g.fromBrowser(esContentReply(cmd, int64(esReadID), live, true))
		o.take(v)
		inspect(v.send)
		for _, raw := range v.send {
			id, method, session := esOutbound(raw)
			if method == "Page.getFrameTree" && (session == "RELAY" || session == esNavSession) {
				guardPost = true
				esDrain(g, &o, g.fromBrowser(esFrameTreeAnswer(id, session, live, "L-POST")), live)
			}
			if method == "Target.getTargetInfo" && session == "RELAY" {
				guardPost = true
				esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(id, live)), live)
			}
			if method == "Target.getTargetInfo" && session == esSession {
				clientPost = true
			}
		}
	}
	o.notePayloadsFor(live)
	if clientPost {
		t.Fatalf("post-read must use the guard watch/relay session, never the client session")
	}
	if o.delivered {
		t.Fatalf("post-read path must drop protected content; guardPost=%t", guardPost)
	}
	if !guardPost && !o.refused {
		t.Fatalf("expected a post-read on RELAY or %s before releasing a covered reply", esNavSession)
	}
}

func TestExistingSessionPostReadDeadlineFailClosed(t *testing.T) {
	t.Setenv(HoldTimeoutEnv, "5ms")
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	live := esProtectedURLs()[0]
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	pv := g.fromCommand(&command{id: int64(esReadID), method: cmd.method, session: esSession, params: cmd.params}, int64(esReadID), true)
	if pv.forward != nil {
		esDrain(g, &o, g.fromBrowser(esContentReply(cmd, int64(esReadID), live, true)), live)
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		o.notePayloadsFor(live)
		if o.refused || esSessionCut(g) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	o.notePayloadsFor(live)
	if o.delivered {
		t.Fatalf("deadline must fail closed without forwarding protected content")
	}
	if !o.refused && !esSessionCut(g) {
		t.Fatalf("post-read deadline must refuse (and cut when the document cannot be classified ordinary)")
	}
}
