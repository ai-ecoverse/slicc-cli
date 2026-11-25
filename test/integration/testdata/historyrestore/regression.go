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
	hrTarget     = "HS-PAGE"
	hrClient     = "HS-CLIENT"
	hrNav        = "HS-GUARD"
	hrAttachID   = 70
	hrOrdinary   = "https://ordinary.test/page"
	hrSeven      = "https://seven.sliccy.ai/"
	hrTextTarget = "target is on seven's origin"
)

type hrChrome struct {
	t        *testing.T
	g        *guard
	typ      string
	url      string
	origin   string
	unreach  string
	loader   string
	title    string
	history  []map[string]any
	current  int
	hold     bool
	skipInfo bool
	asked    bool
	kids     map[string]map[string]any
	planted  map[string]string
	held     [][]byte
	next     int64
	client   []string
	guardOut []string
	logs     []string
	extras   int
}

func hrNewChrome(t *testing.T) *hrChrome {
	t.Helper()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	c := &hrChrome{t: t, typ: "page", url: hrOrdinary, origin: "https://ordinary.test", loader: "L0", title: "page", next: 100, kids: map[string]map[string]any{}, planted: map[string]string{}}
	c.g = newGuard(o, func(format string, args ...any) { c.logs = append(c.logs, fmt.Sprintf(format, args...)) })
	c.g.relaySession = "RELAY"
	c.history = []map[string]any{
		{"id": 1, "url": hrOrdinary, "userTypedURL": hrOrdinary, "title": "page", "transitionType": "typed"},
	}
	return c
}

func (c *hrChrome) protectedEntry() {
	c.history = []map[string]any{
		{"id": 1, "url": hrSeven, "userTypedURL": hrSeven, "title": "seven", "transitionType": "typed"},
		{"id": 2, "url": c.url, "userTypedURL": c.url, "title": "page", "transitionType": "link"},
	}
	c.current = 1
}

func hrOutbound(raw []byte) (id int64, method, session string, params map[string]any) {
	var m struct {
		ID        *int64         `json:"id"`
		Method    string         `json:"method"`
		SessionID string         `json:"sessionId"`
		Params    map[string]any `json:"params"`
	}
	_ = json.Unmarshal(raw, &m)
	if m.ID != nil {
		id = *m.ID
	}
	return id, m.Method, m.SessionID, m.Params
}

func (c *hrChrome) take(v browserVerdict) {
	if v.forward != nil {
		c.client = append(c.client, string(v.forward))
	}
	for _, p := range v.page {
		c.client = append(c.client, string(p))
	}
	before := len(c.guardOut)
	c.outbound(v.send)
	c.extras += len(c.guardOut) - before
}

func hrReply(id int64, session string, result any) []byte {
	m := map[string]any{"id": id, "result": result}
	if session != "" {
		m["sessionId"] = session
	}
	return encode(m)
}

func (c *hrChrome) frame() map[string]any {
	f := map[string]any{"id": hrTarget, "loaderId": c.loader, "url": c.url, "securityOrigin": c.origin, "mimeType": "text/html"}
	if c.unreach != "" {
		f["unreachableUrl"] = c.unreach
	}
	return f
}

func (c *hrChrome) info() map[string]any {
	return map[string]any{"targetId": hrTarget, "type": c.typ, "url": c.url, "title": c.title, "browserContextId": "CTX", "attached": true}
}

func (c *hrChrome) outbound(send [][]byte) {
	for _, raw := range send {
		c.guardOut = append(c.guardOut, string(raw))
		id, method, session, params := hrOutbound(raw)
		target, onGuard := c.guardTarget(session)
		switch method {
		case "Target.attachToTarget":
			if session != "RELAY" {
				continue
			}
			want, _ := params["targetId"].(string)
			if want == hrTarget {
				c.feed(hrReply(id, session, map[string]any{"sessionId": hrNav}))
			} else if c.kids[want] != nil {
				c.feed(hrReply(id, session, map[string]any{"sessionId": "G-" + want}))
			}
		case "Page.getNavigationHistory":
			c.asked = true
			c.feed(hrReply(id, session, map[string]any{"currentIndex": c.current, "entries": c.history}))
		case "Target.getTargetInfo":
			want, _ := params["targetId"].(string)
			if want != hrTarget {
				if k := c.kids[want]; k != nil {
					c.feed(hrReply(id, session, map[string]any{"targetInfo": k}))
				}
				continue
			}
			if c.skipInfo && c.asked {
				continue
			}
			c.feed(hrReply(id, session, map[string]any{"targetInfo": c.info()}))
		case "Page.getFrameTree":
			if c.hold && (target == hrTarget || c.planted[target] != "") {
				c.held = append(c.held, raw)
				continue
			}
			c.feed(hrReply(id, session, map[string]any{"frameTree": map[string]any{"frame": c.frameOf(target)}}))
		case "Target.detachFromTarget", "Runtime.runIfWaitingForDebugger":
		default:
			if id != 0 && onGuard {
				c.feed(hrReply(id, session, map[string]any{}))
			}
		}
	}
}

func (c *hrChrome) guardTarget(session string) (string, bool) {
	if session == hrNav {
		return hrTarget, true
	}
	if id, ok := strings.CutPrefix(session, "G-"); ok {
		return id, true
	}
	for id, nav := range c.planted {
		if nav == session {
			return id, true
		}
	}
	return "", false
}

func (c *hrChrome) frameOf(target string) map[string]any {
	if target == hrTarget || target == "" {
		return c.frame()
	}
	if nav, ok := c.planted[target]; ok {
		nt := c.g.navTargets[target]
		loader, url := "L-"+target, hrOrdinary
		if nt != nil {
			if nt.loaderID != "" {
				loader = nt.loaderID
			}
			if nt.documentURL != "" {
				url = nt.documentURL
			}
		}
		_ = nav
		return map[string]any{"id": target, "loaderId": loader, "url": url, "securityOrigin": hrOriginOf(url), "mimeType": "text/html"}
	}
	k := c.kids[target]
	if k == nil {
		return map[string]any{}
	}
	url, _ := k["url"].(string)
	return map[string]any{"id": target, "parentId": hrTarget, "loaderId": "K-" + target, "url": url, "securityOrigin": hrOriginOf(url), "mimeType": "text/html"}
}

func (c *hrChrome) answerHeldOne(fail bool) bool {
	if len(c.held) == 0 {
		return false
	}
	raw := c.held[0]
	c.held = c.held[1:]
	id, _, session, _ := hrOutbound(raw)
	if fail {
		c.feed(encode(map[string]any{"id": id, "sessionId": session, "error": map[string]any{"code": -32000, "message": "Frame tree is not available"}}))
		return true
	}
	target, _ := c.guardTarget(session)
	c.feed(hrReply(id, session, map[string]any{"frameTree": map[string]any{"frame": c.frameOf(target)}}))
	return true
}

func (c *hrChrome) answerHeld(fail bool) {
	for round := 0; len(c.held) > 0 && round < 16; round++ {
		if !c.answerHeldOne(fail) {
			return
		}
	}
}

func (c *hrChrome) feed(raw []byte) {
	c.take(c.g.fromBrowser(raw))
}

func hrEvent(session, method string, params map[string]any) []byte {
	m := map[string]any{"method": method, "params": params}
	if session != "" {
		m["sessionId"] = session
	}
	return encode(m)
}

func (c *hrChrome) attach() {
	t := c.t
	t.Helper()
	c.feed(hrEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": c.info()}))
	pv := c.g.fromCommand(&command{id: hrAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": hrTarget, "flatten": true}}, hrAttachID, true)
	if pv.reply != nil {
		t.Fatalf("client attach refused: %s", pv.reply)
	}
	c.outbound(pv.send)
	c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": hrClient, "waitingForDebugger": false, "targetInfo": c.info()}))
	c.feed(hrReply(hrAttachID, "", map[string]any{"sessionId": hrClient}))
	if c.g.sessions[hrClient] == nil {
		t.Fatalf("client session not admitted: %v", c.client)
	}
	c.client = nil
	c.extras = 0
}

func (c *hrChrome) nt() *navTarget {
	return c.g.navTargets[hrTarget]
}

func hrLockedTab(t *testing.T) *hrChrome {
	t.Helper()
	c := hrNewChrome(t)
	c.protectedEntry()
	c.attach()
	c.requireLocked()
	return c
}

func hrCleanTab(t *testing.T) *hrChrome {
	t.Helper()
	c := hrNewChrome(t)
	c.attach()
	c.seedDocIdentity()
	nt := c.nt()
	if nt == nil || !nt.done || nt.failed || nt.historyLocked {
		t.Fatalf("clean tab not set up: %+v", nt)
	}
	return c
}

func (c *hrChrome) requireLocked() {
	c.t.Helper()
	nt := c.nt()
	if nt == nil || !nt.done || nt.failed || !nt.historyLocked || (nt.confirm != nil && (!nt.confirm.done || nt.confirm.failed)) || nt.errorURL != "" {
		c.t.Fatalf("history-locked ordinary tab not set up: %+v", nt)
	}
	if nt.loaderID == "" {
		nt.loaderID = c.loader
	}
	if nt.documentURL == "" {
		nt.documentURL = c.url
	}
	if s := c.g.sessions[hrClient]; s == nil || s.typ != c.typ || s.refused {
		c.t.Fatalf("client session not usable: %+v", s)
	}
}

func (c *hrChrome) seedDocIdentity() {
	if nt := c.nt(); nt != nil {
		if nt.loaderID == "" {
			nt.loaderID = c.loader
		}
		if nt.documentURL == "" {
			nt.documentURL = c.url
		}
	}
}

func (c *hrChrome) sendRaw(raw string) (string, pageVerdict) {
	cmd, id, ok := parseCommand([]byte(raw))
	v := c.g.fromCommand(cmd, id, ok)
	c.outbound(v.send)
	switch {
	case v.reply != nil:
		return "reply " + string(v.reply), v
	case v.forward != nil:
		return "forward " + string(v.forward), v
	case v.wait != nil:
		return "wait", v
	}
	return "other", v
}

func (c *hrChrome) clientRestore(url, origin, loader string) {
	c.url, c.origin, c.unreach, c.loader = url, origin, "", loader
	c.feed(hrEvent(hrClient, "Page.frameNavigated", map[string]any{"frame": c.frame(), "type": "BackForwardCacheRestore"}))
}

func (c *hrChrome) guardRestore(url string) {
	c.url, c.origin, c.unreach, c.loader = url, "https://seven.sliccy.ai", "", "L9"
	c.feed(hrEvent(hrNav, "Page.frameNavigated", map[string]any{"frame": c.frame(), "type": "BackForwardCacheRestore"}))
}

func (c *hrChrome) infoChanged(url, title string) {
	c.url, c.title = url, title
	c.feed(hrEvent("RELAY", "Target.targetInfoChanged", map[string]any{"targetInfo": c.info()}))
}

func hrOriginOf(raw string) string {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return "null"
	}
	host, _, _ := strings.Cut(rest, "/")
	return scheme + "://" + host
}

func (c *hrChrome) kidInfo(id, typ, url string) map[string]any {
	info := map[string]any{"targetId": id, "type": typ, "url": url, "title": "", "browserContextId": "CTX", "attached": true}
	if typ == "iframe" {
		info["parentFrameId"] = hrTarget
		info["parentId"] = hrTarget
	} else {
		info["parentId"] = hrTarget
		info["openerId"] = hrTarget
	}
	return info
}

func (c *hrChrome) kidAnnounced(id, typ, url string, clientPage bool) {
	info := c.kidInfo(id, typ, url)
	c.kids[id] = info
	if typ == "iframe" {
		p := map[string]any{"frameId": id, "parentFrameId": hrTarget}
		c.feed(hrEvent(hrNav, "Page.frameAttached", p))
		if clientPage {
			c.feed(hrEvent(hrClient, "Page.frameAttached", p))
		}
	}
	c.feed(hrEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": info}))
}

func (c *hrChrome) kidAttached(id string) {
	c.feed(hrEvent(hrClient, "Target.attachedToTarget", map[string]any{"sessionId": "S-" + id, "waitingForDebugger": false, "targetInfo": c.kids[id]}))
}

func (c *hrChrome) kidGone(id string) {
	c.feed(hrEvent(hrClient, "Target.detachedFromTarget", map[string]any{"sessionId": "S-" + id, "targetId": id}))
	c.feed(hrEvent("RELAY", "Target.targetDestroyed", map[string]any{"targetId": id}))
	delete(c.kids, id)
}

func (c *hrChrome) attachedForwarded(id string) bool {
	for _, m := range c.client {
		if strings.Contains(m, `"method":"Target.attachedToTarget"`) && strings.Contains(m, `"sessionId":"S-`+id+`"`) {
			return true
		}
	}
	return false
}

func hrLogHas(logs []string, parts ...string) bool {
	for _, line := range logs {
		ok := true
		for _, p := range parts {
			if !strings.Contains(line, p) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func (c *hrChrome) kidUsable(id string) string {
	c.next++
	raw := fmt.Sprintf(`{"id":%d,"method":"Runtime.evaluate","params":{"expression":"1"},"sessionId":"S-%s"}`, c.next, id)
	out, _ := c.sendRaw(raw)
	if strings.HasPrefix(out, "forward") {
		return "forwarded"
	}
	var r struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(strings.TrimPrefix(out, "reply ")), &r)
	return "answer: " + r.Error.Message
}

func (c *hrChrome) kidState(id string) string {
	if c.g.hidden["S-"+id] {
		return "hidden"
	}
	if c.g.held["S-"+id] != nil {
		return "held"
	}
	if c.g.sessions["S-"+id] == nil {
		return "none"
	}
	ev := "/ev-dropped"
	c.client = nil
	c.feed(hrEvent("S-"+id, "Runtime.consoleAPICalled", map[string]any{"type": "log", "args": []any{}, "executionContextId": 1, "timestamp": 1}))
	if len(c.client) > 0 {
		ev = "/ev-delivered"
	}
	switch got := c.kidUsable(id); got {
	case "forwarded":
		return "fwd" + ev
	case "answer: slicc: refused: " + hrTextTarget:
		return "refused" + ev
	case "answer: " + missingSessionMessage:
		return "missing" + ev
	default:
		return got
	}
}

func (c *hrChrome) detachSent(id string) bool {
	for _, o := range c.guardOut {
		_, method, _, params := hrOutbound([]byte(o))
		if method == "Target.detachFromTarget" && params["sessionId"] == "S-"+id {
			return true
		}
	}
	return false
}

func hrRestoreSteps(kid, url string) map[string]func(c *hrChrome) {
	return map[string]func(c *hrChrome){
		"navCommit":    func(c *hrChrome) { c.guardRestore(hrSeven) },
		"clientCommit": func(c *hrChrome) { c.clientRestore(hrSeven, "https://seven.sliccy.ai", "L9") },
		"info":         func(c *hrChrome) { c.infoChanged(hrSeven, "seven") },
		"confirm":      func(c *hrChrome) { c.answerHeld(false) },
		"newAttach": func(c *hrChrome) {
			typ := "iframe"
			switch {
			case strings.HasPrefix(kid, "NEW-SW"):
				typ = "shared_worker"
			case strings.HasPrefix(kid, "NEW-SVC"):
				typ = "service_worker"
			case strings.HasPrefix(kid, "NEW-WV"):
				typ = "webview"
			case strings.HasPrefix(kid, "NEW-OT"):
				typ = "other"
			case strings.HasPrefix(kid, "NEW-W"):
				typ = "worker"
			}
			c.kidAnnounced(kid, typ, url, true)
			c.kidAttached(kid)
		},
		"oldGone": func(c *hrChrome) { c.kidGone("OLD") },
	}
}

func hrRestoreTrace(t *testing.T, blank bool, kid, kidURL string, order []string) string {
	c := hrNewChrome(t)
	if blank {
		c.url, c.origin = "about:blank", "null"
	}
	c.protectedEntry()
	c.attach()
	c.requireLocked()
	c.kidAnnounced("OLD", "iframe", "https://other.test/frame", true)
	c.client = nil
	c.kidAttached("OLD")
	c.answerHeld(false)
	if !c.attachedForwarded("OLD") {
		t.Fatalf("the ordinary child was not delivered: %q logs=%q", c.client, c.logs)
	}
	c.hold = true
	steps := hrRestoreSteps(kid, kidURL)
	var trace []string
	c.guardOut = nil
	for _, st := range order {
		c.client = nil
		steps[st](c)
		line := st + ":"
		if st == "newAttach" {
			line += fmt.Sprintf(" delivered=%t", c.attachedForwarded(kid))
		}
		if st == "oldGone" {
			line += fmt.Sprintf(" client=%d", len(c.client))
		}
		line += " OLD=" + c.kidState("OLD")
		if c.kids[kid] != nil || c.g.held["S-"+kid] != nil || c.g.hidden["S-"+kid] || c.g.sessions["S-"+kid] != nil {
			line += " NEW=" + c.kidState(kid)
		}
		trace = append(trace, line)
	}
	trace = append(trace, fmt.Sprintf("detach OLD=%t NEW=%t", c.detachSent("OLD"), c.detachSent(kid)))
	return strings.Join(trace, " | ")
}

type hrRaceRow struct {
	name  string
	blank bool
	kid   string
	url   string
	order []string
	want  string
}

var hrRaceRows = []hrRaceRow{
	{"ordinary/iframe/attach-first", false, "NEW", "https://third.test/embed", []string{"newAttach", "confirm", "navCommit", "clientCommit", "info", "oldGone"}, "newAttach: delivered=false OLD=refused/ev-dropped NEW=held | confirm: OLD=fwd/ev-delivered NEW=fwd/ev-delivered | navCommit: OLD=refused/ev-dropped NEW=refused/ev-dropped | clientCommit: OLD=refused/ev-dropped NEW=refused/ev-dropped | info: OLD=refused/ev-dropped NEW=refused/ev-dropped | oldGone: client=0 OLD=refused/ev-dropped NEW=refused/ev-dropped | detach OLD=true NEW=true"},
	{"ordinary/iframe/guard-commit-first", false, "NEW", "https://third.test/embed", []string{"navCommit", "newAttach", "confirm", "clientCommit", "info", "oldGone"}, "navCommit: OLD=refused/ev-dropped | newAttach: delivered=false OLD=refused/ev-dropped NEW=hidden | confirm: OLD=refused/ev-dropped NEW=hidden | clientCommit: OLD=refused/ev-dropped NEW=hidden | info: OLD=refused/ev-dropped NEW=hidden | oldGone: client=0 OLD=refused/ev-dropped NEW=hidden | detach OLD=true NEW=true"},
	{"ordinary/iframe/guard-commit-first-no-client-commit", false, "NEW", "https://third.test/embed", []string{"navCommit", "newAttach", "info", "confirm", "oldGone"}, "navCommit: OLD=refused/ev-dropped | newAttach: delivered=false OLD=refused/ev-dropped NEW=hidden | info: OLD=refused/ev-dropped NEW=hidden | confirm: OLD=refused/ev-dropped NEW=hidden | oldGone: client=0 OLD=refused/ev-dropped NEW=hidden | detach OLD=true NEW=true"},
	{"ordinary/iframe/info-first", false, "NEW", "https://third.test/embed", []string{"info", "newAttach", "navCommit", "clientCommit", "confirm", "oldGone"}, "info: OLD=refused/ev-dropped | newAttach: delivered=false OLD=refused/ev-dropped NEW=held | navCommit: OLD=refused/ev-dropped NEW=hidden | clientCommit: OLD=refused/ev-dropped NEW=hidden | confirm: OLD=refused/ev-dropped NEW=hidden | oldGone: client=0 OLD=refused/ev-dropped NEW=hidden | detach OLD=true NEW=true"},
	{"ordinary/iframe/attach-after-protect", false, "NEW", "https://third.test/embed", []string{"navCommit", "clientCommit", "info", "confirm", "newAttach", "oldGone"}, "navCommit: OLD=refused/ev-dropped | clientCommit: OLD=refused/ev-dropped | info: OLD=refused/ev-dropped | confirm: OLD=refused/ev-dropped | newAttach: delivered=false OLD=refused/ev-dropped NEW=hidden | oldGone: client=0 OLD=refused/ev-dropped NEW=hidden | detach OLD=true NEW=true"},
	{"ordinary/vault-iframe/attach-first", false, "NEW", "https://vault.sliccy.ai/frame", []string{"newAttach", "confirm", "navCommit", "oldGone"}, "newAttach: delivered=false OLD=fwd/ev-delivered NEW=hidden | confirm: OLD=fwd/ev-delivered NEW=hidden | navCommit: OLD=refused/ev-dropped NEW=hidden | oldGone: client=0 OLD=refused/ev-dropped NEW=hidden | detach OLD=true NEW=true"},
	{"ordinary/same-origin-child/attach-first", false, "NEW", "https://seven.sliccy.ai/embed", []string{"newAttach", "confirm", "navCommit", "oldGone"}, "newAttach: delivered=false OLD=fwd/ev-delivered NEW=hidden | confirm: OLD=fwd/ev-delivered NEW=hidden | navCommit: OLD=refused/ev-dropped NEW=hidden | oldGone: client=0 OLD=refused/ev-dropped NEW=hidden | detach OLD=true NEW=true"},
	{"ordinary/blob-worker/attach-first", false, "NEW-W", "blob:https://seven.sliccy.ai/0b1c", []string{"newAttach", "confirm", "navCommit", "oldGone"}, "newAttach: delivered=false OLD=fwd/ev-delivered NEW=hidden | confirm: OLD=fwd/ev-delivered NEW=hidden | navCommit: OLD=refused/ev-dropped NEW=hidden | oldGone: client=0 OLD=refused/ev-dropped NEW=hidden | detach OLD=true NEW=true"},
	{"ordinary/seven-worker/attach-first", false, "NEW-W", "https://seven.sliccy.ai/worker.js", []string{"newAttach", "confirm", "navCommit", "oldGone"}, "newAttach: delivered=false OLD=fwd/ev-delivered NEW=hidden | confirm: OLD=fwd/ev-delivered NEW=hidden | navCommit: OLD=refused/ev-dropped NEW=hidden | oldGone: client=0 OLD=refused/ev-dropped NEW=hidden | detach OLD=true NEW=true"},
	{"ordinary/data-worker/attach-first", false, "NEW-W", "data:text/javascript,1", []string{"newAttach", "confirm", "navCommit", "oldGone"}, "newAttach: delivered=false OLD=fwd/ev-delivered NEW=hidden | confirm: OLD=fwd/ev-delivered NEW=hidden | navCommit: OLD=refused/ev-dropped NEW=hidden | oldGone: client=0 OLD=refused/ev-dropped NEW=hidden | detach OLD=true NEW=true"},
	{"ordinary/third-worker/attach-first", false, "NEW-W", "https://third.test/w.js", []string{"newAttach", "confirm", "navCommit", "oldGone"}, "newAttach: delivered=false OLD=refused/ev-dropped NEW=held | confirm: OLD=fwd/ev-delivered NEW=fwd/ev-delivered | navCommit: OLD=refused/ev-dropped NEW=refused/ev-dropped | oldGone: client=0 OLD=refused/ev-dropped NEW=refused/ev-dropped | detach OLD=true NEW=true"},
	{"ordinary/shared-worker/attach-first", false, "NEW-SW", "https://third.test/shared.js", []string{"newAttach", "confirm", "navCommit", "oldGone"}, "newAttach: delivered=false OLD=refused/ev-dropped NEW=held | confirm: OLD=fwd/ev-delivered NEW=fwd/ev-delivered | navCommit: OLD=refused/ev-dropped NEW=refused/ev-dropped | oldGone: client=0 OLD=refused/ev-dropped NEW=refused/ev-dropped | detach OLD=true NEW=true"},
	{"ordinary/service-worker/attach-first", false, "NEW-SVC", "https://third.test/sw.js", []string{"newAttach", "confirm", "navCommit", "oldGone"}, "newAttach: delivered=false OLD=refused/ev-dropped NEW=held | confirm: OLD=fwd/ev-delivered NEW=fwd/ev-delivered | navCommit: OLD=refused/ev-dropped NEW=refused/ev-dropped | oldGone: client=0 OLD=refused/ev-dropped NEW=refused/ev-dropped | detach OLD=true NEW=true"},
	{"ordinary/webview/attach-first", false, "NEW-WV", "https://third.test/view", []string{"newAttach", "confirm", "navCommit", "oldGone"}, "newAttach: delivered=false OLD=refused/ev-dropped NEW=held | confirm: OLD=fwd/ev-delivered NEW=answer: slicc: refused: the target's history may restore a protected document | navCommit: OLD=refused/ev-dropped NEW=refused/ev-dropped | oldGone: client=0 OLD=refused/ev-dropped NEW=refused/ev-dropped | detach OLD=true NEW=true"},
	{"ordinary/other/attach-first", false, "NEW-OT", "https://third.test/other", []string{"newAttach", "confirm", "navCommit", "oldGone"}, "newAttach: delivered=false OLD=refused/ev-dropped NEW=held | confirm: OLD=fwd/ev-delivered NEW=fwd/ev-delivered | navCommit: OLD=refused/ev-dropped NEW=refused/ev-dropped | oldGone: client=0 OLD=refused/ev-dropped NEW=refused/ev-dropped | detach OLD=true NEW=true"},
	{"blank/iframe/attach-first", true, "NEW", "https://third.test/embed", []string{"newAttach", "confirm", "navCommit", "oldGone"}, "newAttach: delivered=false OLD=refused/ev-dropped NEW=held | confirm: OLD=fwd/ev-delivered NEW=fwd/ev-delivered | navCommit: OLD=refused/ev-dropped NEW=refused/ev-dropped | oldGone: client=0 OLD=refused/ev-dropped NEW=refused/ev-dropped | detach OLD=true NEW=true"},
	{"blank/iframe/guard-commit-first", true, "NEW", "https://third.test/embed", []string{"navCommit", "newAttach", "confirm", "oldGone"}, "navCommit: OLD=refused/ev-dropped | newAttach: delivered=false OLD=refused/ev-dropped NEW=hidden | confirm: OLD=refused/ev-dropped NEW=hidden | oldGone: client=0 OLD=refused/ev-dropped NEW=hidden | detach OLD=true NEW=true"},
}

func TestHistoryRestoreChildren(t *testing.T) {
	for _, row := range hrRaceRows {
		t.Run(row.name, func(t *testing.T) {
			got := hrRestoreTrace(t, row.blank, row.kid, row.url, row.order)
			if got != row.want {
				t.Errorf("restore trace\n got: %s\nwant: %s", got, row.want)
			}
		})
	}
}

func TestHistoryRestoreExplicitAttach(t *testing.T) {
	for _, row := range []struct {
		name   string
		commit bool
		want   string
	}{
		{"before-restore-events", false, "reply=held state=held"},
		{"after-guard-commit", true, "cmd=refused state=none"},
	} {
		t.Run(row.name, func(t *testing.T) {
			c := hrLockedTab(t)
			c.hold = true
			if row.commit {
				c.guardRestore(hrSeven)
			}
			c.kidAnnounced("NEW", "iframe", "https://third.test/embed", true)
			pv := c.g.fromCommand(&command{id: 900, method: "Target.attachToTarget", params: map[string]any{"targetId": "NEW", "flatten": true}}, 900, true)
			c.outbound(pv.send)
			got := "cmd=refused"
			if pv.reply != nil {
				got = "cmd=refused"
			} else if pv.forward != nil {
				got = "cmd=forward"
				c.client = nil
				c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-NEW", "waitingForDebugger": false, "targetInfo": c.kids["NEW"]}))
				c.feed(hrReply(900, "", map[string]any{"sessionId": "S-NEW"}))
				switch {
				case c.attachedForwarded("NEW"):
					got = "reply=forward"
				case c.g.held["S-NEW"] != nil:
					got = "reply=held"
				case len(c.client) > 0:
					got = "reply=other"
				default:
					got = "reply=held"
				}
			}
			got += " state=" + c.kidState("NEW")
			if got != row.want {
				t.Fatalf("got %s want %s; client=%q logs=%q held=%v", got, row.want, c.client, c.logs, c.g.held["S-NEW"])
			}
		})
	}
}

func TestHistoryRestoreOrdinaryTwin(t *testing.T) {
	c := hrCleanTab(t)
	c.kidAnnounced("K", "iframe", "https://third.test/embed", true)
	c.client = nil
	c.logs = nil
	c.extras = 0
	raw := string(hrEvent(hrClient, "Target.attachedToTarget", map[string]any{"sessionId": "S-K", "waitingForDebugger": false, "targetInfo": c.kids["K"]}))
	c.feed([]byte(raw))
	for _, line := range c.logs {
		if strings.Contains(line, "holding a child of a history-locked page") {
			t.Fatalf("ordinary twin held a child: %q", c.logs)
		}
	}
	if len(c.client) != 1 || c.client[0] != raw {
		t.Fatalf("ordinary twin not byte-identical: %q want %s", c.client, raw)
	}
	if c.kidState("K") != "fwd/ev-delivered" {
		t.Fatalf("ordinary twin state: %s", c.kidState("K"))
	}
}

func TestHistoryRestoreLockedNeverRestores(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.kidAnnounced("K", "iframe", "https://third.test/embed", true)
	c.client = nil
	c.extras = 0
	raw := string(hrEvent(hrClient, "Target.attachedToTarget", map[string]any{"sessionId": "S-K", "waitingForDebugger": false, "targetInfo": c.kids["K"]}))
	c.feed([]byte(raw))
	if c.attachedForwarded("K") {
		t.Fatalf("delivered before confirm: %q", c.client)
	}
	if c.kidState("K") != "held" {
		t.Fatalf("want held, got %s", c.kidState("K"))
	}
	c.client = nil
	c.answerHeld(false)
	if !c.attachedForwarded("K") {
		t.Fatalf("not delivered after confirm: %q logs=%q", c.client, c.logs)
	}
	delivered := ""
	for _, m := range c.client {
		if strings.Contains(m, `"method":"Target.attachedToTarget"`) {
			delivered = m
			break
		}
	}
	if !strings.Contains(delivered, `"sessionId":"S-K"`) || !strings.Contains(delivered, `"url":"https://third.test/embed"`) {
		t.Fatalf("delivered bytes missing fields: %s", delivered)
	}
	if c.extras < 1 {
		t.Fatalf("expected extra guard confirm messages, got %d", c.extras)
	}
}

func TestHistoryRestoreIdentityChangeHides(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.kidAnnounced("K", "iframe", "https://third.test/embed", true)
	c.client = nil
	c.kidAttached("K")
	if c.kidState("K") != "held" {
		t.Fatalf("want held: %s", c.kidState("K"))
	}
	h := c.g.held["S-K"]
	if h == nil {
		t.Fatal("not held")
	}
	h.historySnap = docIdentity{frameID: hrTarget, loaderID: "L-read", url: hrOrdinary}
	if nt := c.nt(); nt != nil {
		nt.loaderID = "L-after-read"
		nt.documentURL = "https://ordinary.test/other"
	}
	if t := c.g.targets[hrTarget]; t != nil {
		t.url = "https://ordinary.test/other"
	}
	part := c.g.deliverHistoryHoldLocked(h, true, h.historyConfirmMinSeq)
	c.take(part)
	if c.attachedForwarded("K") {
		t.Fatalf("delivered after identity change after read: %q", c.client)
	}
	if c.kidState("K") != "hidden" {
		t.Fatalf("want hidden after identity change after read: %s", c.kidState("K"))
	}
}

func TestHistoryRestoreDeadlineHides(t *testing.T) {
	t.Setenv(HoldTimeoutEnv, "50ms")
	c := hrLockedTab(t)
	c.hold = true
	c.kidAnnounced("K", "iframe", "https://third.test/embed", true)
	c.client = nil
	c.kidAttached("K")
	if c.g.held["S-K"] == nil {
		t.Fatal("not held")
	}
	time.Sleep(80 * time.Millisecond)
	page, send := c.g.expireHold("S-K")
	c.take(browserVerdict{page: page, send: send})
	if c.attachedForwarded("K") {
		t.Fatalf("deadline forwarded: %q", c.client)
	}
	if !c.g.hidden["S-K"] && c.kidState("K") != "hidden" && c.kidState("K") != "none" {
		t.Fatalf("deadline did not hide: state=%s hidden=%v", c.kidState("K"), c.g.hidden["S-K"])
	}
}

func TestHistoryRestoreExactlyOnce(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.kidAnnounced("K", "iframe", "https://third.test/embed", true)
	c.client = nil
	c.kidAttached("K")
	c.kidAttached("K")
	c.answerHeld(false)
	n := 0
	for _, m := range c.client {
		if strings.Contains(m, `"method":"Target.attachedToTarget"`) && strings.Contains(m, `"sessionId":"S-K"`) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("delivered %d times, want 1; %q", n, c.client)
	}
}

func TestHistoryRestoreExplicitExactlyOnce(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = false
	c.kidAnnounced("K", "iframe", "https://third.test/embed", true)
	pv := c.g.fromCommand(&command{id: 901, method: "Target.attachToTarget", params: map[string]any{"targetId": "K", "flatten": true}}, 901, true)
	c.outbound(pv.send)
	c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-K", "waitingForDebugger": false, "targetInfo": c.kids["K"]}))
	c.client = nil
	c.feed(hrReply(901, "", map[string]any{"sessionId": "S-K"}))
	c.feed(hrReply(901, "", map[string]any{"sessionId": "S-K"}))
	c.answerHeld(false)
	replies := 0
	for _, m := range c.client {
		if strings.Contains(m, `"id":901`) {
			replies++
		}
	}
	if replies != 1 {
		t.Fatalf("explicit attach answered %d times: %q", replies, c.client)
	}
}

func TestHistoryRestoreChildDetachesWhileHeld(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.kidAnnounced("K", "iframe", "https://third.test/embed", true)
	c.kidAttached("K")
	c.feed(hrEvent(hrClient, "Target.detachedFromTarget", map[string]any{"sessionId": "S-K", "targetId": "K"}))
	c.client = nil
	c.answerHeld(false)
	if c.attachedForwarded("K") {
		t.Fatalf("delivered after detach-while-held: %q", c.client)
	}
}

func TestHistoryRestoreEmptySnapHides(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	if nt := c.nt(); nt != nil {
		nt.loaderID = ""
	}
	c.kidAnnounced("K", "iframe", "https://third.test/embed", true)
	c.client = nil
	c.kidAttached("K")
	if h := c.g.held["S-K"]; h == nil || h.historySnap.loaderID != "" {
		t.Fatalf("want empty loader attach snap: %+v", h)
	}
	c.loader = "L-filled"
	c.answerHeld(false)
	if !c.attachedForwarded("K") {
		t.Fatalf("empty attach snap not delivered after confirm read: %q logs=%q state=%s", c.client, c.logs, c.kidState("K"))
	}
}

func TestHistoryRestoreEmptyAttachSnapExplicitDelivers(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	if nt := c.nt(); nt != nil {
		nt.loaderID = ""
	}
	c.kidAnnounced("NEW", "iframe", "https://third.test/embed", true)
	pv := c.g.fromCommand(&command{id: 910, method: "Target.attachToTarget", params: map[string]any{"targetId": "NEW", "flatten": true}}, 910, true)
	c.outbound(pv.send)
	c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-NEW", "waitingForDebugger": false, "targetInfo": c.kids["NEW"]}))
	c.client = nil
	c.feed(hrReply(910, "", map[string]any{"sessionId": "S-NEW"}))
	if c.g.held["S-NEW"] == nil {
		t.Fatal("explicit attach not held")
	}
	c.loader = "L-confirm"
	c.answerHeld(false)
	answered := false
	for _, m := range c.client {
		if strings.Contains(m, `"id":910`) && strings.Contains(m, `"sessionId":"S-NEW"`) {
			answered = true
		}
	}
	if !answered {
		t.Fatalf("explicit reply not answered after confirm: %q logs=%q", c.client, c.logs)
	}
}

func TestHistoryRestoreReadEmptyIdentityHides(t *testing.T) {
	for _, row := range []struct {
		name string
		snap docIdentity
	}{
		{"empty-loader", docIdentity{frameID: hrTarget, loaderID: "", url: hrOrdinary}},
		{"empty-frame", docIdentity{frameID: "", loaderID: "L-read", url: hrOrdinary}},
		{"empty-url", docIdentity{frameID: hrTarget, loaderID: "L-read", url: ""}},
	} {
		t.Run(row.name, func(t *testing.T) {
			c := hrLockedTab(t)
			c.hold = true
			c.kidAnnounced("K", "iframe", "https://third.test/embed", true)
			c.kidAttached("K")
			h := c.g.held["S-K"]
			if h == nil {
				t.Fatal("not held")
			}
			h.historySnap = row.snap
			part := c.g.deliverHistoryHoldLocked(h, true, h.historyConfirmMinSeq)
			c.take(part)
			if c.attachedForwarded("K") {
				t.Fatalf("delivered with %s read identity: %q", row.name, c.client)
			}
			if c.kidState("K") != "hidden" {
				t.Fatalf("want hidden for %s: %s", row.name, c.kidState("K"))
			}
		})
	}
}

func TestHistoryRestoreConfirmProtectedParentHides(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.kidAnnounced("K", "iframe", "https://third.test/embed", true)
	c.kidAttached("K")
	c.url, c.origin, c.loader = hrSeven, "https://seven.sliccy.ai", "L-prot"
	c.answerHeld(false)
	if c.attachedForwarded("K") {
		t.Fatalf("delivered under protected confirm: %q", c.client)
	}
	if c.kidState("K") != "hidden" && c.kidState("K") != "refused/ev-dropped" && c.kidState("K") != "none" {
		t.Fatalf("want hidden/refused under protected confirm: %s logs=%q", c.kidState("K"), c.logs)
	}
}

func TestHistoryRestoreConfirmWrongFrameHides(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.kidAnnounced("K", "iframe", "https://third.test/embed", true)
	c.kidAttached("K")
	if len(c.held) == 0 {
		t.Fatal("no pending confirm")
	}
	raw := c.held[0]
	c.held = c.held[1:]
	id, _, session, _ := hrOutbound(raw)
	c.feed(hrReply(id, session, map[string]any{"frameTree": map[string]any{"frame": map[string]any{
		"id": "OTHER-FRAME", "loaderId": "L-other", "url": hrOrdinary, "securityOrigin": "https://ordinary.test", "mimeType": "text/html",
	}}}))
	if c.attachedForwarded("K") {
		t.Fatalf("delivered after wrong-frame confirm: %q", c.client)
	}
	if c.kidState("K") != "hidden" && c.kidState("K") != "held" && c.kidState("K") != "none" {
		t.Fatalf("want hide/held after wrong-frame confirm: %s", c.kidState("K"))
	}
	hidden := false
	for _, line := range c.logs {
		if strings.Contains(line, "history hold hidden") {
			hidden = true
		}
	}
	if !hidden && c.kidState("K") != "hidden" {
		t.Fatalf("wrong-frame confirm did not hide: state=%s logs=%q", c.kidState("K"), c.logs)
	}
}

func TestHistoryRestoreFirstProtectedWithChildren(t *testing.T) {
	c := hrCleanTab(t)
	c.kidAnnounced("OLD", "iframe", "https://other.test/frame", true)
	c.client = nil
	c.kidAttached("OLD")
	if !c.attachedForwarded("OLD") {
		t.Fatalf("ordinary child not delivered: %q", c.client)
	}
	c.hold = true
	c.guardOut = nil
	c.client = nil
	c.guardRestore(hrSeven)
	if c.kidState("OLD") != "refused/ev-dropped" && c.kidState("OLD") != "hidden" {
		t.Fatalf("first protected commit left OLD usable: %s logs=%q", c.kidState("OLD"), c.logs)
	}
	if !c.detachSent("OLD") && !c.g.hidden["S-OLD"] && !c.g.refused["S-OLD"] {
		t.Fatalf("first protected commit did not detach OLD; detach=%v refused=%v", c.detachSent("OLD"), c.g.refused["S-OLD"])
	}
}

func (c *hrChrome) plantPage(id, nav string, locked, done bool) {
	c.t.Helper()
	info := map[string]any{"targetId": id, "type": "page", "url": hrOrdinary, "title": id, "browserContextId": "CTX", "attached": false}
	c.feed(hrEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": info}))
	if old := c.g.navTargets[id]; old != nil {
		if old.session != "" {
			delete(c.g.navSessions, old.session)
		}
		delete(c.g.navTargets, id)
	}
	nt := &navTarget{target: id, ready: make(chan struct{}), start: time.Now(), session: nav, loaderID: "L-" + id, documentURL: hrOrdinary, historyLocked: locked}
	if done {
		nt.done = true
		close(nt.ready)
	}
	c.g.navTargets[id] = nt
	if nav != "" {
		c.g.navSessions[nav] = nt
	}
	c.planted[id] = nav
	if tg := c.g.targets[id]; tg != nil {
		tg.url = hrOrdinary
	}
}

func (c *hrChrome) orphanAnnounce(id, typ, url string) map[string]any {
	info := map[string]any{"targetId": id, "type": typ, "url": url, "title": "", "browserContextId": "OTHER", "attached": true}
	c.kids[id] = info
	c.feed(hrEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": info}))
	return info
}

func (c *hrChrome) orphanAttach(id string) {
	c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-" + id, "waitingForDebugger": false, "targetInfo": c.kids[id]}))
}

func (c *hrChrome) orphanResolveOpener(id, owner string) {
	info := c.kids[id]
	info["openerId"] = owner
	c.feed(hrEvent("RELAY", "Target.targetInfoChanged", map[string]any{"targetInfo": info}))
}

func hrUnresolvedTwoPages(t *testing.T, orderBA bool, aLocked, bLocked, aDone, bDone bool) *hrChrome {
	t.Helper()
	c := hrNewChrome(t)
	type spec struct {
		id, nav      string
		locked, done bool
	}
	pages := []spec{
		{"PAGE-A", "NAV-A", aLocked, aDone},
		{"PAGE-B", "NAV-B", bLocked, bDone},
	}
	if orderBA {
		pages[0], pages[1] = pages[1], pages[0]
	}
	for _, p := range pages {
		c.plantPage(p.id, p.nav, p.locked, p.done)
	}
	return c
}

func hrUnresolvedOutcome(t *testing.T, orderBA bool, resolve func(c *hrChrome), after func(c *hrChrome) string) string {
	t.Helper()
	c := hrUnresolvedTwoPages(t, orderBA, true, true, true, true)
	c.hold = true
	c.orphanAnnounce("ORPHAN", "webview", "https://third.test/view")
	c.client = nil
	c.orphanAttach("ORPHAN")
	h := c.g.held["S-ORPHAN"]
	if h == nil || !h.historyUnresolved || h.historyParent != "" {
		t.Fatalf("want child-owned unresolved hold (no pinned parent): %+v", h)
	}
	if c.attachedForwarded("ORPHAN") {
		t.Fatalf("forwarded before owner resolve: %q", c.client)
	}
	resolve(c)
	return after(c)
}

func TestHistoryRestoreUnresolvedOwnerLockedStream(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.orphanAnnounce("ORPHAN", "webview", "https://third.test/view")
	c.client = nil
	c.orphanAttach("ORPHAN")
	if c.attachedForwarded("ORPHAN") {
		t.Fatalf("unresolved owner forwarded on locked stream: %q", c.client)
	}
	h := c.g.held["S-ORPHAN"]
	if h == nil || !h.historyUnresolved || h.historyParent != "" {
		t.Fatalf("want unresolved history hold without pinned parent: %+v", h)
	}
	page, send := c.g.expireHold("S-ORPHAN")
	c.take(browserVerdict{page: page, send: send})
	if c.attachedForwarded("ORPHAN") {
		t.Fatalf("unresolved hold forwarded at deadline: %q", c.client)
	}
}

func TestHistoryRestoreUnresolvedOwnerCleanStream(t *testing.T) {
	c := hrCleanTab(t)
	info := c.orphanAnnounce("ORPHAN", "webview", "https://third.test/view")
	c.client = nil
	c.logs = nil
	raw := string(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-ORPHAN", "waitingForDebugger": false, "targetInfo": info}))
	c.feed([]byte(raw))
	for _, line := range c.logs {
		if strings.Contains(line, "holding unresolved-owner") || strings.Contains(line, "holding a child of a history-locked page") {
			t.Fatalf("clean stream held unresolved child: %q", c.logs)
		}
	}
	if len(c.client) != 1 || c.client[0] != raw {
		t.Fatalf("clean stream unresolved owner not byte-identical: %q want %s", c.client, raw)
	}
}

func hrUndecidedOnlyStream(t *testing.T) *hrChrome {
	t.Helper()
	c := hrNewChrome(t)
	c.attach()
	c.seedDocIdentity()
	c.plantPage("PAGE-U", "NAV-U", false, false)
	nt := c.g.navTargets["PAGE-U"]
	if nt == nil || nt.done || nt.historyLocked {
		t.Fatalf("undecided ordinary page not set up: %+v", nt)
	}
	if c.streamConcern() {
		t.Fatalf("undecided-only stream must not be a history concern")
	}
	return c
}

func (c *hrChrome) streamConcern() bool {
	c.g.mu.Lock()
	defer c.g.mu.Unlock()
	return c.g.streamHasHistoryConcernLocked()
}

func hrAssertByteIdenticalOrphanAttach(t *testing.T, c *hrChrome, id, typ, url string) {
	t.Helper()
	info := c.orphanAnnounce(id, typ, url)
	c.client = nil
	c.logs = nil
	raw := string(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-" + id, "waitingForDebugger": false, "targetInfo": info}))
	c.feed([]byte(raw))
	for _, line := range c.logs {
		if strings.Contains(line, "holding unresolved-owner") || strings.Contains(line, "holding a child of a history-locked page") {
			t.Fatalf("ordinary undecided stream held child: %q", c.logs)
		}
	}
	if len(c.client) != 1 || c.client[0] != raw {
		t.Fatalf("ordinary undecided stream not byte-identical: %q want %s", c.client, raw)
	}
}

func TestHistoryRestoreUndecidedOnlyStreamByteIdentical(t *testing.T) {
	c := hrUndecidedOnlyStream(t)
	hrAssertByteIdenticalOrphanAttach(t, c, "ORPHAN", "webview", "https://third.test/view")
}

func TestHistoryRestoreUndecidedOnlyInheritedIframeNoHistoryHold(t *testing.T) {
	c := hrUndecidedOnlyStream(t)
	c.orphanAnnounce("CHILD", "iframe", "https://third.test/embed")
	c.client = nil
	c.logs = nil
	c.orphanAttach("CHILD")
	for _, line := range c.logs {
		if strings.Contains(line, "holding unresolved-owner") {
			t.Fatalf("undecided-only stream must not history-hold inherited iframe: %q", c.logs)
		}
	}
}

func TestHistoryRestoreUndecidedOnlyPlainWorkerByteIdentical(t *testing.T) {
	c := hrUndecidedOnlyStream(t)
	hrAssertByteIdenticalOrphanAttach(t, c, "W-plain", "worker", "https://ordinary.test/w.js")
}

func TestHistoryRestoreUndecidedOnlyLatePlainWorkerByteIdentical(t *testing.T) {
	c := hrUndecidedOnlyStream(t)
	hrAssertByteIdenticalOrphanAttach(t, c, "W-plain-2", "worker", "https://ordinary.test/w2.js")
}

func TestHistoryRestoreUndecidedOnlyUnresolvedWorkerTimeoutPath(t *testing.T) {
	c := hrUndecidedOnlyStream(t)
	info := c.orphanAnnounce("W-plain", "worker", "")
	c.client = nil
	c.logs = nil
	c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-W-plain", "waitingForDebugger": true, "targetInfo": info}))
	for _, line := range c.logs {
		if strings.Contains(line, "holding unresolved-owner") || strings.Contains(line, "history hold") {
			t.Fatalf("undecided-only stream must not history-hold empty-URL worker: %q", c.logs)
		}
	}
	foundWorkerHold := false
	for _, line := range c.logs {
		if strings.Contains(line, "holding an auto-attached worker until its URL or creator frame is known") {
			foundWorkerHold = true
		}
	}
	if !foundWorkerHold && c.g.held["S-W-plain"] == nil {
		t.Fatalf("expected ordinary worker URL-hold path, not history: logs=%q held=%v", c.logs, c.g.held["S-W-plain"])
	}
}

func TestHistoryRestoreOwnOwnerUndecidedNoLockedStreamNoHistoryHold(t *testing.T) {
	c := hrUndecidedOnlyStream(t)
	info := c.kidInfo("T2", "iframe", "https://ordinary.test/fixture")
	info["parentFrameId"] = "PAGE-U"
	info["parentId"] = "PAGE-U"
	c.kids["T2"] = info
	c.feed(hrEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": info}))
	c.client = nil
	c.logs = nil
	c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "CHILD", "waitingForDebugger": false, "targetInfo": info}))
	for _, line := range c.logs {
		if strings.Contains(line, "holding unresolved-owner") {
			t.Fatalf("own-owner undecided without locked stream must not history-hold: %q", c.logs)
		}
	}
}

func TestHistoryRestoreOwnOwnerUndecidedWithLockedStreamHolds(t *testing.T) {
	c := hrUnresolvedTwoPages(t, false, true, false, true, false)
	c.hold = true
	info := c.kidInfo("W", "worker", "https://ordinary.test/w.js")
	info["openerId"] = "PAGE-B"
	info["parentId"] = "PAGE-B"
	c.kids["W"] = info
	c.feed(hrEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": info}))
	c.client = nil
	c.orphanAttach("W")
	h := c.g.held["S-W"]
	if h == nil || h.historyParent != "PAGE-B" {
		t.Fatalf("own undecided owner on locked stream must hold: %+v", h)
	}
	if c.attachedForwarded("W") {
		t.Fatalf("forwarded before owner decides: %q", c.client)
	}
}

func TestHistoryRestoreOwnOwnerUndecidedThenHistoryLockedWindow(t *testing.T) {
	c := hrNewChrome(t)
	c.attach()
	c.seedDocIdentity()
	nt := c.nt()
	if nt == nil {
		t.Fatal("missing HS-PAGE navTarget")
	}
	nt.done = false
	nt.historyLocked = false
	nt.confirm = nil
	if c.streamConcern() {
		t.Fatalf("undecided ordinary owner must not be stream concern yet")
	}
	info := map[string]any{"targetId": "T2", "type": "iframe", "url": "https://ordinary.test/fixture", "title": "", "browserContextId": "CTX", "attached": true, "parentFrameId": hrTarget, "parentId": hrTarget}
	c.kids["T2"] = info
	c.feed(hrEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": info}))
	c.client = nil
	c.logs = nil
	c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "CHILD", "waitingForDebugger": false, "targetInfo": info}))
	historyHeld := false
	for _, line := range c.logs {
		if strings.Contains(line, "holding unresolved-owner") {
			historyHeld = true
		}
	}
	fwdBefore := false
	for _, m := range c.client {
		if strings.Contains(m, `"method":"Target.attachedToTarget"`) && strings.Contains(m, `"sessionId":"CHILD"`) {
			fwdBefore = true
		}
	}
	c.g.mu.Lock()
	unconfirmedBefore := c.g.targetUnconfirmedLocked(hrTarget)
	c.g.mu.Unlock()
	c.protectedEntry()
	c.asked = false
	c.hold = true
	c.g.mu.Lock()
	beforeCommits := nt.commits
	sendLock := c.g.noteHistoryLockedCommitLocked(nt, beforeCommits-1)
	c.g.mu.Unlock()
	c.outbound(sendLock)
	c.g.mu.Lock()
	unconfirmedMid := c.g.targetUnconfirmedLocked(hrTarget)
	protectedAfterLock := false
	if t2 := c.g.targets["T2"]; t2 != nil && t2.protected {
		protectedAfterLock = true
	}
	c.g.mu.Unlock()
	c.guardRestore(hrSeven)
	c.clientRestore(hrSeven, "https://seven.sliccy.ai", "L9")
	c.infoChanged(hrSeven, "seven")
	c.answerHeld(false)
	protectedAfter := false
	if t2 := c.g.targets["T2"]; t2 != nil && t2.protected {
		protectedAfter = true
	}
	hiddenAfter := c.g.hidden["CHILD"] || c.g.refused["CHILD"]
	c.g.mu.Lock()
	unconfirmedAfter := c.g.targetUnconfirmedLocked(hrTarget)
	c.g.mu.Unlock()
	evalAfter := "n/a"
	if c.g.sessions["CHILD"] != nil {
		c.next++
		out, _ := c.sendRaw(fmt.Sprintf(`{"id":%d,"method":"Runtime.evaluate","params":{"expression":"1"},"sessionId":"CHILD"}`, c.next))
		evalAfter = out
	}
	keptFromClient := protectedAfter || hiddenAfter || !strings.HasPrefix(evalAfter, "forward")
	t.Logf("WINDOW: historyHeldAtAttach=%t fwdBeforeLock=%t unconfirmedBefore=%t unconfirmedMid=%t protectedAfterLock=%t unconfirmedAfter=%t protectedAfterRestore=%t hiddenAfter=%t evalAfter=%s keptFromClient=%t logs=%q", historyHeld, fwdBefore, unconfirmedBefore, unconfirmedMid, protectedAfterLock, unconfirmedAfter, protectedAfter, hiddenAfter, evalAfter, keptFromClient, c.logs)
	fmt.Printf("RESULT window historyHeld=%t fwdBefore=%t unconfirmedBefore=%t unconfirmedMid=%t protectedAfterLock=%t unconfirmedAfter=%t protectedAfter=%t hiddenAfter=%t evalAfter=%s keptFromClient=%t\n", historyHeld, fwdBefore, unconfirmedBefore, unconfirmedMid, protectedAfterLock, unconfirmedAfter, protectedAfter, hiddenAfter, evalAfter, keptFromClient)
	if historyHeld {
		t.Fatalf("candidate A must not history-hold at attach without locked stream")
	}
	if !fwdBefore {
		t.Fatalf("window setup: expected forward before lock under candidate A")
	}
}

func hrClassifyChildReply(out string, reply []byte) string {
	msg := string(reply)
	switch {
	case strings.HasPrefix(out, "forward"):
		return "forwarded"
	case strings.Contains(msg, refusedOutlived):
		return "refused-outlived"
	case strings.Contains(msg, refusedTarget):
		return "refused-target"
	case strings.Contains(msg, refusedGuard):
		return "refused-guard"
	case strings.HasPrefix(out, "reply") && strings.Contains(msg, `"error"`):
		return "refused-other"
	case strings.HasPrefix(out, "reply"):
		return "reply-ok"
	case out == "wait":
		return "wait"
	default:
		return out
	}
}

func hrLateAnswerVerdict(c *hrChrome, before int, got string) string {
	if len(c.client) == before || got == "" {
		return "dropped-silent"
	}
	switch {
	case strings.Contains(got, refusedOutlived):
		return "refused-outlived"
	case strings.Contains(got, refusedTarget):
		return "refused-target"
	case strings.Contains(got, `"error"`):
		return "refused-other"
	case strings.Contains(got, `"result"`) || strings.Contains(got, `"data"`) || strings.Contains(got, `"root"`):
		return "forwarded"
	default:
		return "forwarded"
	}
}

func TestHistoryRestoreInFlightChildCommandsAtProtectCommit(t *testing.T) {
	c := hrNewChrome(t)
	c.attach()
	c.seedDocIdentity()
	nt := c.nt()
	nt.done = false
	nt.historyLocked = false
	nt.confirm = nil
	info := map[string]any{"targetId": "T2", "type": "iframe", "url": "https://ordinary.test/fixture", "title": "", "browserContextId": "CTX", "attached": true, "parentFrameId": hrTarget, "parentId": hrTarget}
	c.kids["T2"] = info
	c.feed(hrEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": info}))
	c.client = nil
	c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "CHILD", "waitingForDebugger": false, "targetInfo": info}))
	fwd := false
	for _, m := range c.client {
		if strings.Contains(m, `"method":"Target.attachedToTarget"`) && strings.Contains(m, `"sessionId":"CHILD"`) {
			fwd = true
		}
	}
	if !fwd {
		t.Fatalf("row(i) setup: child must be forwarded under A; logs=%q", c.logs)
	}
	c.hold = true
	c.next++
	evalID := c.next
	out, v := c.sendRaw(fmt.Sprintf(`{"id":%d,"method":"Runtime.evaluate","params":{"expression":"1"},"sessionId":"CHILD"}`, evalID))
	if hrClassifyChildReply(out, v.reply) != "forwarded" {
		t.Fatalf("row(i) evaluate must be in flight: %s", out)
	}
	c.next++
	shotID := c.next
	out, v = c.sendRaw(fmt.Sprintf(`{"id":%d,"method":"Page.captureScreenshot","params":{},"sessionId":"CHILD"}`, shotID))
	if hrClassifyChildReply(out, v.reply) != "forwarded" {
		t.Fatalf("row(i) screenshot must be in flight: %s", out)
	}
	c.next++
	domID := c.next
	out, v = c.sendRaw(fmt.Sprintf(`{"id":%d,"method":"DOM.getDocument","params":{},"sessionId":"CHILD"}`, domID))
	if hrClassifyChildReply(out, v.reply) != "forwarded" {
		t.Fatalf("row(i) DOM.getDocument must be in flight: %s", out)
	}
	c.protectedEntry()
	c.asked = false
	c.g.mu.Lock()
	nt.documentURL = hrSeven
	nt.done = true
	sendLock := c.g.noteHistoryLockedCommitLocked(nt, nt.commits-1)
	c.g.mu.Unlock()
	c.outbound(sendLock)
	c.answerHeld(false)
	c.g.mu.Lock()
	protected := c.g.targets["T2"] != nil && c.g.targets["T2"].protected
	sessRef := c.g.sessionRefusedLocked("CHILD")
	c.g.mu.Unlock()
	deliver := func(id int64, result any) string {
		before := len(c.client)
		c.feed(hrReply(id, "CHILD", result))
		got := ""
		if len(c.client) > before {
			got = c.client[len(c.client)-1]
		}
		return hrLateAnswerVerdict(c, before, got)
	}
	lateEval := deliver(evalID, map[string]any{"result": map[string]any{"type": "number", "value": 1}})
	lateShot := deliver(shotID, map[string]any{"data": "QUJD"})
	lateDOM := deliver(domID, map[string]any{"root": map[string]any{"nodeId": 1}})
	c.next++
	afterOut, afterV := c.sendRaw(fmt.Sprintf(`{"id":%d,"method":"Runtime.evaluate","params":{"expression":"2"},"sessionId":"CHILD"}`, c.next))
	after := hrClassifyChildReply(afterOut, afterV.reply)
	t.Logf("row(i) protected=%t sessRef=%t lateEval=%s lateShot=%s lateDOM=%s after=%s", protected, sessRef, lateEval, lateShot, lateDOM, after)
	fmt.Printf("RESULT row(i) protected=%t sessRef=%t lateEval=%s lateShot=%s lateDOM=%s after=%s\n", protected, sessRef, lateEval, lateShot, lateDOM, after)
	for _, pair := range []struct{ name, v string }{{"evaluate", lateEval}, {"screenshot", lateShot}, {"dom", lateDOM}} {
		if pair.v == "forwarded" || pair.v == "reply-ok" {
			t.Fatalf("row(i) late %s answer must not be forwarded: %s", pair.name, pair.v)
		}
		if pair.v != "refused-target" {
			t.Fatalf("row(i) late %s want refused-target, got %s", pair.name, pair.v)
		}
	}
	if after == "forwarded" || after == "reply-ok" {
		t.Fatalf("row(i) command after conceal must not be answered with content: %s", after)
	}
}

func TestHistoryRestoreNewChildAtPendingProtectCommit(t *testing.T) {
	for _, order := range []string{"history-before-commit", "commit-before-history"} {
		t.Run(order, func(t *testing.T) {
			c := hrNewChrome(t)
			c.attach()
			c.seedDocIdentity()
			nt := c.nt()
			nt.done = false
			nt.historyLocked = false
			nt.confirm = nil
			c.hold = true
			c.client = nil
			c.logs = nil
			if order == "history-before-commit" {
				c.protectedEntry()
				c.asked = false
				c.g.mu.Lock()
				nt.historyLocked = true
				nt.done = true
				nt.documentURL = hrSeven
				c.g.mu.Unlock()
			} else {
				c.g.mu.Lock()
				nt.documentURL = hrSeven
				nt.done = false
				nt.historyLocked = false
				c.g.mu.Unlock()
			}
			info := map[string]any{"targetId": "NEW", "type": "iframe", "url": "https://frame.other.test/inner", "title": "", "browserContextId": "CTX", "attached": true, "parentFrameId": hrTarget, "parentId": hrTarget}
			c.kids["NEW"] = info
			c.feed(hrEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": info}))
			c.client = nil
			c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-NEW", "waitingForDebugger": false, "targetInfo": info}))
			fwd := false
			held := c.g.held["S-NEW"] != nil
			for _, m := range c.client {
				if strings.Contains(m, `"method":"Target.attachedToTarget"`) && strings.Contains(m, `"sessionId":"S-NEW"`) {
					fwd = true
				}
			}
			t.Logf("row(ii)/%s pre attachForwarded=%t held=%t parentDone=%t parentLocked=%t", order, fwd, held, nt.done, nt.historyLocked)
			fmt.Printf("RESULT row(ii)/%s pre attachForwarded=%t held=%t\n", order, fwd, held)
			if c.g.targets[hrTarget] != nil && c.g.targets[hrTarget].protected {
				t.Fatalf("row(ii)/%s setup: parent must not be protected before commit", order)
			}
			if !fwd && !held {
				t.Fatalf("row(ii)/%s: new child must be forwarded or held before protect commit", order)
			}
			evalID := int64(0)
			if fwd {
				c.next++
				evalID = c.next
				out, v := c.sendRaw(fmt.Sprintf(`{"id":%d,"method":"Runtime.evaluate","params":{"expression":"1"},"sessionId":"S-NEW"}`, evalID))
				if hrClassifyChildReply(out, v.reply) != "forwarded" {
					t.Fatalf("row(ii)/%s in-flight evaluate must forward before commit: %s", order, out)
				}
			}
			c.g.mu.Lock()
			nt.done = true
			nt.historyLocked = true
			nt.documentURL = hrSeven
			send := c.g.navProtectedCommitLocked(nt)
			c.g.mu.Unlock()
			c.outbound(send)
			c.answerHeld(false)
			c.g.mu.Lock()
			protected := c.g.targets["NEW"] != nil && c.g.targets["NEW"].protected
			hidden := c.g.hidden["S-NEW"] || c.g.refused["S-NEW"]
			sessRef := c.g.sessions["S-NEW"] != nil && c.g.sessionRefusedLocked("S-NEW")
			stillHeld := c.g.held["S-NEW"] != nil
			c.g.mu.Unlock()
			late := "n/a"
			if fwd && evalID != 0 {
				beforeClient := len(c.client)
				c.feed(hrReply(evalID, "S-NEW", map[string]any{"result": map[string]any{"type": "number", "value": 1}}))
				got := ""
				if len(c.client) > beforeClient {
					got = c.client[len(c.client)-1]
				}
				late = hrLateAnswerVerdict(c, beforeClient, got)
			}
			after := "n/a"
			if c.g.sessions["S-NEW"] != nil {
				c.next++
				afterOut, afterV := c.sendRaw(fmt.Sprintf(`{"id":%d,"method":"Runtime.evaluate","params":{"expression":"2"},"sessionId":"S-NEW"}`, c.next))
				after = hrClassifyChildReply(afterOut, afterV.reply)
			}
			t.Logf("row(ii)/%s post protected=%t hidden=%t sessRef=%t stillHeld=%t late=%s after=%s", order, protected, hidden, sessRef, stillHeld, late, after)
			fmt.Printf("RESULT row(ii)/%s post protected=%t hidden=%t sessRef=%t stillHeld=%t late=%s after=%s\n", order, protected, hidden, sessRef, stillHeld, late, after)
			if !protected && !hidden && !sessRef && stillHeld {
				t.Fatalf("row(ii)/%s: hold must be resolved at commit (conceal or refuse), still held", order)
			}
			if !protected && !hidden && !sessRef && !stillHeld && fwd {
				t.Fatalf("row(ii)/%s: forwarded child must be concealed at commit", order)
			}
			if late == "forwarded" || late == "reply-ok" {
				t.Fatalf("row(ii)/%s: in-flight answer must not be forwarded after commit: %s", order, late)
			}
			if after == "forwarded" || after == "reply-ok" {
				t.Fatalf("row(ii)/%s: command after commit must not be answered: %s", order, after)
			}
			if order == "commit-before-history" && !fwd {
				t.Fatalf("row(ii)/%s: expected forward before commit when history undecided", order)
			}
			if order == "history-before-commit" && !held && !fwd {
				t.Fatalf("row(ii)/%s: expected hold or forward when history already locked", order)
			}
		})
	}
}

func TestProbeInheritedParentOwnOwnerUndecidedBranch(t *testing.T) {
	c := hrNewChrome(t)
	c.attach()
	c.seedDocIdentity()
	c.plantPage("T1", "NAV-T1", false, false)
	nt := c.g.navTargets["T1"]
	if nt == nil || nt.done || nt.historyLocked {
		t.Fatalf("T1 must be undecided unlocked: %+v", nt)
	}
	info := map[string]any{"targetId": "T2", "type": "iframe", "url": "https://ordinary.test/fixture", "title": "", "browserContextId": "CTX", "attached": true, "parentFrameId": "T1", "parentId": "T1"}
	c.kids["T2"] = info
	c.feed(hrEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": info}))
	c.client = nil
	c.logs = nil
	c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "CHILD", "waitingForDebugger": false, "targetInfo": info}))
	h := c.g.held["CHILD"]
	branch := "none"
	switch {
	case h != nil && h.historyUnresolved && h.historyParent == "T1":
		branch = "own-owner-undecided (historyUnresolvedAttachEligibleLocked first branch, parent=T1)"
	case h != nil && h.historyUnresolved && h.historyParent == "":
		branch = "stream-unresolved-orphan"
	case h != nil && h.historyParent == "T1" && !h.historyUnresolved:
		branch = "locked-owner-hold"
	case h != nil:
		branch = fmt.Sprintf("other-hold unresolved=%t parent=%q", h.historyUnresolved, h.historyParent)
	case c.attachedForwarded("T2") || strings.Contains(strings.Join(c.client, "\n"), `"sessionId":"CHILD"`):
		branch = "forwarded-to-client"
	default:
		branch = "no-history-hold"
	}
	streamConcern := c.streamConcern()
	t.Logf("PROBE tip+A: branch=%s streamConcern=%t held=%v attachedFwd=%t logs=%q", branch, streamConcern, h != nil, c.attachedForwarded("T2") || strings.Contains(strings.Join(c.client, "\n"), "CHILD"), c.logs)
	fmt.Printf("RESULT tip+A branch=%s streamConcern=%t held=%t\n", branch, streamConcern, h != nil)
}

func TestProbeInheritedParentEmptyURLThenBlank(t *testing.T) {
	c := hrNewChrome(t)
	c.attach()
	c.seedDocIdentity()
	c.plantPage("T1", "NAV-T1", false, false)
	info := map[string]any{"targetId": "T2", "type": "iframe", "url": "", "title": "", "browserContextId": "CTX", "attached": true, "parentFrameId": "T1", "parentId": "T1"}
	c.kids["T2"] = info
	c.feed(hrEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": info}))
	c.client = nil
	c.logs = nil
	c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "CHILD", "waitingForDebugger": false, "targetInfo": info}))
	h1 := c.g.held["CHILD"]
	logs1 := append([]string{}, c.logs...)
	info["url"] = "about:blank"
	c.kids["T2"] = info
	c.feed(hrEvent("RELAY", "Target.targetInfoChanged", map[string]any{"targetInfo": info}))
	c.feed(hrEvent("CHILD", "Page.frameNavigated", map[string]any{"type": "Navigation", "frame": map[string]any{"id": "T2", "parentId": "T1", "url": "about:blank", "securityOrigin": "://"}}))
	h2 := c.g.held["CHILD"]
	fwd := false
	for _, m := range c.client {
		if strings.Contains(m, `"method":"Target.attachedToTarget"`) && strings.Contains(m, `"sessionId":"CHILD"`) {
			fwd = true
		}
	}
	t.Logf("PROBE empty-then-blank tip+A: heldAfterAttach=%t heldAfterBlank=%t fwd=%t logsAttach=%q logsAfter=%q", h1 != nil, h2 != nil, fwd, logs1, c.logs)
	fmt.Printf("RESULT tip+A empty-then-blank heldAttach=%t heldBlank=%t fwd=%t\n", h1 != nil, h2 != nil, fwd)
}

func TestHistoryRestoreUnresolvedOwnerResolvesLocked(t *testing.T) {
	after := func(c *hrChrome) string {
		h := c.g.held["S-ORPHAN"]
		if h == nil || h.historyUnresolved || h.historyParent != "PAGE-A" {
			return fmt.Sprintf("after-resolve hold=%+v", h)
		}
		c.client = nil
		c.answerHeld(false)
		if !c.attachedForwarded("ORPHAN") {
			return fmt.Sprintf("not delivered on ordinary+unchanged: %q state=%s", c.client, c.kidState("ORPHAN"))
		}
		return "delivered"
	}
	resolve := func(c *hrChrome) { c.orphanResolveOpener("ORPHAN", "PAGE-A") }
	gotAB := hrUnresolvedOutcome(t, false, resolve, after)
	gotBA := hrUnresolvedOutcome(t, true, resolve, after)
	if gotAB != "delivered" || gotBA != "delivered" {
		t.Fatalf("locked-owner fresh confirm path orderAB=%q orderBA=%q", gotAB, gotBA)
	}
}

func TestHistoryRestoreUnresolvedOwnerResolvesLockedHide(t *testing.T) {
	after := func(c *hrChrome) string {
		h := c.g.held["S-ORPHAN"]
		if h == nil || h.historyUnresolved {
			return fmt.Sprintf("not-ready:%+v", h)
		}
		parent := h.historyParent
		h.historySnap = docIdentity{frameID: parent, loaderID: "L-read", url: hrOrdinary}
		if nt := c.g.navTargets[parent]; nt != nil {
			nt.loaderID = "L-changed"
			nt.documentURL = hrOrdinary
		}
		c.client = nil
		c.held = nil
		part := c.g.deliverHistoryHoldLocked(h, true, h.historyConfirmMinSeq)
		c.take(part)
		if c.attachedForwarded("ORPHAN") {
			return "delivered"
		}
		if c.kidState("ORPHAN") != "hidden" && !c.g.hidden["S-ORPHAN"] {
			return "not-hidden:" + c.kidState("ORPHAN")
		}
		return "hidden"
	}
	resolve := func(c *hrChrome) { c.orphanResolveOpener("ORPHAN", "PAGE-A") }
	gotAB := hrUnresolvedOutcome(t, false, resolve, after)
	gotBA := hrUnresolvedOutcome(t, true, resolve, after)
	if gotAB != "hidden" || gotBA != "hidden" {
		t.Fatalf("identity change after read hide orderAB=%q orderBA=%q", gotAB, gotBA)
	}
}

func TestHistoryRestoreUnresolvedOwnerResolvesOrdinary(t *testing.T) {
	run := func(orderBA bool) string {
		c := hrUnresolvedTwoPages(t, orderBA, true, false, true, true)
		c.g.navTargets["PAGE-B"].historyLocked = false
		c.hold = true
		c.orphanAnnounce("ORPHAN", "webview", "https://third.test/view")
		c.client = nil
		raw := string(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-ORPHAN", "waitingForDebugger": false, "targetInfo": c.kids["ORPHAN"]}))
		c.orphanAttach("ORPHAN")
		if c.g.held["S-ORPHAN"] == nil || !c.g.held["S-ORPHAN"].historyUnresolved {
			t.Fatalf("want unresolved hold: %+v", c.g.held["S-ORPHAN"])
		}
		c.client = nil
		c.orphanResolveOpener("ORPHAN", "PAGE-B")
		if c.g.held["S-ORPHAN"] != nil {
			return fmt.Sprintf("still held: %+v", c.g.held["S-ORPHAN"])
		}
		if !c.attachedForwarded("ORPHAN") {
			return fmt.Sprintf("not delivered: %q", c.client)
		}
		for _, m := range c.client {
			if strings.Contains(m, `"method":"Target.attachedToTarget"`) && strings.Contains(m, `"sessionId":"S-ORPHAN"`) {
				if m != raw {
					return "not-byte-identical"
				}
				return "delivered-identical"
			}
		}
		return "missing-attach"
	}
	gotAB, gotBA := run(false), run(true)
	if gotAB != "delivered-identical" || gotBA != "delivered-identical" {
		t.Fatalf("ordinary decided deliver orderAB=%q orderBA=%q", gotAB, gotBA)
	}
}

func TestHistoryRestoreUnresolvedOwnerAwaitDecide(t *testing.T) {
	run := func(orderBA bool, thenLocked bool) string {
		c := hrUnresolvedTwoPages(t, orderBA, true, false, true, false)
		c.hold = true
		c.orphanAnnounce("ORPHAN", "webview", "https://third.test/view")
		c.client = nil
		c.orphanAttach("ORPHAN")
		c.orphanResolveOpener("ORPHAN", "PAGE-B")
		h := c.g.held["S-ORPHAN"]
		if h == nil || !h.historyUnresolved || h.historyParent != "PAGE-B" {
			return fmt.Sprintf("await hold=%+v", h)
		}
		if c.attachedForwarded("ORPHAN") {
			return "early-forward"
		}
		nt := c.g.navTargets["PAGE-B"]
		nt.settle(false)
		nt.historyLocked = thenLocked
		nt.loaderID = "L-PAGE-B"
		nt.documentURL = hrOrdinary
		c.feed(hrEvent("RELAY", "Target.targetInfoChanged", map[string]any{"targetInfo": map[string]any{"targetId": "PAGE-B", "type": "page", "url": hrOrdinary, "title": "PAGE-B", "browserContextId": "CTX"}}))
		if thenLocked {
			h = c.g.held["S-ORPHAN"]
			if h == nil || h.historyUnresolved || h.historyParent != "PAGE-B" {
				return fmt.Sprintf("locked after decide hold=%+v", h)
			}
			c.client = nil
			c.answerHeld(false)
			if !c.attachedForwarded("ORPHAN") {
				return "locked-not-delivered"
			}
			return "locked-delivered"
		}
		if c.g.held["S-ORPHAN"] != nil {
			return "ordinary-still-held"
		}
		if !c.attachedForwarded("ORPHAN") {
			return "ordinary-not-delivered"
		}
		return "ordinary-delivered"
	}
	for _, thenLocked := range []bool{false, true} {
		gotAB := run(false, thenLocked)
		gotBA := run(true, thenLocked)
		if gotAB != gotBA || (!thenLocked && gotAB != "ordinary-delivered") || (thenLocked && gotAB != "locked-delivered") {
			t.Fatalf("await-decide thenLocked=%t orderAB=%q orderBA=%q", thenLocked, gotAB, gotBA)
		}
	}
}

func TestHistoryRestoreUnresolvedOwnerDeadline(t *testing.T) {
	run := func(orderBA bool) string {
		c := hrUnresolvedTwoPages(t, orderBA, true, true, true, true)
		c.hold = true
		c.orphanAnnounce("ORPHAN", "webview", "https://third.test/view")
		pv := c.g.fromCommand(&command{id: 940, method: "Target.attachToTarget", params: map[string]any{"targetId": "ORPHAN", "flatten": true}}, 940, true)
		c.outbound(pv.send)
		c.orphanAttach("ORPHAN")
		c.feed(hrReply(940, "", map[string]any{"sessionId": "S-ORPHAN"}))
		h := c.g.held["S-ORPHAN"]
		if h == nil || !h.historyUnresolved || !h.hasReply {
			t.Fatalf("want parked unresolved reply: %+v", h)
		}
		c.client = nil
		page, send := c.g.expireHold("S-ORPHAN")
		c.take(browserVerdict{page: page, send: send})
		if c.attachedForwarded("ORPHAN") {
			return "forwarded"
		}
		replies := 0
		for _, m := range c.client {
			if strings.Contains(m, `"id":940`) {
				replies++
				if !strings.Contains(m, refusedTarget) {
					return "non-refusal"
				}
			}
		}
		if replies != 1 {
			return fmt.Sprintf("replies=%d", replies)
		}
		return "hidden-refused-once"
	}
	gotAB, gotBA := run(false), run(true)
	if gotAB != "hidden-refused-once" || gotBA != "hidden-refused-once" {
		t.Fatalf("deadline orderAB=%q orderBA=%q", gotAB, gotBA)
	}
}

func TestHistoryRestoreUnresolvedOwnerIndependentOfConcernPages(t *testing.T) {
	run := func(orderBA bool) string {
		c := hrUnresolvedTwoPages(t, orderBA, true, false, true, false)
		c.plantPage("PAGE-C", "NAV-C", false, true)
		c.g.navTargets["PAGE-C"].historyLocked = false
		c.hold = true
		c.orphanAnnounce("ORPHAN", "webview", "https://third.test/view")
		c.client = nil
		c.orphanAttach("ORPHAN")
		c.orphanResolveOpener("ORPHAN", "PAGE-C")
		h := c.g.held["S-ORPHAN"]
		if h != nil {
			return fmt.Sprintf("held on concern pages: parent=%q unresolved=%t", h.historyParent, h.historyUnresolved)
		}
		if !c.attachedForwarded("ORPHAN") {
			return "not-delivered"
		}
		return "delivered"
	}
	gotAB, gotBA := run(false), run(true)
	if gotAB != "delivered" || gotBA != "delivered" {
		t.Fatalf("independent owner orderAB=%q orderBA=%q", gotAB, gotBA)
	}
}

func TestHistoryRestoreUnresolvedOwnerKindDeadlineMeasure(t *testing.T) {
	kinds := []string{"iframe", "worker", "shared_worker", "service_worker", "webview", "other", "background_page"}
	var deadlineKinds []string
	var unresolvedKinds []string
	held := 0
	var waited []int64
	for _, kind := range kinds {
		c := hrLockedTab(t)
		c.hold = true
		id := "K-" + kind
		c.orphanAnnounce(id, kind, "https://third.test/"+kind)
		c.client = nil
		c.orphanAttach(id)
		h := c.g.held["S-"+id]
		if h == nil || !h.historyUnresolved {
			continue
		}
		held++
		unresolvedKinds = append(unresolvedKinds, kind)
		at := h.at
		c.orphanResolveOpener(id, hrTarget)
		h2 := c.g.held["S-"+id]
		if h2 != nil && !h2.historyUnresolved && h2.historyParent == hrTarget {
			waited = append(waited, time.Since(at).Milliseconds())
			c.client = nil
			c.answerHeld(false)
			continue
		}
		if h2 != nil && h2.historyUnresolved {
			page, send := c.g.expireHold("S-" + id)
			c.take(browserVerdict{page: page, send: send})
			deadlineKinds = append(deadlineKinds, kind)
		}
	}
	t.Logf("unresolved held count=%d kinds=%v resolveWaitMs=%v deadlineKinds=%v", held, unresolvedKinds, waited, deadlineKinds)
	for _, k := range unresolvedKinds {
		switch k {
		case "background_page", "shared_worker", "service_worker":
			t.Fatalf("browser-level kind %s must not take the unresolved-owner history hold on a merely-locked stream", k)
		}
	}
}

func TestHistoryRestoreUnresolvedOrdinaryProtectedOwnerHides(t *testing.T) {
	c := hrUnresolvedTwoPages(t, false, true, false, true, true)
	c.g.navTargets["PAGE-B"].historyLocked = false
	c.g.targets["PAGE-B"].protected = true
	c.hold = true
	info := map[string]any{"targetId": "ORPHAN", "type": "iframe", "url": "https://third.test/embed", "title": "", "browserContextId": "OTHER", "attached": true}
	c.kids["ORPHAN"] = info
	c.feed(hrEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": info}))
	c.client = nil
	c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-ORPHAN", "waitingForDebugger": false, "targetInfo": info}))
	if c.g.held["S-ORPHAN"] == nil || !c.g.held["S-ORPHAN"].historyUnresolved {
		t.Fatalf("want unresolved hold: %+v", c.g.held["S-ORPHAN"])
	}
	info["parentFrameId"] = "PAGE-B"
	info["parentId"] = "PAGE-B"
	info["openerId"] = "PAGE-B"
	c.feed(hrEvent("RELAY", "Target.targetInfoChanged", map[string]any{"targetInfo": info}))
	if c.attachedForwarded("ORPHAN") {
		t.Fatalf("delivered under protected non-history-locked owner: %q", c.client)
	}
	if c.g.held["S-ORPHAN"] != nil && c.g.held["S-ORPHAN"].historyUnresolved {
		t.Fatalf("still unresolved after ordinary resolve: %+v", c.g.held["S-ORPHAN"])
	}
	if !c.g.hidden["S-ORPHAN"] && !c.g.refused["S-ORPHAN"] && c.kidState("ORPHAN") != "hidden" {
		t.Fatalf("want hidden under protected owner: state=%s", c.kidState("ORPHAN"))
	}
}

func TestHistoryRestoreUnresolvedOrdinaryProtectedURLChildHides(t *testing.T) {
	c := hrUnresolvedTwoPages(t, false, true, false, true, true)
	c.g.navTargets["PAGE-B"].historyLocked = false
	c.hold = true
	c.orphanAnnounce("ORPHAN", "iframe", "https://seven.sliccy.ai/embed")
	c.client = nil
	c.orphanAttach("ORPHAN")
	c.orphanResolveOpener("ORPHAN", "PAGE-B")
	if c.attachedForwarded("ORPHAN") {
		t.Fatalf("protected-URL child delivered: %q", c.client)
	}
	if !c.g.hidden["S-ORPHAN"] && !c.g.refused["S-ORPHAN"] {
		t.Fatalf("want hide for protected-URL child: held=%v state=%s", c.g.held["S-ORPHAN"], c.kidState("ORPHAN"))
	}
}

func TestHistoryRestoreUnresolvedOrdinaryEmptyURLHolds(t *testing.T) {
	c := hrUnresolvedTwoPages(t, false, true, false, true, true)
	c.g.navTargets["PAGE-B"].historyLocked = false
	c.hold = true
	info := map[string]any{"targetId": "ORPHAN", "type": "iframe", "url": "", "title": "", "browserContextId": "OTHER", "attached": true}
	c.kids["ORPHAN"] = info
	c.feed(hrEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": info}))
	c.client = nil
	c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-ORPHAN", "waitingForDebugger": false, "targetInfo": info}))
	info["parentFrameId"] = "PAGE-B"
	info["parentId"] = "PAGE-B"
	info["openerId"] = "PAGE-B"
	c.feed(hrEvent("RELAY", "Target.targetInfoChanged", map[string]any{"targetInfo": info}))
	h := c.g.held["S-ORPHAN"]
	if h == nil || h.historyUnresolved || h.historyParent != "" {
		t.Fatalf("want empty-URL hold after ordinary resolve: %+v", h)
	}
	if c.attachedForwarded("ORPHAN") {
		t.Fatalf("empty-URL child delivered: %q", c.client)
	}
}

func TestHistoryRestoreServiceWorkerNotConcernHeld(t *testing.T) {
	c := hrLockedTab(t)
	info := c.orphanAnnounce("SW", "service_worker", "https://third.test/sw.js")
	c.client = nil
	c.logs = nil
	raw := string(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-SW", "waitingForDebugger": false, "targetInfo": info}))
	c.feed([]byte(raw))
	if h := c.g.held["S-SW"]; h != nil && (h.historyUnresolved || h.historyParent != "") {
		t.Fatalf("service worker history-held on locked stream: %+v", h)
	}
	for _, line := range c.logs {
		if strings.Contains(line, "holding unresolved-owner") || strings.Contains(line, "holding a child of a history-locked page") {
			t.Fatalf("service worker took history hold: %q", c.logs)
		}
	}
	if len(c.client) != 1 || c.client[0] != raw {
		t.Fatalf("service worker not byte-identical: %q want %s", c.client, raw)
	}
}

func TestHistoryRestoreSharedWorkerBrowserLevelNotConcernHeld(t *testing.T) {
	c := hrLockedTab(t)
	info := c.orphanAnnounce("SH", "shared_worker", "https://third.test/shared.js")
	c.client = nil
	raw := string(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-SH", "waitingForDebugger": false, "targetInfo": info}))
	c.feed([]byte(raw))
	if h := c.g.held["S-SH"]; h != nil && (h.historyUnresolved || h.historyParent != "") {
		t.Fatalf("shared worker history-held on locked stream: %+v", h)
	}
	if len(c.client) != 1 || c.client[0] != raw {
		t.Fatalf("shared worker not byte-identical: %q want %s", c.client, raw)
	}
}

func TestHistoryRestoreDedicatedWorkerCreatorLocked(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.kidAnnounced("DW", "worker", "https://third.test/w.js", true)
	c.client = nil
	c.kidAttached("DW")
	h := c.g.held["S-DW"]
	if h == nil || h.historyUnresolved || h.historyParent != hrTarget {
		t.Fatalf("want history hold under locked creator: %+v", h)
	}
	if c.attachedForwarded("DW") {
		t.Fatalf("forwarded before confirm: %q", c.client)
	}
	c.answerHeld(false)
	if !c.attachedForwarded("DW") {
		t.Fatalf("not delivered after confirm: %q", c.client)
	}
}

func TestHistoryRestoreDedicatedWorkerCreatorOrdinary(t *testing.T) {
	c := hrUnresolvedTwoPages(t, false, true, false, true, true)
	c.g.navTargets["PAGE-B"].historyLocked = false
	info := map[string]any{"targetId": "DW", "type": "worker", "url": "https://third.test/w.js", "title": "", "browserContextId": "CTX", "attached": true, "openerId": "PAGE-B"}
	c.kids["DW"] = info
	c.feed(hrEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": info}))
	c.client = nil
	raw := string(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-DW", "waitingForDebugger": false, "targetInfo": info}))
	c.feed([]byte(raw))
	if h := c.g.held["S-DW"]; h != nil && (h.historyUnresolved || h.historyParent != "") {
		t.Fatalf("dedicated worker of ordinary page history-held: %+v", h)
	}
	if len(c.client) != 1 || c.client[0] != raw {
		t.Fatalf("ordinary-creator worker not byte-identical: %q", c.client)
	}
}

func TestHistoryRestoreUnresolvedStaleDoneConfirmNeedsNewRead(t *testing.T) {
	c := hrUnresolvedTwoPages(t, false, true, true, true, true)
	c.hold = true
	nt := c.g.navTargets["PAGE-A"]
	c.g.beginConfirmLocked(nt)
	nt.confirm.issuedSeq = 1
	c.g.confirmSeq = 1
	nt.confirm.settle(false)
	c.orphanAnnounce("ORPHAN", "webview", "https://third.test/view")
	c.client = nil
	c.orphanAttach("ORPHAN")
	c.orphanResolveOpener("ORPHAN", "PAGE-A")
	if c.attachedForwarded("ORPHAN") {
		t.Fatalf("delivered on stale done confirm without new read: %q", c.client)
	}
	h := c.g.held["S-ORPHAN"]
	if h == nil || h.historyUnresolved || h.historyParent != "PAGE-A" {
		t.Fatalf("want locked-owner hold awaiting fresh confirm: %+v", h)
	}
	if len(c.held) == 0 {
		t.Fatal("expected a new getFrameTree after resolve to locked owner")
	}
	c.client = nil
	c.answerHeld(false)
	if !c.attachedForwarded("ORPHAN") {
		t.Fatalf("not delivered after fresh confirm: %q", c.client)
	}
}

func TestHistoryRestoreConfirmInFlightChildNeedsSecondRead(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.kidAnnounced("OLD", "iframe", "https://third.test/old", true)
	c.client = nil
	c.kidAttached("OLD")
	if len(c.held) == 0 {
		t.Fatal("expected in-flight getFrameTree for first child")
	}
	firstIssued := c.g.confirmSeq
	if firstIssued == 0 {
		t.Fatal("confirm read was not issued")
	}
	c.client = nil
	c.kidAnnounced("NEW", "iframe", "https://third.test/new", true)
	c.kidAttached("NEW")
	hNew := c.g.held["S-NEW"]
	if hNew == nil || hNew.historyConfirmMinSeq <= firstIssued {
		t.Fatalf("NEW hold must require a read after first issue: %+v firstIssued=%d", hNew, firstIssued)
	}
	c.client = nil
	c.answerHeldOne(false)
	if !c.attachedForwarded("OLD") {
		t.Fatalf("OLD not delivered by first read: %q", c.client)
	}
	if c.attachedForwarded("NEW") {
		t.Fatalf("NEW delivered by stale in-flight read: %q", c.client)
	}
	if c.g.held["S-NEW"] == nil {
		t.Fatal("NEW should still be held for second read")
	}
	if c.g.confirmSecondReads < 1 || len(c.held) == 0 {
		t.Fatalf("want second read issued: secondReads=%d pending=%d", c.g.confirmSecondReads, len(c.held))
	}
	c.client = nil
	c.answerHeldOne(false)
	if !c.attachedForwarded("NEW") {
		t.Fatalf("NEW not delivered by second ordinary read: %q", c.client)
	}
	if c.g.confirmSeq != 2 || c.g.confirmSecondReads != 1 {
		t.Fatalf("want exactly two parent reads (one reissue): confirmSeq=%d secondReads=%d", c.g.confirmSeq, c.g.confirmSecondReads)
	}
	if !hrLogHas(c.logs, "history hold delivered", "session=S-NEW", "readSeqs=[1 2]") {
		t.Fatalf("NEW must record in-flight+second readSeqs=[1 2]: %q", c.logs)
	}
	if len(c.held) != 0 {
		t.Fatalf("no further getFrameTree after second read: pending=%d", len(c.held))
	}
}

func TestHistoryRestoreSecondReadRestampsIdentity(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.loader = "L-A"
	if nt := c.nt(); nt != nil {
		nt.loaderID = "L-A"
	}
	c.kidAnnounced("OLD", "iframe", "https://third.test/old", true)
	c.client = nil
	c.kidAttached("OLD")
	firstIssued := c.g.confirmSeq
	c.kidAnnounced("NEW", "iframe", "https://third.test/new", true)
	c.kidAttached("NEW")
	hNew := c.g.held["S-NEW"]
	if hNew == nil || hNew.historyConfirmMinSeq <= firstIssued {
		t.Fatalf("NEW hold must require a later read: %+v firstIssued=%d", hNew, firstIssued)
	}
	c.client = nil
	c.answerHeldOne(false)
	if !c.attachedForwarded("OLD") {
		t.Fatalf("OLD not delivered by read A: %q", c.client)
	}
	if c.attachedForwarded("NEW") {
		t.Fatalf("NEW released by read A: %q", c.client)
	}
	hNew = c.g.held["S-NEW"]
	if hNew == nil || !hNew.historyReadStamped || hNew.historySnap.loaderID != "L-A" {
		t.Fatalf("NEW must keep read-A stamp while unreleased: %+v", hNew)
	}
	c.loader = "L-B"
	c.client = nil
	c.answerHeldOne(false)
	if !c.attachedForwarded("NEW") {
		t.Fatalf("NEW not delivered by read B (compare must use B's identity Y, not stale A): %q logs=%q snap=%+v", c.client, c.logs, hNew.historySnap)
	}
	if !hrLogHas(c.logs, "history hold delivered", "session=S-NEW") {
		t.Fatalf("want delivered by later read B: %q", c.logs)
	}
}

func TestHistoryRestoreConfirmInFlightSecondReadProtectedHides(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.kidAnnounced("OLD", "iframe", "https://third.test/old", true)
	c.kidAttached("OLD")
	c.kidAnnounced("NEW", "iframe", "https://third.test/new", true)
	c.kidAttached("NEW")
	c.client = nil
	c.answerHeldOne(false)
	if c.attachedForwarded("NEW") {
		t.Fatalf("NEW delivered by first read: %q", c.client)
	}
	c.url = hrSeven
	c.origin = "https://seven.sliccy.ai"
	c.client = nil
	c.answerHeldOne(false)
	if c.attachedForwarded("NEW") {
		t.Fatalf("NEW delivered when second read was protected: %q", c.client)
	}
	if !c.g.hidden["S-NEW"] && c.kidState("NEW") != "hidden" && c.kidState("NEW") != "refused/ev-dropped" {
		t.Fatalf("want NEW hidden after protected second read: state=%s", c.kidState("NEW"))
	}
}

func TestHistoryRestoreConfirmSharedThenLateHold(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	nt := c.nt()
	sess := nt.session
	nt.session = ""
	c.kidAnnounced("A", "iframe", "https://third.test/a", true)
	c.kidAttached("A")
	c.kidAnnounced("B", "iframe", "https://third.test/b", true)
	c.kidAttached("B")
	if c.g.held["S-A"] == nil || c.g.held["S-B"] == nil {
		t.Fatal("A and B must be held before the shared read is issued")
	}
	if c.g.held["S-A"].historyConfirmMinSeq != c.g.held["S-B"].historyConfirmMinSeq {
		t.Fatalf("A and B should share the same minSeq: A=%d B=%d", c.g.held["S-A"].historyConfirmMinSeq, c.g.held["S-B"].historyConfirmMinSeq)
	}
	nt.session = sess
	c.g.mu.Lock()
	send := c.g.beginHistoryParentConfirmLocked(nt)
	c.g.mu.Unlock()
	c.outbound(send)
	issued := c.g.confirmSeq
	if issued == 0 || c.g.held["S-A"].historyConfirmMinSeq > issued {
		t.Fatalf("shared read not acceptable: issued=%d min=%d", issued, c.g.held["S-A"].historyConfirmMinSeq)
	}
	c.client = nil
	c.answerHeldOne(false)
	if !c.attachedForwarded("A") || !c.attachedForwarded("B") {
		t.Fatalf("shared read should release both: %q", c.client)
	}
	if c.g.confirmSeq != 1 {
		t.Fatalf("A and B must share exactly one read: confirmSeq=%d", c.g.confirmSeq)
	}
	if !hrLogHas(c.logs, "history hold delivered", "session=S-A", "readSeqs=[1]") || !hrLogHas(c.logs, "history hold delivered", "session=S-B", "readSeqs=[1]") {
		t.Fatalf("A/B should each show one shared readSeq: %q", c.logs)
	}
	c.client = nil
	c.logs = nil
	c.kidAnnounced("C", "iframe", "https://third.test/c", true)
	c.kidAttached("C")
	if c.g.held["S-C"] == nil {
		t.Fatal("late hold C must wait for its own read")
	}
	if c.g.held["S-C"].historyConfirmMinSeq <= issued {
		t.Fatalf("C must not accept prior read: min=%d issued=%d", c.g.held["S-C"].historyConfirmMinSeq, issued)
	}
	c.client = nil
	c.answerHeldOne(false)
	if !c.attachedForwarded("C") {
		t.Fatalf("C not delivered by its own read: %q", c.client)
	}
	if c.g.confirmSeq != 2 || c.g.confirmSecondReads != 0 {
		t.Fatalf("late hold needs exactly one more read (total 2): confirmSeq=%d secondReads=%d", c.g.confirmSeq, c.g.confirmSecondReads)
	}
	if !hrLogHas(c.logs, "history hold delivered", "session=S-C", "readSeqs=[2]") {
		t.Fatalf("C should show exactly one own readSeq: %q", c.logs)
	}
}

func TestHistoryRestoreExplicitAttachConfirmFreshness(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.kidAnnounced("NEW", "iframe", "https://third.test/new", true)
	pv := c.g.fromCommand(&command{id: 930, method: "Target.attachToTarget", params: map[string]any{"targetId": "NEW", "flatten": true}}, 930, true)
	if pv.reply != nil {
		t.Fatalf("explicit attach refused before confirm: %s", pv.reply)
	}
	c.outbound(pv.send)
	c.kidAnnounced("OLD", "iframe", "https://third.test/old", true)
	c.kidAttached("OLD")
	firstIssued := c.g.confirmSeq
	if firstIssued == 0 {
		t.Fatal("expected in-flight confirm before parking explicit reply")
	}
	c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-NEW", "waitingForDebugger": false, "targetInfo": c.kids["NEW"]}))
	c.client = nil
	c.feed(hrReply(930, "", map[string]any{"sessionId": "S-NEW"}))
	h := c.g.held["S-NEW"]
	if h == nil || !h.hasReply || h.historyConfirmMinSeq <= firstIssued {
		t.Fatalf("explicit hold freshness: %+v firstIssued=%d", h, firstIssued)
	}
	c.client = nil
	c.answerHeldOne(false)
	for _, m := range c.client {
		if strings.Contains(m, `"id":930`) {
			t.Fatalf("explicit reply answered by stale read: %s", m)
		}
	}
	if c.attachedForwarded("NEW") {
		t.Fatalf("explicit NEW delivered by stale read")
	}
	c.client = nil
	c.answerHeldOne(false)
	replies := 0
	for _, m := range c.client {
		if strings.Contains(m, `"id":930`) {
			replies++
		}
	}
	if replies != 1 || !c.attachedForwarded("NEW") {
		t.Fatalf("want one reply and deliver on fresh read: replies=%d client=%q", replies, c.client)
	}
}

func TestHistoryRestoreStaleForwardHides(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.kidAnnounced("NEW", "iframe", "https://third.test/new", true)
	pv := c.g.fromCommand(&command{id: 931, method: "Target.attachToTarget", params: map[string]any{"targetId": "NEW", "flatten": true}}, 931, true)
	if pv.reply != nil {
		t.Fatalf("explicit attach refused: %s", pv.reply)
	}
	c.outbound(pv.send)
	c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-NEW", "waitingForDebugger": false, "targetInfo": c.kids["NEW"]}))
	c.client = nil
	c.feed(hrReply(931, "", map[string]any{"sessionId": "S-NEW"}))
	h := c.g.held["S-NEW"]
	if h == nil || !h.hasReply {
		t.Fatalf("need parked explicit reply: %+v", h)
	}
	h.historyAttachMinSeq = 2
	h.historyConfirmMinSeq = 2
	reply, replyID, replySid, hasReply := h.reply, h.replyID, h.replySid, h.hasReply
	attachSeq := h.historyAttachMinSeq
	c.client = nil
	c.logs = nil
	c.g.mu.Lock()
	delete(c.g.held, h.id)
	v := c.g.forwardHistoryHoldLocked(h, attachSeq, 1, reply, replyID, replySid, hasReply)
	c.g.mu.Unlock()
	c.take(v)
	if c.attachedForwarded("NEW") {
		t.Fatalf("stale forward must not deliver: %q", c.client)
	}
	replies := 0
	for _, m := range c.client {
		if strings.Contains(m, `"id":931`) {
			replies++
			if !strings.Contains(m, refusedTarget) {
				t.Fatalf("want refusedTarget reply: %s", m)
			}
		}
	}
	if replies != 1 {
		t.Fatalf("want one refused reply: replies=%d client=%q", replies, c.client)
	}
	if !c.g.hidden["S-NEW"] && c.kidState("NEW") != "hidden" && c.kidState("NEW") != "refused/ev-dropped" {
		t.Fatalf("want hidden after stale forward: state=%s", c.kidState("NEW"))
	}
	sawRefuse := false
	for _, line := range c.logs {
		if strings.Contains(line, "refusing history hold delivery by stale confirm") && strings.Contains(line, "attachSeq=2") && strings.Contains(line, "readSeq=1") {
			sawRefuse = true
		}
	}
	if !sawRefuse {
		t.Fatalf("want stale-refuse log with seqs: %q", c.logs)
	}
}

func TestHistoryRestoreMinZeroHoldNoReissue(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.kidAnnounced("OLD", "iframe", "https://third.test/old", true)
	c.kidAttached("OLD")
	readsBefore := c.g.confirmSeq
	if readsBefore == 0 {
		t.Fatal("expected a confirm read issued before the min-0 hold")
	}
	c.kidAnnounced("ZERO", "iframe", "https://third.test/zero", true)
	c.kidAttached("ZERO")
	h := c.g.held["S-ZERO"]
	if h == nil {
		t.Fatal("ZERO must be held")
	}
	h.historyAttachMinSeq = 0
	h.historyConfirmMinSeq = 0
	c.client = nil
	c.answerHeldOne(false)
	if c.attachedForwarded("ZERO") {
		t.Fatalf("min-0 hold must not be delivered by the completed read: %q", c.client)
	}
	if c.g.confirmSeq != readsBefore || c.g.confirmSecondReads != 0 || len(c.held) != 0 {
		t.Fatalf("min-0 must not reissue: before=%d confirmSeq=%d secondReads=%d pending=%d", readsBefore, c.g.confirmSeq, c.g.confirmSecondReads, len(c.held))
	}
	if c.g.held["S-ZERO"] == nil {
		t.Fatal("min-0 hold should remain until deadline")
	}
	c.client = nil
	c.logs = nil
	page, send := c.g.expireHold("S-ZERO")
	c.take(browserVerdict{page: page, send: send})
	if c.attachedForwarded("ZERO") {
		t.Fatalf("deadline must hide min-0 hold: %q", c.client)
	}
	if !c.g.hidden["S-ZERO"] && c.kidState("ZERO") != "hidden" && c.kidState("ZERO") != "refused/ev-dropped" {
		t.Fatalf("want hidden at deadline: state=%s", c.kidState("ZERO"))
	}
	if !hrLogHas(c.logs, "reached its deadline", "session=S-ZERO", "deadline=true") && !hrLogHas(c.logs, "history hold hidden", "session=S-ZERO", "deadline=true") {
		t.Fatalf("want deadline log for ZERO: %q", c.logs)
	}
	if c.g.confirmSeq != readsBefore {
		t.Fatalf("deadline must not issue more reads: before=%d after=%d", readsBefore, c.g.confirmSeq)
	}
}

func TestHistoryRestoreAllHoldKindsStamped(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.kidAnnounced("EX", "iframe", "https://third.test/ex", true)
	pv := c.g.fromCommand(&command{id: 932, method: "Target.attachToTarget", params: map[string]any{"targetId": "EX", "flatten": true}}, 932, true)
	if pv.reply != nil {
		t.Fatalf("explicit attach refused: %s", pv.reply)
	}
	c.outbound(pv.send)
	c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-EX", "waitingForDebugger": false, "targetInfo": c.kids["EX"]}))
	c.feed(hrReply(932, "", map[string]any{"sessionId": "S-EX"}))
	if h := c.g.held["S-EX"]; h == nil || !h.hasReply || h.historyAttachMinSeq == 0 || h.historyConfirmMinSeq == 0 {
		t.Fatalf("explicit-attach reply hold must be stamped: %+v", c.g.held["S-EX"])
	}
	c.kidAnnounced("IFR", "iframe", "https://third.test/ifr", true)
	c.kidAttached("IFR")
	if h := c.g.held["S-IFR"]; h == nil || h.historyAttachMinSeq == 0 || h.historyConfirmMinSeq == 0 {
		t.Fatalf("auto-attach iframe must be stamped: %+v", c.g.held["S-IFR"])
	}
	info := map[string]any{"targetId": "WK", "type": "worker", "url": "https://third.test/w.js", "title": "", "browserContextId": "CTX", "attached": true, "openerId": hrTarget}
	c.kids["WK"] = info
	c.feed(hrEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": info}))
	c.feed(hrEvent(hrClient, "Target.attachedToTarget", map[string]any{"sessionId": "S-WK", "waitingForDebugger": false, "targetInfo": info}))
	if h := c.g.held["S-WK"]; h == nil || h.historyAttachMinSeq == 0 || h.historyConfirmMinSeq == 0 {
		t.Fatalf("auto-attach worker must be stamped: %+v", c.g.held["S-WK"])
	}
	c.orphanAnnounce("WV", "webview", "https://third.test/view")
	c.orphanAttach("WV")
	if h := c.g.held["S-WV"]; h == nil || !h.historyUnresolved || h.historyAttachMinSeq == 0 || h.historyConfirmMinSeq == 0 {
		t.Fatalf("unresolved webview must be stamped: %+v", c.g.held["S-WV"])
	}
	c.orphanResolveOpener("WV", hrTarget)
	if h := c.g.held["S-WV"]; h == nil || h.historyUnresolved || h.historyAttachMinSeq == 0 || h.historyConfirmMinSeq == 0 {
		t.Fatalf("resolved-to-locked webview must stay stamped: %+v", c.g.held["S-WV"])
	}
}

func TestHistoryRestoreOrdinaryPopupNotHistoryHeld(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	info := map[string]any{"targetId": "POP", "type": "page", "url": "https://ordinary.test/popup", "title": "popup", "browserContextId": "CTX", "attached": true, "openerId": hrTarget}
	c.kids["POP"] = info
	c.feed(hrEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": info}))
	c.client = nil
	c.logs = nil
	c.feed(hrEvent(hrClient, "Target.attachedToTarget", map[string]any{"sessionId": "S-POP", "waitingForDebugger": false, "targetInfo": info}))
	for _, line := range c.logs {
		if strings.Contains(line, "holding a child of a history-locked page") || strings.Contains(line, "holding unresolved-owner") {
			t.Fatalf("ordinary-URL popup was history-held: %q", c.logs)
		}
	}
	if h := c.g.held["S-POP"]; h != nil && (h.historyParent != "" || h.historyUnresolved) {
		t.Fatalf("ordinary-URL popup must not use history hold: %+v", h)
	}
}

func TestHistoryRestoreExplicitDetachAnswersOnce(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.kidAnnounced("K", "iframe", "https://third.test/embed", true)
	pv := c.g.fromCommand(&command{id: 902, method: "Target.attachToTarget", params: map[string]any{"targetId": "K", "flatten": true}}, 902, true)
	c.outbound(pv.send)
	c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-K", "waitingForDebugger": false, "targetInfo": c.kids["K"]}))
	c.client = nil
	c.feed(hrReply(902, "", map[string]any{"sessionId": "S-K"}))
	if c.g.held["S-K"] == nil || !c.g.held["S-K"].hasReply {
		t.Fatalf("explicit reply not parked: %+v", c.g.held["S-K"])
	}
	c.client = nil
	c.feed(hrEvent(hrClient, "Target.detachedFromTarget", map[string]any{"sessionId": "S-K", "targetId": "K"}))
	replies := 0
	for _, m := range c.client {
		if strings.Contains(m, `"id":902`) {
			replies++
			if !strings.Contains(m, refusedTarget) {
				t.Fatalf("detach-while-held answer was not refusal: %s", m)
			}
		}
	}
	if replies != 1 {
		t.Fatalf("explicit detach-while-held answered %d times: %q", replies, c.client)
	}
	c.client = nil
	c.answerHeld(false)
	for _, m := range c.client {
		if strings.Contains(m, `"id":902`) {
			t.Fatalf("second answer after confirm: %q", c.client)
		}
	}
}

func TestHistoryRestoreParentSessionLost(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.kidAnnounced("K", "iframe", "https://third.test/embed", true)
	c.kidAttached("K")
	c.feed(hrEvent("RELAY", "Target.detachedFromTarget", map[string]any{"sessionId": hrNav, "targetId": hrTarget}))
	c.client = nil
	c.answerHeld(false)
	if c.attachedForwarded("K") {
		t.Fatalf("delivered after parent guard lost: %q", c.client)
	}
}

func TestHistoryRestoreProtectedDifferentTwin(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = false
	c.g.targets[hrTarget].protected = true
	c.kidAnnounced("K", "iframe", "https://third.test/embed", true)
	c.client = nil
	c.kidAttached("K")
	if c.attachedForwarded("K") {
		t.Fatalf("delivered under protected parent: %q", c.client)
	}
}

func TestHistoryRestoreSameValue(t *testing.T) {
	for i := 0; i < 3; i++ {
		c := hrLockedTab(t)
		c.hold = false
		c.kidAnnounced("K", "iframe", "https://third.test/embed", true)
		c.client = nil
		c.kidAttached("K")
		c.answerHeld(false)
		if !c.attachedForwarded("K") {
			t.Fatalf("round %d not delivered", i)
		}
	}
}

func TestHistoryRestoreHoldCountLog(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = false
	c.kidAnnounced("K", "iframe", "https://third.test/embed", true)
	c.kidAttached("K")
	found := false
	for _, line := range c.logs {
		if strings.Contains(line, "holding a child of a history-locked page") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing hold log: %q", c.logs)
	}
	_ = os.Getenv(HoldTimeoutEnv)
}

func (c *hrChrome) clientListOOPIF(id string) (listed bool, url, title, parentID, parentFrameID, typ string, created, changed, attached bool, attachVerdict string) {
	c.t.Helper()
	c.next++
	listID := c.next
	pv := c.g.fromCommand(&command{id: listID, method: "Target.getTargets", params: map[string]any{}}, listID, true)
	c.outbound(pv.send)
	rawInfos := []any{c.info()}
	for _, k := range c.kids {
		rawInfos = append(rawInfos, k)
	}
	c.client = nil
	c.feed(hrReply(listID, "", map[string]any{"targetInfos": rawInfos}))
	for _, m := range c.client {
		if !strings.Contains(m, fmt.Sprintf(`"id":%d`, listID)) {
			continue
		}
		var reply struct {
			Result struct {
				TargetInfos []struct {
					TargetID      string `json:"targetId"`
					Type          string `json:"type"`
					URL           string `json:"url"`
					Title         string `json:"title"`
					ParentID      string `json:"parentId"`
					ParentFrameID string `json:"parentFrameId"`
				} `json:"targetInfos"`
			} `json:"result"`
		}
		_ = json.Unmarshal([]byte(m), &reply)
		for _, info := range reply.Result.TargetInfos {
			if info.TargetID == id {
				listed, typ, url, title, parentID, parentFrameID = true, info.Type, info.URL, info.Title, info.ParentID, info.ParentFrameID
			}
		}
	}
	for _, m := range c.client {
		_ = m
	}
	created = strings.Contains(strings.Join(c.client, "\n"), `"method":"Target.targetCreated"`) && strings.Contains(strings.Join(c.client, "\n"), `"targetId":"`+id+`"`)
	c.next++
	attachID := c.next
	c.client = nil
	apv := c.g.fromCommand(&command{id: attachID, method: "Target.attachToTarget", params: map[string]any{"targetId": id, "flatten": true}}, attachID, true)
	if apv.reply != nil {
		attachVerdict = "refused:" + string(apv.reply)
	} else {
		c.outbound(apv.send)
		c.feed(hrEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-" + id, "waitingForDebugger": false, "targetInfo": c.kids[id]}))
		c.feed(hrReply(attachID, "", map[string]any{"sessionId": "S-" + id}))
		if c.g.held["S-"+id] != nil {
			attachVerdict = "held"
		} else if c.g.hidden["S-"+id] || c.g.refused["S-"+id] {
			attachVerdict = "hidden/refused"
		} else if c.g.sessions["S-"+id] != nil && !c.g.sessions["S-"+id].refused {
			attachVerdict = "session"
		} else {
			attachVerdict = "other"
		}
	}
	return
}

func TestHistoryRestoreGuardedProtectedOOPIFListing(t *testing.T) {
	c := hrLockedTab(t)
	c.hold = true
	c.client = nil
	c.logs = nil
	frameURL := "https://frame.other.test/inner"
	c.kidAnnounced("OOPIF", "iframe", frameURL, true)
	createdBefore := false
	for _, m := range c.client {
		if strings.Contains(m, `"method":"Target.targetCreated"`) && strings.Contains(m, `"targetId":"OOPIF"`) {
			createdBefore = true
		}
	}
	c.client = nil
	c.kidAttached("OOPIF")
	attachedBefore := c.attachedForwarded("OOPIF")
	heldBefore := c.g.held["S-OOPIF"] != nil
	listed, url, title, parentID, parentFrameID, typ, _, _, _, attachV := c.clientListOOPIF("OOPIF")
	t.Logf("phase=locked-ordinary-before-restore created=%t attachedFwd=%t held=%t listed=%t type=%q url=%q title=%q parentId=%q parentFrameId=%q attach=%s", createdBefore, attachedBefore, heldBefore, listed, typ, url, title, parentID, parentFrameID, attachV)

	c.client = nil
	c.guardRestore(hrSeven)
	listedMid, urlMid, titleMid, parentIDMid, parentFrameIDMid, typMid, _, _, _, attachMid := c.clientListOOPIF("OOPIF")
	t.Logf("phase=mid-restore-guard-commit listed=%t type=%q url=%q title=%q parentId=%q parentFrameId=%q attach=%s held=%t", listedMid, typMid, urlMid, titleMid, parentIDMid, parentFrameIDMid, attachMid, c.g.held["S-OOPIF"] != nil)
	c.clientRestore(hrSeven, "https://seven.sliccy.ai", "L9")
	c.infoChanged(hrSeven, "seven")
	c.answerHeld(false)
	c.client = nil
	c.logs = nil
	if c.kids["OOPIF"] == nil {
		c.kidAnnounced("OOPIF", "iframe", frameURL, true)
	}
	createdAfter := false
	changedAfter := false
	for _, m := range c.client {
		if strings.Contains(m, `"targetId":"OOPIF"`) {
			if strings.Contains(m, `"method":"Target.targetCreated"`) {
				createdAfter = true
			}
			if strings.Contains(m, `"method":"Target.targetInfoChanged"`) {
				changedAfter = true
			}
		}
	}
	c.client = nil
	if c.g.held["S-OOPIF"] == nil && c.g.sessions["S-OOPIF"] == nil && !c.g.hidden["S-OOPIF"] && !c.g.refused["S-OOPIF"] {
		c.kidAttached("OOPIF")
	}
	attachedAfter := c.attachedForwarded("OOPIF")
	listed2, url2, title2, parentID2, parentFrameID2, typ2, _, _, _, attachV2 := c.clientListOOPIF("OOPIF")
	t.Logf("phase=restored-to-protected created=%t changed=%t attachedFwd=%t listed=%t type=%q url=%q title=%q parentId=%q parentFrameId=%q attach=%s", createdAfter, changedAfter, attachedAfter, listed2, typ2, url2, title2, parentID2, parentFrameID2, attachV2)
	if listed2 && (url2 != "" || title2 != "") {
		t.Fatalf("guarded client listed content-bearing OOPIF after restore to protected: listed=%t url=%q title=%q", listed2, url2, title2)
	}
	if attachV2 == "session" {
		t.Fatalf("guarded client got attachable OOPIF session after restore to protected: %s", attachV2)
	}
}
