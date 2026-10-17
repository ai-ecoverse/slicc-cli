package integration

import (
	"crypto/x509"
	"encoding/base64"
	wire "encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/caps/ws"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
	"github.com/gorilla/websocket"
)

const (
	wsSecretQuery   = "q-0451"
	wsSecretToken   = "Bearer tok-0451"
	wsSecretPayload = "payload-0451"
	wsFancyText     = "héllo wörld ✓ "
)

type wsUpstream struct {
	srv      *httptest.Server
	tls      *httptest.Server
	mu       sync.Mutex
	outcomes map[string][]string
	headers  map[string]http.Header
	hits     map[string]int
}

type wsRecord struct {
	Type   int    `json:"type"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
	Text   string `json:"text"`
	Code   int    `json:"code"`
	Reason string `json:"reason"`
}

type wsResult struct {
	Opened struct {
		Protocol *string `json:"protocol"`
	} `json:"opened"`
	Shared    bool       `json:"shared"`
	Records   []wsRecord `json:"records"`
	Ended     bool       `json:"ended"`
	Truncated bool       `json:"truncated"`
	Leftover  int        `json:"leftover"`
	TimedOut  bool       `json:"timedOut"`
	StepError string     `json:"stepError"`
	Reset     string     `json:"reset"`
	Detail    string     `json:"detail"`
	Error     string     `json:"error"`
}

func newWSUpstream(t *testing.T) *wsUpstream {
	t.Helper()
	u := &wsUpstream{outcomes: map[string][]string{}, headers: map[string]http.Header{}, hits: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/echo", u.echo)
	mux.HandleFunc("/headers", u.echo)
	mux.HandleFunc("/close", u.closeFirst)
	mux.HandleFunc("/drop", u.drop)
	mux.HandleFunc("/send", u.send)
	mux.HandleFunc("/ping", u.ping)
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		u.hit(r)
		code, _ := strconv.Atoi(r.URL.Query().Get("code"))
		http.Error(w, "no", code)
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		u.hit(r)
		http.Redirect(w, r, "/target", http.StatusFound)
	})
	mux.HandleFunc("/target", u.echo)
	u.srv = httptest.NewServer(mux)
	u.tls = httptest.NewTLSServer(mux)
	t.Cleanup(u.srv.Close)
	t.Cleanup(u.tls.Close)
	return u
}

func (u *wsUpstream) url(path string) string {
	return "ws" + strings.TrimPrefix(u.srv.URL, "http") + path
}

func (u *wsUpstream) hit(r *http.Request) string {
	tag := r.URL.Query().Get("tag")
	u.mu.Lock()
	defer u.mu.Unlock()
	u.hits[r.URL.Path]++
	u.headers[tag] = r.Header.Clone()
	return tag
}

func (u *wsUpstream) note(tag, outcome string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.outcomes[tag] = append(u.outcomes[tag], outcome)
}

func (u *wsUpstream) waitOutcome(t *testing.T, tag, want string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		u.mu.Lock()
		got := slices.Clone(u.outcomes[tag])
		u.mu.Unlock()
		if slices.Contains(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("upstream %s: outcomes %q, want %q", tag, got, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (u *wsUpstream) outcomesOf(tag string) []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.outcomes[tag])
}

func (u *wsUpstream) header(tag string) http.Header {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.headers[tag]
}

func (u *wsUpstream) hitCount(path string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.hits[path]
}

func (u *wsUpstream) upgrade(w http.ResponseWriter, r *http.Request, buffer int) (*websocket.Conn, string) {
	tag := u.hit(r)
	up := websocket.Upgrader{Subprotocols: []string{"chat.v2", "chat.v1"}, WriteBufferSize: buffer, CheckOrigin: func(*http.Request) bool { return true }}
	c, err := up.Upgrade(w, r, nil)
	if err != nil {
		u.note(tag, "upgrade failed")
		return nil, tag
	}
	c.SetCloseHandler(func(code int, text string) error {
		u.note(tag, fmt.Sprintf("close %d %s", code, text))
		_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, "echo:"+text), time.Now().Add(time.Second))
		return nil
	})
	return c, tag
}

func (u *wsUpstream) readOut(c *websocket.Conn, tag string, echo bool) {
	defer func() { _ = c.Close() }()
	for {
		t, msg, err := c.ReadMessage()
		if err != nil {
			var ce *websocket.CloseError
			if !errors.As(err, &ce) || ce.Code == websocket.CloseAbnormalClosure {
				u.note(tag, "dropped")
			}
			return
		}
		if t == websocket.TextMessage {
			u.note(tag, "text "+string(msg))
		}
		if echo {
			if err := c.WriteMessage(t, msg); err != nil {
				return
			}
		}
	}
}

func (u *wsUpstream) echo(w http.ResponseWriter, r *http.Request) {
	c, tag := u.upgrade(w, r, 0)
	if c != nil {
		u.readOut(c, tag, true)
	}
}

func (u *wsUpstream) closeFirst(w http.ResponseWriter, r *http.Request) {
	c, tag := u.upgrade(w, r, 0)
	if c == nil {
		return
	}
	c.SetCloseHandler(func(code int, text string) error {
		u.note(tag, fmt.Sprintf("reply %d %s", code, text))
		return nil
	})
	code, _ := strconv.Atoi(r.URL.Query().Get("code"))
	_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, r.URL.Query().Get("reason")), time.Now().Add(time.Second))
	u.readOut(c, tag, false)
}

func (u *wsUpstream) drop(w http.ResponseWriter, r *http.Request) {
	c, _ := u.upgrade(w, r, 0)
	if c == nil {
		return
	}
	_ = c.WriteMessage(websocket.TextMessage, []byte("bye"))
	time.Sleep(200 * time.Millisecond)
	_ = c.NetConn().Close()
}

func xorshiftBytes(size int, seed uint32) []byte {
	out := make([]byte, (size+3)/4*4)
	x := seed
	for i := 0; i < len(out); i += 4 {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		wire.LittleEndian.PutUint32(out[i:], x)
	}
	return out[:size]
}

func (u *wsUpstream) send(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fragments := q.Get("mode") == "fragments"
	buffer := 0
	if fragments {
		buffer = 1024
	}
	c, tag := u.upgrade(w, r, buffer)
	if c == nil {
		return
	}
	t, msg := websocket.BinaryMessage, []byte(nil)
	if q.Get("text") == "1" {
		t, msg = websocket.TextMessage, []byte(strings.Repeat(wsFancyText, 3000))
	} else {
		size, _ := strconv.Atoi(q.Get("size"))
		msg = xorshiftBytes(size, 7)
	}
	go u.readOut(c, tag, false)
	if !fragments {
		_ = c.WriteMessage(t, msg)
		return
	}
	mw, err := c.NextWriter(t)
	if err != nil {
		return
	}
	for at := 0; at < len(msg); at += 333 {
		_, _ = mw.Write(msg[at:min(len(msg), at+333)])
	}
	_ = mw.Close()
}

func (u *wsUpstream) ping(w http.ResponseWriter, r *http.Request) {
	c, tag := u.upgrade(w, r, 0)
	if c == nil {
		return
	}
	pong := make(chan string, 1)
	c.SetPongHandler(func(data string) error {
		select {
		case pong <- data:
		default:
		}
		return nil
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		u.readOut(c, tag, false)
	}()
	_ = c.WriteControl(websocket.PingMessage, []byte("are-you-there"), time.Now().Add(time.Second))
	select {
	case data := <-pong:
		_ = c.WriteMessage(websocket.TextMessage, []byte("pong:"+data))
	case <-time.After(10 * time.Second):
		_ = c.WriteMessage(websocket.TextMessage, []byte("no pong"))
	}
	_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1000, "done"), time.Now().Add(time.Second))
	<-done
}

func (e *linkEnv) ws(t *testing.T, opts map[string]any) wsResult {
	t.Helper()
	var r wsResult
	e.p.eval("linkTest.ws.run("+jsArgs(opts)+")", &r)
	if r.Error != "" || r.TimedOut || r.StepError != "" {
		t.Fatalf("ws %v: %+v", opts["open"], r)
	}
	return r
}

func wsOpen(url string) map[string]any {
	return map[string]any{"url": url, "protocols": []string{}, "headers": [][2]string{}}
}

func wsTypes(r wsResult) []int {
	out := make([]int, len(r.Records))
	for i, rec := range r.Records {
		out[i] = rec.Type
	}
	return out
}

func wsRaw(records ...[]byte) string {
	var all []byte
	for _, r := range records {
		all = append(all, r...)
	}
	return base64.StdEncoding.EncodeToString(all)
}

func TestLinkWebSockets(t *testing.T) {
	env := setupLink(t, false)
	up := newWSUpstream(t)
	t.Run("hello caps", func(t *testing.T) {
		var h struct {
			Caps []string `json:"caps"`
		}
		env.p.eval("linkTest.ready()", &h)
		if !slices.Contains(h.Caps, "ws") {
			t.Fatalf("caps %v lack ws", h.Caps)
		}
	})
	t.Run("text and binary", func(t *testing.T) { env.testWSEcho(t, up) })
	t.Run("record boundaries", func(t *testing.T) { env.testWSBoundaries(t, up) })
	t.Run("large messages", func(t *testing.T) { env.testWSLarge(t, up) })
	t.Run("fragmented messages", func(t *testing.T) { env.testWSFragments(t, up) })
	t.Run("the 64 MiB limit", func(t *testing.T) { env.testWSLimit(t, up) })
	t.Run("unknown and malformed records", func(t *testing.T) { env.testWSProtocolErrors(t, up) })
	t.Run("page closes first", func(t *testing.T) { env.testWSPageClose(t, up) })
	t.Run("far end closes first", func(t *testing.T) { env.testWSFarClose(t, up) })
	t.Run("abrupt drops", func(t *testing.T) { env.testWSDrops(t, up) })
	t.Run("pings stay on the node", func(t *testing.T) { env.testWSPing(t, up) })
	t.Run("handshake headers", func(t *testing.T) { env.testWSHeaders(t, up) })
	t.Run("handshake refusals", func(t *testing.T) { env.testWSRefusals(t, up) })
	t.Run("bad OPENs", func(t *testing.T) { env.testWSBadOpen(t, up) })
	t.Run("nothing private is logged", func(t *testing.T) { env.testWSPrivacy(t, up) })
}

func (e *linkEnv) testWSEcho(t *testing.T, up *wsUpstream) {
	for _, shared := range []bool{false, true} {
		tag := "echo-" + strconv.FormatBool(shared)
		r := e.ws(t, map[string]any{"open": wsOpen(up.url("/echo?tag=" + tag)), "shared": shared, "steps": []map[string]any{
			{"send": "text", "text": wsFancyText},
			{"send": "binary", "size": 1000, "seed": 5},
			{"send": "text", "text": ""},
			{"send": "binary", "size": 0},
			{"wait": 4},
			{"close": 1000, "reason": "done"},
			{"end": true},
		}})
		if r.Shared != shared || r.Opened.Protocol == nil || *r.Opened.Protocol != "" || !r.Ended || r.Reset != "" {
			t.Fatalf("shared=%v: %+v", shared, r)
		}
		want := []int{1, 2, 1, 2, 8}
		if !slices.Equal(wsTypes(r), want) {
			t.Fatalf("shared=%v: records %+v", shared, r.Records)
		}
		if r.Records[0].Text != wsFancyText || r.Records[1].Bytes != 1000 || r.Records[1].SHA256 != xorshiftSum(1000, 5) || r.Records[2].Bytes != 0 || r.Records[3].Bytes != 0 {
			t.Fatalf("shared=%v: records %+v", shared, r.Records)
		}
		if c := r.Records[4]; c.Code != 1000 || c.Reason != "echo:done" {
			t.Fatalf("shared=%v: close %+v", shared, c)
		}
		up.waitOutcome(t, tag, "close 1000 done")
	}
}

func (e *linkEnv) testWSBoundaries(t *testing.T, up *wsUpstream) {
	two := wsRaw(ws.Record(ws.RecordText, []byte("one")), ws.Record(ws.RecordText, []byte("two")), ws.Record(ws.RecordBinary, []byte{}))
	r := e.ws(t, map[string]any{"open": wsOpen(up.url("/echo?tag=bounds")), "steps": []map[string]any{
		{"raw": two},
		{"send": "text", "text": "split-across-frames", "pieces": 3, "pause": 2},
		{"wait": 4},
		{"close": 1000},
		{"end": true},
	}})
	if !slices.Equal(wsTypes(r), []int{1, 1, 2, 1, 8}) || r.Records[0].Text != "one" || r.Records[1].Text != "two" || r.Records[3].Text != "split-across-frames" {
		t.Fatalf("records %+v", r.Records)
	}
}

func (e *linkEnv) testWSLarge(t *testing.T, up *wsUpstream) {
	r := e.ws(t, map[string]any{"open": wsOpen(up.url("/echo?tag=large")), "steps": []map[string]any{
		{"send": "binary", "size": 16 << 20, "seed": 3},
		{"wait": 1},
		{"close": 1000},
		{"end": true},
	}})
	if len(r.Records) != 2 || r.Records[0].Bytes != 16<<20 || r.Records[0].SHA256 != xorshiftSum(16<<20, 3) {
		t.Fatalf("records %+v", r.Records)
	}
	r = e.ws(t, map[string]any{"open": wsOpen(up.url("/send?tag=at-limit&size=" + strconv.Itoa(ws.MaxMessage))), "steps": []map[string]any{
		{"wait": 1},
		{"close": 1000},
		{"end": true},
	}})
	if len(r.Records) != 2 || r.Records[0].Bytes != ws.MaxMessage || r.Records[0].SHA256 != xorshiftSum(ws.MaxMessage, 7) {
		t.Fatalf("records %+v", r.Records)
	}
}

func (e *linkEnv) testWSFragments(t *testing.T, up *wsUpstream) {
	r := e.ws(t, map[string]any{"open": wsOpen(up.url("/send?tag=frag-bin&mode=fragments&size=300001")), "steps": []map[string]any{
		{"wait": 1}, {"close": 1000}, {"end": true},
	}})
	if len(r.Records) != 2 || r.Records[0].Type != 2 || r.Records[0].Bytes != 300001 || r.Records[0].SHA256 != xorshiftSum(300001, 7) {
		t.Fatalf("binary records %+v", r.Records)
	}
	r = e.ws(t, map[string]any{"open": wsOpen(up.url("/send?tag=frag-text&mode=fragments&text=1")), "steps": []map[string]any{
		{"wait": 1}, {"close": 1000}, {"end": true},
	}})
	if len(r.Records) != 2 || r.Records[0].Type != 1 || r.Records[0].Text != strings.Repeat(wsFancyText, 3000) {
		t.Fatalf("text records: %d, want 2: %+v", len(r.Records), r)
	}
}

func (e *linkEnv) testWSLimit(t *testing.T, up *wsUpstream) {
	r := e.ws(t, map[string]any{"open": wsOpen(up.url("/echo?tag=over-page")), "steps": []map[string]any{
		{"head": []int{2, ws.MaxMessage + 1}},
	}})
	if !slices.Equal(wsTypes(r), []int{8}) || r.Records[0].Code != 1009 || !r.Ended {
		t.Fatalf("page over the limit: %+v", r)
	}
	up.waitOutcome(t, "over-page", "close 1009 a message over 64 MiB")
	r = e.ws(t, map[string]any{"open": wsOpen(up.url("/send?tag=over-far&size=" + strconv.Itoa(ws.MaxMessage+1)))})
	if !slices.Equal(wsTypes(r), []int{8}) || r.Records[0].Code != 1009 || !r.Ended {
		t.Fatalf("far end over the limit: %+v", r)
	}
}

func (e *linkEnv) testWSProtocolErrors(t *testing.T, up *wsUpstream) {
	cases := []struct {
		tag   string
		steps []map[string]any
	}{
		{"unknown-type", []map[string]any{{"send": "text", "text": "before"}, {"head": []int{9, 0}}}},
		{"close-1006", []map[string]any{{"raw": wsRaw(ws.Record(ws.RecordClose, []byte{0x03, 0xee}))}}},
		{"close-short", []map[string]any{{"raw": wsRaw(ws.Record(ws.RecordClose, []byte{0x03}))}}},
		{"close-long", []map[string]any{{"raw": wsRaw(ws.Record(ws.RecordClose, append([]byte{0x03, 0xe8}, []byte(strings.Repeat("x", 124))...)))}}},
	}
	for _, c := range cases {
		r := e.ws(t, map[string]any{"open": wsOpen(up.url("/echo?tag=" + c.tag)), "steps": c.steps})
		if len(r.Records) == 0 {
			t.Fatalf("%s: no records, want a close 1002 last: %+v", c.tag, r)
		}
		last := r.Records[len(r.Records)-1]
		if last.Type != 8 || last.Code != 1002 || !r.Ended {
			t.Fatalf("%s: %+v", c.tag, r)
		}
		up.waitOutcome(t, c.tag, "close 1002 "+last.Reason)
	}
}

func (e *linkEnv) testWSPageClose(t *testing.T, up *wsUpstream) {
	r := e.ws(t, map[string]any{"open": wsOpen(up.url("/echo?tag=page-first")), "steps": []map[string]any{
		{"send": "text", "text": "hi"}, {"wait": 1}, {"close": 4000, "reason": "bye"}, {"end": true},
	}})
	if !slices.Equal(wsTypes(r), []int{1, 8}) || r.Records[1].Code != 4000 || r.Records[1].Reason != "echo:bye" || !r.Ended {
		t.Fatalf("page first: %+v", r)
	}
	up.waitOutcome(t, "page-first", "close 4000 bye")
	r = e.ws(t, map[string]any{"open": wsOpen(up.url("/echo?tag=page-empty")), "steps": []map[string]any{
		{"close": nil}, {"end": true},
	}})
	if !slices.Equal(wsTypes(r), []int{8}) || r.Records[0].Code != 1005 || r.Records[0].Bytes != 0 || !r.Ended {
		t.Fatalf("empty close: %+v", r)
	}
	up.waitOutcome(t, "page-empty", "close 1005 ")
}

func (e *linkEnv) testWSFarClose(t *testing.T, up *wsUpstream) {
	r := e.ws(t, map[string]any{"open": wsOpen(up.url("/close?tag=far-first&code=4001&reason=server+bye")), "answerClose": map[string]any{"code": 4001, "reason": "ack"}})
	if !slices.Equal(wsTypes(r), []int{8}) || r.Records[0].Code != 4001 || r.Records[0].Reason != "server bye" || !r.Ended {
		t.Fatalf("far end first: %+v", r)
	}
	up.waitOutcome(t, "far-first", "reply 4001 ")
	r = e.ws(t, map[string]any{"open": wsOpen(up.url("/close?tag=far-empty&code=1005"))})
	if !slices.Equal(wsTypes(r), []int{8}) || r.Records[0].Code != 1005 || r.Records[0].Bytes != 0 || !r.Ended {
		t.Fatalf("far end empty close: %+v", r)
	}
}

func (e *linkEnv) testWSDrops(t *testing.T, up *wsUpstream) {
	r := e.ws(t, map[string]any{"open": wsOpen(up.url("/drop?tag=far-drop"))})
	if !slices.Equal(wsTypes(r), []int{1}) || r.Records[0].Text != "bye" || !r.Ended || r.Reset != "" {
		t.Fatalf("far end drop: %+v", r)
	}
	r = e.ws(t, map[string]any{"open": wsOpen(up.url("/echo?tag=page-drop")), "steps": []map[string]any{
		{"send": "text", "text": "x"}, {"wait": 1}, {"end": true},
	}})
	if !slices.Equal(wsTypes(r), []int{1}) || !r.Ended || r.Reset != "" {
		t.Fatalf("page drop: %+v", r)
	}
	up.waitOutcome(t, "page-drop", "dropped")
	r = e.ws(t, map[string]any{"open": wsOpen(up.url("/echo?tag=page-partial")), "steps": []map[string]any{
		{"head": []int{1, 100}}, {"raw": base64.StdEncoding.EncodeToString([]byte("0123456789"))}, {"end": true},
	}})
	if len(r.Records) != 0 || !r.Ended || r.Reset != "" {
		t.Fatalf("page ends inside a record: %+v", r)
	}
	up.waitOutcome(t, "page-partial", "dropped")
	if got := up.outcomesOf("page-partial"); len(got) != 1 {
		t.Fatalf("the far end saw %q", got)
	}
	e.ws(t, map[string]any{"open": wsOpen(up.url("/echo?tag=page-reset")), "steps": []map[string]any{
		{"send": "text", "text": "x"}, {"wait": 1}, {"reset": "ECONNRESET"},
	}})
	up.waitOutcome(t, "page-reset", "dropped")
}

func (e *linkEnv) testWSPing(t *testing.T, up *wsUpstream) {
	r := e.ws(t, map[string]any{"open": wsOpen(up.url("/ping?tag=ping")), "answerClose": map[string]any{"code": 1000}})
	if !slices.Equal(wsTypes(r), []int{1, 8}) || r.Records[0].Text != "pong:are-you-there" || r.Records[1].Code != 1000 || r.Records[1].Reason != "done" {
		t.Fatalf("ping: %+v", r)
	}
}

func (e *linkEnv) testWSHeaders(t *testing.T, up *wsUpstream) {
	open := map[string]any{
		"url":       up.url("/headers?tag=headers#frag"),
		"protocols": []string{"chat.v1", "chat.v2"},
		"headers": [][2]string{
			{"Origin", "https://prog.example"}, {"Authorization", wsSecretToken},
			{"Host", "evil.example"}, {"Connection", "close, X-Gone"}, {"X-Gone", "1"}, {"Upgrade", "h2c"},
			{"Sec-WebSocket-Key", "AAAAAAAAAAAAAAAAAAAAAA=="}, {"Sec-WebSocket-Version", "8"},
			{"Sec-WebSocket-Extensions", "permessage-deflate"}, {"Sec-WebSocket-Protocol", "evil"},
			{"X-Thing", "a"}, {"x-thing", "b"}, {"Cookie", "a=1"}, {"cookie", "b=2"}, {"X-Latin", "caf\u00e9"},
			{"Accept-Encoding", "gzip"}, {"Proxy-Authorization", "secret"},
		},
	}
	r := e.ws(t, map[string]any{"open": open, "steps": []map[string]any{{"close": 1000}, {"end": true}}})
	if r.Opened.Protocol == nil || *r.Opened.Protocol != "chat.v2" {
		t.Fatalf("opened %+v", r)
	}
	h := up.header("headers")
	want := map[string]string{
		"Origin": "https://prog.example", "Authorization": wsSecretToken, "Upgrade": "websocket", "Connection": "Upgrade",
		"Sec-Websocket-Version": "13", "Sec-Websocket-Protocol": "chat.v1, chat.v2", "X-Thing": "a, b", "Cookie": "a=1; b=2", "X-Latin": "caf\xe9",
	}
	for k, v := range want {
		if got := strings.Join(h.Values(k), "|"); got != v {
			t.Fatalf("%s: got %q, want %q (all %v)", k, got, v, h)
		}
	}
	for _, k := range []string{"Sec-Websocket-Extensions", "X-Gone", "User-Agent", "Accept-Encoding", "Proxy-Authorization"} {
		if v, ok := h[k]; ok {
			t.Fatalf("%s sent as %q", k, v)
		}
	}
	if key := h.Get("Sec-Websocket-Key"); key == "" || key == "AAAAAAAAAAAAAAAAAAAAAA==" {
		t.Fatalf("key %q", key)
	}
	r = e.ws(t, map[string]any{"open": map[string]any{"url": up.url("/echo?tag=no-proto")}, "steps": []map[string]any{{"close": 1000}, {"end": true}}})
	if r.Opened.Protocol == nil || *r.Opened.Protocol != "" || up.header("no-proto").Get("Sec-Websocket-Protocol") != "" {
		t.Fatalf("no protocols: %+v", r)
	}
}

func (e *linkEnv) testWSRefusals(t *testing.T, up *wsUpstream) {
	closedHost, closedPort := hostPort(t, e.closed)
	cases := []struct {
		url, code, detail string
	}{
		{up.url("/status?code=403"), "ECONNREFUSED", "HTTP 403"},
		{up.url("/status?code=200"), "ECONNREFUSED", "HTTP 200"},
		{up.url("/redirect"), "ECONNREFUSED", "HTTP 302"},
		{fmt.Sprintf("ws://%s:%d/", closedHost, closedPort), "ECONNREFUSED", ""},
		{fmt.Sprintf("ws://127.0.0.1:%d/devtools/browser", e.b.port), "EACCES", ""},
		{fmt.Sprintf("ws://localhost:%d/devtools/browser", e.b.port), "EACCES", ""},
		{"ws://169.254.169.254/", "EACCES", ""},
		{"wss" + strings.TrimPrefix(up.tls.URL, "https") + "/echo", "ECONNREFUSED", "x509: "},
	}
	for _, c := range cases {
		var r wsResult
		e.p.eval("linkTest.ws.run("+jsArgs(map[string]any{"open": wsOpen(c.url)})+")", &r)
		if r.Reset != c.code || !strings.HasPrefix(r.Detail, c.detail) {
			t.Fatalf("%s: %+v", c.url, r)
		}
	}
	if up.hitCount("/target") != 0 {
		t.Fatal("the redirect was followed")
	}
}

func (e *linkEnv) testWSBadOpen(t *testing.T, up *wsUpstream) {
	good := up.url("/echo")
	cases := []map[string]any{
		{},
		{"url": 7},
		{"url": strings.Replace(good, "ws://", "http://", 1)},
		{"url": strings.Replace(good, "ws://", "ws://user:pw@", 1)},
		{"url": "ws:///path"},
		{"url": good, "protocols": []string{"a b"}},
		{"url": good, "protocols": []string{"a", "a"}},
		{"url": good, "headers": [][2]string{{"bad name", "x"}}},
		{"url": good, "headers": [][2]string{{"X", "a\u0100"}}},
		{"url": good, "headers": [][2]string{{"X", "a\nb"}}},
		{"url": good, "headers": []any{[]string{"X"}}},
	}
	for _, open := range cases {
		var r wsResult
		e.p.eval("linkTest.ws.run("+jsArgs(map[string]any{"open": open})+")", &r)
		if r.Reset != "EPROTO" {
			t.Fatalf("%v: %+v", open, r)
		}
	}
}

func (e *linkEnv) testWSPrivacy(t *testing.T, up *wsUpstream) {
	open := wsOpen(up.url("/echo?tag=private&secret=" + wsSecretQuery))
	open["headers"] = [][2]string{{"Authorization", wsSecretToken}}
	r := e.ws(t, map[string]any{"open": open, "steps": []map[string]any{
		{"send": "text", "text": wsSecretPayload}, {"wait": 1}, {"close": 4002, "reason": wsSecretPayload}, {"end": true},
	}})
	if len(r.Records) != 2 {
		t.Fatalf("records %+v", r.Records)
	}
	e.a.waitStderr(t, "ws "+up.url("/echo")+": closed with 4002", 10*time.Second)
	logs := e.a.stderr.String()
	for _, s := range []string{wsSecretQuery, wsSecretToken, wsSecretPayload, "tag=private"} {
		if strings.Contains(logs, s) {
			t.Fatalf("the node log shows %q:\n%s", s, logs)
		}
	}
	if !strings.Contains(logs, "ws "+up.url("/echo")+": open") {
		t.Fatalf("no ws line in the log:\n%s", logs)
	}
}

func TestLinkWebSocketsOverTLS(t *testing.T) {
	up := newWSUpstream(t)
	roots := x509.NewCertPool()
	roots.AddCert(up.tls.Certificate())
	env := setupInProcess(t, policy.Local(), func(n *node.Node) {
		ws.RegisterOptions(n, ws.Options{Dialer: n.Dialer(), RootCAs: roots, Logf: n.Logf})
	})
	url := "wss" + strings.TrimPrefix(up.tls.URL, "https") + "/echo?tag=tls"
	r := env.ws(t, map[string]any{"open": map[string]any{"url": url, "protocols": []string{"chat.v1"}}, "steps": []map[string]any{
		{"send": "text", "text": "over tls"}, {"wait": 1}, {"close": 1000}, {"end": true},
	}})
	if !slices.Equal(wsTypes(r), []int{1, 8}) || r.Records[0].Text != "over tls" || r.Opened.Protocol == nil || *r.Opened.Protocol != "chat.v1" {
		t.Fatalf("wss: %+v", r)
	}
	var h struct {
		Caps []string `json:"caps"`
	}
	env.p.eval("linkTest.ready()", &h)
	if !slices.Equal(h.Caps, []string{"ws"}) {
		t.Fatalf("caps %v", h.Caps)
	}
}

func followWS(t *testing.T, env *linkEnv) {
	t.Helper()
	up := newWSUpstream(t)
	port := portOf(t, up.srv.URL)
	r := env.ws(t, map[string]any{"open": wsOpen("ws://ref.link.test:" + port + "/echo?tag=follow"), "steps": []map[string]any{
		{"send": "text", "text": "remote"}, {"wait": 1}, {"close": 1000}, {"end": true},
	}})
	if !slices.Equal(wsTypes(r), []int{1, 8}) || r.Records[0].Text != "remote" {
		t.Fatalf("follow ws: %+v", r)
	}
	var refused wsResult
	env.p.eval("linkTest.ws.run("+jsArgs(map[string]any{"open": wsOpen("ws://127.0.0.2:" + port + "/echo")})+")", &refused)
	if refused.Reset != "EACCES" {
		t.Fatalf("follow ws outside the allow rule: %+v", refused)
	}
}
