package cdp

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const diagC3TraceEnv = "SLICC_TEST_DIAGC3_TRACE"

var diagC3On atomic.Bool

func init() {
	diagC3On.Store(os.Getenv(diagC3TraceEnv) != "")
}

func diagC3Enabled() bool {
	return diagC3On.Load() || os.Getenv(diagC3TraceEnv) != ""
}

func wrapDiagC3Logf(base func(string, ...any)) func(string, ...any) {
	if base == nil || !diagC3Enabled() {
		return base
	}
	return func(format string, args ...any) {
		base("DIAGC3 "+format, args...)
	}
}

func diagC3WireInteresting(method string) bool {
	switch {
	case method == "":
		return false
	case strings.HasPrefix(method, "Target."):
		return true
	case method == "Runtime.runIfWaitingForDebugger":
		return true
	case strings.HasPrefix(method, "Fetch."):
		return true
	case method == "Page.navigate" || method == "Page.frameNavigated" || method == "Page.lifecycleEvent":
		return true
	default:
		return false
	}
}

func diagC3Summarize(data []byte) (method, session, detail string) {
	var m struct {
		ID        any             `json:"id"`
		Method    string          `json:"method"`
		SessionID string          `json:"sessionId"`
		Params    json.RawMessage `json:"params"`
		Error     json.RawMessage `json:"error"`
		Result    json.RawMessage `json:"result"`
	}
	if json.Unmarshal(data, &m) != nil {
		return "", "", ""
	}
	method = m.Method
	session = m.SessionID
	if method == "" || !diagC3WireInteresting(method) {
		return method, session, ""
	}
	var p map[string]any
	_ = json.Unmarshal(m.Params, &p)
	parts := make([]string, 0, 8)
	if m.ID != nil {
		parts = append(parts, "id="+diagC3ID(m.ID))
	}
	if info, _ := p["targetInfo"].(map[string]any); info != nil {
		parts = append(parts, "type="+strAny(info["type"]), "target="+strAny(info["targetId"]), "url="+shortURL(strAny(info["url"])), "opener="+strAny(info["openerId"]))
	}
	if sid, _ := p["sessionId"].(string); sid != "" && sid != session {
		parts = append(parts, "child="+sid)
	}
	if waiting, ok := p["waitingForDebugger"].(bool); ok {
		parts = append(parts, "waiting="+strconv.FormatBool(waiting))
	}
	if req, _ := p["request"].(map[string]any); req != nil {
		parts = append(parts, "req="+shortURL(strAny(req["url"])))
	}
	if u, _ := p["url"].(string); u != "" {
		parts = append(parts, "url="+shortURL(u))
	}
	if rid, _ := p["requestId"].(string); rid != "" {
		parts = append(parts, "requestId="+rid)
	}
	if rt, _ := p["resourceType"].(string); rt != "" {
		parts = append(parts, "resourceType="+rt)
	}
	if frame, _ := p["frame"].(map[string]any); frame != nil {
		parts = append(parts, "frameUrl="+shortURL(strAny(frame["url"])), "frameId="+strAny(frame["id"]))
	}
	if tid, _ := p["targetId"].(string); tid != "" {
		parts = append(parts, "target="+tid)
	}
	if auto, ok := p["autoAttach"].(bool); ok {
		parts = append(parts, "autoAttach="+strconv.FormatBool(auto))
	}
	if wait, ok := p["waitForDebuggerOnStart"].(bool); ok {
		parts = append(parts, "waitForDebuggerOnStart="+strconv.FormatBool(wait))
	}
	if flat, ok := p["flatten"].(bool); ok {
		parts = append(parts, "flatten="+strconv.FormatBool(flat))
	}
	return method, session, strings.Join(parts, " ")
}

func (rl *relay) diagC3Wire(dir string, data []byte) {
	if rl == nil || rl.logf == nil || !diagC3Enabled() {
		return
	}
	method, session, detail := diagC3Summarize(data)
	if method == "" || !diagC3WireInteresting(method) {
		return
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	if detail == "" {
		rl.logf("wire %s t=%s method=%s session=%s", dir, ts, method, session)
		return
	}
	rl.logf("wire %s t=%s method=%s session=%s %s", dir, ts, method, session, detail)
}

func strAny(v any) string {
	s, _ := v.(string)
	return s
}

func diagC3ID(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case json.Number:
		return n.String()
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func shortURL(raw string) string {
	if raw == "" {
		return ""
	}
	if len(raw) <= 160 {
		return raw
	}
	return raw[:157] + "..."
}

func attachURLKind(raw string) string {
	switch {
	case raw == "":
		return "empty"
	case strings.HasPrefix(raw, "about:"):
		return "about"
	case strings.HasPrefix(raw, "chrome-error:"):
		return "chrome-error"
	case strings.HasPrefix(raw, "http:") || strings.HasPrefix(raw, "https:"):
		return "http"
	default:
		return "other"
	}
}
