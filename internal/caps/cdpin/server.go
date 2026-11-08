package cdpin

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	Cap         = "cdp-in"
	Kind        = "cdp"
	DefaultPort = 9333
	OpenTimeout = 30 * time.Second
	attachID    = -2
	hostRefusal = "Host header is specified and is not an IP address or localhost."
)

type Config struct {
	Port        int
	Link        func() *link.Link
	Clients     func(int)
	Logf        func(string, ...any)
	OpenTimeout time.Duration
}

type Server struct {
	cfg  Config
	id   string
	port int
	ln   net.Listener
	srv  *http.Server
	ctx  context.Context
	stop context.CancelFunc

	ready   atomic.Pointer[time.Time]
	mu      sync.Mutex
	sockets map[*socket]struct{}
	closed  bool
	wg      sync.WaitGroup
}

func Listen(cfg Config) (*Server, error) {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Clients == nil {
		cfg.Clients = func(int) {}
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = OpenTimeout
	}
	if cfg.Link == nil {
		cfg.Link = func() *link.Link { return nil }
	}
	lns, port, err := bindAll(cfg.Port)
	if err != nil {
		return nil, err
	}
	ctx, stop := context.WithCancel(context.Background())
	s := &Server{cfg: cfg, id: newID(), port: port, ctx: ctx, stop: stop, sockets: map[*socket]struct{}{}}
	s.ln = Guard(merge(lns, s.admit, cfg.Logf), cfg.Logf)
	s.srv = &http.Server{Handler: s, ReadHeaderTimeout: 30 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	return s, nil
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (s *Server) Port() int { return s.port }

func (s *Server) URL() string { return "http://127.0.0.1:" + strconv.Itoa(s.port) }

func (s *Server) BrowserPath() string { return "/devtools/browser/" + s.id }

func (s *Server) admit() bool {
	at := s.ready.Load()
	return at != nil && !time.Now().Before(*at)
}

func (s *Server) Serve(at time.Time) {
	s.ready.Store(&at)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = s.srv.Serve(s.ln)
	}()
}

func (s *Server) Clients() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sockets)
}

func (s *Server) track(k *socket) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.sockets[k] = struct{}{}
	n := len(s.sockets)
	s.mu.Unlock()
	s.cfg.Clients(n)
	return true
}

func (s *Server) untrack(k *socket) {
	s.mu.Lock()
	if _, ok := s.sockets[k]; !ok {
		s.mu.Unlock()
		return
	}
	delete(s.sockets, k)
	n, closing := len(s.sockets), s.closed
	s.mu.Unlock()
	if !closing {
		s.cfg.Clients(n)
	}
}

func (s *Server) all() []*socket {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*socket, 0, len(s.sockets))
	for k := range s.sockets {
		out = append(out, k)
	}
	return out
}

func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	_ = s.srv.Close()
	if s.ln != nil {
		_ = s.ln.Close()
	}
	var wg sync.WaitGroup
	for _, k := range s.all() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			k.shut(1001, "slicc is stopping")
			select {
			case <-k.st.Done():
			case <-time.After(endWithin):
			}
		}()
	}
	wg.Wait()
	s.stop()
	s.wg.Wait()
}

func validHost(host string) bool {
	h := host
	if strings.HasPrefix(h, "[") {
		end := strings.Index(h, "]")
		if end < 0 {
			return false
		}
		rest := h[end+1:]
		h = h[:end+1]
		if rest != "" && !validPort(strings.TrimPrefix(rest, ":"), rest) {
			return false
		}
		return h == "[::1]"
	}
	if name, port, ok := strings.Cut(h, ":"); ok {
		if !validPort(port, ":"+port) {
			return false
		}
		h = name
	}
	return strings.EqualFold(h, "localhost") || h == "127.0.0.1"
}

func validPort(port, raw string) bool {
	if !strings.HasPrefix(raw, ":") || port == "" {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535
}

func (s *Server) ownOrigins() []string {
	p := strconv.Itoa(s.port)
	return []string{"http://127.0.0.1:" + p, "http://localhost:" + p, "http://[::1]:" + p}
}

func (s *Server) originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	for _, o := range s.ownOrigins() {
		if strings.EqualFold(origin, o) {
			return true
		}
	}
	return false
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !validHost(r.Host) {
		plain(w, http.StatusForbidden, hostRefusal)
		return
	}
	origins := r.Header.Values("Origin")
	site := strings.ToLower(r.Header.Get("Sec-Fetch-Site"))
	if len(origins) > 1 || (len(origins) == 1 && !s.originAllowed(origins[0])) || site == "cross-site" || site == "same-site" {
		s.cfg.Logf("cdp %s %s ← 403 origin", r.Method, pathKind(r.URL.Path))
		plain(w, http.StatusForbidden, "Rejected an incoming connection from another origin.")
		return
	}
	p := r.URL.Path
	switch {
	case p == s.BrowserPath():
		s.serveSocket(w, r, "")
	case strings.HasPrefix(p, "/devtools/page/") && len(p) > len("/devtools/page/"):
		s.serveSocket(w, r, strings.TrimPrefix(p, "/devtools/page/"))
	case p == "/json/version" || p == "/json/version/":
		s.version(w, r)
	case p == "/json" || p == "/json/" || p == "/json/list" || p == "/json/list/":
		s.list(w, r)
	case p == "/json/new":
		s.newTarget(w, r)
	case strings.HasPrefix(p, "/json/activate/"):
		s.targetOp(w, r, "Target.activateTarget", strings.TrimPrefix(p, "/json/activate/"), "Target activated")
	case strings.HasPrefix(p, "/json/close/"):
		s.targetOp(w, r, "Target.closeTarget", strings.TrimPrefix(p, "/json/close/"), "Target is closing")
	default:
		plain(w, http.StatusNotFound, "Unknown command: "+strings.TrimPrefix(p, "/"))
	}
}

func pathKind(p string) string {
	for _, prefix := range []string{"/devtools/browser/", "/devtools/page/", "/json/activate/", "/json/close/"} {
		if strings.HasPrefix(p, prefix) {
			return prefix
		}
	}
	return p
}

func plain(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg))
}

func writeJSON(w http.ResponseWriter, v any) {
	b, _ := json.MarshalIndent(v, "", "   ")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	status, msg := failure(err)
	s.cfg.Logf("cdp %s %s ← %d", r.Method, pathKind(r.URL.Path), status)
	plain(w, status, msg)
}

func failure(err error) (int, string) {
	var rf *Refused
	var ce *CDPError
	switch {
	case errors.Is(err, ErrNoLink):
		return http.StatusBadGateway, ErrNoLink.Error()
	case errors.As(err, &rf) && rf.Code == "EACCES":
		return http.StatusForbidden, rf.Error()
	case errors.As(err, &rf):
		return http.StatusBadGateway, rf.Error()
	case errors.As(err, &ce):
		return http.StatusInternalServerError, ce.Message
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded):
		return http.StatusGatewayTimeout, "seven didn't answer"
	}
	return http.StatusBadGateway, err.Error()
}

func (s *Server) openStream(r *http.Request) (*link.Stream, error) {
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.OpenTimeout)
	defer cancel()
	return open(ctx, s.cfg.Link())
}

func (s *Server) wsBase(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
		if strings.Contains(h, ":") {
			host = "[" + h + "]"
		}
	}
	return "ws://" + host + ":" + strconv.Itoa(s.port)
}

func (s *Server) version(w http.ResponseWriter, r *http.Request) {
	st, err := s.openStream(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer finish(st)
	v, err := openedVersion(st)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, map[string]string{
		"Browser":              v.Product,
		"Protocol-Version":     v.ProtocolVersion,
		"User-Agent":           v.UserAgent,
		"V8-Version":           v.JSVersion,
		"WebKit-Version":       webKitVersion(v),
		"webSocketDebuggerUrl": s.wsBase(r) + s.BrowserPath(),
	})
}

func webKitVersion(v Version) string {
	_, rest, ok := strings.Cut(v.UserAgent, "AppleWebKit/")
	if !ok {
		return ""
	}
	ver, _, _ := strings.Cut(rest, " ")
	if v.Revision == "" {
		return ver
	}
	return ver + " (" + v.Revision + ")"
}

type targetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

type listEntry struct {
	Description string `json:"description"`
	ID          string `json:"id"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	URL         string `json:"url"`
	WSURL       string `json:"webSocketDebuggerUrl"`
}

func (s *Server) entry(r *http.Request, t targetInfo) listEntry {
	return listEntry{ID: t.TargetID, Title: t.Title, Type: t.Type, URL: t.URL, WSURL: s.wsBase(r) + "/devtools/page/" + url.PathEscape(t.TargetID)}
}

func targets(st *link.Stream, id int64, within time.Duration) ([]targetInfo, error) {
	res, err := call(st, id, "Target.getTargets", map[string]any{}, within)
	if err != nil {
		return nil, err
	}
	var out struct {
		TargetInfos []targetInfo `json:"targetInfos"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, &CDPError{Message: "a target list that isn't JSON"}
	}
	return out.TargetInfos, nil
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		plain(w, http.StatusMethodNotAllowed, "Use GET")
		return
	}
	st, err := s.openStream(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer finish(st)
	ts, err := targets(st, 1, s.cfg.OpenTimeout)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []listEntry{}
	for _, t := range ts {
		if t.Type == "browser" || t.Type == "tab" {
			continue
		}
		out = append(out, s.entry(r, t))
	}
	writeJSON(w, out)
}

var catalogProtect = protect.MustNew(nil)

func newURLRefusal(target string) string {
	if target == "" || target == "about:blank" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(target), "about:") {
		return "slicc: refused: a browser-side navigation to about: gives the page the origin of whatever drives the browser, so the guard doesn’t send it; open a new tab on about:blank instead"
	}
	if catalogProtect.URL(target) {
		return protect.Refusal()
	}
	return ""
}

func (s *Server) newTarget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		plain(w, http.StatusMethodNotAllowed, "Using unsafe HTTP verb "+r.Method+" to invoke /json/new. This action supports only PUT verb.")
		return
	}
	target := r.URL.RawQuery
	if u, err := url.QueryUnescape(target); err == nil {
		target = u
	}
	if r.URL.Fragment != "" {
		target += "#" + r.URL.Fragment
	}
	if target == "" {
		target = "about:blank"
	}
	st, err := s.openStream(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer finish(st)
	res, err := call(st, 1, "Target.createTarget", map[string]any{"url": "about:blank"}, s.cfg.OpenTimeout)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var created struct {
		TargetID string `json:"targetId"`
	}
	_ = json.Unmarshal(res, &created)
	if created.TargetID == "" {
		s.fail(w, r, &CDPError{Message: "no target"})
		return
	}
	closeCreated := func(id int64) {
		_, _ = call(st, id, "Target.closeTarget", map[string]any{"targetId": created.TargetID}, s.cfg.OpenTimeout)
	}
	attached, err := call(st, 2, "Target.attachToTarget", map[string]any{"targetId": created.TargetID, "flatten": true}, s.cfg.OpenTimeout)
	if err != nil {
		closeCreated(90)
		s.fail(w, r, err)
		return
	}
	var sess struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(attached, &sess)
	if sess.SessionID == "" {
		closeCreated(91)
		s.fail(w, r, &CDPError{Message: "no session for that target"})
		return
	}
	if reason := newURLRefusal(target); reason != "" {
		closeCreated(92)
		s.fail(w, r, &CDPError{Message: reason})
		return
	}
	if target != "about:blank" {
		if _, err := callOn(st, 3, sess.SessionID, "Page.navigate", map[string]any{"url": target}, s.cfg.OpenTimeout); err != nil {
			closeCreated(93)
			s.fail(w, r, err)
			return
		}
	}
	_, _ = call(st, 4, "Target.detachFromTarget", map[string]any{"sessionId": sess.SessionID}, s.cfg.OpenTimeout)
	info := targetInfo{TargetID: created.TargetID, Type: "page", URL: target}
	if ts, err := targets(st, 5, s.cfg.OpenTimeout); err == nil {
		for _, t := range ts {
			if t.TargetID == created.TargetID {
				info = t
			}
		}
	}
	writeJSON(w, s.entry(r, info))
}

func (s *Server) targetOp(w http.ResponseWriter, r *http.Request, method, id, ok string) {
	if id == "" {
		plain(w, http.StatusNotFound, "No such target id: ")
		return
	}
	if u, err := url.PathUnescape(id); err == nil {
		id = u
	}
	st, err := s.openStream(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer finish(st)
	if _, err := call(st, 1, method, map[string]any{"targetId": id}, s.cfg.OpenTimeout); err != nil {
		s.fail(w, r, err)
		return
	}
	plain(w, http.StatusOK, ok)
}
