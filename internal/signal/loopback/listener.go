package loopback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
)

const (
	FirstPort     = 17117
	LastPort      = 17126
	PairPath      = "/slicc/v1/pair"
	SignalPath    = "/slicc/v1/signal"
	MaxBody       = 64 << 10
	FragmentKey   = "slicc-pair"
	AnswerTimeout = 10 * time.Second
)

type Config struct {
	Avoid         []int
	Identity      *Identity
	Peers         *Peers
	Origins       *signal.Origins
	FirstPort     int
	LastPort      int
	Name          string
	NodeID        string
	AnswerTimeout time.Duration
	Logf          func(format string, args ...any)
	Status        func(format string, args ...any)
}

type Listener struct {
	cfg    Config
	port   int
	lns    []net.Listener
	tokens tokens
	rate   limiter

	mu      sync.Mutex
	conns   map[string]*peerConn
	live    map[string]*link.Link
	serve   func(context.Context, signal.Conn) error
	ctx     context.Context
	closing bool
	wg      sync.WaitGroup
}

func Listen(cfg Config) (*Listener, error) {
	if cfg.FirstPort == 0 {
		cfg.FirstPort, cfg.LastPort = FirstPort, LastPort
	}
	if cfg.LastPort < cfg.FirstPort {
		cfg.LastPort = cfg.FirstPort
	}
	if cfg.AnswerTimeout <= 0 {
		cfg.AnswerTimeout = AnswerTimeout
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Status == nil {
		cfg.Status = func(string, ...any) {}
	}
	preferred := 0
	if cfg.Identity != nil {
		preferred = cfg.Identity.Port
	}
	lns, port, err := bind(preferred, cfg.FirstPort, cfg.LastPort, cfg.Avoid)
	if err != nil {
		return nil, err
	}
	l := &Listener{cfg: cfg, port: port, lns: lns, rate: limiter{max: PairLimit, per: PairWindow}, conns: map[string]*peerConn{}, live: map[string]*link.Link{}}
	if cfg.Identity != nil {
		if err := cfg.Identity.RememberPort(port); err != nil {
			cfg.Logf("loopback: remember port: %v", err)
		}
	}
	return l, nil
}

func bind(preferred, first, last int, avoid []int) ([]net.Listener, int, error) {
	six := ipv6Loopback()
	var ports []int
	if preferred >= first && preferred <= last && !slices.Contains(avoid, preferred) {
		ports = append(ports, preferred)
	}
	for p := first; p <= last; p++ {
		if p != preferred && !slices.Contains(avoid, p) {
			ports = append(ports, p)
		}
	}
	for _, p := range ports {
		l4, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err != nil {
			continue
		}
		lns := []net.Listener{l4}
		if six {
			l6, err := net.Listen("tcp6", net.JoinHostPort("::1", strconv.Itoa(p)))
			if err != nil {
				_ = l4.Close()
				continue
			}
			lns = append(lns, l6)
		}
		return lns, p, nil
	}
	return nil, 0, fmt.Errorf("no free port in %d-%d on 127.0.0.1", first, last)
}

func ipv6Loopback() bool {
	l, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

func (l *Listener) Port() int { return l.port }

func (l *Listener) Addrs() []string {
	out := make([]string, 0, len(l.lns))
	for _, ln := range l.lns {
		out = append(out, ln.Addr().String())
	}
	return out
}

func (l *Listener) Mint() string { return l.tokens.mint(time.Now()) }

func (l *Listener) Valid(tok string) bool { return l.tokens.valid(tok, time.Now()) }

func (l *Listener) Used(tok string) bool { return l.tokens.used(tok) }

func (l *Listener) PairingURL(page, token string) (string, error) {
	u, err := url.Parse(page)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%q is not an http(s) URL", page)
	}
	u.Fragment, u.RawFragment = "", ""
	fp := ""
	if l.cfg.Identity != nil {
		fp = l.cfg.Identity.Fingerprint
	}
	return u.String() + "#" + FragmentKey + "=" + strconv.Itoa(l.port) + "." + token + "." + fp, nil
}

func (l *Listener) Serve(ctx context.Context, serve func(context.Context, signal.Conn) error) error {
	l.mu.Lock()
	l.ctx, l.serve = ctx, serve
	l.mu.Unlock()
	srv := &http.Server{
		Handler:           l,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          log.New(io.Discard, "", 0),
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	for _, ln := range l.lns {
		go func() { _ = srv.Serve(ln) }()
	}
	go l.sweep(ctx)
	<-ctx.Done()
	_ = srv.Close()
	l.mu.Lock()
	l.closing = true
	conns := l.conns
	l.conns = map[string]*peerConn{}
	l.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	l.wg.Wait()
	return nil
}

func (l *Listener) Close() error {
	for _, ln := range l.lns {
		_ = ln.Close()
	}
	return nil
}

func (l *Listener) conn(fp string) *peerConn {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.serve == nil || l.closing {
		return nil
	}
	if c, ok := l.conns[fp]; ok {
		return c
	}
	c := newPeerConn()
	l.conns[fp] = c
	l.wg.Add(1)
	serve, ctx := l.serve, l.ctx
	go func() {
		defer l.wg.Done()
		if err := serve(ctx, c); err != nil {
			l.cfg.Logf("loopback: %s: %v", ShortFingerprint(fp), err)
		}
	}()
	return c
}

func (l *Listener) hostOK(host string) bool {
	host = strings.ToLower(host)
	p := ":" + strconv.Itoa(l.port)
	return host == "127.0.0.1"+p || host == "[::1]"+p || host == "localhost"+p
}

func (l *Listener) origin(r *http.Request) string {
	vals := r.Header.Values("Origin")
	if len(vals) != 1 || !l.cfg.Origins.Allowed(vals[0]) {
		return ""
	}
	return vals[0]
}

func (l *Listener) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !l.hostOK(r.Host) || (r.RequestURI != PairPath && r.RequestURI != SignalPath) {
		l.cfg.Logf("loopback: refused %s: host or path", r.Method)
		forbid(w, "")
		return
	}
	origin := l.origin(r)
	if origin == "" {
		l.cfg.Logf("loopback: refused %s %s: origin", r.Method, r.RequestURI)
		forbid(w, "")
		return
	}
	switch r.Method {
	case http.MethodOptions:
		l.preflight(w, r, origin)
	case http.MethodPost:
		l.post(w, r, origin)
	default:
		forbid(w, origin)
	}
}

func forbid(w http.ResponseWriter, origin string) {
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
}

func (l *Listener) preflight(w http.ResponseWriter, r *http.Request, origin string) {
	if r.Header.Get("Access-Control-Request-Method") != http.MethodPost {
		forbid(w, "")
		return
	}
	for _, h := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
		if h = strings.TrimSpace(h); h != "" && !strings.EqualFold(h, "content-type") {
			forbid(w, "")
			return
		}
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Allow-Methods", "POST")
	h.Set("Access-Control-Allow-Headers", "Content-Type")
	h.Set("Access-Control-Allow-Private-Network", "true")
	h.Set("Access-Control-Max-Age", "600")
	h.Set("Vary", "Origin")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

type request struct {
	Token string `json:"token"`
	Offer string `json:"offer"`
}

func readRequest(r *http.Request) (request, bool) {
	var req request
	if r.ContentLength > MaxBody {
		return req, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err != nil || len(body) > MaxBody {
		return req, false
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' || json.Unmarshal(body, &req) != nil || req.Offer == "" {
		return req, false
	}
	return req, true
}

func (l *Listener) post(w http.ResponseWriter, r *http.Request, origin string) {
	pairing := r.RequestURI == PairPath
	if pairing && !l.rate.allow(time.Now()) {
		l.cfg.Logf("loopback: refused a pairing: more than %d a minute", PairLimit)
		forbid(w, origin)
		return
	}
	req, ok := readRequest(r)
	if !ok {
		l.cfg.Logf("loopback: refused %s: body", r.RequestURI)
		forbid(w, origin)
		return
	}
	fp, err := Fingerprint(req.Offer)
	if err != nil {
		l.cfg.Logf("loopback: refused %s: %v", r.RequestURI, err)
		forbid(w, origin)
		return
	}
	norm := browser.NormalizeOrigin(origin)
	if pairing {
		if !l.tokens.burn(req.Token, time.Now()) {
			l.cfg.Status("refused a pairing request from %s: the token is wrong, used or expired", norm)
			forbid(w, origin)
			return
		}
		if err := l.cfg.Peers.Pin(fp, norm); err != nil {
			l.cfg.Status("could not save the pairing: %v", err)
			forbid(w, origin)
			return
		}
		l.cfg.Status("paired with seven on %s (%s)", norm, ShortFingerprint(fp))
	} else if pinned, ok := l.cfg.Peers.Origin(fp); !ok || pinned != norm {
		l.cfg.Logf("loopback: refused a signal from %s (%s): not paired from there", norm, ShortFingerprint(fp))
		forbid(w, origin)
		return
	}
	l.answer(w, r, origin, norm, fp, req.Offer)
}

func (l *Listener) answer(w http.ResponseWriter, r *http.Request, origin, norm, fp, offer string) {
	c := l.conn(fp)
	if c == nil {
		forbid(w, origin)
		return
	}
	sdp, err := c.offer(r.Context(), norm, offer, l.cfg.AnswerTimeout)
	if err != nil {
		l.cfg.Status("could not answer seven on %s (%s): %v", norm, ShortFingerprint(fp), err)
		forbid(w, origin)
		return
	}
	body, _ := json.Marshal(map[string]string{"answer": sdp, "node": l.cfg.NodeID, "name": l.cfg.Name})
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Vary", "Origin")
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
