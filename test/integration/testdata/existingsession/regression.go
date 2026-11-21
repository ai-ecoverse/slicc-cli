package cdp

import (
	"encoding/json"
	"fmt"
	"strconv"
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
	return esFrameNavigatedUnreachableType(url, loader, unreachable, "Navigation")
}

func esFrameNavigatedUnreachableType(url, loader, unreachable, navType string) []byte {
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
	if navType == "" {
		navType = "Navigation"
	}
	return esEncode(map[string]any{
		"sessionId": esNavSession,
		"method":    "Page.frameNavigated",
		"params": map[string]any{
			"frame": frame,
			"type":  navType,
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
		fetchArmed:  true,
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

func TestExistingSessionOrdinaryNavigationStaysReadable(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	esDeliverLearn(g, &o, esOrdinaryB, esEventsBoth)
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	esIssueContent(g, &o, int64(esReadID), cmd, esOrdinaryB, true)
	o.notePayloadsFor(esOrdinaryB)
	if o.delivered && strings.Contains(strings.Join(o.payloads, ""), esSecret) {
		t.Fatalf("ordinary navigation must not deliver protected content")
	}
	if esSessionCut(g) {
		t.Fatalf("ordinary→ordinary must not cut the session")
	}
	if o.refused && !esHasRetriable(o) && strings.Contains(o.refusal, "slicc: refused:") {
		t.Fatalf("after ordinary commit learn+read, post-read URL class + taint must accept, not hard refuse: %s", o.refusal)
	}
	pv2 := g.fromCommand(&command{id: int64(esReadID2), method: cmd.method, session: esSession, params: cmd.params}, int64(esReadID2), true)
	if pv2.reply != nil {
		t.Fatalf("session must remain usable after ordinary navigation accept: %s", pv2.reply)
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

func TestExistingSessionOutcomeBOrdinaryIdentityChangeAccepts(t *testing.T) {
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
	pv2 := g.fromCommand(&command{id: int64(esReadID2), method: cmd.method, session: esSession, params: cmd.params}, int64(esReadID2), true)
	if pv2.reply != nil {
		t.Fatalf("outcome (b): session must remain usable after post-read accept: %s", pv2.reply)
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

func esHoldCoveredGetInfoID(t *testing.T, g *guard, o *esOutcome, id int64, cmd esCommand, reply []byte) int64 {
	t.Helper()
	pv := g.fromCommand(&command{id: id, method: cmd.method, session: esSession, params: cmd.params}, id, true)
	if pv.forward == nil {
		t.Fatalf("admit must forward: reply=%s", pv.reply)
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
	return infoID
}

func esChromeErrorBlockedDest() string {
	return "https://vault.sliccy.ai/doc?n=redirect-secret"
}

func TestExistingSessionChromeErrorTargetInfoLagsProtectedUnreachableNoCut(t *testing.T) {
	failed := esChromeErrorBlockedDest()
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), esOrdinary, true))
	esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable("chrome-error://chromewebdata/", "L-ERR", failed)), failed)
	esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, failed)), failed)
	if esSessionCut(g) {
		t.Fatal("targetInfo-lags: live chrome-error with getTargetInfo reporting the recorded unreachable must not cut")
	}
	if !esHasRetriable(o) {
		t.Fatalf("targetInfo-lags: held read must be retriable, never delivered: payloads=%v", o.payloads)
	}
	if strings.Contains(strings.Join(o.payloads, ""), esSecret) {
		t.Fatalf("targetInfo-lags: must not deliver held content: payloads=%v", o.payloads)
	}
	pv := g.fromCommand(&command{id: int64(esReadID2), method: "Page.reload", session: esSession, params: map[string]any{}}, int64(esReadID2), true)
	if pv.reply != nil && strings.Contains(string(pv.reply), "slicc: refused:") {
		t.Fatalf("targetInfo-lags: Page.reload must stay usable: %s", pv.reply)
	}
	pv2 := g.fromCommand(&command{id: int64(esReadID3), method: "Page.navigate", session: esSession, params: map[string]any{"url": esOrdinaryB}}, int64(esReadID3), true)
	if pv2.reply != nil && strings.Contains(string(pv2.reply), "slicc: refused:") {
		t.Fatalf("targetInfo-lags: Page.navigate to a safe URL must stay usable: %s", pv2.reply)
	}
}

func TestExistingSessionChromeErrorProtectedInfoNoEvidenceCuts(t *testing.T) {
	live := esChromeErrorBlockedDest()
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), esOrdinary, true))
	esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, live)), live)
	if !esSessionCut(g) {
		t.Fatal("protected getTargetInfo with no chrome-error nav evidence must cut")
	}
}

func TestExistingSessionChromeErrorSameURLTargetInfoTaintMatchBeforeTainted(t *testing.T) {
	failed := esChromeErrorBlockedDest()
	chromeDoc := "chrome-error://chromewebdata/"
	t.Run("match-despite-taint-recovery", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), esOrdinary, true))
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable(chromeDoc, "L-ERR", failed)), failed)
		g.mu.Lock()
		taintBefore := g.docIdentityTaint[esTarget]
		placeBefore := g.docPlaceSeq[esTarget]
		g.mu.Unlock()
		esFireTargetInfoChanged(g, &o, failed, chromeDoc, failed)
		g.mu.Lock()
		taintAfter := g.docIdentityTaint[esTarget]
		placeAfter := g.docPlaceSeq[esTarget]
		live := g.docIdentityLiveChromeErrorNavLocked(esTarget)
		g.mu.Unlock()
		if taintAfter <= taintBefore {
			t.Fatalf("same-url targetInfo while live chrome-error must bump taint (watch before same-place skip): before=%d after=%d", taintBefore, taintAfter)
		}
		if placeAfter != placeBefore || !live {
			t.Fatalf("same-url targetInfo must not bump place or drop live chrome-error: before=%d after=%d live=%v", placeBefore, placeAfter, live)
		}
		esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, failed)), failed)
		if esSessionCut(g) {
			t.Fatal("Match arm must precede tainted: same-url targetInfo taint must not cut the Match settle")
		}
		if !esHasRetriable(o) {
			t.Fatalf("Match settle after same-url taint bump must be retriable no-cut: payloads=%v", o.payloads)
		}
		pv := g.fromCommand(&command{id: int64(esReadID2), method: "Page.reload", session: esSession, params: map[string]any{}}, int64(esReadID2), true)
		if pv.reply != nil && strings.Contains(string(pv.reply), "slicc: refused:") {
			t.Fatalf("recovery Page.reload must stay usable: %s", pv.reply)
		}
		pv2 := g.fromCommand(&command{id: int64(esReadID3), method: "Page.navigate", session: esSession, params: map[string]any{"url": esOrdinaryB}}, int64(esReadID3), true)
		if pv2.reply != nil && strings.Contains(string(pv2.reply), "slicc: refused:") {
			t.Fatalf("recovery Page.navigate must stay usable: %s", pv2.reply)
		}
	})
	t.Run("non-error-release-after-taint-cuts", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), esOrdinary, true))
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable(chromeDoc, "L-ERR", failed)), failed)
		g.mu.Lock()
		taintBefore := g.docIdentityTaint[esTarget]
		g.mu.Unlock()
		esFireTargetInfoChanged(g, &o, failed, chromeDoc, failed)
		g.mu.Lock()
		taintAfter := g.docIdentityTaint[esTarget]
		g.mu.Unlock()
		if taintAfter <= taintBefore {
			t.Fatalf("same-url targetInfo must bump taint: before=%d after=%d", taintBefore, taintAfter)
		}
		esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, esOrdinaryB)), esOrdinaryB)
		if !esSessionCut(g) {
			t.Fatal("non-error release for a hold tainted by same-url targetInfo must cut (fail-closed)")
		}
	})
}

func TestExistingSessionChromeErrorProtectedInfoDifferentUnreachableCuts(t *testing.T) {
	failed := esChromeErrorBlockedDest()
	other := "https://seven.sliccy.ai/doc?n=other-blocked"
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), esOrdinary, true))
	esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable("chrome-error://chromewebdata/", "L-ERR", failed)), failed)
	esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, other)), other)
	if !esSessionCut(g) {
		t.Fatal("chrome-error whose recorded unreachable differs from getTargetInfo protected URL must cut")
	}
}

func TestExistingSessionChromeErrorThenLaterProtectedCommitCuts(t *testing.T) {
	failed := esChromeErrorBlockedDest()
	t.Run("later-protected-document", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), esOrdinary, true))
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable("chrome-error://chromewebdata/", "L-ERR", failed)), failed)
		esDrain(g, &o, g.fromBrowser(esFrameNavigated(failed, "L-PROT")), failed)
		esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, failed)), failed)
		if !esSessionCut(g) {
			t.Fatal("chrome-error then a later protected document commit must cut")
		}
	})
	t.Run("stale-place", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), esOrdinary, true))
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable("chrome-error://chromewebdata/", "L-ERR", failed)), failed)
		esDrain(g, &o, g.fromBrowser(esRelay("Target.targetInfoChanged", esOrdinaryB)), esOrdinaryB)
		esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, failed)), failed)
		if !esSessionCut(g) {
			t.Fatal("chrome-error whose place seq is no longer latest must cut")
		}
	})
}

func esDrainTargetInfoKeepChromeErrorDoc(g *guard, o *esOutcome, v browserVerdict, infoURL, chromeDoc, failed string) {
	o.take(v)
	for _, raw := range v.send {
		id, method, session := esOutbound(raw)
		switch {
		case method == "Target.getTargetInfo" && session == "RELAY":
			esDrainTargetInfoKeepChromeErrorDoc(g, o, g.fromBrowser(esTargetInfoAnswer(id, infoURL)), infoURL, chromeDoc, failed)
		case method == "Page.getFrameTree" && (session == esNavSession || session == "RELAY"):
			loader := "L-POST"
			if nt := g.navTargets[esTarget]; nt != nil && nt.loaderID != "" {
				loader = nt.loaderID
			}
			tree := esEncode(map[string]any{
				"id": id, "sessionId": session,
				"result": map[string]any{"frameTree": map[string]any{"frame": map[string]any{
					"id": esTarget, "loaderId": loader, "url": chromeDoc, "securityOrigin": "://", "unreachableUrl": failed, "mimeType": "text/html",
				}}},
			})
			esDrainTargetInfoKeepChromeErrorDoc(g, o, g.fromBrowser(tree), infoURL, chromeDoc, failed)
		}
	}
}

func esFireTargetInfoChanged(g *guard, o *esOutcome, url, chromeDoc, failed string) {
	esDrainTargetInfoKeepChromeErrorDoc(g, o, g.fromBrowser(esRelay("Target.targetInfoChanged", url)), url, chromeDoc, failed)
}

func esFireTargetInfoChangedPlaceOnly(g *guard, o *esOutcome, url string) {
	v := g.fromBrowser(esRelay("Target.targetInfoChanged", url))
	o.take(v)
	for _, raw := range v.send {
		id, method, session := esOutbound(raw)
		if method == "Target.getTargetInfo" && session == "RELAY" {
			o.take(g.fromBrowser(esTargetInfoAnswer(id, url)))
		}
	}
}

func TestExistingSessionChromeErrorSamePlaceTargetInfoOrders(t *testing.T) {
	failed := esChromeErrorBlockedDest()
	chromeDoc := "chrome-error://chromewebdata/"
	other := "https://seven.sliccy.ai/doc?n=other-blocked"
	t.Run("nav-then-target-same", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), esOrdinary, true))
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable(chromeDoc, "L-ERR", failed)), failed)
		g.mu.Lock()
		placeBefore := g.docPlaceSeq[esTarget]
		g.mu.Unlock()
		esFireTargetInfoChangedPlaceOnly(g, &o, failed)
		g.mu.Lock()
		placeAfter := g.docPlaceSeq[esTarget]
		live := g.docIdentityLiveChromeErrorNavLocked(esTarget)
		g.mu.Unlock()
		if placeAfter != placeBefore || !live {
			t.Fatalf("nav then targetInfo(same as errorURL) must not bump place: before=%d after=%d live=%v", placeBefore, placeAfter, live)
		}
		esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, failed)), failed)
		if esSessionCut(g) {
			t.Fatal("nav then targetInfo(same as errorURL) must keep the chrome-error no-cut exemption")
		}
		if !esHasRetriable(o) {
			t.Fatalf("held read must be retriable: payloads=%v", o.payloads)
		}
	})
	t.Run("target-then-nav-same", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), esOrdinary, true))
		esFireTargetInfoChangedPlaceOnly(g, &o, failed)
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable(chromeDoc, "L-ERR", failed)), failed)
		g.mu.Lock()
		placeBefore := g.docPlaceSeq[esTarget]
		g.mu.Unlock()
		esFireTargetInfoChangedPlaceOnly(g, &o, failed)
		g.mu.Lock()
		placeAfter := g.docPlaceSeq[esTarget]
		live := g.docIdentityLiveChromeErrorNavLocked(esTarget)
		g.mu.Unlock()
		if placeAfter != placeBefore || !live {
			t.Fatalf("targetInfo(same) after nav must not bump place: before=%d after=%d live=%v", placeBefore, placeAfter, live)
		}
		esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, failed)), failed)
		if esSessionCut(g) {
			t.Fatal("targetInfo(same) then nav chrome-error must keep the no-cut exemption")
		}
		if !esHasRetriable(o) {
			t.Fatalf("held read must be retriable: payloads=%v", o.payloads)
		}
	})
	t.Run("nav-then-target-different", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), esOrdinary, true))
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable(chromeDoc, "L-ERR", failed)), failed)
		g.mu.Lock()
		chromePlace := g.docChromeErrorPlace[esTarget]
		doc := g.navTargets[esTarget].documentURL
		g.mu.Unlock()
		esFireTargetInfoChangedPlaceOnly(g, &o, other)
		g.mu.Lock()
		place := g.docPlaceSeq[esTarget]
		samePlace := g.docIdentityTargetURLIsLiveChromeErrorDestLocked(esTarget, failed)
		live := g.docIdentityLiveChromeErrorNavLocked(esTarget)
		docAfter := g.navTargets[esTarget].documentURL
		g.mu.Unlock()
		if docAfter != doc || docAfter != chromeDoc {
			t.Fatalf("place-only different targetInfo must not rewrite the chrome-error nav document: before=%q after=%q", doc, docAfter)
		}
		if place == chromePlace || samePlace || live {
			t.Fatalf("different target url must bump place and drop exemption: place=%d chromePlace=%d samePlace=%v live=%v", place, chromePlace, samePlace, live)
		}
		esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, failed)), failed)
		if !esSessionCut(g) {
			t.Fatal("after different-url place bump, settle with the failed URL must cut")
		}
	})
	t.Run("nav-then-same-then-protected-commit", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), esOrdinary, true))
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable(chromeDoc, "L-ERR", failed)), failed)
		esFireTargetInfoChangedPlaceOnly(g, &o, failed)
		esDrain(g, &o, g.fromBrowser(esFrameNavigated(failed, "L-PROT")), failed)
		esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, failed)), failed)
		if !esSessionCut(g) {
			t.Fatal("nav then same targetInfo then a new protected commit must cut")
		}
	})
	t.Run("nav-then-same-then-lagging-ordinary", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), esOrdinary, true))
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable(chromeDoc, "L-ERR", failed)), failed)
		esFireTargetInfoChangedPlaceOnly(g, &o, failed)
		esFireTargetInfoChangedPlaceOnly(g, &o, esOrdinaryB)
		g.mu.Lock()
		samePlace := g.docIdentityTargetURLIsLiveChromeErrorDestLocked(esTarget, failed)
		g.mu.Unlock()
		if samePlace {
			t.Fatal("same-place must not apply after a newer place (lagging same-url must not re-arm)")
		}
		esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, failed)), failed)
		if !esSessionCut(g) {
			t.Fatal("lagging targetInfo(old ordinary) after a newer place must not re-arm the exemption")
		}
	})
	t.Run("same-place-requires-chrome-error-doc", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable(chromeDoc, "L-ERR", failed)), failed)
		g.mu.Lock()
		g.navTargets[esTarget].documentURL = esOrdinaryB
		samePlace := g.docIdentityTargetURLIsLiveChromeErrorDestLocked(esTarget, failed)
		g.mu.Unlock()
		if samePlace {
			t.Fatal("same-place must not apply when the nav document is not chrome-error")
		}
	})
}

func TestExistingSessionChromeErrorDestFailedAlignSnapshotRewrite(t *testing.T) {
	recorded := esChromeErrorBlockedDest()
	rewritten := "https://seven.sliccy.ai/doc?n=other-blocked"
	chromeDoc := "chrome-error://chromewebdata/"
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable(chromeDoc, "L-ERR", recorded)), recorded)
	g.mu.Lock()
	nt := g.navTargets[esTarget]
	if nt == nil || g.docChromeErrorFailed[esTarget] != recorded || nt.errorURL != recorded {
		g.mu.Unlock()
		t.Fatal("expected nav-recorded Failed and errorURL to match the frameNavigated unreachable")
	}
	placeBefore := g.docPlaceSeq[esTarget]
	g.noteErrorPageSnapshotLocked(nt, chromeDoc, "://", rewritten)
	if nt.errorURL != rewritten || g.docChromeErrorFailed[esTarget] != recorded {
		g.mu.Unlock()
		t.Fatalf("snapshot rewrite must change errorURL without re-recording Failed: errorURL=%q Failed=%q", nt.errorURL, g.docChromeErrorFailed[esTarget])
	}
	g.mu.Unlock()
	esFireTargetInfoChangedPlaceOnly(g, &o, rewritten)
	g.mu.Lock()
	placeAfter := g.docPlaceSeq[esTarget]
	samePlace := g.docIdentityTargetURLIsLiveChromeErrorDestLocked(esTarget, rewritten)
	live := g.docIdentityLiveChromeErrorNavLocked(esTarget)
	g.mu.Unlock()
	if samePlace || live {
		t.Fatalf("Dest/Live must be false when Failed != errorURL after snapshot rewrite: samePlace=%v live=%v", samePlace, live)
	}
	if placeAfter == placeBefore {
		t.Fatalf("same-url targetInfo after snapshot rewrite of errorURL must bump place (Dest must not suppress): before=%d after=%d", placeBefore, placeAfter)
	}
}

func TestExistingSessionChromeErrorOrdinarySamePlaceTargetInfoOrders(t *testing.T) {
	failed := esChromeErrorOrdinaryFailedURL()
	chromeDoc := "chrome-error://chromewebdata/"
	t.Run("nav-then-target-same", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), failed, true))
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable(chromeDoc, "L-ERR", failed)), failed)
		g.mu.Lock()
		placeBefore := g.docPlaceSeq[esTarget]
		g.mu.Unlock()
		esFireTargetInfoChangedPlaceOnly(g, &o, failed)
		g.mu.Lock()
		placeAfter := g.docPlaceSeq[esTarget]
		live := g.docIdentityLiveChromeErrorNavLocked(esTarget)
		samePlace := g.docIdentityTargetURLIsLiveChromeErrorDestLocked(esTarget, failed)
		g.mu.Unlock()
		if placeAfter != placeBefore || !live || !samePlace {
			t.Fatalf("ordinary same-place: targetInfo(same as errorURL) must not bump place: before=%d after=%d live=%v samePlace=%v", placeBefore, placeAfter, live, samePlace)
		}
		esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, failed)), failed)
		if esSessionCut(g) {
			t.Fatal("ordinary same-place Match must not cut")
		}
		if esHasRetriable(o) {
			t.Fatalf("ordinary same-place settle must deliver the held reply: payloads=%v", o.payloads)
		}
		if !strings.Contains(strings.Join(o.payloads, ""), fmt.Sprintf(`"id":%d`, esReadID)) {
			t.Fatalf("ordinary same-place settle must return the held result: payloads=%v", o.payloads)
		}
	})
	t.Run("target-then-nav-same", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), failed, true))
		esFireTargetInfoChangedPlaceOnly(g, &o, failed)
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable(chromeDoc, "L-ERR", failed)), failed)
		g.mu.Lock()
		placeBefore := g.docPlaceSeq[esTarget]
		g.mu.Unlock()
		esFireTargetInfoChangedPlaceOnly(g, &o, failed)
		g.mu.Lock()
		placeAfter := g.docPlaceSeq[esTarget]
		live := g.docIdentityLiveChromeErrorNavLocked(esTarget)
		samePlace := g.docIdentityTargetURLIsLiveChromeErrorDestLocked(esTarget, failed)
		g.mu.Unlock()
		if placeAfter != placeBefore || !live || !samePlace {
			t.Fatalf("ordinary same-place after nav: before=%d after=%d live=%v samePlace=%v", placeBefore, placeAfter, live, samePlace)
		}
		esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, failed)), failed)
		if esSessionCut(g) {
			t.Fatal("ordinary same-place target-then-nav must not cut")
		}
		if esHasRetriable(o) {
			t.Fatalf("ordinary same-place target-then-nav must deliver: payloads=%v", o.payloads)
		}
	})
}

func TestExistingSessionChromeErrorOrdinarySamePlaceFailAndExpire(t *testing.T) {
	failed := esChromeErrorOrdinaryFailedURL()
	chromeDoc := "chrome-error://chromewebdata/"
	t.Run("fail", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), failed, true))
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable(chromeDoc, "L-ERR", failed)), failed)
		esFireTargetInfoChangedPlaceOnly(g, &o, failed)
		g.mu.Lock()
		if !g.docIdentityLiveChromeErrorNavLocked(esTarget) || !g.docIdentityTargetURLIsLiveChromeErrorDestLocked(esTarget, failed) {
			g.mu.Unlock()
			t.Fatal("expected live chrome-error + same-place after ordinary observe")
		}
		g.mu.Unlock()
		bad, _ := json.Marshal(map[string]any{"id": infoID, "sessionId": "RELAY", "error": map[string]any{"code": -32000, "message": "Target closed"}})
		esDrain(g, &o, g.fromBrowser(bad), failed)
		if esSessionCut(g) {
			t.Fatal("ordinary same-place fail path must not cut")
		}
		if !esHasRetriable(o) {
			t.Fatalf("ordinary same-place fail path must retriable: payloads=%v", o.payloads)
		}
	})
	t.Run("expire", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		_ = esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), failed, true))
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable(chromeDoc, "L-ERR", failed)), failed)
		esFireTargetInfoChangedPlaceOnly(g, &o, failed)
		g.mu.Lock()
		var holdKey callKey
		for k, h := range g.docIdentityHolds {
			if h != nil && h.target == esTarget {
				h.deadline = time.Now().Add(-time.Second)
				holdKey = k
				break
			}
		}
		g.flushAsync = func(send, page [][]byte) {
			for _, p := range page {
				o.payloads = append(o.payloads, string(p))
			}
			for _, s := range send {
				o.payloads = append(o.payloads, string(s))
			}
		}
		g.mu.Unlock()
		if holdKey.session == "" {
			t.Fatal("expected a held identity reply to expire")
		}
		g.expireDocIdentityHold(holdKey)
		if esSessionCut(g) {
			t.Fatal("ordinary same-place expire path must not cut")
		}
		if !esHasRetriable(o) {
			t.Fatalf("ordinary same-place expire path must retriable: payloads=%v", o.payloads)
		}
	})
}

func TestExistingSessionChromeErrorFailAndExpirePathsNoCut(t *testing.T) {
	failed := esChromeErrorBlockedDest()
	t.Run("fail", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), esOrdinary, true))
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable("chrome-error://chromewebdata/", "L-ERR", failed)), failed)
		bad, _ := json.Marshal(map[string]any{"id": infoID, "sessionId": "RELAY", "error": map[string]any{"code": -32000, "message": "Target closed"}})
		esDrain(g, &o, g.fromBrowser(bad), failed)
		if esSessionCut(g) {
			t.Fatal("failDocIdentityHoldsLocked on a live chrome-error document must not cut")
		}
		if !esHasRetriable(o) {
			t.Fatalf("fail path must retriable-refuse: payloads=%v", o.payloads)
		}
	})
	t.Run("expire", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		_ = esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), esOrdinary, true))
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable("chrome-error://chromewebdata/", "L-ERR", failed)), failed)
		g.mu.Lock()
		var holdKey callKey
		for k, h := range g.docIdentityHolds {
			if h != nil && h.target == esTarget {
				h.deadline = time.Now().Add(-time.Second)
				holdKey = k
				break
			}
		}
		g.flushAsync = func(send, page [][]byte) {
			for _, p := range page {
				o.payloads = append(o.payloads, string(p))
			}
			for _, s := range send {
				o.payloads = append(o.payloads, string(s))
			}
		}
		g.mu.Unlock()
		if holdKey.session == "" {
			t.Fatal("expected a held identity reply to expire")
		}
		g.expireDocIdentityHold(holdKey)
		if esSessionCut(g) {
			t.Fatal("expireDocIdentityHold on a live chrome-error document must not cut")
		}
		if !esHasRetriable(o) {
			t.Fatalf("expire path must retriable-refuse: payloads=%v", o.payloads)
		}
	})
}

func esChromeErrorOrdinaryFailedURL() string {
	return esOrdinary + "/controller/fixture-id"
}

func esChromeErrorOrdinaryErrorControl(t *testing.T, navType string) {
	t.Helper()
	failed := esChromeErrorOrdinaryFailedURL()
	chromeDoc := "chrome-error://chromewebdata/"
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachableType(chromeDoc, "L-ERR", failed, navType)), failed)
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
	infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), failed, true))
	esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, failed)), failed)
	if esSessionCut(g) {
		t.Fatal("ordinary error-document content read must not cut")
	}
	if esHasRetriable(o) {
		t.Fatalf("ordinary error-document content read must return its result, not retriable: payloads=%v", o.payloads)
	}
	if !strings.Contains(strings.Join(o.payloads, ""), fmt.Sprintf(`"id":%d`, esReadID)) {
		t.Fatalf("ordinary error-document content read must deliver the held reply: payloads=%v", o.payloads)
	}
}

func TestExistingSessionChromeErrorOrdinaryErrorControlShapes(t *testing.T) {
	t.Run("fresh/blocked-hop", func(t *testing.T) {
		esChromeErrorOrdinaryErrorControl(t, "Navigation")
	})
	t.Run("bfcache-eligible/blocked-hop", func(t *testing.T) {
		esChromeErrorOrdinaryErrorControl(t, "BackForwardCacheRestore")
	})
}

func TestExistingSessionChromeErrorOrdinaryFailAndExpirePaths(t *testing.T) {
	failed := esChromeErrorOrdinaryFailedURL()
	chromeDoc := "chrome-error://chromewebdata/"
	t.Run("fail", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		infoID := esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), failed, true))
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable(chromeDoc, "L-ERR", failed)), failed)
		bad, _ := json.Marshal(map[string]any{"id": infoID, "sessionId": "RELAY", "error": map[string]any{"code": -32000, "message": "Target closed"}})
		esDrain(g, &o, g.fromBrowser(bad), failed)
		if esSessionCut(g) {
			t.Fatal("ordinary live chrome-error fail path must follow the ordinary path (no cut)")
		}
		if !esHasRetriable(o) {
			t.Fatalf("ordinary live chrome-error fail path must retriable where the ordinary path already is: payloads=%v", o.payloads)
		}
	})
	t.Run("expire", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		_ = esHoldCoveredGetInfoID(t, g, &o, int64(esReadID), cmd, esContentReply(cmd, int64(esReadID), failed, true))
		esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable(chromeDoc, "L-ERR", failed)), failed)
		g.mu.Lock()
		var holdKey callKey
		for k, h := range g.docIdentityHolds {
			if h != nil && h.target == esTarget {
				h.deadline = time.Now().Add(-time.Second)
				holdKey = k
				break
			}
		}
		g.flushAsync = func(send, page [][]byte) {
			for _, p := range page {
				o.payloads = append(o.payloads, string(p))
			}
			for _, s := range send {
				o.payloads = append(o.payloads, string(s))
			}
		}
		g.mu.Unlock()
		if holdKey.session == "" {
			t.Fatal("expected a held identity reply to expire")
		}
		g.expireDocIdentityHold(holdKey)
		if esSessionCut(g) {
			t.Fatal("ordinary live chrome-error expire path must follow the ordinary path (no cut)")
		}
		if !esHasRetriable(o) {
			t.Fatalf("ordinary live chrome-error expire path must retriable where the ordinary path already is: payloads=%v", o.payloads)
		}
	})
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

const esExtFrameURL = "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob/frame.html"

type esTaintSource struct {
	name       string
	url        string
	fire       func(*guard, *esOutcome, string)
	wantTaint  bool
	guardSees  bool
	assumption string
}

func esInFlightHoldSecret(t *testing.T, g *guard, o *esOutcome) (infoID int64) {
	t.Helper()
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "document.body.innerText"}}
	pv := g.fromCommand(&command{id: int64(esReadID), method: cmd.method, session: esSession, params: cmd.params}, int64(esReadID), true)
	if pv.forward == nil {
		t.Fatalf("admit must forward: %s", pv.reply)
	}
	held := g.fromBrowser(esContentReply(cmd, int64(esReadID), esProtectedURLs()[0], true))
	o.take(held)
	for _, raw := range held.send {
		id, method, session := esOutbound(raw)
		if method == "Target.getTargetInfo" && session == "RELAY" {
			infoID = id
		}
	}
	if infoID == 0 {
		t.Fatal("expected identity hold getTargetInfo")
	}
	return infoID
}

func esSettleOrdinary(g *guard, o *esOutcome, infoID int64) {
	esDrain(g, o, g.fromBrowser(esTargetInfoAnswer(infoID, esOrdinaryB)), esOrdinaryB)
	o.notePayloadsFor(esProtectedURLs()[0])
}

func esFrameStartedNavigating(url string) []byte {
	return esEncode(map[string]any{
		"sessionId": esNavSession,
		"method":    "Page.frameStartedNavigating",
		"params":    map[string]any{"frameId": esTarget, "url": url, "loaderId": "L-START", "navigationType": "Navigation"},
	})
}

func esFrameNavigatedTyped(url, loader, navType string) []byte {
	raw := esFrameNavigated(url, loader)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	params, _ := m["params"].(map[string]any)
	params["type"] = navType
	return esEncode(m)
}

func esExecutionContextCreated(id int64, origin, unique string) []byte {
	return esEncode(map[string]any{
		"sessionId": esSession,
		"method":    "Runtime.executionContextCreated",
		"params": map[string]any{
			"context": map[string]any{
				"id": id, "origin": origin, "name": "", "uniqueId": unique,
				"auxData": map[string]any{"isDefault": true, "frameId": esTarget},
			},
		},
	})
}

func esTaintSources() []esTaintSource {
	protected := esProtectedURLs()
	return []esTaintSource{
		{name: "frameNavigated-protected", url: protected[0], wantTaint: true, guardSees: true, fire: func(g *guard, o *esOutcome, u string) {
			esDrain(g, o, g.fromBrowser(esFrameNavigated(u, "L-SRC")), u)
		}},
		{name: "frameNavigated-unparseable", url: "view-source:!!!not-a-url!!!", wantTaint: true, guardSees: true, fire: func(g *guard, o *esOutcome, u string) {
			esDrain(g, o, g.fromBrowser(esFrameNavigated(u, "L-BAD")), u)
		}},
		{name: "frameNavigated-empty-to-wrapped", url: "blob:", wantTaint: true, guardSees: true, fire: func(g *guard, o *esOutcome, u string) {
			esDrain(g, o, g.fromBrowser(esFrameNavigated(u, "L-WRAP")), u)
		}},
		{name: "frameNavigated-bfcache-restore-protected", url: protected[0], wantTaint: true, guardSees: true, fire: func(g *guard, o *esOutcome, u string) {
			esDrain(g, o, g.fromBrowser(esFrameNavigatedTyped(u, "L-BF", "BackForwardCacheRestore")), u)
		}},
		{name: "frameNavigated-prerender-type-protected", url: protected[0], wantTaint: true, guardSees: true, assumption: "any Page.frameNavigated type (including a Prerender activation type if Chrome emits one) bumps via noteOrdinaryDocIdentityFromNavLocked", fire: func(g *guard, o *esOutcome, u string) {
			esDrain(g, o, g.fromBrowser(esFrameNavigatedTyped(u, "L-PRERENDER", "Prerender")), u)
		}},
		{name: "history-navigateToHistoryEntry-then-frameNavigated", url: protected[0], wantTaint: true, guardSees: true, assumption: "navigateToHistoryEntry itself does not bump; the resulting frameNavigated commit does", fire: func(g *guard, o *esOutcome, u string) {
			_ = g.fromCommand(&command{id: int64(esReadID2), method: "Page.navigateToHistoryEntry", session: esSession, params: map[string]any{"entryId": json.Number("1")}}, int64(esReadID2), true)
			esDrain(g, o, g.fromBrowser(esFrameNavigated(u, "L-HIST")), u)
		}},
		{name: "watch-session-url-already-protected-observe", url: protected[0], wantTaint: true, guardSees: true, assumption: "already-protected targetInfoChanged notes watch URL", fire: func(g *guard, o *esOutcome, u string) {
			g.mu.Lock()
			if t0 := g.targets[esTarget]; t0 != nil {
				t0.protected = true
				t0.url = u
			}
			g.mu.Unlock()
			esDrain(g, o, g.fromBrowser(esRelay("Target.targetInfoChanged", u)), u)
		}},
		{name: "targetInfoChanged-newly-protected", url: protected[0], wantTaint: true, guardSees: true, fire: func(g *guard, o *esOutcome, u string) {
			esDrain(g, o, g.fromBrowser(esRelay("Target.targetInfoChanged", u)), u)
		}},
		{name: "targetInfoChanged-unparseable", url: "view-source:!!!not-a-url!!!", wantTaint: true, guardSees: true, fire: func(g *guard, o *esOutcome, u string) {
			esDrain(g, o, g.fromBrowser(esRelay("Target.targetInfoChanged", u)), u)
		}},
		{name: "targetInfoChanged-empty-to-wrapped", url: "blob:", wantTaint: true, guardSees: true, fire: func(g *guard, o *esOutcome, u string) {
			esDrain(g, o, g.fromBrowser(esRelay("Target.targetInfoChanged", u)), u)
		}},
		{name: "frameStartedNavigating-protected", url: protected[0], wantTaint: true, guardSees: true, fire: func(g *guard, o *esOutcome, u string) {
			esDrain(g, o, g.fromBrowser(esFrameStartedNavigating(u)), u)
		}},
		{name: "targetCreated-protected-observe", url: protected[1], wantTaint: true, guardSees: true, fire: func(g *guard, o *esOutcome, u string) {
			esDrain(g, o, g.fromBrowser(esRelay("Target.targetCreated", u)), u)
		}},
		{name: "attachedToTarget-protected-url", url: protected[0], wantTaint: true, guardSees: true, assumption: "attachedToTarget on an already-admitted page session is unusual; modelled as a second attach event carrying a protected targetInfo URL", fire: func(g *guard, o *esOutcome, u string) {
			esDrain(g, o, g.fromBrowser(esEncode(map[string]any{
				"method": "Target.attachedToTarget",
				"params": map[string]any{
					"sessionId": "S-ES-2", "waitingForDebugger": false,
					"targetInfo": map[string]any{"targetId": esTarget, "type": "page", "url": u, "browserContextId": "CTX"},
				},
			})), u)
		}},
		{name: "navigatedWithinDocument-protected", url: protected[0], wantTaint: false, guardSees: false, assumption: "documented residual: same-document navigation does not change the document; no identity handler", fire: func(g *guard, o *esOutcome, u string) {
			esDrain(g, o, g.fromBrowser(esEncode(map[string]any{
				"sessionId": esNavSession, "method": "Page.navigatedWithinDocument",
				"params": map[string]any{"frameId": esTarget, "url": u},
			})), u)
		}},
		{name: "frameRestoredFromBackForwardCache", url: protected[0], wantTaint: false, guardSees: false, assumption: "not in event map; bfcache restores arrive as frameNavigated type=BackForwardCacheRestore", fire: func(g *guard, o *esOutcome, u string) {
			_ = g
			_ = o
			_ = u
		}},
		{name: "prerender-activation-dedicated-event", url: protected[0], wantTaint: false, guardSees: false, assumption: "documented residual until link-route trace; activation arriving as Page.frameNavigated (any type) is covered by frameNavigated-prerender-type-protected", fire: func(g *guard, o *esOutcome, u string) {
			_ = g
			_ = o
			_ = u
		}},
	}
}

func TestExistingSessionP1TaintSourceInFlightReply(t *testing.T) {
	for _, src := range esTaintSources() {
		src := src
		t.Run(src.name, func(t *testing.T) {
			if !src.guardSees {
				t.Skip(src.assumption)
			}
			g := esGuard(t)
			_ = esAdmit(t, g)
			var o esOutcome
			g.mu.Lock()
			before := g.docIdentityTaint[esTarget]
			g.mu.Unlock()
			infoID := esInFlightHoldSecret(t, g, &o)
			src.fire(g, &o, src.url)
			g.mu.Lock()
			after := g.docIdentityTaint[esTarget]
			g.mu.Unlock()
			bumped := after != before
			if bumped != src.wantTaint {
				t.Fatalf("taint bump want=%t got=%t (before=%d after=%d)", src.wantTaint, bumped, before, after)
			}
			esSettleOrdinary(g, &o, infoID)
			joined := strings.Join(o.payloads, "")
			if strings.Contains(joined, esSecret) {
				t.Fatalf("P1-1: in-flight reply must not deliver protected content after source %s (wantTaint=%t); payloads=%v", src.name, src.wantTaint, o.payloads)
			}
			if !esSessionCut(g) && !o.refused && !esHasRetriable(o) {
				t.Fatalf("P1-1: source %s must drop+cut (or retriable) the held reply; cut=%t payloads=%v", src.name, esSessionCut(g), o.payloads)
			}
		})
	}
}

func TestExistingSessionP1RefusedSessionMustNotDeliverHeldReply(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	infoID := esInFlightHoldSecret(t, g, &o)
	g.mu.Lock()
	g.addRefusedLocked(esSession)
	if s := g.sessions[esSession]; s != nil {
		s.refused = true
	}
	g.mu.Unlock()
	esSettleOrdinary(g, &o, infoID)
	joined := strings.Join(o.payloads, "")
	if strings.Contains(joined, esSecret) {
		t.Fatalf("P1-1: held reply must not deliver when the session is already refused/cut (ordinary post-read, no taint); payloads=%v", o.payloads)
	}
}

func TestExistingSessionP1ExtensionShownPaintIdentityThenShown(t *testing.T) {
	method := "Page.captureScreenshot"
	if !protect.ExtensionShownMethod(method) {
		t.Fatalf("%s must be ExtensionShownMethod per catalog", method)
	}
	t.Run("identity-refuse-leaves-no-extension-hold", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		live := esProtectedURLs()[0]
		g.mu.Lock()
		if t0 := g.targets[esTarget]; t0 != nil {
			t0.url = live
		}
		g.targets["ES-EXT"] = &target{id: "ES-EXT", typ: "iframe", url: esExtFrameURL, ctx: "CTX", parentFrame: esTarget}
		g.noteExtensionFrameURLLocked(esTarget, "ES-EXT", esExtFrameURL)
		g.mu.Unlock()
		var o esOutcome
		id := int64(esReadID)
		pv := g.afterExtensionShown(&command{id: id, method: method, session: esSession, params: map[string]any{}}, false, true)
		if pv.forward == nil {
			t.Fatalf("admit must forward: %s", pv.reply)
		}
		reply := esContentReply(esCommand{method: method}, id, live, true)
		v := g.fromBrowser(reply)
		o.take(v)
		g.mu.Lock()
		armed := g.heldExtensionPaints[callKey{esSession, id}]
		idHeld := false
		extAfter := false
		for k, h := range g.docIdentityHolds {
			if k.session == esSession && k.id == id {
				idHeld = true
				extAfter = h != nil && h.extensionAfter
			}
		}
		g.mu.Unlock()
		if armed == nil || !armed.probeDeferred {
			t.Fatal("P1-2: paint deadline must arm at paint start even when identity will refuse")
		}
		if !idHeld || !extAfter {
			t.Fatalf("P1-2: expected identity hold with extensionAfter; idHeld=%t extAfter=%t send=%d", idHeld, extAfter, len(v.send))
		}
		var infoID int64
		for _, raw := range v.send {
			rid, mth, session := esOutbound(raw)
			if mth == "Target.getTargetInfo" && session == "RELAY" {
				infoID = rid
			}
		}
		if infoID == 0 {
			t.Fatal("expected identity post-read")
		}
		esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, live)), live)
		g.mu.Lock()
		extLeft := len(g.heldExtensionPaints)
		idLeft := len(g.docIdentityHolds)
		g.mu.Unlock()
		if extLeft != 0 || idLeft != 0 {
			t.Fatalf("P1-2: identity refuse must leave no extension or identity hold; ext=%d id=%d", extLeft, idLeft)
		}
		if strings.Contains(strings.Join(o.payloads, ""), esSecret) {
			t.Fatalf("P1-2: refused identity must not deliver secret; payloads=%v", o.payloads)
		}
	})
	t.Run("identity-accept-then-extension-probe", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		g.mu.Lock()
		g.targets["ES-EXT"] = &target{id: "ES-EXT", typ: "iframe", url: esExtFrameURL, ctx: "CTX", parentFrame: esTarget}
		g.noteExtensionFrameURLLocked(esTarget, "ES-EXT", esExtFrameURL)
		g.mu.Unlock()
		var o esOutcome
		id := int64(esReadID)
		pv := g.afterExtensionShown(&command{id: id, method: method, session: esSession, params: map[string]any{}}, false, true)
		if pv.forward == nil {
			t.Fatalf("admit must forward: %s", pv.reply)
		}
		reply := esContentReply(esCommand{method: method}, id, esOrdinary, true)
		v := g.fromBrowser(reply)
		o.take(v)
		g.mu.Lock()
		armed := g.heldExtensionPaints[callKey{esSession, id}]
		deferred := armed != nil && armed.probeDeferred
		g.mu.Unlock()
		if !deferred {
			t.Fatal("P1-2: paint deadline must arm at paint start (deferred probe) during identity hold")
		}
		var infoID int64
		for _, raw := range v.send {
			rid, mth, session := esOutbound(raw)
			if mth == "Target.getTargetInfo" && session == "RELAY" {
				infoID = rid
			}
		}
		if infoID == 0 {
			t.Fatal("expected identity post-read before extension probe")
		}
		esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, esOrdinaryB)), esOrdinaryB)
		g.mu.Lock()
		armed = g.heldExtensionPaints[callKey{esSession, id}]
		if armed == nil || armed.probeDeferred {
			t.Fatal("P1-2: identity accept must start the deferred extension probe")
		}
		_ = g.finishExtensionPaintLocked(callKey{esSession, id}, true, true)
		o.payloads = append(o.payloads, stringBytes(g.takePendingPageLocked())...)
		left := len(g.heldExtensionPaints)
		g.mu.Unlock()
		if left != 0 {
			t.Fatalf("extension finish must clear hold; left=%d", left)
		}
	})
}

func TestExistingSessionExtensionPaintDeadlineArmsAtPaintStart(t *testing.T) {
	t.Setenv(extensionPaintHoldEnv, "40ms")
	g := esGuard(t)
	_ = esAdmit(t, g)
	g.mu.Lock()
	g.targets["ES-EXT"] = &target{id: "ES-EXT", typ: "iframe", url: esExtFrameURL, ctx: "CTX", parentFrame: esTarget}
	g.noteExtensionFrameURLLocked(esTarget, "ES-EXT", esExtFrameURL)
	g.mu.Unlock()
	var o esOutcome
	id := int64(esReadID)
	method := "Page.captureScreenshot"
	pv := g.afterExtensionShown(&command{id: id, method: method, session: esSession, params: map[string]any{}}, false, true)
	if pv.forward == nil {
		t.Fatalf("admit must forward: %s", pv.reply)
	}
	reply := esContentReply(esCommand{method: method}, id, esOrdinary, true)
	v := g.fromBrowser(reply)
	o.take(v)
	g.mu.Lock()
	armed := g.heldExtensionPaints[callKey{esSession, id}]
	gen := uint64(0)
	deadline := time.Time{}
	if armed != nil {
		gen = armed.generation
		deadline = armed.deadline
	}
	g.mu.Unlock()
	if armed == nil || !armed.probeDeferred || gen == 0 {
		t.Fatal("paint deadline must arm at paint-reply arrival while identity holds")
	}
	if time.Until(deadline) > extensionPaintHoldDuration() {
		t.Fatalf("deadline must be measured from paint start, not a later hold release")
	}
	time.Sleep(time.Until(deadline) + 20*time.Millisecond)
	page, _ := g.expireExtensionPaint(callKey{esSession, id}, gen)
	if len(page) == 0 {
		g.mu.Lock()
		page = g.takePendingPageLocked()
		g.mu.Unlock()
	}
	joined := strings.Join(append(o.payloads, stringBytes(page)...), "")
	if !strings.Contains(joined, protect.ShownReason()) && !strings.Contains(joined, "error") {
		t.Fatalf("paint deadline must refuse without waiting on identity settle; got %q", joined)
	}
	g.mu.Lock()
	_, idLeft := g.docIdentityHolds[callKey{esSession, id}]
	_, paintLeft := g.heldExtensionPaints[callKey{esSession, id}]
	g.mu.Unlock()
	if idLeft || paintLeft {
		t.Fatalf("deadline must clear identity and paint holds; id=%t paint=%t", idLeft, paintLeft)
	}
}

func TestExistingSessionE7PinnedEvaluateOnProtectedWithoutShownPlaceWindow(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	vault := "https://vault.sliccy.ai/doc"
	g.mu.Lock()
	if nt := g.navTargets[esTarget]; nt != nil {
		nt.documentURL = vault
	}
	g.noteDocIdentityPlaceLocked(esTarget, vault)
	g.noteDocIdentityWatchURLLocked(esTarget, vault)
	g.mu.Unlock()
	pv := g.fromCommand(&command{
		id: int64(esReadID), method: "Runtime.evaluate", session: esSession,
		params: map[string]any{"expression": "1+1", "objectId": "isolated-world-obj"},
	}, int64(esReadID), true)
	if pv.reply != nil {
		t.Fatalf("E7 model: objectId-pinned evaluate without a shown place-window must not refuse at admit: %s", pv.reply)
	}
	if pv.forward == nil {
		t.Fatal("E7 model: expected forward")
	}
	g.mu.Lock()
	_, admitted := g.docIdentityCalls[callKey{esSession, esReadID}]
	g.mu.Unlock()
	if admitted {
		t.Fatal("E7 model: watch-bad documentURL alone must not admit pinned evaluate into identity hold (exact hunk: docIdentityPinnedNeedsHoldLocked watchBad before shownContextPlaceSeqLocked)")
	}
	reply := esEncode(map[string]any{
		"id": int64(esReadID), "sessionId": esSession,
		"result": map[string]any{"result": map[string]any{"type": "number", "value": 2}},
	})
	held := g.fromBrowser(reply)
	o.take(held)
	if held.forward == nil {
		t.Fatalf("E7 model: evaluate reply must forward, not identity-hold; send=%v page=%v", held.send, o.payloads)
	}
	for _, raw := range held.send {
		_, mth, session := esOutbound(raw)
		if mth == "Target.getTargetInfo" && session == "RELAY" {
			t.Fatal("E7 model: identity post-read must not run for this pinned evaluate (wire step that held the reply)")
		}
	}
}

func TestExistingSessionE7FrameTreeResolvesWhilePinnedEvaluateOnWatchBad(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	vault := "https://vault.sliccy.ai/doc"
	g.mu.Lock()
	if nt := g.navTargets[esTarget]; nt != nil {
		nt.documentURL = vault
	}
	g.noteDocIdentityWatchURLLocked(esTarget, vault)
	g.mu.Unlock()
	eval := g.fromCommand(&command{
		id: int64(esReadID), method: "Runtime.evaluate", session: esSession,
		params: map[string]any{"expression": "0", "objectId": "isolated-world-obj"},
	}, int64(esReadID), true)
	if eval.forward == nil {
		t.Fatalf("evaluate admit: %s", eval.reply)
	}
	g.mu.Lock()
	_, admitted := g.docIdentityCalls[callKey{esSession, esReadID}]
	g.mu.Unlock()
	if admitted {
		t.Fatal("E7 model: pinned evaluate must not enter identity hold without a shown place-window")
	}
	tree := g.fromCommand(&command{id: int64(esReadID2), method: "Page.getFrameTree", session: esSession, params: map[string]any{}}, int64(esReadID2), true)
	if tree.forward == nil {
		t.Fatalf("getFrameTree must forward on the client session: %s", tree.reply)
	}
	treeReply := esEncode(map[string]any{
		"id": int64(esReadID2), "sessionId": esSession,
		"result": map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": esTarget, "url": esOrdinary}}},
	})
	tr := g.fromBrowser(treeReply)
	o.take(tr)
	if tr.forward == nil {
		t.Fatalf("getFrameTree reply must reach the client (not be held/confused with identity): payloads=%v", o.payloads)
	}
	if !strings.Contains(string(tr.forward), esTarget) {
		t.Fatalf("E7 model: frameTree.frame.id unresolved: %s", tr.forward)
	}
	evalReply := esEncode(map[string]any{
		"id": int64(esReadID), "sessionId": esSession,
		"result": map[string]any{"result": map[string]any{"type": "number", "value": 0}},
	})
	er := g.fromBrowser(evalReply)
	o.take(er)
	if er.forward == nil {
		t.Fatalf("E7 model: evaluate reply must not be identity-held (would cut/confuse later frame-tree reads): send=%v", er.send)
	}
}

func stringBytes(pages [][]byte) []string {
	var out []string
	for _, p := range pages {
		out = append(out, string(p))
	}
	return out
}

func TestExistingSessionP1PinnedEvaluateOnShownViewSourceContext(t *testing.T) {
	inner := "https://vault.sliccy.ai/doc"
	type row struct {
		name       string
		targetURL  string
		origin     string
		pin        string
		seed       string
		wantShow   bool
		wantSecret bool
		wantErrPg  bool
		wantIdent  bool
		assumption string
	}
	blobURL := "blob:https://vault.sliccy.ai/0c1d2e3f-4a5b-6c7d-8e9f-001122334455"
	fsURL := "filesystem:https://vault.sliccy.ai/temporary/x.html"
	vaultOrigin := "https://vault.sliccy.ai"
	rows := []row{
		{name: "timing-blank-opaque-before-place", targetURL: esOrdinary, origin: "://", pin: "contextId", seed: "blank-before-place", wantShow: true, wantSecret: false, wantIdent: true, assumption: "DIAGSHAPE measured: initial about:blank context is opaque+shown; option (a) identity-holds until the document is placed"},
		{name: "timing-error-page-opaque-shown", targetURL: "chrome-error://chromewebdata/", origin: "://", pin: "contextId", seed: "error-page", wantShow: true, wantSecret: false, wantErrPg: true, assumption: "DIAGSHAPE measured: blocked view-source:vault → chrome-error frame, opaque error-page context shown; pinned evaluate refused by the error-page rule"},
		{name: "loaded-wrapper-inner-origin-hidden", targetURL: "view-source:" + inner, origin: vaultOrigin, pin: "contextId", seed: "url-only", wantShow: false, wantSecret: false, assumption: "DIAGSHAPE measured (view-source control + blob/filesystem): final default context origin is the protected INNER origin → Loaded → contextCreatedEvent suppresses"},
		{name: "blob-lagging-protected-origin-hidden", targetURL: blobURL, origin: vaultOrigin, pin: "contextId", seed: "lagging-origin", wantShow: false, wantSecret: false, assumption: "DIAGSHAPE Run2 shape in lagging order: cached URL still ordinary while frameNavigated/targetInfoChanged held; context origin is vault → Loaded(origin) hides even with stale URL"},
		{name: "filesystem-lagging-protected-origin-hidden", targetURL: fsURL, origin: vaultOrigin, pin: "contextId", seed: "lagging-origin", wantShow: false, wantSecret: false, assumption: "DIAGSHAPE Run2 filesystem lagging: same as blob — protected-origin context hidden by Loaded while nav events held"},
		{name: "blob-after-classify-origin-hidden", targetURL: blobURL, origin: vaultOrigin, pin: "contextId", seed: "tree-then-context", wantShow: false, wantSecret: false, assumption: "DIAGSHAPE Run2 unlagged: guard learned frame first → frame-under-protected / Loaded hides vault-origin context"},
		{name: "sandboxed-null-origin-blob-residual", targetURL: blobURL, origin: "null", pin: "contextId", seed: "url-only", wantShow: true, wantSecret: false, wantIdent: true, assumption: "DIAGSHAPE residual (unmeasured): sandboxed/null-origin protected blob — Loaded(origin) does not hide; option (a) sizes the pin window"},
		{name: "blob-opaque-before-place", targetURL: blobURL, origin: "://", pin: "contextId", seed: "url-only", wantShow: true, wantSecret: false, wantIdent: true, assumption: "SYNTHETIC: opaque blob context does not occur on measured wrappers; option (a) defence-in-depth stress"},
		{name: "blob-opaque-null-uniqueContextId", targetURL: blobURL, origin: "null", pin: "uniqueContextId", seed: "url-only", wantShow: true, wantSecret: false, wantIdent: true, assumption: "SYNTHETIC: uniqueContextId pin path for option (a); real null-origin residual is sandboxed-null-origin-blob-residual"},
		{name: "filesystem-opaque-before-place", targetURL: fsURL, origin: "://", pin: "contextId", seed: "url-only", wantShow: true, wantSecret: false, wantIdent: true, assumption: "SYNTHETIC: opaque filesystem context unmeasured; option (a) defence-in-depth"},
	}
	for _, tc := range rows {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			g := esGuard(t)
			_ = esAdmit(t, g)
			var o esOutcome
			switch tc.seed {
			case "error-page":
				failed := inner
				announced := "chrome-error://chromewebdata/"
				g.mu.Lock()
				if t0 := g.targets[esTarget]; t0 != nil {
					t0.url = announced
				}
				if nt := g.navTargets[esTarget]; nt != nil {
					nt.documentURL = announced
					nt.errorURL = failed
				}
				g.setErrorProofLocked(esTarget, errorProofProtected)
				g.mu.Unlock()
				esDrain(g, &o, g.fromBrowser(esFrameNavigatedUnreachable(announced, "L-ERR", failed)), failed)
			case "blank-before-place":
				g.mu.Lock()
				g.docPlaceSeq[esTarget] = 0
				g.docPlaceURL[esTarget] = ""
				if t0 := g.targets[esTarget]; t0 != nil {
					t0.url = "about:blank"
				}
				if nt := g.navTargets[esTarget]; nt != nil {
					nt.documentURL = "about:blank"
				}
				g.mu.Unlock()
			case "tree-then-context":
				g.mu.Lock()
				if t0 := g.targets[esTarget]; t0 != nil {
					t0.url = tc.targetURL
				}
				g.mu.Unlock()
				esDrain(g, &o, g.fromBrowser(esRelay("Target.targetInfoChanged", tc.targetURL)), tc.targetURL)
				g.mu.Lock()
				if nt := g.navTargets[esTarget]; nt != nil {
					nt.documentURL = tc.targetURL
				}
				g.noteFrameTreeResultLocked(esTarget, json.RawMessage(`{"frameTree":{"frame":{"id":"`+esTarget+`","url":"`+tc.targetURL+`","securityOrigin":"https://vault.sliccy.ai"}}}`))
				g.mu.Unlock()
			case "lagging-origin":
			default:
				g.mu.Lock()
				if t0 := g.targets[esTarget]; t0 != nil {
					t0.url = tc.targetURL
				}
				g.mu.Unlock()
			}
			ctxID := int64(42)
			unique := "view-source-ctx-1"
			live := tc.targetURL
			if tc.seed == "error-page" {
				live = "chrome-error://chromewebdata/"
			}
			if tc.seed == "lagging-origin" {
				live = esOrdinary
			}
			esDrain(g, &o, g.fromBrowser(esExecutionContextCreated(ctxID, tc.origin, unique)), live)
			if tc.seed == "blank-before-place" {
				g.mu.Lock()
				g.noteDocIdentityPlaceLocked(esTarget, esOrdinary)
				if t0 := g.targets[esTarget]; t0 != nil {
					t0.url = esOrdinary
				}
				if nt := g.navTargets[esTarget]; nt != nil {
					nt.documentURL = esOrdinary
				}
				g.mu.Unlock()
			}
			g.mu.Lock()
			shown := g.shown[esTarget] != nil && g.shown[esTarget].contexts[ctxID] != nil
			hidden := g.contextHiddenLocked(esTarget, ctxID)
			g.mu.Unlock()
			if shown != tc.wantShow {
				t.Fatalf("context show want=%t got shown=%t hidden=%t (%s)", tc.wantShow, shown, hidden, tc.assumption)
			}
			params := map[string]any{"expression": "document.documentElement.outerHTML"}
			switch tc.pin {
			case "contextId":
				params["contextId"] = json.Number("42")
			case "uniqueContextId":
				params["uniqueContextId"] = unique
			}
			pv := g.fromCommand(&command{id: int64(esReadID), method: "Runtime.evaluate", session: esSession, params: params}, int64(esReadID), true)
			if !tc.wantShow {
				if pv.reply == nil {
					t.Fatalf("hidden/suppressed context must refuse pinned evaluate at admit")
				}
				return
			}
			if tc.wantErrPg {
				if pv.reply == nil || !strings.Contains(string(pv.reply), "error page for a protected URL") {
					t.Fatalf("error-page opaque context must refuse pinned evaluate by the error-page rule: %s", pv.reply)
				}
				return
			}
			if pv.reply != nil {
				t.Fatalf("shown context pinned evaluate must not refuse at admit: %s", pv.reply)
			}
			if pv.forward == nil {
				t.Fatal("expected forward")
			}
			g.mu.Lock()
			_, admitted := g.docIdentityCalls[callKey{esSession, esReadID}]
			g.mu.Unlock()
			if tc.wantIdent && !admitted {
				t.Fatal("P1-3 option (a): pinned evaluate must enter docIdentityCalls")
			}
			if !tc.wantIdent {
				return
			}
			body := "<html>" + esSecret + "</html>"
			settleURL := tc.targetURL
			if tc.seed == "blank-before-place" {
				body = "<html>ordinary-blank</html>"
				settleURL = esOrdinary
			}
			reply := esEncode(map[string]any{
				"id": int64(esReadID), "sessionId": esSession,
				"result": map[string]any{"result": map[string]any{"type": "string", "value": body}},
			})
			held := g.fromBrowser(reply)
			o.take(held)
			var infoID int64
			for _, raw := range held.send {
				rid, mth, session := esOutbound(raw)
				if mth == "Target.getTargetInfo" && session == "RELAY" {
					infoID = rid
				}
			}
			if infoID == 0 {
				t.Fatal("expected identity post-read for pinned evaluate")
			}
			esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, settleURL)), settleURL)
			joined := strings.Join(o.payloads, "")
			gotSecret := strings.Contains(joined, esSecret)
			if gotSecret != tc.wantSecret {
				t.Fatalf("P1-3: secret want=%t got=%t (%s); payloads=%v", tc.wantSecret, gotSecret, tc.assumption, o.payloads)
			}
			if gotSecret {
				t.Fatalf("P1-3: pinned evaluate must not deliver protected source (%s); payloads=%v", tc.assumption, o.payloads)
			}
		})
	}
}

func esHrefEvaluateReply(id int64, setURL string) []byte {
	return esEncode(map[string]any{
		"id": id, "sessionId": esSession,
		"result": map[string]any{"result": map[string]any{"type": "string", "value": setURL}},
	})
}

func esEvaluateWithNavHold(t *testing.T, g *guard, o *esOutcome, setURL, startedURL string) (infoID int64, admitTaint uint64) {
	t.Helper()
	expr := "location.href=" + strconv.Quote(setURL)
	pv := g.fromCommand(&command{
		id: int64(esReadID), method: "Runtime.evaluate", session: esSession,
		params: map[string]any{"expression": expr},
	}, int64(esReadID), true)
	if pv.forward == nil {
		t.Fatalf("unpinned href evaluate must admit: %s", pv.reply)
	}
	g.mu.Lock()
	_, admitted := g.docIdentityCalls[callKey{esSession, esReadID}]
	admitTaint = g.docIdentityTaint[esTarget]
	g.mu.Unlock()
	if !admitted {
		t.Fatal("unpinned Runtime.evaluate must enter docIdentityCalls")
	}
	held := g.fromBrowser(esHrefEvaluateReply(int64(esReadID), setURL))
	o.take(held)
	if held.forward != nil {
		t.Fatalf("evaluate reply must be identity-held until post-read; forward=%s", held.forward)
	}
	for _, raw := range held.send {
		id, method, session := esOutbound(raw)
		if method == "Target.getTargetInfo" && session == "RELAY" {
			infoID = id
		}
	}
	if infoID == 0 {
		t.Fatal("expected identity hold Target.getTargetInfo on RELAY")
	}
	esDrain(g, o, g.fromBrowser(esFrameStartedNavigating(startedURL)), startedURL)
	return infoID, admitTaint
}

func TestExistingSessionE7EvaluateWithNavigationNetworkDelivers(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	network := "https://vault.sliccy.ai/doc?n=e7-network"
	infoID, admitTaint := esEvaluateWithNavHold(t, g, &o, network, network)
	g.mu.Lock()
	afterStarted := g.docIdentityTaint[esTarget]
	g.mu.Unlock()
	if afterStarted != admitTaint {
		t.Fatalf("E7 evaluate-with-nav network form: frameStartedNavigating must not bump taint (admit=%d after=%d)", admitTaint, afterStarted)
	}
	esSettleOrdinary(g, &o, infoID)
	if esSessionCut(g) {
		t.Fatal("E7 evaluate-with-nav network form: session must not be cut")
	}
	joined := strings.Join(o.payloads, "")
	if strings.Contains(joined, "slicc: refused:") {
		t.Fatalf("E7 evaluate-with-nav network form: evaluate reply must deliver, not refuse; payloads=%v", o.payloads)
	}
	if !strings.Contains(joined, network) && !strings.Contains(joined, `"id":71`) {
		t.Fatalf("E7 evaluate-with-nav network form: expected delivered evaluate reply; payloads=%v", o.payloads)
	}
	tree := g.fromCommand(&command{id: int64(esReadID2), method: "Page.getFrameTree", session: esSession, params: map[string]any{}}, int64(esReadID2), true)
	if tree.forward == nil {
		t.Fatalf("E7 evaluate-with-nav: getFrameTree must still forward after network href hold: %s", tree.reply)
	}
	tr := g.fromBrowser(esEncode(map[string]any{
		"id": int64(esReadID2), "sessionId": esSession,
		"result": map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": esTarget, "url": esOrdinary}}},
	}))
	o.take(tr)
	if tr.forward == nil || !strings.Contains(string(tr.forward), esTarget) {
		t.Fatalf("E7 evaluate-with-nav: frameTree.frame.id unresolved after network href; payloads=%v forward=%s", o.payloads, tr.forward)
	}
}

func TestExistingSessionE7EvaluateWithNavigationWrapperCuts(t *testing.T) {
	wrappers := []struct {
		name string
		url  string
	}{
		{"blob", "blob:https://vault.sliccy.ai/0c1d2e3f-4a5b-6c7d-8e9f-001122334455"},
		{"filesystem", "filesystem:https://vault.sliccy.ai/temporary/x.html"},
		{"view-source-blob", "view-source:blob:https://vault.sliccy.ai/0c1d2e3f-4a5b-6c7d-8e9f-001122334455"},
	}
	for _, w := range wrappers {
		w := w
		t.Run(w.name, func(t *testing.T) {
			g := esGuard(t)
			_ = esAdmit(t, g)
			var o esOutcome
			infoID, admitTaint := esEvaluateWithNavHold(t, g, &o, w.url, w.url)
			g.mu.Lock()
			afterStarted := g.docIdentityTaint[esTarget]
			g.mu.Unlock()
			if afterStarted == admitTaint {
				t.Fatalf("E7 evaluate-with-nav wrapper %s: frameStartedNavigating must bump taint", w.name)
			}
			esSettleOrdinary(g, &o, infoID)
			if !esSessionCut(g) && !o.refused && !strings.Contains(strings.Join(o.payloads, ""), "slicc: refused:") {
				t.Fatalf("E7 evaluate-with-nav wrapper %s: must cut or refuse; payloads=%v", w.name, o.payloads)
			}
			if strings.Contains(strings.Join(o.payloads, ""), esSecret) {
				t.Fatalf("E7 evaluate-with-nav wrapper %s: must not deliver protected content", w.name)
			}
		})
	}
}

func TestExistingSessionExpiredDeferredPaintProbeUnderLockReturns(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	id := int64(esReadID)
	k := callKey{esSession, id}
	finished := make(chan struct{})
	g.mu.Lock()
	if g.heldExtensionPaints == nil {
		g.heldExtensionPaints = map[callKey]*heldExtensionPaint{}
	}
	g.heldExtensionPaints[k] = &heldExtensionPaint{
		session:       esSession,
		target:        esTarget,
		frames:        []string{esTarget},
		generation:    1,
		deadline:      time.Now().Add(-time.Second),
		probeDeferred: true,
		m:             message{ID: &id, SessionID: esSession},
	}
	go func() {
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			panic("#881: startDeferredExtensionPaintProbeLocked under g.mu deadlocked (called locking expireExtensionPaint)")
		}
	}()
	_ = g.startDeferredExtensionPaintProbeLocked(k)
	close(finished)
	_, left := g.heldExtensionPaints[k]
	g.mu.Unlock()
	if left {
		t.Fatal("#881: expired deferred paint must be cleared by the Locked expire helper")
	}
}

func TestExistingSessionE7EvaluateWithNavigationRedirectChain(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	var o esOutcome
	network := "https://vault.sliccy.ai/doc?n=e7-redirect"
	infoID, admitTaint := esEvaluateWithNavHold(t, g, &o, "https://ordinary.test/redirect-to-vault", network)
	g.mu.Lock()
	afterStarted := g.docIdentityTaint[esTarget]
	g.mu.Unlock()
	if afterStarted != admitTaint {
		t.Fatalf("E7 evaluate-with-nav redirect chain: network started URL must not bump (admit=%d after=%d)", admitTaint, afterStarted)
	}
	esSettleOrdinary(g, &o, infoID)
	if esSessionCut(g) {
		t.Fatal("E7 evaluate-with-nav redirect chain: network shape started URL must not cut when post-read stays ordinary")
	}
	joined := strings.Join(o.payloads, "")
	if strings.Contains(joined, "slicc: refused:") {
		t.Fatalf("E7 evaluate-with-nav redirect chain: must deliver; payloads=%v", o.payloads)
	}
}

func TestExistingSessionE7EvaluateWithNavigationNetworkThenWrapperCuts(t *testing.T) {
	wrappers := []string{
		"blob:https://vault.sliccy.ai/0c1d2e3f-4a5b-6c7d-8e9f-001122334455",
		"filesystem:https://vault.sliccy.ai/temporary/x.html",
		"view-source:blob:https://vault.sliccy.ai/0c1d2e3f-4a5b-6c7d-8e9f-001122334455",
	}
	for _, wrapper := range wrappers {
		wrapper := wrapper
		t.Run(wrapper, func(t *testing.T) {
			g := esGuard(t)
			_ = esAdmit(t, g)
			var o esOutcome
			network := "https://vault.sliccy.ai/doc?n=e7-then-wrap"
			infoID, admitTaint := esEvaluateWithNavHold(t, g, &o, network, network)
			g.mu.Lock()
			afterNetwork := g.docIdentityTaint[esTarget]
			g.mu.Unlock()
			if afterNetwork != admitTaint {
				t.Fatalf("first network started must not bump; admit=%d after=%d", admitTaint, afterNetwork)
			}
			esDrain(g, &o, g.fromBrowser(esFrameStartedNavigating(wrapper)), wrapper)
			g.mu.Lock()
			afterWrapper := g.docIdentityTaint[esTarget]
			g.mu.Unlock()
			if afterWrapper == afterNetwork {
				t.Fatalf("wrapper started inside the same hold must still bump taint")
			}
			esSettleOrdinary(g, &o, infoID)
			if !esSessionCut(g) && !strings.Contains(strings.Join(o.payloads, ""), "slicc: refused:") {
				t.Fatalf("network-then-wrapper must cut/refuse; payloads=%v", o.payloads)
			}
		})
	}
}

func TestExistingSessionE7ExistingTabFetchArmedAllowsNetwork(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	g.mu.Lock()
	nt := g.navTargets[esTarget]
	patterns := len(g.patterns)
	g.mu.Unlock()
	if nt == nil || !nt.done || patterns == 0 {
		t.Fatalf("existing-session model: nav session must already be ready (Fetch.enable armed at navAttachedLocked) before client evaluate; nt=%v done=%v patterns=%d", nt != nil, nt != nil && nt.done, patterns)
	}
	var o esOutcome
	network := "https://vault.sliccy.ai/doc?n=e7-existing-tab"
	infoID, admitTaint := esEvaluateWithNavHold(t, g, &o, network, network)
	g.mu.Lock()
	after := g.docIdentityTaint[esTarget]
	g.mu.Unlock()
	if after != admitTaint {
		t.Fatalf("existing tab with nav Fetch armed: network started must not bump")
	}
	esSettleOrdinary(g, &o, infoID)
	if esSessionCut(g) || strings.Contains(strings.Join(o.payloads, ""), "slicc: refused:") {
		t.Fatalf("existing tab with Fetch armed: must deliver; payloads=%v", o.payloads)
	}
}

func TestExistingSessionE7EvaluateWithNavigationAllowedFetchForms(t *testing.T) {
	forms := []struct {
		name string
		url  string
	}{
		{"plain-https-vault", "https://vault.sliccy.ai/doc?n=form-plain"},
		{"plain-http-vault", "http://vault.sliccy.ai/doc?n=form-http"},
		{"plain-https-seven", "https://seven.sliccy.ai/doc?n=form-seven"},
		{"idn-punycode", "https://xn--bcher-kva.sliccy.ai/doc"},
		{"percent-host", "https://vault%2esliccy%2eai/doc"},
		{"userinfo-evil-at-seven", "http://evil@seven.sliccy.ai/"},
		{"userinfo-seven-at-vault", "http://seven@vault.sliccy.ai/"},
		{"trailing-dot", "https://vault.sliccy.ai./doc"},
		{"upper-scheme-host", "HTTPS://VAULT.SLICCY.AI/doc"},
		{"port-443-explicit", "https://vault.sliccy.ai:443/doc"},
		{"port-80-explicit", "http://vault.sliccy.ai:80/doc"},
		{"port-8443", "https://vault.sliccy.ai:8443/doc"},
	}
	for _, f := range forms {
		f := f
		t.Run(f.name, func(t *testing.T) {
			g := esGuard(t)
			_ = esAdmit(t, g)
			g.mu.Lock()
			allow := g.docIdentityStartedNetworkAllowLocked(esTarget, f.url)
			urlOK := g.o.URL(f.url)
			gate := g.docIdentityFetchGateCoversLocked(f.url)
			g.mu.Unlock()
			if !urlOK || !gate || !allow {
				t.Fatalf("form %s must be allow-listed (o.URL=%t fetchGate=%t allow=%t)", f.name, urlOK, gate, allow)
			}
			var o esOutcome
			infoID, admitTaint := esEvaluateWithNavHold(t, g, &o, f.url, f.url)
			g.mu.Lock()
			after := g.docIdentityTaint[esTarget]
			g.mu.Unlock()
			if after != admitTaint {
				t.Fatalf("form %s: started must not bump", f.name)
			}
			esSettleOrdinary(g, &o, infoID)
			if esSessionCut(g) || strings.Contains(strings.Join(o.payloads, ""), "slicc: refused:") {
				t.Fatalf("form %s: must deliver; payloads=%v", f.name, o.payloads)
			}
		})
	}
}

func TestExistingSessionE7EvaluateWithNavigationUserinfoSevenAtEvilStaysTaint(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	evil := "http://seven@evil.example/"
	g.mu.Lock()
	if g.docIdentityStartedNetworkAllowLocked(esTarget, evil) {
		t.Fatal("http://seven@evil.example/ must not be allow-listed (host is evil)")
	}
	g.mu.Unlock()
}

func TestExistingSessionEmptyRelaySessionFailClosedRetriable(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	g.relaySession = ""
	var o esOutcome
	cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "document.body.innerText"}}
	live := "https://vault.sliccy.ai/doc?n=empty-relay"
	esIssueContent(g, &o, int64(esReadID), cmd, live, true)
	o.notePayloadsFor(live)
	if o.delivered || strings.Contains(strings.Join(o.payloads, ""), esSecret) {
		t.Fatalf("empty relaySession must not forward a covered reply ungated: payloads=%v", o.payloads)
	}
	if !esHasRetriable(o) {
		t.Fatalf("empty relaySession must fail closed with retriable context-destroyed: payloads=%v refusal=%q", o.payloads, o.refusal)
	}
	if esSessionCut(g) {
		t.Fatalf("empty relaySession retriable must not cut the session")
	}
}

func TestExistingSessionFetchGateCanonicalOnly(t *testing.T) {
	forms := []struct {
		name  string
		url   string
		cover bool
		allow bool
	}{
		{"plain-https", "https://vault.sliccy.ai/doc", true, true},
		{"port-443", "https://vault.sliccy.ai:443/doc", true, true},
		{"port-80", "http://vault.sliccy.ai:80/doc", true, true},
		{"port-8443", "https://vault.sliccy.ai:8443/doc", true, true},
		{"userinfo-seven-at-evil", "http://seven@evil.example/", false, false},
		{"userinfo-evil-at-seven", "http://evil@seven.sliccy.ai/", true, true},
		{"trailing-dot", "https://vault.sliccy.ai./doc", true, true},
		{"upper", "HTTPS://VAULT.SLICCY.AI/doc", true, true},
		{"idn", "https://xn--bcher-kva.sliccy.ai/doc", true, true},
		{"percent-host", "https://vault%2esliccy%2eai/doc", true, true},
		{"raw-matches-canonical-fails", "http://vault.sliccy.ai:99999/doc", false, false},
	}
	for _, f := range forms {
		f := f
		t.Run(f.name, func(t *testing.T) {
			g := esGuard(t)
			_ = esAdmit(t, g)
			g.mu.Lock()
			gate := g.docIdentityFetchGateCoversLocked(f.url)
			allow := g.docIdentityStartedNetworkAllowLocked(esTarget, f.url)
			g.mu.Unlock()
			if gate != f.cover {
				t.Fatalf("Fetch gate cover: got %t want %t", gate, f.cover)
			}
			if allow != f.allow {
				t.Fatalf("started allow: got %t want %t", allow, f.allow)
			}
		})
	}
}

func TestExistingSessionE7AllowRequiresFetchArmed(t *testing.T) {
	network := "https://vault.sliccy.ai/doc?n=fetch-armed"
	holdThenDisarmStarted := func(t *testing.T, g *guard, o *esOutcome, disarm func(*guard)) (infoID int64, admitTaint uint64) {
		t.Helper()
		pv := g.fromCommand(&command{id: int64(esReadID), method: "Runtime.evaluate", session: esSession, params: map[string]any{"expression": "location.href=" + strconv.Quote(network)}}, int64(esReadID), true)
		if pv.reply != nil {
			t.Fatalf("unpinned href evaluate must admit: %s", pv.reply)
		}
		if pv.forward == nil {
			t.Fatal("expected forward")
		}
		g.mu.Lock()
		_, admitted := g.docIdentityCalls[callKey{esSession, int64(esReadID)}]
		admitTaint = g.docIdentityTaint[esTarget]
		g.mu.Unlock()
		if !admitted {
			t.Fatal("unpinned Runtime.evaluate must enter docIdentityCalls")
		}
		held := g.fromBrowser(esHrefEvaluateReply(int64(esReadID), network))
		o.take(held)
		if held.forward != nil {
			t.Fatalf("evaluate reply must be identity-held; forward=%s", held.forward)
		}
		for _, raw := range held.send {
			id, method, session := esOutbound(raw)
			if method == "Target.getTargetInfo" && session == "RELAY" {
				infoID = id
			}
		}
		if infoID == 0 {
			t.Fatal("expected identity hold Target.getTargetInfo on RELAY")
		}
		disarm(g)
		g.mu.Lock()
		if g.docIdentityStartedNetworkAllowLocked(esTarget, network) {
			t.Fatal("unarmed Fetch must not allow network started")
		}
		g.mu.Unlock()
		esDrain(g, o, g.fromBrowser(esFrameStartedNavigating(network)), network)
		return infoID, admitTaint
	}
	cases := []struct {
		name   string
		disarm func(*guard)
	}{
		{"fetch-enable-failed", func(g *guard) {
			g.mu.Lock()
			if nt := g.navTargets[esTarget]; nt != nil {
				nt.fetchArmed = false
			}
			g.mu.Unlock()
		}},
		{"mid-re-enable", func(g *guard) {
			g.mu.Lock()
			if nt := g.navTargets[esTarget]; nt != nil {
				nt.fetchArmed = false
				nt.documentsAsked = true
			}
			g.mu.Unlock()
		}},
		{"detached-nav-session", func(g *guard) {
			g.mu.Lock()
			if nt := g.navTargets[esTarget]; nt != nil {
				nt.fetchArmed = false
				nt.session = ""
			}
			g.mu.Unlock()
		}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			g := esGuard(t)
			_ = esAdmit(t, g)
			var o esOutcome
			infoID, admitTaint := holdThenDisarmStarted(t, g, &o, c.disarm)
			g.mu.Lock()
			after := g.docIdentityTaint[esTarget]
			g.mu.Unlock()
			if after == admitTaint {
				t.Fatalf("%s: frameStartedNavigating must bump taint when Fetch unarmed", c.name)
			}
			esSettleOrdinary(g, &o, infoID)
			if !esSessionCut(g) && !strings.Contains(strings.Join(o.payloads, ""), "slicc: refused:") {
				t.Fatalf("%s: must cut or refuse after unarmed network start; payloads=%v", c.name, o.payloads)
			}
		})
	}
	t.Run("armed-allows", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		g.mu.Lock()
		armed := g.docIdentityNavFetchArmedLocked(esTarget)
		allow := g.docIdentityStartedNetworkAllowLocked(esTarget, network)
		g.mu.Unlock()
		if !armed || !allow {
			t.Fatalf("armed nav Fetch must allow; armed=%t allow=%t", armed, allow)
		}
	})
}

func TestExistingSessionEmptyPostReadURL(t *testing.T) {
	holdAndAsk := func(t *testing.T, g *guard, o *esOutcome) (infoID int64) {
		t.Helper()
		cmd := esCommand{label: "Runtime.evaluate", method: "Runtime.evaluate", params: map[string]any{"expression": "1"}}
		pv := g.fromCommand(&command{id: int64(esReadID), method: cmd.method, session: esSession, params: cmd.params}, int64(esReadID), true)
		if pv.forward == nil {
			t.Fatalf("admit must forward: %s", pv.reply)
		}
		held := g.fromBrowser(esContentReply(cmd, int64(esReadID), esOrdinary, true))
		o.take(held)
		for _, raw := range held.send {
			id, method, session := esOutbound(raw)
			if method == "Target.getTargetInfo" && session == "RELAY" {
				infoID = id
			}
		}
		if infoID == 0 {
			t.Fatal("expected getTargetInfo")
		}
		return infoID
	}
	nextAsk := func(g *guard, o *esOutcome, v browserVerdict) int64 {
		o.take(v)
		for _, raw := range v.send {
			id, method, session := esOutbound(raw)
			if method == "Target.getTargetInfo" && session == "RELAY" {
				return id
			}
		}
		return 0
	}
	t.Run("empty-then-ordinary-delivers", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		infoID := holdAndAsk(t, g, &o)
		infoID = nextAsk(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, "")))
		if infoID == 0 {
			t.Fatal("empty URL must re-ask getTargetInfo")
		}
		esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, esOrdinaryB)), esOrdinaryB)
		if esSessionCut(g) || esHasRetriable(o) {
			t.Fatalf("empty then ordinary must deliver; payloads=%v", o.payloads)
		}
		if !strings.Contains(strings.Join(o.payloads, ""), fmt.Sprintf(`"id":%d`, esReadID)) {
			t.Fatalf("expected delivered evaluate; payloads=%v", o.payloads)
		}
	})
	t.Run("empty-at-bound-retriable", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		infoID := holdAndAsk(t, g, &o)
		for i := 0; i < maxDocIdentityEmptyAsks; i++ {
			v := g.fromBrowser(esTargetInfoAnswer(infoID, ""))
			next := nextAsk(g, &o, v)
			if i+1 < maxDocIdentityEmptyAsks {
				if next == 0 {
					t.Fatalf("empty try %d must re-ask", i+1)
				}
				infoID = next
				continue
			}
			if next != 0 {
				t.Fatal("empty at bound must not re-ask again")
			}
			o.payloads = append(o.payloads, stringBytes(g.takePendingPageLocked())...)
		}
		if !esHasRetriable(o) {
			t.Fatalf("empty at bound must be retriable; payloads=%v", o.payloads)
		}
		if esSessionCut(g) {
			t.Fatal("empty at bound must not cut")
		}
		if strings.Contains(strings.Join(o.payloads, ""), esSecret) {
			t.Fatal("empty at bound must not deliver")
		}
	})
	t.Run("empty-then-protected-cuts", func(t *testing.T) {
		g := esGuard(t)
		_ = esAdmit(t, g)
		var o esOutcome
		infoID := holdAndAsk(t, g, &o)
		infoID = nextAsk(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, "")))
		if infoID == 0 {
			t.Fatal("empty URL must re-ask")
		}
		live := esProtectedURLs()[0]
		esDrain(g, &o, g.fromBrowser(esTargetInfoAnswer(infoID, live)), live)
		if !esSessionCut(g) && !strings.Contains(strings.Join(o.payloads, ""), "slicc: refused:") {
			t.Fatalf("empty then protected must cut; payloads=%v", o.payloads)
		}
		if strings.Contains(strings.Join(o.payloads, ""), esSecret) {
			t.Fatal("must not deliver secret")
		}
	})
}

func TestExistingSessionDuplicatePaintArmDropsIdentityHold(t *testing.T) {
	g := esGuard(t)
	_ = esAdmit(t, g)
	method := "Page.captureScreenshot"
	g.mu.Lock()
	g.targets["ES-EXT"] = &target{id: "ES-EXT", typ: "iframe", url: esExtFrameURL, ctx: "CTX", parentFrame: esTarget}
	g.noteExtensionFrameURLLocked(esTarget, "ES-EXT", esExtFrameURL)
	g.heldExtensionPaints = map[callKey]*heldExtensionPaint{
		{esSession, int64(esReadID)}: {generation: 1, deadline: time.Now().Add(time.Minute), probeDeferred: true},
	}
	g.mu.Unlock()
	var o esOutcome
	id := int64(esReadID)
	pv := g.afterExtensionShown(&command{id: id, method: method, session: esSession, params: map[string]any{}}, false, true)
	if pv.forward == nil {
		t.Fatalf("admit must forward: %s", pv.reply)
	}
	reply := esContentReply(esCommand{method: method}, id, esOrdinary, true)
	v := g.fromBrowser(reply)
	o.take(v)
	g.mu.Lock()
	_, idLeft := g.docIdentityHolds[callKey{esSession, id}]
	_, askLeft := g.docIdentityAsks[esTarget]
	g.mu.Unlock()
	if idLeft || askLeft {
		t.Fatalf("duplicate paint arm refusal must drop identity hold and ask; hold=%t ask=%t", idLeft, askLeft)
	}
	joined := strings.Join(o.payloads, "")
	if !strings.Contains(joined, protect.ShownReason()) {
		t.Fatalf("expected Shown refusal; payloads=%v", o.payloads)
	}
	if strings.Contains(joined, fmt.Sprintf(`"id":%d`, id)) && strings.Contains(joined, `"result"`) {
		t.Fatalf("must not also deliver the paint result; payloads=%v", o.payloads)
	}
}

func TestExistingSessionExtensionPaintDeadlineRelayAuthoritative(t *testing.T) {
	t.Setenv(extensionPaintHoldEnv, "40ms")
	g := esGuard(t)
	_ = esAdmit(t, g)
	g.mu.Lock()
	g.targets["ES-EXT"] = &target{id: "ES-EXT", typ: "iframe", url: esExtFrameURL, ctx: "CTX", parentFrame: esTarget}
	g.noteExtensionFrameURLLocked(esTarget, "ES-EXT", esExtFrameURL)
	g.mu.Unlock()
	id := int64(esReadID)
	method := "Page.captureScreenshot"
	pv := g.afterExtensionShown(&command{id: id, method: method, session: esSession, params: map[string]any{}}, false, true)
	if pv.forward == nil {
		t.Fatalf("admit must forward: %s", pv.reply)
	}
	reply := esContentReply(esCommand{method: method}, id, esOrdinary, true)
	v := g.fromBrowser(reply)
	if len(v.extensionPaints) != 1 {
		t.Fatalf("arm must return extensionPaints for relay expireExtensionPaintLater; got %d", len(v.extensionPaints))
	}
	deadline := v.extensionPaints[0].deadline
	gen := v.extensionPaints[0].generation
	time.Sleep(time.Until(deadline) + 30*time.Millisecond)
	g.mu.Lock()
	_, paintLeft := g.heldExtensionPaints[callKey{esSession, id}]
	g.mu.Unlock()
	if !paintLeft {
		t.Fatal("guard must not self-expire paint holds; relay expireExtensionPaintLater is authoritative")
	}
	page, _ := g.expireExtensionPaint(callKey{esSession, id}, gen)
	if len(page) == 0 {
		t.Fatal("relay-style expireExtensionPaint must refuse after deadline")
	}
}
