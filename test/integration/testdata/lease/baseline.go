package lease

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/coder/websocket"
)

var blBase = time.Now()

func blAt(t time.Time) string {
	if t.IsZero() {
		return "none"
	}
	return fmt.Sprintf("%.1f", float64(t.Sub(blBase).Microseconds())/1000)
}

func blSince(from, to time.Time) string {
	if from.IsZero() || to.IsZero() {
		return "none"
	}
	return fmt.Sprintf("%.1f", float64(to.Sub(from).Microseconds())/1000)
}

func blErr(e error) string {
	if e == nil {
		return "nil"
	}
	return e.Error()
}

func blLine(t *testing.T, row string, kv ...any) {
	t.Helper()
	var b strings.Builder
	b.WriteString("ci-proof lease-baseline ")
	b.WriteString(row)
	for i := 0; i+1 < len(kv); i += 2 {
		fmt.Fprintf(&b, " %v=%v", kv[i], kv[i+1])
	}
	t.Log(b.String())
}

func blWord(s string, n int) string {
	s = strings.Join(strings.Fields(s), "_")
	if len(s) > n {
		s = s[:n]
	}
	if s == "" {
		return "empty"
	}
	return s
}

type blEvent struct {
	at      time.Time
	kind, n string
	path    string
}

type blGate struct {
	ch   chan struct{}
	once sync.Once
}

type blSite struct {
	main, other *httptest.Server
	mu          sync.Mutex
	events      []blEvent
	gates       map[string]*blGate
	protected   int
	closed      string
}

func (s *blSite) note(kind, n, path string) {
	s.mu.Lock()
	s.events = append(s.events, blEvent{time.Now(), kind, n, path})
	s.mu.Unlock()
}

func (s *blSite) gate(n string) *blGate {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.gates[n]
	if g == nil {
		g = &blGate{ch: make(chan struct{})}
		s.gates[n] = g
	}
	return g
}

func (s *blSite) open(n string) { g := s.gate(n); g.once.Do(func() { close(g.ch) }) }

func (s *blSite) find(kind, n string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e.kind == kind && e.n == n {
			return e.at
		}
	}
	return time.Time{}
}

func (s *blSite) wait(kind, n string, d time.Duration) time.Time {
	for end := time.Now().Add(d); ; time.Sleep(2 * time.Millisecond) {
		if at := s.find(kind, n); !at.IsZero() || time.Now().After(end) {
			return at
		}
	}
}

func (s *blSite) hops(n string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.events {
		if e.n == n && e.kind == "doc" {
			out = append(out, e.path)
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ",")
}

func (s *blSite) count(kind, n string, before time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := 0
	for _, e := range s.events {
		if e.kind == kind && e.n == n && e.at.Before(before) {
			c++
		}
	}
	return c
}

func (s *blSite) protectedHits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.protected
}

func (s *blSite) url(path string) string { return s.main.URL + path }

func blHTML(w http.ResponseWriter, format string, args ...any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, format, args...)
}

func (s *blSite) serve(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	n := r.URL.Query().Get("n")
	if strings.HasPrefix(r.Host, "seven.sliccy.ai") {
		s.mu.Lock()
		s.protected++
		s.mu.Unlock()
		s.note("doc", n, "protected"+p)
		blHTML(w, "<h1>PROTECTED DOCUMENT</h1>")
		return
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) >= 2 && n == "" {
		n = parts[len(parts)-1]
	}
	if p == "/favicon.ico" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Header.Get("Sec-Fetch-Dest") == "document" || r.Header.Get("Sec-Fetch-Dest") == "iframe" {
		s.note("doc", n, p)
	}
	switch {
	case p == "/fast":
		blHTML(w, "<h1>Baseline fast %s</h1>", n)
	case p == "/slow":
		s.note("slow-arrived", n, p)
		select {
		case <-s.gate(n).ch:
			s.note("slow-released", n, p)
			blHTML(w, "<h1>Baseline slow %s</h1>", n)
		case <-r.Context().Done():
			s.note("slow-aborted", n, p)
		}
	case p == "/burst":
		ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
		blHTML(w, `<h1>Baseline burst %s</h1><script>const stop=performance.now()+12000;const step=()=>{const e=performance.now()+%d;while(performance.now()<e);if(performance.now()<stop)setTimeout(step,0)};setTimeout(step,200)</script>`, n, ms)
	case strings.HasPrefix(p, "/r/"):
		to := ""
		switch parts[1] {
		case "same":
			to = s.main.URL + "/final/" + n
		case "inprefix":
			to = s.main.URL + "/r/final/" + n
		case "other":
			to = s.other.URL + "/final/" + n
		case "protected":
			u, _ := url.Parse(s.main.URL)
			to = "https://seven.sliccy.ai:" + u.Port() + "/final/" + n
		case "error":
			to = s.closed + "/final/" + n
		case "final":
			blHTML(w, "<h1>Prefix final %s</h1>", n)
			return
		}
		http.Redirect(w, r, to, http.StatusFound)
	case strings.HasPrefix(p, "/final/"):
		blHTML(w, "<h1>Final %s</h1>", n)
	case p == "/page/assign":
		blHTML(w, `<h1>Assign source %s</h1><script>setTimeout(()=>location.assign('/final/%s-assigned'),300)</script>`, n, n)
	case p == "/page/meta":
		blHTML(w, `<meta http-equiv="refresh" content="1;url=/final/%s-meta"><h1>Meta source %s</h1>`, n, n)
	case p == "/page/push":
		blHTML(w, `<h1>Push source %s</h1><script>setTimeout(()=>history.pushState({},'','/pushed/%s'),300)</script>`, n, n)
	case p == "/page/write":
		blHTML(w, `<h1>Write source %s</h1><script>setTimeout(()=>{document.open();document.write('<h1>Rewritten %s</h1>');document.close()},300)</script>`, n, n)
	case p == "/page/frames":
		u, _ := url.Parse(s.main.URL)
		blHTML(w, `<h1>Framed top %s</h1><iframe src="https://seven.sliccy.ai:%s/child/%s"></iframe><iframe src="%s/child/%s"></iframe><iframe src="/child/%s"></iframe>`, n, u.Port(), n, s.other.URL, n, n)
	case strings.HasPrefix(p, "/child/"):
		blHTML(w, "<p>CHILD SAME ORIGIN %s</p>", n)
	case p == "/race/nav":
		k, _ := strconv.Atoi(r.URL.Query().Get("k"))
		s.note("race-doc", n, strconv.Itoa(k))
		blHTML(w, `<h1>Race doc %d</h1><script>setTimeout(()=>location.replace('/race/nav?n=%s&k=%d'),40)</script>`, k, n, k+1)
	case p == "/race/push":
		blHTML(w, `<h1>Race push %s</h1><script>let k=0;setInterval(()=>history.replaceState({},'','/race/push?n=%s&k='+(++k)),2)</script>`, n, n)
	case p == "/race/text":
		blHTML(w, `<h1 id="h">Race text 0</h1><script>let k=0;setInterval(()=>{document.getElementById('h').textContent='Race text '+(++k)},2)</script>`)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func newBLSite(t *testing.T) *blSite {
	t.Helper()
	s := &blSite{gates: map[string]*blGate{}}
	s.main = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.main.Close)
	s.other = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.note("doc", strings.TrimPrefix(r.URL.Path[strings.LastIndex(r.URL.Path, "/"):], "/"), "other"+r.URL.Path)
		blHTML(w, "<p>OTHER ORIGIN DOCUMENT</p>")
	}))
	t.Cleanup(s.other.Close)
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	s.closed = "https://" + l.Addr().String()
	_ = l.Close()
	return s
}

type blMsg struct {
	at      time.Time
	conn    int
	out     bool
	id      int64
	method  string
	failed  bool
	unique  bool
	target  string
	reqURL  string
	resType string
}

type blProxy struct {
	srv     *httptest.Server
	up      string
	mu      sync.Mutex
	conns   int
	log     []blMsg
	pending map[[2]int64]blMsg
}

func newBLProxy(t *testing.T, upstream string) *blProxy {
	t.Helper()
	p := &blProxy{up: upstream, pending: map[[2]int64]blMsg{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		resp, e := http.Get(upstream + "/json/version")
		if e != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		var v map[string]any
		if json.NewDecoder(resp.Body).Decode(&v) != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if s, ok := v["webSocketDebuggerUrl"].(string); ok {
			if u, e := url.Parse(s); e == nil {
				u.Host = r.Host
				v["webSocketDebuggerUrl"] = u.String()
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	})
	mux.HandleFunc("/devtools/", p.serve)
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *blProxy) serve(w http.ResponseWriter, r *http.Request) {
	u, _ := url.Parse(p.up)
	dctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	up, _, e := websocket.Dial(dctx, "ws://"+u.Host+r.URL.Path, nil)
	cancel()
	if e != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	c, e := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if e != nil {
		_ = up.CloseNow()
		return
	}
	c.SetReadLimit(-1)
	up.SetReadLimit(-1)
	p.mu.Lock()
	p.conns++
	conn := p.conns
	p.mu.Unlock()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan struct{}, 2)
	pump := func(from, to *websocket.Conn, out bool) {
		defer func() { done <- struct{}{} }()
		for {
			typ, data, e := from.Read(ctx)
			if e != nil {
				return
			}
			p.note(conn, out, data)
			if to.Write(ctx, typ, data) != nil {
				return
			}
		}
	}
	go pump(c, up, true)
	go pump(up, c, false)
	<-done
	_ = c.CloseNow()
	_ = up.CloseNow()
}

func (p *blProxy) note(conn int, out bool, data []byte) {
	var m struct {
		ID     int64           `json:"id"`
		Method string          `json:"method"`
		Error  json.RawMessage `json:"error"`
		Params struct {
			UniqueContextID string `json:"uniqueContextId"`
			TargetID        string `json:"targetId"`
			TargetInfo      struct {
				TargetID string `json:"targetId"`
			} `json:"targetInfo"`
			ResourceType string `json:"resourceType"`
			Request      struct {
				URL string `json:"url"`
			} `json:"request"`
		} `json:"params"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	msg := blMsg{at: time.Now(), conn: conn, out: out, id: m.ID, method: m.Method, failed: len(m.Error) > 0 && string(m.Error) != "null", unique: m.Params.UniqueContextID != "", target: m.Params.TargetID, reqURL: m.Params.Request.URL, resType: m.Params.ResourceType}
	if msg.target == "" {
		msg.target = m.Params.TargetInfo.TargetID
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	key := [2]int64{int64(conn), m.ID}
	if out && m.ID != 0 {
		p.pending[key] = msg
	} else if !out && m.ID != 0 && m.Method == "" {
		req := p.pending[key]
		delete(p.pending, key)
		msg.method, msg.unique, msg.target = req.method, req.unique, req.target
	}
	p.log = append(p.log, msg)
}

func (p *blProxy) find(after time.Time, out bool, method string, pred func(blMsg) bool) blMsg {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range p.log {
		if m.out == out && m.method == method && !m.at.Before(after) && (pred == nil || pred(m)) {
			return m
		}
	}
	return blMsg{}
}

func (p *blProxy) waitFor(after time.Time, out bool, method string, pred func(blMsg) bool, d time.Duration) blMsg {
	for end := time.Now().Add(d); ; time.Sleep(time.Millisecond) {
		if m := p.find(after, out, method, pred); !m.at.IsZero() || time.Now().After(end) {
			return m
		}
	}
}

func (p *blProxy) reply(req blMsg, d time.Duration) blMsg {
	if req.at.IsZero() {
		return blMsg{}
	}
	return p.waitFor(req.at, false, req.method, func(m blMsg) bool { return m.conn == req.conn && m.id == req.id }, d)
}

func (p *blProxy) methods(from, to time.Time, out bool) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := map[string]int{}
	for _, m := range p.log {
		if m.out == out && !m.at.Before(from) && m.at.Before(to) && m.method != "" {
			seen[m.method]++
		}
	}
	var keys []string
	for k, v := range seen {
		keys = append(keys, fmt.Sprintf("%s:%d", k, v))
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "none"
	}
	return strings.Join(keys, ",")
}

func (p *blProxy) outstanding(at time.Time) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	open := map[[2]int64]string{}
	for _, m := range p.log {
		if m.at.After(at) {
			break
		}
		key := [2]int64{int64(m.conn), m.id}
		if m.out && m.id != 0 {
			open[key] = m.method
		} else if !m.out && m.id != 0 && m.method != "" {
			delete(open, key)
		}
	}
	var names []string
	for _, v := range open {
		names = append(names, v)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ",")
}

type blRig struct {
	t      *testing.T
	site   *blSite
	proxy  *blProxy
	direct *cdp.Client
}

func blSetup(t *testing.T) *blRig {
	t.Helper()
	endpoint := os.Getenv("SLICC_LEASE_BASELINE_ENDPOINT")
	if endpoint == "" {
		t.Skip("real-Chrome lease baseline harness")
	}
	r := &blRig{t: t, site: newBLSite(t), proxy: newBLProxy(t, endpoint)}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, e := cdp.Dial(ctx, endpoint)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Close() })
	r.direct = c
	return r
}

func (r *blRig) broker() *Broker {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	b, e := New(ctx, r.proxy.srv.URL, "work", []string{"https://seven.sliccy.ai"})
	if e != nil {
		r.t.Fatal(e)
	}
	r.t.Cleanup(b.Close)
	return b
}

func (r *blRig) origin() []Rule { return []Rule{{Kind: "origin", URL: r.site.main.URL}} }

func blGrant(t *testing.T, b *Broker, ttl int, rules []Rule) (*active, time.Time) {
	t.Helper()
	jkt := enc.EncodeToString(random(32))
	p, e := b.register(jkt, open{Kind: Cap, Mode: "request", Allow: rules, TTL: ttl}, make(chan struct{}))
	if e != nil {
		t.Fatalf("register: %v", e)
	}
	at := time.Now()
	if e := b.Approve(p.Request); e != nil {
		t.Fatalf("approve: %v", e)
	}
	d := <-p.deliver
	if d.err != nil {
		t.Fatal(d.err)
	}
	c, e := b.verify(d.jwt, jkt)
	if e != nil {
		t.Fatal(e)
	}
	b.mu.Lock()
	a := b.active[c.ID]
	b.mu.Unlock()
	if a == nil {
		t.Fatal("grant not active")
	}
	return a, at
}

type blOp struct {
	mu    sync.Mutex
	start time.Time
	end   time.Time
	err   error
	sends []time.Time
	res   []any
	done  chan struct{}
}

func blRun(b *Broker, a *active, op, args string) *blOp {
	o := &blOp{start: time.Now(), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(a.ctx)
	go func() {
		defer cancel()
		e := b.action(ctx, a, op, json.RawMessage(args), func(v any) bool {
			o.mu.Lock()
			o.sends = append(o.sends, time.Now())
			o.res = append(o.res, v)
			o.mu.Unlock()
			return true
		})
		o.mu.Lock()
		o.err, o.end = e, time.Now()
		o.mu.Unlock()
		close(o.done)
	}()
	return o
}

func (o *blOp) wait(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case <-o.done:
	case <-time.After(d):
		t.Fatalf("op did not return within %s", d)
	}
}

func (o *blOp) text() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.res) == 0 {
		return "none"
	}
	m, _ := o.res[0].(map[string]any)
	s, _ := m["text"].(string)
	return blWord(s, 48)
}

func (o *blOp) keys() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.res) == 0 {
		return "none"
	}
	m, _ := o.res[0].(map[string]any)
	var k []string
	for key := range m {
		k = append(k, key)
	}
	sort.Strings(k)
	return "[" + strings.Join(k, ",") + "]"
}

func (o *blOp) sentAfter(t time.Time) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	c := 0
	for _, s := range o.sends {
		if !s.Before(t) {
			c++
		}
	}
	return c
}

func (r *blRig) do(b *Broker, a *active, op, args string) *blOp {
	r.t.Helper()
	o := blRun(b, a, op, args)
	o.wait(r.t, 12*time.Second)
	return o
}

func blGoto(u string) string { b, _ := json.Marshal(map[string]string{"url": u}); return string(b) }

func blCtxDone(a *active) func(time.Duration) (time.Time, string) {
	ch := make(chan time.Time, 1)
	go func() { <-a.ctx.Done(); ch <- time.Now() }()
	return func(d time.Duration) (time.Time, string) {
		select {
		case at := <-ch:
			return at, blErr(a.ctx.Err())
		case <-time.After(d):
			return time.Time{}, "pending"
		}
	}
}

func blRemoved(b *Broker, id string) func(time.Duration) time.Time {
	ch := make(chan time.Time, 1)
	go func() {
		for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
			b.mu.Lock()
			_, ok := b.active[id]
			b.mu.Unlock()
			if !ok {
				ch <- time.Now()
				return
			}
		}
	}()
	return func(d time.Duration) time.Time {
		select {
		case at := <-ch:
			return at
		case <-time.After(d):
			return time.Time{}
		}
	}
}

func (r *blRig) tabURL(target string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ts, e := r.direct.Targets(ctx)
	if e != nil {
		return "error"
	}
	for _, ti := range ts {
		if ti.TargetID == target {
			if u, e := url.Parse(ti.URL); e == nil && u.Host != "" {
				if u.Host == strings.TrimPrefix(r.site.main.URL, "https://") {
					return u.RequestURI()
				}
				return u.Scheme + "://" + map[bool]string{true: "other", false: u.Hostname()}[u.Host == strings.TrimPrefix(r.site.other.URL, "https://")] + u.Path
			}
			return blWord(ti.URL, 40)
		}
	}
	return "gone"
}

func (r *blRig) tabGone(target string, d time.Duration) time.Time {
	for end := time.Now().Add(d); ; time.Sleep(5 * time.Millisecond) {
		if target == "" {
			return time.Time{}
		}
		if r.tabURL(target) == "gone" {
			return time.Now()
		}
		if time.Now().After(end) {
			return time.Time{}
		}
	}
}

func (r *blRig) closeTarget(target string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = r.direct.Call(ctx, "", "Target.closeTarget", map[string]any{"targetId": target}, nil)
}

func blGen(b *Broker, a *active) uint64 {
	b.bs.mu.Lock()
	defer b.bs.mu.Unlock()
	return a.tab.generation
}

func blCounts(b *Broker) string {
	s := b.List()
	return fmt.Sprintf("pending:%d,active:%d", len(s.Pending), len(s.Active))
}

func TestLeaseBaselineRevokeGoto(t *testing.T) {
	r := blSetup(t)
	b := r.broker()
	a, _ := blGrant(t, b, 60, r.origin())
	first := r.do(b, a, "goto", blGoto(r.site.url("/fast?n=g1")))
	if first.err != nil {
		t.Fatalf("setup goto: %v", first.err)
	}
	target := a.tab.target
	op := blRun(b, a, "goto", blGoto(r.site.url("/slow?n=g2")))
	arrived := r.site.wait("slow-arrived", "g2", 8*time.Second)
	if arrived.IsZero() {
		t.Fatal("slow navigation never reached the site")
	}
	time.Sleep(200 * time.Millisecond)
	ctxDone := blCtxDone(a)
	before := blCounts(b)
	r0 := time.Now()
	re := b.Revoke(a.ID)
	r1 := time.Now()
	ctxAt, ctxErr := ctxDone(5 * time.Second)
	op.wait(t, 10*time.Second)
	closeOut := r.proxy.waitFor(r0, true, "Target.closeTarget", nil, 5*time.Second)
	closeIn := r.proxy.reply(closeOut, 5*time.Second)
	gone := r.tabGone(target, 6*time.Second)
	abort := r.site.wait("slow-aborted", "g2", 3*time.Second)
	r.site.open("g2")
	time.Sleep(300 * time.Millisecond)
	_, curErr := b.current(a.claims)
	blLine(t, "R1-goto-broker", "pid", os.Getpid(), "browser_pid", os.Getenv("SLICC_LEASE_BASELINE_BROWSER_PID"),
		"nav_request_arrived_ms", blAt(arrived), "revoke_call_ms", blAt(r0), "revoke_return_ms", blAt(r1), "revoke_err", blErr(re),
		"ctx_cancel_after_ms", blSince(r0, ctxAt), "ctx_err", blWord(ctxErr, 30), "op_return_after_ms", blSince(r0, op.end), "op_err", blErr(op.err),
		"close_target_sent_after_ms", blSince(r0, closeOut.at), "close_target_reply_after_ms", blSince(r0, closeIn.at), "close_target_failed", closeIn.failed,
		"tab_gone_after_ms", blSince(r0, gone), "nav_request_aborted_after_ms", blSince(r0, abort),
		"attempted_nav_reached_site", !arrived.IsZero(), "undone_nav_aborted_by_browser", !abort.IsZero(), "late_release_served", !r.site.find("slow-released", "g2").IsZero(),
		"sends_total", len(op.sends), "sends_after_revoke", op.sentAfter(r0), "current_after", blErr(curErr), "status_before", before, "status_after", blCounts(b))
	if len(op.sends) != 0 {
		t.Fatal("a result was handed to the holder for the revoked in-flight goto")
	}
	if curErr == nil {
		t.Fatal("revoked grant still current")
	}
}

func TestLeaseBaselineRevokeSnapshot(t *testing.T) {
	r := blSetup(t)
	for _, row := range []struct {
		name   string
		queued bool
	}{{"R1-snapshot-broker-read-queued", true}, {"R1-snapshot-broker-before-read", false}} {
		b := r.broker()
		a, _ := blGrant(t, b, 60, r.origin())
		n := "s" + strconv.FormatBool(row.queued)
		first := r.do(b, a, "goto", blGoto(r.site.url("/burst?ms=700&n="+n)))
		if first.err != nil {
			t.Fatalf("setup goto: %v", first.err)
		}
		target := a.tab.target
		time.Sleep(600 * time.Millisecond)
		op := blRun(b, a, "snapshot", "{}")
		isRead := func(m blMsg) bool { return m.unique }
		var evalOut blMsg
		if row.queued {
			evalOut = r.proxy.waitFor(op.start, true, "Runtime.evaluate", isRead, 6*time.Second)
		} else {
			time.Sleep(50 * time.Millisecond)
		}
		ctxDone := blCtxDone(a)
		r0 := time.Now()
		inFlight := r.proxy.outstanding(r0)
		re := b.Revoke(a.ID)
		r1 := time.Now()
		ctxAt, ctxErr := ctxDone(5 * time.Second)
		op.wait(t, 10*time.Second)
		if !row.queued {
			evalOut = r.proxy.find(op.start, true, "Runtime.evaluate", isRead)
		}
		evalIn := r.proxy.reply(evalOut, 3*time.Second)
		closeOut := r.proxy.waitFor(r0, true, "Target.closeTarget", nil, 5*time.Second)
		closeIn := r.proxy.reply(closeOut, 5*time.Second)
		gone := r.tabGone(target, 8*time.Second)
		blLine(t, row.name, "pid", os.Getpid(), "browser_pid", os.Getenv("SLICC_LEASE_BASELINE_BROWSER_PID"), "burst_ms", 700,
			"op_start_ms", blAt(op.start), "revoke_call_ms", blAt(r0), "revoke_return_ms", blAt(r1), "revoke_err", blErr(re),
			"outstanding_cdp_at_revoke", inFlight, "read_dispatched_before_revoke", !evalOut.at.IsZero() && evalOut.at.Before(r0),
			"read_dispatched_after_ms", blSince(r0, evalOut.at), "read_reply_after_revoke_ms", blSince(r0, evalIn.at), "read_reply_failed", evalIn.failed, "read_ran_in_browser", !evalIn.at.IsZero() && !evalIn.failed,
			"ctx_cancel_after_ms", blSince(r0, ctxAt), "ctx_err", blWord(ctxErr, 30), "op_return_after_ms", blSince(r0, op.end), "op_err", blErr(op.err),
			"close_target_sent_after_ms", blSince(r0, closeOut.at), "close_target_reply_after_ms", blSince(r0, closeIn.at), "tab_gone_after_ms", blSince(r0, gone),
			"sends_total", len(op.sends), "sends_after_revoke", op.sentAfter(r0), "status_after", blCounts(b))
		if op.sentAfter(r1) != 0 {
			t.Fatal("a snapshot result was handed to the holder after revoke returned")
		}
		if gone.IsZero() {
			r.closeTarget(target)
		}
	}
}

func TestLeaseBaselineExpiry(t *testing.T) {
	r := blSetup(t)
	b := r.broker()
	a, approved := blGrant(t, b, 3, r.origin())
	exp := time.Unix(a.Exp, 0)
	removed := blRemoved(b, a.ID)
	ctxDone := blCtxDone(a)
	first := r.do(b, a, "goto", blGoto(r.site.url("/fast?n=e1")))
	if first.err != nil {
		t.Fatalf("setup goto: %v", first.err)
	}
	target := a.tab.target
	op := blRun(b, a, "goto", blGoto(r.site.url("/slow?n=e2")))
	arrived := r.site.wait("slow-arrived", "e2", 3*time.Second)
	ctxAt, ctxErr := ctxDone(6 * time.Second)
	removedAt := removed(6 * time.Second)
	op.wait(t, 10*time.Second)
	closeOut := r.proxy.waitFor(exp.Add(-time.Second), true, "Target.closeTarget", nil, 5*time.Second)
	gone := r.tabGone(target, 6*time.Second)
	abort := r.site.wait("slow-aborted", "e2", 3*time.Second)
	r.site.open("e2")
	_, curErr := b.current(a.claims)
	after := blRun(b, a, "snapshot", "{}")
	after.wait(t, 5*time.Second)
	blLine(t, "R2-goto-broker", "pid", os.Getpid(), "ttl_s", 3, "approve_ms", blAt(approved), "exp_wall_ms", blAt(exp), "granted_term_ms", blSince(approved, exp),
		"op_dispatched_before_exp", !arrived.IsZero() && arrived.Before(exp), "ctx_done_minus_exp_ms", blSince(exp, ctxAt), "ctx_err", blWord(ctxErr, 30),
		"timer_removed_minus_exp_ms", blSince(exp, removedAt), "op_return_minus_exp_ms", blSince(exp, op.end), "op_err", blErr(op.err),
		"close_target_minus_exp_ms", blSince(exp, closeOut.at), "tab_gone_minus_exp_ms", blSince(exp, gone), "nav_aborted_minus_exp_ms", blSince(exp, abort),
		"sends_total", len(op.sends), "check_before_dispatch_after_exp", blErr(curErr), "action_after_exp", blErr(after.err), "status_after", blCounts(b))
	if len(op.sends) != 0 || len(after.sends) != 0 {
		t.Fatal("a result was handed to the holder for an expired grant")
	}
	b2 := r.broker()
	a2, approved2 := blGrant(t, b2, 3, r.origin())
	exp2 := time.Unix(a2.Exp, 0)
	removed2 := blRemoved(b2, a2.ID)
	ctx2 := blCtxDone(a2)
	first2 := r.do(b2, a2, "goto", blGoto(r.site.url("/burst?ms=300&n=e3")))
	if first2.err != nil {
		t.Fatalf("setup goto: %v", first2.err)
	}
	target2 := a2.tab.target
	var ops []*blOp
	for time.Now().Before(exp2.Add(500*time.Millisecond)) && len(ops) < 40 {
		o := blRun(b2, a2, "snapshot", "{}")
		o.wait(t, 8*time.Second)
		ops = append(ops, o)
	}
	ctxAt2, ctxErr2 := ctx2(5 * time.Second)
	removedAt2 := removed2(5 * time.Second)
	delivered, refused, crossing, afterRemoved := 0, 0, 0, 0
	var lastSend time.Time
	crossErr := "none"
	for _, o := range ops {
		if len(o.sends) > 0 {
			delivered++
			if o.sends[len(o.sends)-1].After(lastSend) {
				lastSend = o.sends[len(o.sends)-1]
			}
		} else {
			refused++
		}
		if o.start.Before(exp2) && !o.end.Before(exp2) {
			crossing++
			crossErr = blErr(o.err)
		}
		if !removedAt2.IsZero() {
			afterRemoved += o.sentAfter(removedAt2)
		}
	}
	gone2 := r.tabGone(target2, 6*time.Second)
	blLine(t, "R2-snapshot-broker", "pid", os.Getpid(), "ttl_s", 3, "approve_ms", blAt(approved2), "exp_wall_ms", blAt(exp2), "granted_term_ms", blSince(approved2, exp2),
		"ops", len(ops), "delivered", delivered, "refused", refused, "ops_crossing_exp", crossing, "crossing_op_err", crossErr,
		"last_send_minus_exp_ms", blSince(exp2, lastSend), "ctx_done_minus_exp_ms", blSince(exp2, ctxAt2), "ctx_err", blWord(ctxErr2, 30),
		"timer_removed_minus_exp_ms", blSince(exp2, removedAt2), "sends_after_timer_removed", afterRemoved, "tab_gone_minus_exp_ms", blSince(exp2, gone2))
	if afterRemoved != 0 {
		t.Fatal("a snapshot result was handed over after the expiry timer retired the grant")
	}
	if gone2.IsZero() {
		r.closeTarget(target2)
	}
}

func TestLeaseBaselineClock(t *testing.T) {
	r := blSetup(t)
	b := r.broker()
	a, _ := blGrant(t, b, 60, r.origin())
	now := time.Now()
	_, within := b.currentAt(a.claims, now.Add(30*time.Second))
	_, forward := b.currentAt(a.claims, now.Add(61*time.Second))
	_, backward := b.currentAt(a.claims, now.Add(-time.Hour))
	listed := len(b.List().Active)
	short, _ := blGrant(t, b, 2, r.origin())
	removed := blRemoved(b, short.ID)
	removedAt := removed(5 * time.Second)
	_, backAfter := b.currentAt(short.claims, time.Now().Add(-time.Hour))
	_, nowAfter := b.currentAt(short.claims, time.Now())
	blLine(t, "R9-clock-broker", "method", "currentAt-injected-now", "ttl_s", 60, "step_plus_30s", blErr(within), "step_forward_past_exp", blErr(forward),
		"step_backward_1h_while_active", blErr(backward), "listed_active", listed, "short_ttl_s", 2, "timer_removed_minus_exp_ms", blSince(time.Unix(short.Exp, 0), removedAt),
		"step_backward_1h_after_timer", blErr(backAfter), "real_now_after_timer", blErr(nowAfter),
		"live_and_post_send_check", "not-measured-uses-time.Now", "armed_timer_under_wall_step", "not-measured-host-clock-untouched")
	if forward == nil {
		t.Fatal("a check at a time past Exp admitted the grant")
	}
	if !removedAt.IsZero() && backAfter == nil {
		t.Fatal("a grant retired by its timer was admitted at an earlier wall time")
	}
}

func TestLeaseBaselineLimits(t *testing.T) {
	r := blSetup(t)
	b := r.broker()
	var errs []string
	var ids []string
	for i := 0; i < 5; i++ {
		jkt := enc.EncodeToString(random(32))
		p, e := b.register(jkt, open{Kind: Cap, Mode: "request", Allow: r.origin(), TTL: 60}, make(chan struct{}))
		if e != nil {
			errs = append(errs, "register:"+e.Error())
			continue
		}
		e = b.Approve(p.Request)
		errs = append(errs, blErr(e))
		b.mu.Lock()
		for id := range b.active {
			if !strings.Contains(strings.Join(ids, ","), id) {
				ids = append(ids, id)
			}
		}
		b.mu.Unlock()
	}
	for _, id := range ids {
		_ = b.Revoke(id)
	}
	var perr []string
	for i := 0; i < 17; i++ {
		_, e := b.register(enc.EncodeToString(random(32)), open{Kind: Cap, Mode: "request", Allow: r.origin(), TTL: 60}, make(chan struct{}))
		if e != nil {
			perr = append(perr, strconv.Itoa(i+1)+":"+e.Error())
		}
	}
	b.mu.Lock()
	var life time.Duration
	for _, p := range b.pending {
		life = time.Until(p.until)
	}
	for id := range b.pending {
		b.reject(id, rejected)
	}
	b.mu.Unlock()
	jkt := enc.EncodeToString(random(32))
	_, d1 := b.register(jkt, open{Kind: Cap, Mode: "request", Allow: r.origin(), TTL: 60}, make(chan struct{}))
	_, d2 := b.register(jkt, open{Kind: Cap, Mode: "request", Allow: r.origin(), TTL: 60}, make(chan struct{}))
	ttl := func(n int) bool { return b.validRequest(open{Kind: Cap, Mode: "request", Allow: r.origin(), TTL: n}) }
	minTerm, maxTerm := time.Hour, time.Duration(0)
	b.mu.Lock()
	for id := range b.pending {
		b.reject(id, rejected)
	}
	b.mu.Unlock()
	for i := 0; i < 12; i++ {
		a, at := blGrant(t, b, 60, r.origin())
		term := time.Unix(a.Exp, 0).Sub(at)
		minTerm, maxTerm = min(minTerm, term), max(maxTerm, term)
		_ = b.Revoke(a.ID)
		time.Sleep(time.Duration(97*i%1000) * time.Millisecond / 3)
	}
	blLine(t, "R7-limits-broker", "max_ttl_s", MaxTTL, "ttl_86400_valid", ttl(86400), "ttl_86401_valid", ttl(86401), "ttl_0_valid", ttl(0), "ttl_900_valid", ttl(900),
		"approve_1_to_5", strings.Join(errs, ","), "pending_refusals_of_17", strings.Join(perr, ","), "pending_life_s", fmt.Sprintf("%.1f", life.Seconds()),
		"same_holder_second_pending", blErr(d2), "same_holder_first_pending", blErr(d1),
		"term_60s_min_ms", fmt.Sprintf("%.1f", float64(minTerm.Microseconds())/1000), "term_60s_max_ms", fmt.Sprintf("%.1f", float64(maxTerm.Microseconds())/1000))
	b.mu.Lock()
	for id := range b.pending {
		b.reject(id, rejected)
	}
	b.mu.Unlock()
	a, _ := blGrant(t, b, 60, r.origin())
	first := r.do(b, a, "goto", blGoto(r.site.url("/fast?n=l1")))
	if first.err != nil {
		t.Fatalf("setup goto: %v", first.err)
	}
	target := a.tab.target
	op := blRun(b, a, "goto", blGoto(r.site.url("/slow?n=l2")))
	arrived := r.site.wait("slow-arrived", "l2", 5*time.Second)
	time.Sleep(100 * time.Millisecond)
	second := r.do(b, a, "snapshot", "{}")
	op.wait(t, 10*time.Second)
	time.Sleep(500 * time.Millisecond)
	stillOpen := r.site.find("slow-aborted", "l2").IsZero()
	gen0 := blGen(b, a)
	r.site.open("l2")
	released := r.site.wait("slow-released", "l2", 3*time.Second)
	time.Sleep(800 * time.Millisecond)
	gen1 := blGen(b, a)
	late := r.do(b, a, "snapshot", "{}")
	blLine(t, "R7-op-context-broker", "op_context_s", 5, "op_elapsed_ms", blSince(op.start, op.end), "op_err", blErr(op.err), "nav_arrived", !arrived.IsZero(),
		"concurrent_op_err", blErr(second.err), "concurrent_op_ms", blSince(second.start, second.end),
		"browser_request_open_500ms_after_op_refused", stillOpen, "late_nav_committed", !released.IsZero(), "tab_now", r.tabURL(target),
		"broker_generation_changed", gen1 != gen0, "next_snapshot_err", blErr(late.err), "next_snapshot_text", late.text())
	_ = b.Revoke(a.ID)
}

func TestLeaseBaselineAdmissions(t *testing.T) {
	r := blSetup(t)
	b := r.broker()
	a, _ := blGrant(t, b, 60, r.origin())
	_ = b.Revoke(a.ID)
	t0 := time.Now()
	_, cur := b.current(a.claims)
	o := blRun(b, a, "snapshot", "{}")
	o.wait(t, 5*time.Second)
	_, child := b.register(a.CNF.JKT, open{Kind: Cap, Mode: "request", Allow: []Rule{{Kind: "exact", URL: r.site.url("/final/x")}}, Parent: a.ID}, make(chan struct{}))
	p, fresh := b.register(a.CNF.JKT, open{Kind: Cap, Mode: "request", Allow: r.origin(), TTL: 60}, make(chan struct{}))
	pendingAfter := len(b.List().Pending)
	deny := b.Deny(p.Request)
	approveDenied := b.Approve(p.Request)
	d := <-p.deliver
	again := b.Revoke(a.ID)
	b.Close()
	_, closedReg := b.register(enc.EncodeToString(random(32)), open{Kind: Cap, Mode: "request", Allow: r.origin(), TTL: 60}, make(chan struct{}))
	_, closedCur := b.current(a.claims)
	blLine(t, "R8-admissions-broker", "after_revoke_current", blErr(cur), "after_revoke_action", blErr(o.err), "after_revoke_action_ms", blSince(o.start, o.end),
		"after_revoke_expansion_request", blErr(child), "after_revoke_new_request", blErr(fresh), "pending_listed", pendingAfter,
		"owner_deny", blErr(deny), "approve_after_deny", blErr(approveDenied), "holder_decision", blErr(d.err), "revoke_twice", blErr(again),
		"after_close_request", blErr(closedReg), "after_close_current", blErr(closedCur), "close_admissions_op", "none", "checked_ms", blSince(t0, time.Now()))
	if cur == nil || o.err == nil || len(o.sends) != 0 {
		t.Fatal("a revoked grant was admitted")
	}
}
