package vault

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	nethttp "net/http"
	"strconv"
	"sync"
	"time"

	httpcap "github.com/ai-ecoverse/slicc-cli/internal/caps/http"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/tcp"
	"github.com/ai-ecoverse/slicc-cli/internal/jerk"
)

const (
	DefaultOrigin  = "https://vault.sliccy.ai"
	ProofLabel     = "slicc-vault/1"
	MaxRequestBody = 256 << 20
	MaxJerkBody    = 4 << 10
	FetchPath      = "/api/fetch-proxy"
	JerkPath       = "/api/jerk"
	KeySize        = 32
)

var b64 = base64.RawURLEncoding

type Config struct {
	Dialer  httpcap.Dialer
	RootCAs *x509.CertPool
	Version string
	Jerk    *jerk.Jerk
	Logf    func(format string, args ...any)
}

type Listener struct {
	ln      net.Listener
	port    int
	origins map[string]bool

	mu  sync.Mutex
	key []byte

	cfg Config
	tr  nethttp.RoundTripper
	srv *nethttp.Server
}

func Listen(origins []string) (*Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	l := &Listener{ln: ln, port: ln.Addr().(*net.TCPAddr).Port, origins: map[string]bool{}}
	for _, o := range origins {
		l.origins[o] = true
	}
	return l, nil
}

func (l *Listener) Port() int { return l.port }

func (l *Listener) Origins() []string {
	out := make([]string, 0, len(l.origins))
	for o := range l.origins {
		out = append(out, o)
	}
	return out
}

func (l *Listener) SetKey(key []byte) {
	l.mu.Lock()
	l.key = bytes.Clone(key)
	l.mu.Unlock()
}

func (l *Listener) currentKey() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.key
}

func (l *Listener) Serve(cfg Config) {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	l.cfg = cfg
	l.tr = httpcap.NewTransport(httpcap.Options{Dialer: cfg.Dialer, RootCAs: cfg.RootCAs})
	l.srv = &nethttp.Server{Handler: l, ReadHeaderTimeout: 30 * time.Second, MaxHeaderBytes: httpcap.MaxHead + 64<<10, ErrorLog: nil}
	go func() { _ = l.srv.Serve(l.ln) }()
}

func (l *Listener) Close() error {
	if l.srv != nil {
		return l.srv.Close()
	}
	return l.ln.Close()
}

func refuse(w nethttp.ResponseWriter, status int, reason string) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Proxy-Error", "1")
	w.WriteHeader(status)
	b, _ := json.Marshal(map[string]string{"error": reason})
	_, _ = w.Write(b)
}

func (l *Listener) hostOK(host string) bool {
	h, port, err := net.SplitHostPort(host)
	if err != nil || port != strconv.Itoa(l.port) {
		return false
	}
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

func (l *Listener) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	if !l.hostOK(r.Host) {
		refuse(w, 403, "host not allowed")
		return
	}
	path := r.URL.Path
	if path != FetchPath && (path != JerkPath || l.cfg.Jerk == nil) {
		refuse(w, 404, "not found")
		return
	}
	origin := r.Header.Get("Origin")
	if !l.origins[origin] {
		refuse(w, 403, "origin not allowed")
		return
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Expose-Headers", "X-Proxy-Error")
	h.Set("Vary", "Origin")
	if r.Method == nethttp.MethodOptions {
		preflight(w, r)
		return
	}
	if r.Method != nethttp.MethodPost {
		h.Set("Allow", "POST, OPTIONS")
		refuse(w, 405, "method not allowed")
		return
	}
	if path == FetchPath && r.Header.Get("X-Slicc-Raw-Probe") == "1" && r.Header.Get("X-Slicc-Raw-Request") == "" {
		l.probe(w, r, origin)
		return
	}
	key := l.currentKey()
	token := r.Header.Get("X-Bridge-Token")
	if len(key) == 0 || subtle.ConstantTimeCompare([]byte(token), []byte(b64.EncodeToString(key))) != 1 {
		refuse(w, 403, "proxy key missing or wrong")
		return
	}
	if path == JerkPath {
		l.jerk(w, r)
		return
	}
	l.fetch(w, r)
}

func preflight(w nethttp.ResponseWriter, r *nethttp.Request) {
	h := w.Header()
	h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type, X-Bridge-Token, X-Slicc-Raw-Request, X-Slicc-Raw-Probe, X-Slicc-Vault-Challenge")
	h.Set("Access-Control-Max-Age", "600")
	if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
		h.Set("Access-Control-Allow-Private-Network", "true")
	}
	w.WriteHeader(204)
}

func Proof(key []byte, origin, challenge string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(ProofLabel))
	m.Write([]byte{0})
	m.Write([]byte(origin))
	m.Write([]byte{0})
	m.Write([]byte(challenge))
	return b64.EncodeToString(m.Sum(nil))
}

func validChallenge(c string) bool {
	b, err := b64.DecodeString(c)
	return err == nil && len(b) >= 16 && len(b) <= 64
}

func (l *Listener) probe(w nethttp.ResponseWriter, r *nethttp.Request, origin string) {
	challenge, has := r.Header.Get("X-Slicc-Vault-Challenge"), r.Header.Get("X-Slicc-Vault-Challenge") != ""
	if has && !validChallenge(challenge) {
		refuse(w, 400, "malformed X-Slicc-Vault-Challenge header")
		return
	}
	ans := map[string]any{"rawFetch": 1, "requestBodyStreaming": false, "maxRequestBodyBytes": MaxRequestBody, "vault": 1, "version": l.cfg.Version}
	if l.cfg.Jerk != nil {
		ans["jerk"] = 1
	}
	if key := l.currentKey(); has && len(key) > 0 {
		ans["proof"] = Proof(key, origin, challenge)
	}
	writeJSON(w, ans)
}

func writeJSON(w nethttp.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	b, _ := json.Marshal(v)
	_, _ = w.Write(b)
}

func (l *Listener) fetch(w nethttp.ResponseWriter, r *nethttp.Request) {
	raw := r.Header.Get("X-Slicc-Raw-Request")
	if raw == "" {
		refuse(w, 400, "missing X-Slicc-Raw-Request header")
		return
	}
	head, err := httpcap.DecodeHead([]byte(raw))
	if err != nil {
		refuse(w, 400, "malformed X-Slicc-Raw-Request header")
		return
	}
	req, err := httpcap.BuildRequest(head)
	if err != nil {
		refuse(w, 400, "malformed X-Slicc-Raw-Request header")
		return
	}
	if !httpcap.HTTPS443(req.URL) {
		refuse(w, 403, "EACCES: https on port 443 only")
		return
	}
	if req.Method != "GET" && req.Method != "HEAD" {
		body, err := io.ReadAll(io.LimitReader(r.Body, MaxRequestBody+1))
		if err != nil {
			refuse(w, 400, "the request body broke off")
			return
		}
		if len(body) > MaxRequestBody {
			refuse(w, 413, "request body too large")
			return
		}
		req.Body, req.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	}
	resp, err := l.tr.RoundTrip(req.WithContext(r.Context()))
	if err != nil {
		reason := httpcap.Scrub(err, req.URL)
		if code := tcp.Errno(err); code == "EACCES" {
			reason = "EACCES: " + reason
		}
		l.cfg.Logf("vault: %s https://%s: %s", req.Method, req.URL.Host, reason)
		refuse(w, 502, "fetch failed: "+reason)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	l.cfg.Logf("vault: %s https://%s: %d", req.Method, req.URL.Host, resp.StatusCode)
	relay(w, head, resp)
}

func relay(w nethttp.ResponseWriter, head httpcap.RequestHead, resp *nethttp.Response) {
	list := httpcap.HeaderList(resp.Header)
	if httpcap.DecodedPartial(resp.StatusCode, list) {
		refuse(w, 502, "fetch failed: upstream answered a range request with an encoded partial body")
		return
	}
	headers, codings := httpcap.ResponseHeaders(head.Method, resp.StatusCode, list)
	frame, err := httpcap.EncodeHead(httpcap.ResponseHead{Status: resp.StatusCode, StatusText: httpcap.StatusText(resp), Headers: headers, URL: head.URL})
	if err != nil {
		refuse(w, 502, "fetch failed: the response head doesn't encode")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.slicc.raw-fetch")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	if _, err := w.Write(frame); err != nil {
		return
	}
	if !httpcap.HasBody(head.Method, resp.StatusCode) {
		return
	}
	fl, _ := w.(nethttp.Flusher)
	buf := make([]byte, 64<<10)
	src := httpcap.Decode(resp.Body, codings)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			panic(nethttp.ErrAbortHandler)
		}
	}
}

type jerkRequest struct {
	Op       string `json:"op"`
	Provider string `json:"provider"`
	Duration *int   `json:"duration"`
	LeaseID  string `json:"lease_id"`
}

func (l *Listener) jerk(w nethttp.ResponseWriter, r *nethttp.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxJerkBody+1))
	var req jerkRequest
	if err != nil || len(body) > MaxJerkBody || json.Unmarshal(body, &req) != nil || !jerk.ValidProvider(req.Provider) {
		refuse(w, 400, "malformed jerk request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), jerk.Timeout+5*time.Second)
	defer cancel()
	var out jerk.Outcome
	switch req.Op {
	case "lease":
		d := jerk.DefaultDuration
		if req.Duration != nil {
			d = *req.Duration
		}
		if d < 1 || d > jerk.MaxDuration {
			refuse(w, 400, "malformed jerk request")
			return
		}
		out = l.cfg.Jerk.Lease(ctx, req.Provider, d)
	case "drop":
		if !jerk.ValidLeaseID(req.LeaseID) {
			refuse(w, 400, "malformed jerk request")
			return
		}
		out = l.cfg.Jerk.Drop(ctx, req.Provider, req.LeaseID)
	default:
		refuse(w, 400, "malformed jerk request")
		return
	}
	l.cfg.Logf("vault: jerk %s %s: exit %d", req.Provider, req.Op, out.Result.Exit)
	writeJSON(w, out)
}
