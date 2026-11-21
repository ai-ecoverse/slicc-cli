package cdp

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const diagE7TraceEnv = "SLICC_TEST_DIAGE7_TRACE"

var diagE7On atomic.Bool

func init() {
	diagE7On.Store(os.Getenv(diagE7TraceEnv) != "")
}

func diagE7Enabled() bool {
	return diagE7On.Load() || os.Getenv(diagE7TraceEnv) != ""
}

func wrapDiagE7Logf(base func(string, ...any)) func(string, ...any) {
	if base == nil || !diagE7Enabled() {
		return base
	}
	return func(format string, args ...any) {
		base("DIAGE7 "+format, args...)
	}
}

func diagE7WireInteresting(method string) bool {
	switch method {
	case "Page.getFrameTree",
		"Page.createIsolatedWorld",
		"Page.addScriptToEvaluateOnNewDocument",
		"Runtime.executionContextCreated",
		"Runtime.evaluate",
		"Runtime.runIfWaitingForDebugger",
		"Target.getTargetInfo",
		"Target.attachedToTarget",
		"Target.detachedFromTarget",
		"Page.frameNavigated",
		"Page.frameStartedNavigating",
		"Page.lifecycleEvent":
		return true
	default:
		return false
	}
}

func diagE7Str(v any) string {
	s, _ := v.(string)
	return s
}

func diagE7ID(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case int64:
		return strconv.FormatInt(n, 10)
	case int:
		return strconv.Itoa(n)
	case json.Number:
		return n.String()
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

type diagE7Msg struct {
	ID        any             `json:"id"`
	Method    string          `json:"method"`
	SessionID string          `json:"sessionId"`
	Params    json.RawMessage `json:"params"`
	Error     json.RawMessage `json:"error"`
	Result    json.RawMessage `json:"result"`
}

func diagE7ErrorText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		return e.Message
	}
	return string(raw)
}

func diagE7ParamParts(p map[string]any) []string {
	parts := make([]string, 0, 12)
	if frameID, _ := p["frameId"].(string); frameID != "" {
		parts = append(parts, "frameId="+frameID)
	}
	if world, _ := p["worldName"].(string); world != "" {
		parts = append(parts, "worldName="+world)
	}
	if expr, _ := p["expression"].(string); expr != "" {
		if len(expr) > 80 {
			expr = expr[:77] + "..."
		}
		parts = append(parts, "expression="+strconv.Quote(expr))
	}
	for _, key := range []string{"contextId", "executionContextId", "objectId", "uniqueContextId"} {
		if v, ok := p[key]; ok {
			parts = append(parts, key+"="+diagE7ID(v))
		}
	}
	parts = append(parts, diagE7ContextParts(p)...)
	return parts
}

func diagE7ContextParts(p map[string]any) []string {
	ctx, _ := p["context"].(map[string]any)
	if ctx == nil {
		return nil
	}
	parts := []string{"contextId=" + diagE7ID(ctx["id"]), "origin=" + diagE7Str(ctx["origin"]), "uniqueId=" + diagE7Str(ctx["uniqueId"])}
	if aux, _ := ctx["auxData"].(map[string]any); aux != nil {
		parts = append(parts, "frameId="+diagE7Str(aux["frameId"]), "isDefault="+diagE7ID(aux["isDefault"]))
		if name, _ := aux["type"].(string); name != "" {
			parts = append(parts, "auxType="+name)
		}
	}
	if name, _ := ctx["name"].(string); name != "" {
		parts = append(parts, "worldName="+name)
	}
	return parts
}

func diagE7ResultParts(raw json.RawMessage) []string {
	var r map[string]any
	if json.Unmarshal(raw, &r) != nil {
		return nil
	}
	parts := make([]string, 0, 8)
	if tree, _ := r["frameTree"].(map[string]any); tree != nil {
		if frame, _ := tree["frame"].(map[string]any); frame != nil {
			parts = append(parts, "frameTree.frame.id="+diagE7Str(frame["id"]), "frameTree.frame.url="+diagE7Str(frame["url"]))
		} else {
			parts = append(parts, "frameTree.frame=absent")
		}
	}
	if ecid, ok := r["executionContextId"]; ok {
		parts = append(parts, "executionContextId="+diagE7ID(ecid))
	}
	if info, _ := r["targetInfo"].(map[string]any); info != nil {
		parts = append(parts, "target="+diagE7Str(info["targetId"]), "url="+diagE7Str(info["url"]), "type="+diagE7Str(info["type"]))
	}
	return parts
}

func diagE7Summarize(data []byte) (method, session, detail string, id string, errText string, hasResult bool) {
	var m diagE7Msg
	if json.Unmarshal(data, &m) != nil {
		return "", "", "", "", "", false
	}
	method = m.Method
	session = m.SessionID
	if m.ID != nil {
		id = diagE7ID(m.ID)
	}
	hasResult = len(m.Result) > 0 && string(m.Result) != "null"
	errText = diagE7ErrorText(m.Error)
	if method == "" {
		if id == "" {
			return "", session, "", id, errText, hasResult
		}
		method = "(reply)"
	}
	if method != "(reply)" && !diagE7WireInteresting(method) {
		return method, session, "", id, errText, hasResult
	}
	parts := make([]string, 0, 16)
	if id != "" {
		parts = append(parts, "id="+id)
	}
	var p map[string]any
	_ = json.Unmarshal(m.Params, &p)
	parts = append(parts, diagE7ParamParts(p)...)
	if hasResult {
		parts = append(parts, diagE7ResultParts(m.Result)...)
	}
	if errText != "" {
		parts = append(parts, "error="+strconv.Quote(errText))
	}
	return method, session, strings.Join(parts, " "), id, errText, hasResult
}

func (rl *relay) diagE7Wire(dir string, data []byte) {
	if rl == nil || rl.logf == nil || !diagE7Enabled() {
		return
	}
	method, session, detail, id, errText, hasResult := diagE7Summarize(data)
	if method == "" {
		return
	}
	if method != "(reply)" && !diagE7WireInteresting(method) {
		return
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	kind := "event"
	if id != "" {
		kind = "command"
		if method == "(reply)" || hasResult || errText != "" {
			kind = "reply"
		}
	}
	if detail == "" {
		rl.logf("wire %s t=%s kind=%s method=%s session=%s", dir, ts, kind, method, session)
		return
	}
	rl.logf("wire %s t=%s kind=%s method=%s session=%s %s", dir, ts, kind, method, session, detail)
}

func (g *guard) diagE7Disposition(disposition, method, session string, id int64, extra string) {
	if g == nil || g.logf == nil || !diagE7Enabled() {
		return
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	if extra == "" {
		g.logf("disposition=%s t=%s method=%s session=%s id=%d", disposition, ts, method, session, id)
		return
	}
	g.logf("disposition=%s t=%s method=%s session=%s id=%d %s", disposition, ts, method, session, id, extra)
}

func diagE7ReplyShape(data []byte) string {
	method, _, detail, _, errText, hasResult := diagE7Summarize(data)
	parts := []string{}
	if method != "" && method != "(reply)" {
		parts = append(parts, "method="+method)
	}
	if errText != "" {
		parts = append(parts, "error="+strconv.Quote(errText))
	}
	if hasResult {
		parts = append(parts, "hasResult=true")
	}
	if detail != "" {
		parts = append(parts, detail)
	}
	return strings.Join(parts, " ")
}

func (g *guard) diagE7AfterIdentityHold(method string, m *message, k callKey, v browserVerdict) browserVerdict {
	if v.forward == nil {
		g.dropDocIdentityCallLocked(k)
		return v
	}
	send, page, held := g.holdDocIdentityReplyLocked(k, m, v.forward)
	if !held {
		g.diagE7Disposition("FORWARDED", method, m.SessionID, *m.ID, "answerLocked no-identity-hold")
		g.dropDocIdentityCallLocked(k)
		return v
	}
	if len(page) == 0 {
		g.diagE7Disposition("HELD", method, m.SessionID, *m.ID, "docIdentityHold answerLocked")
	}
	v.forward = nil
	v.send = append(v.send, send...)
	v.page = append(v.page, page...)
	return v
}
