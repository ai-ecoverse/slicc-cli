package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	hubMaxRetries   = 3
	hubRetryAfterMs = 10
)

type hubCandidate struct {
	Candidate     string  `json:"candidate"`
	SDPMid        *string `json:"sdpMid,omitempty"`
	SDPMLineIndex *int    `json:"sdpMLineIndex,omitempty"`
}

type hubFailure struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	Retryable    bool   `json:"retryable"`
	RetryAfterMs *int   `json:"retryAfterMs"`
	FailedAt     string `json:"failedAt"`
}

type hubEvent struct {
	Sequence  int               `json:"sequence"`
	SentAt    string            `json:"sentAt"`
	Type      string            `json:"type"`
	Offer     map[string]string `json:"offer,omitempty"`
	Candidate *hubCandidate     `json:"candidate,omitempty"`
	Failure   *hubFailure       `json:"failure,omitempty"`
}

type hubBootstrap struct {
	ID           string
	ControllerID string
	Runtime      string
	RetryCount   int
	State        string
	Failure      *hubFailure
	Events       []hubEvent
	NextSeq      int
}

type hubAnswer struct {
	ControllerID string
	BootstrapID  string
	SDP          string
}

type hubSentCandidate struct {
	ControllerID string
	BootstrapID  string
	Candidate    hubCandidate
}

type hubRequest struct {
	Token  string
	Action string
	Body   map[string]any
}

type fakeTray struct {
	Token            string
	TrayID           string
	WaitAttaches     int
	NoLeader         bool
	SupersededBy     string
	SupersedeMissing bool
	Expired          bool
	OfferSDP         string
	LeaderCandidates []hubCandidate
	FailBootstraps   int
	FailRetryable    bool
	FailCode         string
	IceServers       []map[string]any
	SupersedeOnPoll  string
	LegacySupersede  bool
	LinkOnly         bool
	OnJoin           func(b *hubBootstrap)
	OnFollower       func(action string, b *hubBootstrap, body map[string]any)

	bootstraps []*hubBootstrap
	answers    []hubAnswer
	candidates []hubSentCandidate
	attaches   int
}

type fakeHub struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	trays    map[string]*fakeTray
	requests []hubRequest
	seq      int
	hang     bool
	garbage  string
	flaky    int
}

func newFakeHub(t *testing.T) *fakeHub {
	t.Helper()
	h := &fakeHub{t: t, trays: map[string]*fakeTray{}}
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *fakeHub) addTray(name string) *fakeTray {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	tray := &fakeTray{
		Token:         fmt.Sprintf("tray%s%d.%040x", name, h.seq, 0xc0ffee00+h.seq),
		TrayID:        fmt.Sprintf("tray%s%d", name, h.seq),
		OfferSDP:      testOfferSDP,
		FailRetryable: true,
		FailCode:      "BOOTSTRAP_TIMEOUT",
		IceServers: []map[string]any{
			{"urls": []string{"stun:stun.example.test:3478"}, "username": "", "credential": ""},
			{"urls": []string{"turn:turn.example.test:3478?transport=udp", "turns:turn.example.test:5349?transport=tcp"}, "username": "turn-user-secret", "credential": "turn-credential-secret"},
		},
	}
	h.trays[tray.Token] = tray
	return tray
}

func (h *fakeHub) joinURL(tray *fakeTray) string {
	return h.srv.URL + "/join/" + tray.Token
}

func (h *fakeHub) with(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fn()
}

func (h *fakeHub) requestLog() []hubRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]hubRequest(nil), h.requests...)
}

func (h *fakeHub) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeHubJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "Method not allowed"})
		return
	}
	token, ok := strings.CutPrefix(r.URL.Path, "/join/")
	if !ok || strings.Contains(token, "/") {
		writeHubJSON(w, http.StatusNotFound, map[string]any{"error": "Not found", "code": "NOT_FOUND"})
		return
	}
	var body map[string]any
	if !strings.Contains(r.Header.Get("Content-Type"), "application/json") || json.NewDecoder(r.Body).Decode(&body) != nil {
		body = map[string]any{}
	}
	action, _ := body["action"].(string)
	h.mu.Lock()
	h.requests = append(h.requests, hubRequest{Token: token, Action: action, Body: body})
	hang, garbage := h.hang, h.garbage
	flaky := h.flaky > 0
	if flaky {
		h.flaky--
	}
	h.mu.Unlock()
	if flaky {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("error code: 1101\n"))
		return
	}
	if hang {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		return
	}
	if garbage != "" {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(garbage))
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	status, payload := h.handle(token, action, body)
	if tr := h.trays[token]; tr != nil && tr.SupersededBy != "" {
		h.supersede(w, r, tr, payload)
		return
	}
	writeHubJSON(w, status, payload)
}

func (h *fakeHub) supersede(w http.ResponseWriter, r *http.Request, tr *fakeTray, payload any) {
	w.Header().Add("Link", `<`+h.srv.URL+`/status>; rel="status"; type="application/json"`)
	w.Header().Add("Link", `<`+tr.SupersededBy+`>; rel="successor-version"`)
	status := http.StatusPermanentRedirect
	if tr.LegacySupersede || r.URL.Query().Get("redirect") == "manual" {
		status = http.StatusConflict
	} else {
		w.Header().Set("Location", tr.SupersededBy+"?json=true")
		if m, ok := payload.(map[string]any); ok {
			if res, ok := m["result"].(map[string]any); ok {
				res["action"] = "redirect"
			}
		}
	}
	if tr.LinkOnly {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		_, _ = w.Write([]byte("moved"))
		return
	}
	writeHubJSON(w, status, payload)
}

func (h *fakeHub) handle(token, action string, body map[string]any) (int, any) {
	if !strings.Contains(token, ".") || strings.Count(token, ".") != 1 {
		return http.StatusBadRequest, map[string]any{"error": "Malformed capability token", "code": "MALFORMED_CAPABILITY"}
	}
	controllerID, _ := body["controllerId"].(string)
	if controllerID == "" {
		controllerID = "generated-controller"
	}
	tray := h.trays[token]
	if tray == nil {
		return http.StatusForbidden, h.attachResponse(nil, controllerID, map[string]any{"action": "fail", "code": "INVALID_JOIN_CAPABILITY", "error": "Invalid join capability"}, false)
	}
	if tray.SupersededBy != "" || tray.SupersedeMissing {
		result := map[string]any{"action": "fail", "code": "TRAY_SUPERSEDED", "error": "This session moved to a new tray after the leader reconnected"}
		if tray.SupersededBy != "" {
			result["joinUrl"] = tray.SupersededBy
		}
		return http.StatusConflict, h.attachResponse(tray, controllerID, result, false)
	}
	if tray.Expired {
		return http.StatusGone, h.attachResponse(tray, controllerID, map[string]any{"action": "fail", "code": "TRAY_EXPIRED", "error": "Tray expired because the leader did not reclaim it in time"}, false)
	}
	switch action {
	case "":
		return h.attach(tray, controllerID, body)
	case "poll":
		return h.poll(tray, controllerID, body)
	case "answer":
		return h.answer(tray, controllerID, body)
	case "ice-candidate":
		return h.iceCandidate(tray, controllerID, body)
	case "retry":
		return h.retry(tray, controllerID, body)
	default:
		return http.StatusBadRequest, map[string]any{"error": "Invalid bootstrap request", "code": "INVALID_BOOTSTRAP_REQUEST"}
	}
}

func (h *fakeHub) attach(tray *fakeTray, controllerID string, body map[string]any) (int, any) {
	tray.attaches++
	if tray.NoLeader || tray.WaitAttaches > 0 {
		if tray.WaitAttaches > 0 {
			tray.WaitAttaches--
		}
		code := "LEADER_NOT_CONNECTED"
		if tray.NoLeader {
			code = "LEADER_NOT_ELECTED"
		}
		return http.StatusOK, h.attachResponse(tray, controllerID, map[string]any{"action": "wait", "code": code, "retryAfterMs": hubRetryAfterMs}, false)
	}
	var b *hubBootstrap
	for _, existing := range tray.bootstraps {
		if existing.ControllerID == controllerID && (b == nil || existing.RetryCount >= b.RetryCount) {
			b = existing
		}
	}
	if b == nil {
		runtime, _ := body["runtime"].(string)
		b = h.createBootstrap(tray, controllerID, runtime, 0)
	}
	return http.StatusOK, h.attachResponse(tray, controllerID, map[string]any{"action": "signal", "code": "LEADER_CONNECTED", "bootstrap": bootstrapStatus(b)}, true)
}

func (h *fakeHub) createBootstrap(tray *fakeTray, controllerID, runtime string, retryCount int) *hubBootstrap {
	h.seq++
	b := &hubBootstrap{ID: fmt.Sprintf("bootstrap-%d", h.seq), ControllerID: controllerID, Runtime: runtime, RetryCount: retryCount, State: "pending", NextSeq: 1}
	tray.bootstraps = append(tray.bootstraps, b)
	h.leaderJoinRequested(tray, b)
	return b
}

func (h *fakeHub) leaderJoinRequested(tray *fakeTray, b *hubBootstrap) {
	if tray.FailBootstraps > 0 {
		tray.FailBootstraps--
		retryable := tray.FailRetryable && b.RetryCount < hubMaxRetries
		var after *int
		if retryable {
			v := hubRetryAfterMs
			after = &v
		}
		failure := &hubFailure{Code: tray.FailCode, Message: "Bootstrap attempt timed out after 20000ms", Retryable: retryable, RetryAfterMs: after, FailedAt: hubNow()}
		b.State = "failed"
		b.Failure = failure
		appendEvent(b, hubEvent{Type: "bootstrap.failed", Failure: failure})
		return
	}
	if tray.OnJoin != nil {
		tray.OnJoin(b)
		return
	}
	if tray.OfferSDP == "" {
		return
	}
	appendEvent(b, hubEvent{Type: "bootstrap.offer", Offer: map[string]string{"type": "offer", "sdp": tray.OfferSDP}})
	b.State = "offered"
	for _, c := range tray.LeaderCandidates {
		appendEvent(b, hubEvent{Type: "bootstrap.ice_candidate", Candidate: &c})
	}
}

func appendEvent(b *hubBootstrap, ev hubEvent) {
	ev.Sequence = b.NextSeq
	ev.SentAt = hubNow()
	b.NextSeq++
	b.Events = append(b.Events, ev)
}

func (tray *fakeTray) find(controllerID string, body map[string]any) *hubBootstrap {
	id, _ := body["bootstrapId"].(string)
	for _, b := range tray.bootstraps {
		if b.ID == id && b.ControllerID == controllerID {
			return b
		}
	}
	return nil
}

func (h *fakeHub) poll(tray *fakeTray, controllerID string, body map[string]any) (int, any) {
	if tray.SupersedeOnPoll != "" {
		tray.SupersededBy = tray.SupersedeOnPoll
		return http.StatusConflict, h.attachResponse(tray, controllerID, map[string]any{"action": "fail", "code": "TRAY_SUPERSEDED", "error": "moved", "joinUrl": tray.SupersededBy}, false)
	}
	b := tray.find(controllerID, body)
	if b == nil {
		return http.StatusNotFound, map[string]any{"error": "Bootstrap not found", "code": "BOOTSTRAP_NOT_FOUND"}
	}
	cursor := 0
	if c, ok := body["cursor"].(float64); ok {
		cursor = int(c)
	}
	var events []hubEvent
	for _, ev := range b.Events {
		if ev.Sequence > cursor {
			events = append(events, ev)
		}
	}
	return http.StatusOK, h.bootstrapResponse(tray, b, events)
}

func (h *fakeHub) answer(tray *fakeTray, controllerID string, body map[string]any) (int, any) {
	answer, _ := body["answer"].(map[string]any)
	sdp, _ := answer["sdp"].(string)
	if answer["type"] != "answer" || sdp == "" {
		return http.StatusBadRequest, map[string]any{"error": "A valid bootstrap answer is required", "code": "INVALID_BOOTSTRAP_REQUEST"}
	}
	b := tray.find(controllerID, body)
	if b == nil {
		return http.StatusNotFound, map[string]any{"error": "Bootstrap not found", "code": "BOOTSTRAP_NOT_FOUND"}
	}
	if b.State == "failed" {
		return http.StatusConflict, h.bootstrapResponse(tray, b, nil)
	}
	tray.answers = append(tray.answers, hubAnswer{ControllerID: controllerID, BootstrapID: b.ID, SDP: sdp})
	b.State = "connected"
	if tray.OnFollower != nil {
		tray.OnFollower("answer", b, body)
	}
	return http.StatusOK, h.bootstrapResponse(tray, b, nil)
}

func (h *fakeHub) iceCandidate(tray *fakeTray, controllerID string, body map[string]any) (int, any) {
	raw, _ := json.Marshal(body["candidate"])
	var c hubCandidate
	if json.Unmarshal(raw, &c) != nil || c.Candidate == "" {
		return http.StatusBadRequest, map[string]any{"error": "A valid ICE candidate is required", "code": "INVALID_BOOTSTRAP_REQUEST"}
	}
	b := tray.find(controllerID, body)
	if b == nil {
		return http.StatusNotFound, map[string]any{"error": "Bootstrap not found", "code": "BOOTSTRAP_NOT_FOUND"}
	}
	if b.State == "failed" {
		return http.StatusConflict, h.bootstrapResponse(tray, b, nil)
	}
	tray.candidates = append(tray.candidates, hubSentCandidate{ControllerID: controllerID, BootstrapID: b.ID, Candidate: c})
	if tray.OnFollower != nil {
		tray.OnFollower("ice-candidate", b, body)
	}
	return http.StatusOK, h.bootstrapResponse(tray, b, nil)
}

func (h *fakeHub) retry(tray *fakeTray, controllerID string, body map[string]any) (int, any) {
	b := tray.find(controllerID, body)
	if b == nil {
		return http.StatusNotFound, map[string]any{"error": "Bootstrap not found", "code": "BOOTSTRAP_NOT_FOUND"}
	}
	if b.State != "failed" || b.Failure == nil || !b.Failure.Retryable || b.RetryCount >= hubMaxRetries {
		return http.StatusConflict, h.bootstrapResponse(tray, b, nil)
	}
	runtime, _ := body["runtime"].(string)
	next := h.createBootstrap(tray, controllerID, runtime, b.RetryCount+1)
	return http.StatusOK, h.bootstrapResponse(tray, next, nil)
}

func bootstrapStatus(b *hubBootstrap) map[string]any {
	var after any
	if b.Failure != nil && b.Failure.Retryable {
		after = hubRetryAfterMs
	}
	return map[string]any{
		"controllerId":     b.ControllerID,
		"bootstrapId":      b.ID,
		"attempt":          b.RetryCount + 1,
		"state":            b.State,
		"expiresAt":        time.Now().Add(20 * time.Second).UTC().Format(time.RFC3339Nano),
		"cursor":           b.NextSeq - 1,
		"maxRetries":       hubMaxRetries,
		"retriesRemaining": hubMaxRetries - b.RetryCount,
		"retryAfterMs":     after,
		"failure":          b.Failure,
	}
}

func (h *fakeHub) summary(tray *fakeTray, controllerID string) map[string]any {
	trayID := "unknown"
	if tray != nil {
		trayID = tray.TrayID
	}
	return map[string]any{
		"trayId":           trayID,
		"controllerId":     controllerID,
		"role":             "follower",
		"trust":            "full",
		"leader":           map[string]any{"controllerId": "leader", "connected": true, "reconnectDeadline": nil},
		"participantCount": 2,
	}
}

func (h *fakeHub) attachResponse(tray *fakeTray, controllerID string, result map[string]any, ice bool) map[string]any {
	out := h.summary(tray, controllerID)
	out["result"] = result
	if ice && tray != nil {
		out["iceServers"] = tray.IceServers
	}
	return out
}

func (h *fakeHub) bootstrapResponse(tray *fakeTray, b *hubBootstrap, events []hubEvent) map[string]any {
	out := h.summary(tray, b.ControllerID)
	out["bootstrap"] = bootstrapStatus(b)
	if events == nil {
		events = []hubEvent{}
	}
	out["events"] = events
	out["iceServers"] = tray.IceServers
	return out
}

func writeHubJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func hubNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

const testOfferSDP = "v=0\r\n" +
	"o=- 4611731400430051336 2 IN IP4 127.0.0.1\r\n" +
	"s=-\r\n" +
	"t=0 0\r\n" +
	"a=group:BUNDLE 0\r\n" +
	"a=fingerprint:sha-256 AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89\r\n" +
	"m=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\n" +
	"c=IN IP4 0.0.0.0\r\n" +
	"a=ice-ufrag:ufragsecret\r\n" +
	"a=ice-pwd:icepasswordsecretvalue0000\r\n" +
	"a=candidate:1 1 udp 2122260223 192.0.2.10 54321 typ host\r\n" +
	"a=mid:0\r\n" +
	"a=sctp-port:5000\r\n"
