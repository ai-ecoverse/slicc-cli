package http

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	nethttp "net/http"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/caps/tcp"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
)

const (
	Kind                = "http"
	Cap                 = "http"
	PrebufferSize       = 1 << 20
	TLSHandshakeTimeout = 30 * time.Second
)

type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

type Options struct {
	Dialer  Dialer
	RootCAs *x509.CertPool
	Logf    func(format string, args ...any)
}

func Register(n *node.Node) {
	n.Handle(Cap, Kind, Handler(Options{Dialer: n.Dialer(), Logf: n.Logf}))
}

func NewTransport(o Options) *nethttp.Transport {
	return &nethttp.Transport{
		Proxy:                 nil,
		DialContext:           o.Dialer.DialContext,
		TLSClientConfig:       &tls.Config{RootCAs: o.RootCAs, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   TLSHandshakeTimeout,
		DisableCompression:    true,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

func Handler(o Options) link.Handler {
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	tr := NewTransport(o)
	return func(s *link.Stream) {
		serve(s, tr, o.Logf)
	}
}

func serve(s *link.Stream, tr nethttp.RoundTripper, logf func(string, ...any)) {
	if err := s.Accept(nil); err != nil {
		return
	}
	head, err := ReadHead(s)
	if err != nil {
		resetRead(s, err)
		return
	}
	req, err := BuildRequest(head)
	if err != nil {
		_ = s.Reset("EPROTO: " + err.Error())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-s.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	b := &body{s: s, closed: make(chan struct{}), drained: make(chan struct{})}
	if err := b.attach(req, DeclaredLength(head.Headers)); err != nil {
		resetRead(s, err)
		return
	}
	resp, err := tr.RoundTrip(req.WithContext(ctx))
	if err != nil {
		if ctx.Err() == nil {
			code := tcp.Errno(err)
			logf("http %s %s://%s: %s: %v", req.Method, req.URL.Scheme, req.URL.Host, code, err)
			_ = s.Reset(code + ": fetch failed: " + err.Error())
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()
	logf("http %s %s://%s: %d", req.Method, req.URL.Scheme, req.URL.Host, resp.StatusCode)
	if relay(s, head, resp) {
		select {
		case <-b.drained:
		case <-s.Done():
		}
	}
}

func resetRead(s *link.Stream, err error) {
	var pe protoError
	if errors.As(err, &pe) {
		_ = s.Reset("EPROTO: " + pe.Error())
	}
}

func relay(s *link.Stream, head RequestHead, resp *nethttp.Response) bool {
	list := HeaderList(resp.Header)
	if DecodedPartial(resp.StatusCode, list) {
		_ = s.Reset("EPROTO: upstream answered a range request with an encoded partial body")
		return false
	}
	headers, codings := ResponseHeaders(head.Method, resp.StatusCode, list)
	out := ResponseHead{Status: resp.StatusCode, StatusText: StatusText(resp), Headers: headers, URL: head.URL}
	frame, err := EncodeHead(out)
	if err != nil {
		_ = s.Reset("EPROTO: " + err.Error())
		return false
	}
	if _, err := s.Write(frame); err != nil {
		return false
	}
	if HasBody(head.Method, resp.StatusCode) {
		if err := copyBody(s, Decode(resp.Body, codings)); err != nil {
			_ = s.Reset("ECONNRESET: " + err.Error())
			return false
		}
	}
	return s.CloseWrite() == nil
}

func copyBody(s *link.Stream, r io.Reader) error {
	buf := make([]byte, link.MaxPayload)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := s.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func EncodeHead(v any) ([]byte, error) {
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 0, 0})
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	b := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	binary.BigEndian.PutUint32(b, uint32(len(b)-4))
	return b, nil
}

type body struct {
	s       *link.Stream
	mu      sync.Mutex
	pre     []byte
	once    sync.Once
	closed  chan struct{}
	drained chan struct{}
}

func (b *body) attach(req *nethttp.Request, declared int64) error {
	if req.Method == "GET" || req.Method == "HEAD" {
		req.Body, req.ContentLength = nil, 0
		go b.drain()
		return nil
	}
	pre, complete, err := prebuffer(b.s, PrebufferSize)
	if err != nil {
		return err
	}
	switch {
	case complete && len(pre) == 0:
		req.Body, req.ContentLength = nethttp.NoBody, 0
		close(b.drained)
	case complete:
		req.Body, req.ContentLength = io.NopCloser(bytes.NewReader(pre)), int64(len(pre))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(pre)), nil }
		close(b.drained)
	default:
		b.pre = pre
		req.Body, req.ContentLength = b, declared
		go func() {
			select {
			case <-b.closed:
				b.drain()
			case <-b.s.Done():
			}
		}()
	}
	return nil
}

func prebuffer(r io.Reader, limit int) ([]byte, bool, error) {
	var buf bytes.Buffer
	chunk := make([]byte, link.MaxPayload)
	for buf.Len() < limit {
		n, err := r.Read(chunk)
		buf.Write(chunk[:n])
		if errors.Is(err, io.EOF) {
			return buf.Bytes(), true, nil
		}
		if err != nil {
			return nil, false, err
		}
	}
	return buf.Bytes(), false, nil
}

func (b *body) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pre) > 0 {
		n := copy(p, b.pre)
		b.pre = b.pre[n:]
		return n, nil
	}
	return b.s.Read(p)
}

func (b *body) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func (b *body) drain() {
	defer close(b.drained)
	_, _ = io.Copy(io.Discard, struct{ io.Reader }{b})
}
