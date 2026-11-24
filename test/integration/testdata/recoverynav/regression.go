package cdp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	rnTarget      = "RN-PAGE"
	rnClient      = "RN-CLIENT"
	rnNav         = "RN-GUARD"
	rnAttachID    = 70
	rnAway        = "https://ordinary.test/away"
	rnProtected   = "https://seven.sliccy.ai/missing?q=secret#frag"
	rnVault       = "https://vault.sliccy.ai/doc"
	rnOrdinaryErr = "https://ordinary.test/missing"
	rnUnparseable = "http://exa mple.sliccy.ai/protected"
	rnChromeError = "chrome-error://chromewebdata/"
)

type rnChrome struct {
	t           *testing.T
	g           *guard
	failed      string
	url         string
	origin      string
	unreachable string
	loader      string
	info        string
	title       string
	hold        bool
	held        []int64
	next        int64
	client      []string
}

func rnNewChrome(t *testing.T) *rnChrome {
	t.Helper()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, nil)
	g.relaySession = "RELAY"
	return &rnChrome{t: t, g: g, url: rnAway, origin: "https://ordinary.test", loader: "L0", info: rnAway, title: "away", next: 100}
}

func rnOutbound(raw []byte) (id int64, method, session string) {
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

func (c *rnChrome) take(v browserVerdict) {
	if v.forward != nil {
		c.client = append(c.client, string(v.forward))
	}
	for _, p := range v.page {
		c.client = append(c.client, string(p))
	}
	c.outbound(v.send)
}

func (c *rnChrome) outbound(send [][]byte) {
	for _, raw := range send {
		id, method, session := rnOutbound(raw)
		switch method {
		case "Page.getFrameTree":
			if c.hold {
				c.held = append(c.held, id)
				continue
			}
			c.feed(c.frameTreeAnswer(id, session))
		case "Target.getTargetInfo":
			c.feed(encode(map[string]any{"id": id, "sessionId": session, "result": map[string]any{
				"targetInfo": map[string]any{"targetId": rnTarget, "type": "page", "url": c.info, "title": c.title, "browserContextId": "CTX"},
			}}))
		}
	}
}

func (c *rnChrome) frameTreeAnswer(id int64, session string) []byte {
	frame := map[string]any{"id": rnTarget, "loaderId": c.loader, "url": c.url, "securityOrigin": c.origin, "mimeType": "text/html"}
	if c.unreachable != "" {
		frame["unreachableUrl"] = c.unreachable
	}
	m := map[string]any{"id": id, "result": map[string]any{"frameTree": map[string]any{"frame": frame}}}
	if session != "" {
		m["sessionId"] = session
	}
	return encode(m)
}

func (c *rnChrome) feed(raw []byte) {
	c.take(c.g.fromBrowser(raw))
}

func (c *rnChrome) answerHeld(fail bool) {
	for round := 0; len(c.held) > 0 && round < 16; round++ {
		held := c.held
		c.held = nil
		for _, id := range held {
			if fail {
				c.feed(encode(map[string]any{"id": id, "sessionId": rnNav, "error": map[string]any{"code": -32000, "message": "Frame tree is not available"}}))
				continue
			}
			c.feed(c.frameTreeAnswer(id, rnNav))
		}
	}
}

func rnEvent(session, method string, params map[string]any) []byte {
	m := map[string]any{"method": method, "params": params}
	if session != "" {
		m["sessionId"] = session
	}
	return encode(m)
}

func (c *rnChrome) frameParams() map[string]any {
	frame := map[string]any{"id": rnTarget, "loaderId": c.loader, "url": c.url, "securityOrigin": c.origin, "mimeType": "text/html"}
	if c.unreachable != "" {
		frame["unreachableUrl"] = c.unreachable
	}
	return map[string]any{"frame": frame, "type": "Navigation"}
}

func (c *rnChrome) targetInfo() map[string]any {
	return map[string]any{"targetInfo": map[string]any{"targetId": rnTarget, "type": "page", "url": c.info, "title": c.title, "browserContextId": "CTX", "attached": true}}
}

func (c *rnChrome) started(url, loader string) {
	p := map[string]any{"frameId": rnTarget, "loaderId": loader, "url": url, "navigationType": "differentDocument"}
	c.feed(rnEvent(rnNav, "Page.frameStartedNavigating", p))
	c.feed(rnEvent(rnClient, "Page.frameStartedNavigating", p))
}

func (c *rnChrome) paused(url, request string) {
	c.feed(rnEvent(rnNav, "Fetch.requestPaused", map[string]any{
		"requestId": "F-" + request, "networkId": request, "frameId": rnTarget, "resourceType": "Document",
		"request": map[string]any{"url": url, "method": "GET", "headers": map[string]any{}},
	}))
}

func (c *rnChrome) loadingFailed(request string) {
	p := map[string]any{"requestId": request, "type": "Document", "errorText": "net::ERR_BLOCKED_BY_CLIENT", "canceled": false}
	c.feed(rnEvent(rnNav, "Network.loadingFailed", p))
}

func (c *rnChrome) commitError(failed, loader string) {
	c.url, c.origin, c.unreachable, c.loader = rnChromeError, "://", failed, loader
}

func (c *rnChrome) commitDocument(url, origin, loader string) {
	c.url, c.origin, c.unreachable, c.loader = url, origin, "", loader
}

func (c *rnChrome) guardCommit() {
	c.feed(rnEvent(rnNav, "Page.frameNavigated", c.frameParams()))
}

func (c *rnChrome) clientCommit() {
	c.feed(rnEvent(rnClient, "Page.frameNavigated", c.frameParams()))
}

func (c *rnChrome) infoChanged(url, title string) {
	c.info, c.title = url, title
	c.feed(rnEvent("RELAY", "Target.targetInfoChanged", c.targetInfo()))
}

func (c *rnChrome) stopped() {
	p := map[string]any{"frameId": rnTarget}
	c.feed(rnEvent(rnNav, "Page.frameStoppedLoading", p))
	c.feed(rnEvent(rnClient, "Page.frameStoppedLoading", p))
}

func (c *rnChrome) attach() {
	t := c.t
	t.Helper()
	c.feed(rnEvent("RELAY", "Target.targetCreated", c.targetInfo()))
	ready := make(chan struct{})
	close(ready)
	nt := &navTarget{target: rnTarget, session: rnNav, ready: ready, done: true, fetchArmed: true, documentURL: rnAway, securityOrigin: "https://ordinary.test", loaderID: "L0"}
	c.g.navTargets[rnTarget] = nt
	c.g.navSessions[rnNav] = nt
	pv := c.g.fromCommand(&command{id: rnAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": rnTarget, "flatten": true}}, rnAttachID, true)
	if pv.reply != nil {
		t.Fatalf("client attach refused: %s", pv.reply)
	}
	c.outbound(pv.send)
	c.feed(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{
		"sessionId": rnClient, "waitingForDebugger": false,
		"targetInfo": map[string]any{"targetId": rnTarget, "type": "page", "url": rnAway, "title": "away", "browserContextId": "CTX", "attached": true},
	}}))
	c.feed(encode(map[string]any{"id": rnAttachID, "result": map[string]any{"sessionId": rnClient}}))
	if c.g.sessions[rnClient] == nil {
		t.Fatalf("client session not admitted: %v", c.client)
	}
}

func (c *rnChrome) failLoad(failed string) {
	c.failed = failed
	c.started(failed, "L1")
	if c.g.o.Loaded(failed) {
		c.paused(failed, "L1")
	}
	c.loadingFailed("L1")
	c.commitError(failed, "L1")
	c.guardCommit()
	c.clientCommit()
	c.infoChanged(rnChromeError, protect.Host(failed))
	c.stopped()
	c.answerHeld(false)
}

func rnSettledErrorPage(t *testing.T, failed string) *rnChrome {
	t.Helper()
	c := rnNewChrome(t)
	c.attach()
	c.failLoad(failed)
	nt := c.g.navTargets[rnTarget]
	if nt == nil || !nt.done || nt.failed || nt.confirm == nil || !nt.confirm.done || nt.confirm.failed || nt.errorURL != failed {
		t.Fatalf("error page not settled: nt=%+v", nt)
	}
	if c.g.errorPageSessionLocked(rnClient) != c.g.o.Loaded(failed) {
		t.Fatalf("error page session = %t for %s", c.g.errorPageSessionLocked(rnClient), failed)
	}
	c.hold = true
	return c
}

func (c *rnChrome) step(name string) {
	switch name {
	case "started":
		c.started(c.failed, "L2")
	case "paused":
		if c.g.o.Loaded(c.failed) {
			c.paused(c.failed, "L2")
		}
	case "loadingFailed":
		c.loadingFailed("L2")
	case "guardCommit":
		c.commitError(c.failed, "L2")
		c.guardCommit()
	case "clientCommit":
		c.commitError(c.failed, "L2")
		c.clientCommit()
	case "clientCommitAgain":
		c.clientCommit()
	case "info":
		c.infoChanged(rnChromeError, protect.Host(c.failed))
	case "infoAgain":
		c.infoChanged(rnChromeError, protect.Host(c.failed))
	case "stopped":
		c.stopped()
	case "confirm":
		c.answerHeld(false)
	case "confirmError":
		c.answerHeld(true)
	default:
		c.t.Fatalf("unknown step %q", name)
	}
}

func (c *rnChrome) command(method string, params map[string]any) *command {
	c.next++
	m := map[string]any{"id": c.next, "method": method, "sessionId": rnClient}
	if params != nil {
		m["params"] = params
	}
	cmd, _, ok := parseCommand(encode(m))
	if !ok {
		c.t.Fatalf("parse %s", method)
	}
	return cmd
}

func rnClassify(v pageVerdict) string {
	switch {
	case v.reply != nil:
		var r struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(v.reply, &r)
		if strings.HasPrefix(r.Error.Message, "slicc: refused: ") {
			return "refused: " + strings.TrimPrefix(r.Error.Message, "slicc: refused: ")
		}
		if r.Error.Message != "" {
			return "error: " + r.Error.Message
		}
		return "reply"
	case v.history != nil:
		return "history"
	case v.closeProof != nil:
		return "close-proof"
	case v.extension != nil:
		return "extension"
	case v.wait != nil:
		return "wait"
	case v.forward != nil:
		return "admit"
	}
	return "empty"
}

func (c *rnChrome) verdict(cmd *command) string {
	v := c.g.fromCommand(cmd, cmd.id, true)
	c.outbound(v.send)
	return rnClassify(v)
}

func (c *rnChrome) confirmOpenOrFailed() bool {
	nt := c.g.navTargets[rnTarget]
	return nt != nil && nt.confirm != nil && (!nt.confirm.done || nt.confirm.failed)
}

func rnClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

type rnIssue struct {
	outcome   string
	parked    bool
	gated     bool
	confirmAt bool
}

func (c *rnChrome) run(script []string, at int, mode string, method string, params map[string]any) rnIssue {
	var res rnIssue
	var parked *command
	var wait <-chan struct{}
	issue := func() {
		cmd := c.command(method, params)
		res.confirmAt = c.confirmOpenOrFailed()
		if mode == "relay" {
			if w := c.g.commandWait(cmd); w != nil {
				parked, wait, res.parked = cmd, w, true
				return
			}
		}
		if mode == "toctou" {
			if w := c.g.commandWait(cmd); w != nil {
				c.t.Fatalf("toctou row: the park check already waits at %d", at)
			}
			c.step(script[at])
			res.confirmAt = c.confirmOpenOrFailed()
		}
		res.outcome = c.verdict(cmd)
	}
	release := func() {
		for wait != nil && rnClosed(wait) {
			if w := c.g.commandWait(parked); w != nil {
				wait = w
				continue
			}
			wait = nil
			res.gated = c.confirmOpenOrFailed()
			res.outcome = c.verdict(parked)
		}
	}
	for i := 0; i <= len(script); i++ {
		if i == at {
			issue()
			if mode == "toctou" {
				continue
			}
		}
		release()
		if i < len(script) {
			c.step(script[i])
		}
	}
	release()
	if wait != nil {
		res.outcome = "parked"
	}
	return res
}

var (
	rnSettle   = []string{"started", "paused", "loadingFailed", "guardCommit", "clientCommit", "info", "stopped", "confirm"}
	rnFail     = []string{"started", "paused", "loadingFailed", "guardCommit", "clientCommit", "info", "stopped", "confirmError"}
	rnInfoLead = []string{"started", "paused", "loadingFailed", "info", "guardCommit", "clientCommit", "stopped", "confirm"}
)

var rnScripts = []struct {
	name   string
	script []string
}{
	{"settle", rnSettle},
	{"fail", rnFail},
	{"info-first", rnInfoLead},
}

func rnPosition(script []string, at int) string {
	if at == 0 {
		return "before-" + script[0]
	}
	return "after-" + script[at-1]
}

func rnAdmitted(outcome string) bool {
	return outcome == "admit" || outcome == "history" || outcome == "close-proof"
}

var rnRecovery = []struct {
	name   string
	method string
	params map[string]any
}{
	{"navigate-away", "Page.navigate", map[string]any{"url": rnAway}},
	{"reload", "Page.reload", map[string]any{}},
	{"page-close", "Page.close", nil},
}

func TestRecoveryNavOrderMatrix(t *testing.T) {
	for _, cmd := range rnRecovery {
		for _, sc := range rnScripts {
			for at := 0; at <= len(sc.script); at++ {
				for _, mode := range []string{"direct", "relay"} {
					name := fmt.Sprintf("%s/%s/%s/%s", cmd.name, sc.name, rnPosition(sc.script, at), mode)
					t.Run(name, func(t *testing.T) {
						c := rnSettledErrorPage(t, rnProtected)
						res := c.run(sc.script, at, mode, cmd.method, cmd.params)
						if !rnAdmitted(res.outcome) {
							t.Errorf("own-tab recovery %s on a protected error page was not admitted (parked=%t confirm-open-or-failed=%t gated=%t): %s", cmd.method, res.parked, res.confirmAt, res.gated, res.outcome)
						}
					})
				}
			}
		}
	}
}

func TestRecoveryNavToctouOpensConfirm(t *testing.T) {
	for _, cmd := range rnRecovery {
		t.Run(cmd.name, func(t *testing.T) {
			c := rnSettledErrorPage(t, rnProtected)
			res := c.run(rnSettle, 4, "toctou", cmd.method, cmd.params)
			if !res.confirmAt {
				t.Fatalf("the client commit did not open a confirm")
			}
			if !rnAdmitted(res.outcome) {
				t.Errorf("own-tab recovery %s that passed the park check before the client commit was refused: %s", cmd.method, res.outcome)
			}
		})
	}
}

var rnContent = []struct {
	name   string
	method string
	params map[string]any
}{
	{"evaluate", "Runtime.evaluate", map[string]any{"expression": "location.href"}},
	{"call-function", "Runtime.callFunctionOn", map[string]any{"functionDeclaration": "function() { return location.href; }"}},
	{"dom", "DOM.getDocument", map[string]any{}},
	{"outer-html", "DOM.getOuterHTML", map[string]any{}},
	{"snapshot", "DOMSnapshot.captureSnapshot", map[string]any{"computedStyles": []any{}}},
	{"screenshot", "Page.captureScreenshot", map[string]any{}},
	{"pdf", "Page.printToPDF", map[string]any{}},
	{"screencast", "Page.startScreencast", map[string]any{}},
	{"ax-tree", "Accessibility.getFullAXTree", map[string]any{}},
	{"resource-tree", "Page.getResourceTree", map[string]any{}},
	{"resource-content", "Page.getResourceContent", map[string]any{"frameId": rnTarget, "url": rnProtected}},
	{"scripted-reload", "Page.reload", map[string]any{"scriptToEvaluateOnLoad": "window.__x = 1"}},
	{"debugger-pause", "Debugger.pause", map[string]any{}},
}

func TestRecoveryNavContentStaysRefused(t *testing.T) {
	for _, cmd := range rnContent {
		for _, sc := range rnScripts {
			for at := 0; at <= len(sc.script); at++ {
				name := fmt.Sprintf("%s/%s/%s", cmd.name, sc.name, rnPosition(sc.script, at))
				t.Run(name, func(t *testing.T) {
					c := rnSettledErrorPage(t, rnProtected)
					res := c.run(sc.script, at, "direct", cmd.method, cmd.params)
					if !strings.HasPrefix(res.outcome, "refused: ") {
						t.Fatalf("%s on a protected error page must stay refused: %s", cmd.method, res.outcome)
					}
					if !res.confirmAt && res.outcome != "refused: "+refusedErrorPage {
						t.Errorf("%s on a settled protected error page: want the error-page refusal, got %s", cmd.method, res.outcome)
					}
				})
			}
		}
	}
}

var rnReads = []struct {
	name    string
	method  string
	params  map[string]any
	settled string
}{
	{"frame-tree", "Page.getFrameTree", nil, "admit"},
	{"navigation-history", "Page.getNavigationHistory", nil, "admit"},
	{"runtime-enable", "Runtime.enable", nil, "admit"},
	{"lifecycle", "Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}, "refused: " + refusedHistory},
	{"stop-loading", "Page.stopLoading", nil, "admit"},
	{"reset-history", "Page.resetNavigationHistory", nil, "admit"},
	{"bring-to-front", "Page.bringToFront", nil, "admit"},
	{"target-info", "Target.getTargetInfo", nil, "admit"},
}

func TestRecoveryNavReadsKeepConfirmGate(t *testing.T) {
	for _, cmd := range rnReads {
		for _, sc := range rnScripts {
			for at := 0; at <= len(sc.script); at++ {
				name := fmt.Sprintf("%s/%s/%s", cmd.name, sc.name, rnPosition(sc.script, at))
				t.Run(name, func(t *testing.T) {
					c := rnSettledErrorPage(t, rnProtected)
					res := c.run(sc.script, at, "direct", cmd.method, cmd.params)
					want := cmd.settled
					if res.confirmAt {
						want = "refused: " + refusedGuard
					}
					if res.outcome != want {
						t.Errorf("%s on a protected error page (confirm-open-or-failed=%t): want %q, got %q", cmd.method, res.confirmAt, want, res.outcome)
					}
				})
			}
		}
	}
}

func TestRecoveryNavDestinationsStayRefused(t *testing.T) {
	rows := []struct {
		name   string
		method string
		params map[string]any
		want   string
	}{
		{"navigate-protected", "Page.navigate", map[string]any{"url": rnProtected}, refusedTarget},
		{"navigate-vault", "Page.navigate", map[string]any{"url": rnVault}, refusedTarget},
		{"navigate-unparseable", "Page.navigate", map[string]any{"url": rnUnparseable}, refusedInheritedNavigation},
		{"navigate-about-blank", "Page.navigate", map[string]any{"url": "about:blank"}, refusedInheritedNavigation},
		{"navigate-javascript", "Page.navigate", map[string]any{"url": "javascript:location.href"}, "Page.navigate to a javascript: URL runs script in the page; use Runtime.evaluate"},
		{"scripted-reload", "Page.reload", map[string]any{"scriptToEvaluateOnLoad": "window.__x = 1"}, refusedErrorPage},
	}
	states := []struct {
		name   string
		script []string
		at     int
	}{
		{"settled", rnSettle, 8},
		{"confirm-open", rnSettle, 5},
		{"confirm-failed", rnFail, 8},
	}
	for _, row := range rows {
		for _, st := range states {
			t.Run(row.name+"/"+st.name, func(t *testing.T) {
				c := rnSettledErrorPage(t, rnProtected)
				res := c.run(st.script, st.at, "direct", row.method, row.params)
				if !strings.HasPrefix(res.outcome, "refused: ") {
					t.Fatalf("%s must stay refused: %s", row.name, res.outcome)
				}
				if !res.confirmAt && res.outcome != "refused: "+row.want {
					t.Errorf("%s: want %q, got %q", row.name, row.want, res.outcome)
				}
			})
		}
	}
}

func TestRecoveryNavGuardLostStaysRefused(t *testing.T) {
	for _, cmd := range rnRecovery {
		for _, st := range []struct {
			name   string
			script []string
			at     int
		}{{"settled", rnSettle, 8}, {"confirm-open", rnSettle, 5}, {"confirm-failed", rnFail, 8}} {
			t.Run(cmd.name+"/"+st.name, func(t *testing.T) {
				c := rnSettledErrorPage(t, rnProtected)
				for _, s := range st.script[:st.at] {
					c.step(s)
				}
				c.feed(rnEvent("RELAY", "Target.detachedFromTarget", map[string]any{"sessionId": rnNav, "targetId": rnTarget}))
				nt := c.g.navTargets[rnTarget]
				if nt == nil || !nt.failed {
					t.Fatalf("guard session loss did not fail the nav target: %+v", nt)
				}
				got := c.verdict(c.command(cmd.method, cmd.params))
				if got != "refused: "+refusedGuard {
					t.Errorf("%s after the guard lost the target must stay refused: %s", cmd.method, got)
				}
			})
		}
	}
}

func TestRecoveryNavRestoredProtectedStaysRefused(t *testing.T) {
	for _, cmd := range rnRecovery {
		t.Run(cmd.name, func(t *testing.T) {
			c := rnSettledErrorPage(t, rnProtected)
			c.step("started")
			c.commitDocument(rnProtected, "https://seven.sliccy.ai", "L2")
			c.guardCommit()
			c.clientCommit()
			got := c.verdict(c.command(cmd.method, cmd.params))
			if rnAdmitted(got) {
				t.Errorf("%s on a target that restored a protected document must stay refused: %s", cmd.method, got)
			}
		})
	}
}

func rnHistory(c *rnChrome) {
	nt := c.g.navTargets[rnTarget]
	raw, _ := json.Marshal(map[string]any{"currentIndex": 2, "entries": []any{
		map[string]any{"id": 1, "url": rnAway, "userTypedURL": rnAway, "title": "away"},
		map[string]any{"id": 2, "url": rnVault, "userTypedURL": rnVault, "title": "vault"},
		map[string]any{"id": 3, "url": rnProtected, "userTypedURL": rnProtected, "title": "seven.sliccy.ai"},
	}})
	c.g.rememberHistoryRefusals(nt, message{Result: raw})
}

func rnHistoryAnswer() message {
	raw, _ := json.Marshal(map[string]any{"currentIndex": 2, "entries": []any{
		map[string]any{"id": 1, "url": rnAway, "userTypedURL": rnAway, "title": "away"},
		map[string]any{"id": 2, "url": rnVault, "userTypedURL": rnVault, "title": "vault"},
		map[string]any{"id": 3, "url": rnChromeError, "userTypedURL": rnProtected, "title": "seven.sliccy.ai"},
	}})
	return message{Result: raw}
}

func TestRecoveryNavHistoryKeepsItsRules(t *testing.T) {
	states := []struct {
		name   string
		script []string
		at     int
	}{
		{"settled", rnSettle, 8},
		{"confirm-open", rnSettle, 5},
		{"confirm-failed", rnFail, 8},
	}
	for _, st := range states {
		t.Run("ordinary-entry/"+st.name, func(t *testing.T) {
			c := rnSettledErrorPage(t, rnProtected)
			rnHistory(c)
			for _, s := range st.script[:st.at] {
				c.step(s)
			}
			cmd := c.command("Page.navigateToHistoryEntry", map[string]any{"entryId": json.Number("1")})
			first := c.verdict(cmd)
			if first != "history" {
				t.Fatalf("history entry to an ordinary page was not sent to the history check: %s", first)
			}
			after := rnClassify(c.g.afterHistory(cmd, rnHistoryAnswer(), true))
			if after != "admit" {
				t.Errorf("own-tab recovery back to an ordinary entry was refused: %s", after)
			}
		})
		for _, entry := range []string{"2", "3"} {
			t.Run("protected-entry-"+entry+"/"+st.name, func(t *testing.T) {
				c := rnSettledErrorPage(t, rnProtected)
				rnHistory(c)
				for _, s := range st.script[:st.at] {
					c.step(s)
				}
				cmd := c.command("Page.navigateToHistoryEntry", map[string]any{"entryId": json.Number(entry)})
				first := c.verdict(cmd)
				if first == "history" {
					first = rnClassify(c.g.afterHistory(cmd, rnHistoryAnswer(), true))
				}
				if !strings.HasPrefix(first, "refused: ") {
					t.Errorf("history entry %s that restores a protected document must stay refused: %s", entry, first)
				}
			})
		}
	}
}

func TestRecoveryNavOrdinaryTwin(t *testing.T) {
	for _, cmd := range rnRecovery {
		t.Run("ordinary-error-page/"+cmd.name, func(t *testing.T) {
			c := rnSettledErrorPage(t, rnOrdinaryErr)
			for _, st := range []struct {
				name   string
				script []string
				at     int
				want   string
			}{
				{"confirm-open", rnSettle, 5, "refused: " + refusedGuard},
				{"confirm-failed", rnFail, 8, "refused: " + refusedGuard},
			} {
				cc := rnSettledErrorPage(t, rnOrdinaryErr)
				res := cc.run(st.script, st.at, "direct", cmd.method, cmd.params)
				if !res.confirmAt || res.outcome != st.want {
					t.Errorf("%s on an ordinary error page with the confirm %s: want %q, got %q (confirm-open-or-failed=%t)", cmd.method, st.name, st.want, res.outcome, res.confirmAt)
				}
			}
			res := c.run(rnSettle, 8, "direct", cmd.method, cmd.params)
			if !rnAdmitted(res.outcome) {
				t.Errorf("%s on a settled ordinary error page: %s", cmd.method, res.outcome)
			}
		})
		t.Run("ordinary-page/"+cmd.name, func(t *testing.T) {
			for _, fail := range []bool{false, true} {
				c := rnNewChrome(t)
				c.attach()
				c.hold = true
				c.started("about:blank", "L1")
				c.commitDocument("about:blank", "https://ordinary.test", "L1")
				c.guardCommit()
				c.clientCommit()
				if fail {
					c.answerHeld(true)
				}
				if !c.confirmOpenOrFailed() {
					t.Fatalf("ordinary page: no confirm open (fail=%t)", fail)
				}
				got := c.verdict(c.command(cmd.method, cmd.params))
				if got != "refused: "+refusedGuard {
					t.Errorf("%s on an ordinary page with an unsettled confirm (fail=%t) must keep its refusal: %s", cmd.method, fail, got)
				}
			}
		})
	}
}

func TestRecoveryNavSameValue(t *testing.T) {
	for _, cmd := range rnRecovery {
		t.Run("repeat/"+cmd.name, func(t *testing.T) {
			c := rnSettledErrorPage(t, rnProtected)
			for _, s := range rnFail {
				c.step(s)
			}
			for i := 0; i < 3; i++ {
				if got := c.verdict(c.command(cmd.method, cmd.params)); !rnAdmitted(got) {
					t.Errorf("repeat %d of %s after a failed confirm: %s", i, cmd.method, got)
				}
			}
			if got := c.verdict(c.command("Runtime.evaluate", map[string]any{"expression": "1"})); !strings.HasPrefix(got, "refused: ") {
				t.Errorf("content after repeated recovery commands: %s", got)
			}
		})
		t.Run("replayed-events/"+cmd.name, func(t *testing.T) {
			script := []string{"started", "paused", "loadingFailed", "guardCommit", "clientCommit", "clientCommitAgain", "info", "infoAgain", "stopped", "confirm"}
			for at := 0; at <= len(script); at++ {
				c := rnSettledErrorPage(t, rnProtected)
				res := c.run(script, at, "direct", cmd.method, cmd.params)
				if !rnAdmitted(res.outcome) {
					t.Errorf("%s at %s with replayed events: %s", cmd.method, rnPosition(script, at), res.outcome)
				}
			}
		})
	}
}

func TestRecoveryNavRecoversAfterNavigate(t *testing.T) {
	for _, st := range []struct {
		name   string
		script []string
		at     int
	}{{"confirm-open", rnSettle, 5}, {"confirm-failed", rnFail, 8}, {"settled", rnSettle, 8}} {
		t.Run(st.name, func(t *testing.T) {
			c := rnSettledErrorPage(t, rnProtected)
			for _, s := range st.script[:st.at] {
				c.step(s)
			}
			if got := c.verdict(c.command("Page.navigate", map[string]any{"url": rnAway})); got != "admit" {
				t.Fatalf("recovery navigate: %s", got)
			}
			c.started(rnAway, "L3")
			c.commitDocument(rnAway, "https://ordinary.test", "L3")
			c.guardCommit()
			c.clientCommit()
			c.infoChanged(rnAway, "away")
			c.stopped()
			c.answerHeld(false)
			if c.confirmOpenOrFailed() {
				t.Fatalf("confirm still open or failed after the recovery commit: %+v", c.g.navTargets[rnTarget].confirm)
			}
			if c.g.errorPageSessionLocked(rnClient) {
				t.Fatal("still an error page after the recovery commit")
			}
			if got := c.verdict(c.command("Page.getFrameTree", nil)); got != "admit" {
				t.Errorf("frame tree after recovery: %s", got)
			}
			if got := c.verdict(c.command("Page.resetNavigationHistory", nil)); got != "admit" {
				t.Errorf("reset after recovery: %s", got)
			}
			if got := c.verdict(c.command("Runtime.evaluate", map[string]any{"expression": "location.href"})); got != "refused: "+refusedHistory {
				t.Errorf("evaluate after recovery keeps the history lock: %s", got)
			}
			for _, p := range c.client {
				if out := string(c.g.scrub([]byte(p))); strings.Contains(out, "q=secret") {
					t.Errorf("client saw the protected URL: %s", out)
				}
			}
		})
	}
}

func TestRecoveryNavLiveFixtureShape(t *testing.T) {
	for _, mode := range []string{"toctou", "relay"} {
		t.Run(mode, func(t *testing.T) {
			c := rnSettledErrorPage(t, rnProtected)
			c.hold = false
			frame := c.verdict(c.command("Page.getFrameTree", nil))
			evaluate := c.verdict(c.command("Runtime.evaluate", map[string]any{"expression": "location.href"}))
			dom := c.verdict(c.command("DOM.getDocument", map[string]any{}))
			screenshot := c.verdict(c.command("Page.captureScreenshot", map[string]any{}))
			reload := c.verdict(c.command("Page.reload", map[string]any{}))
			c.hold = true
			res := c.run(rnSettle, 4, mode, "Page.navigate", map[string]any{"url": rnAway})
			if rnAdmitted(res.outcome) {
				c.started(rnAway, "L3")
				c.commitDocument(rnAway, "https://ordinary.test", "L3")
				c.guardCommit()
				c.clientCommit()
				c.infoChanged(rnAway, "away")
				c.stopped()
				c.answerHeld(false)
			}
			reset := c.verdict(c.command("Page.resetNavigationHistory", nil))
			afterReset := c.verdict(c.command("Runtime.evaluate", map[string]any{"expression": "location.href"}))
			tuple := fmt.Sprintf("Frame=%s Evaluate=%s DOM=%s Screenshot=%s Reload=%s Navigate=%s Reset=%s AfterReset=%s", frame, evaluate, dom, screenshot, reload, res.outcome, reset, afterReset)
			refused := func(s string) bool { return strings.HasPrefix(s, "refused: ") }
			if frame != "admit" || reload != "admit" || reset != "admit" || !refused(evaluate) || !refused(dom) || !refused(screenshot) || !refused(afterReset) {
				t.Fatalf("fixture shape changed: %s", tuple)
			}
			if !rnAdmitted(res.outcome) {
				t.Errorf("trial recovery (parked=%t): %s", res.parked, tuple)
			}
		})
	}
}

func TestRecoveryNavUnknownLookup(t *testing.T) {
	cmds := append(append([]struct {
		name   string
		method string
		params map[string]any
	}{}, rnRecovery...), struct {
		name   string
		method string
		params map[string]any
	}{"history", "Page.navigateToHistoryEntry", map[string]any{"entryId": json.Number("1")}})
	rows := []struct {
		name    string
		session string
		prepare func(g *guard)
		want    string
	}{
		{"unknown-session", "RN-UNKNOWN", func(*guard) {}, `{"error":{"code":-32001,"message":"Session with given id not found."},"id":%d,"sessionId":"RN-UNKNOWN"}`},
		{"pending-session", "RN-PENDING", func(g *guard) { _ = g.sessionWait("RN-PENDING") }, "PENDING"},
		{"target-without-nav-guard", "RN-ORPHAN", func(g *guard) {
			g.targets["RN-NOWHERE"] = &target{id: "RN-NOWHERE", typ: "page", url: rnChromeError}
			g.sessions["RN-ORPHAN"] = &session{id: "RN-ORPHAN", target: "RN-NOWHERE", typ: "page"}
		}, `{"error":{"code":-32000,"message":"slicc: refused: slicc can't keep seven's origin out of this target"},"id":%d,"sessionId":"RN-ORPHAN"}`},
	}
	for _, row := range rows {
		for _, cmd := range cmds {
			t.Run(row.name+"/"+cmd.name, func(t *testing.T) {
				c := rnSettledErrorPage(t, rnProtected)
				for _, s := range rnFail {
					c.step(s)
				}
				row.prepare(c.g)
				m := map[string]any{"id": 500, "method": cmd.method, "sessionId": row.session}
				if cmd.params != nil {
					m["params"] = cmd.params
				}
				parsed, _, ok := parseCommand(encode(m))
				if !ok {
					t.Fatal("parse")
				}
				if c.g.errorPageSessionLocked(row.session) {
					t.Fatalf("%s counts as an error page", row.session)
				}
				v := c.g.fromCommand(parsed, 500, true)
				got := string(v.reply)
				if v.reply == nil {
					got = rnClassify(v)
				}
				want := fmt.Sprintf(row.want, 500)
				if row.want == "PENDING" {
					want = "admit"
					if cmd.method == "Page.navigateToHistoryEntry" {
						want = "history"
					}
				}
				if got != want {
					t.Errorf("%s %s: want %s, got %s", row.name, cmd.method, want, got)
				}
			})
		}
	}
}
