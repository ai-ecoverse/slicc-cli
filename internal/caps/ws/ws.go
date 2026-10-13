package ws

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	nethttp "net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
	"github.com/gorilla/websocket"
)

const (
	Kind             = "ws"
	Cap              = "ws"
	HandshakeTimeout = 30 * time.Second
	CloseTimeout     = 5 * time.Second
	bufferSize       = 64 << 10
)

type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

type Options struct {
	Dialer  Dialer
	RootCAs *x509.CertPool
	Logf    func(format string, args ...any)
}

type Opened struct {
	Protocol string `json:"protocol"`
}

func Register(n *node.Node) {
	RegisterOptions(n, Options{Dialer: n.Dialer(), Logf: n.Logf})
}

func RegisterOptions(n *node.Node, o Options) {
	n.Handle(Cap, Kind, Handler(o))
}

func Handler(o Options) link.Handler {
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	return func(s *link.Stream) {
		serve(s, o)
	}
}

func serve(s *link.Stream, o Options) {
	open, err := ParseOpen(s.OpenPayload())
	if err != nil {
		o.Logf("ws: EPROTO, a malformed OPEN")
		_ = s.Reset("EPROTO: " + err.Error())
		return
	}
	name := Name(open.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-s.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	c, err := Dial(ctx, o, open)
	if err != nil {
		if ctx.Err() == nil {
			reason := DialError(err)
			o.Logf("ws %s: %s", name, reason)
			_ = s.Reset(reason)
		}
		return
	}
	defer func() { _ = c.Close() }()
	if err := s.Accept(Opened{Protocol: c.Subprotocol()}); err != nil {
		return
	}
	o.Logf("ws %s: open", name)
	code := relay(ctx, s, c)
	o.Logf("ws %s: %s", name, code)
}

func Dial(ctx context.Context, o Options, open Open) (*websocket.Conn, error) {
	d := &websocket.Dialer{
		NetDialContext:   o.Dialer.DialContext,
		TLSClientConfig:  &tls.Config{RootCAs: o.RootCAs, MinVersion: tls.VersionTLS12},
		HandshakeTimeout: HandshakeTimeout,
		Subprotocols:     open.Protocols,
		ReadBufferSize:   bufferSize,
		WriteBufferSize:  bufferSize,
	}
	c, resp, err := d.DialContext(ctx, open.URL.String(), open.Header)
	if err != nil {
		if resp != nil {
			return nil, &handshakeError{status: resp.StatusCode}
		}
		return nil, err
	}
	switch {
	case c.Subprotocol() != "" && !slices.Contains(open.Protocols, c.Subprotocol()):
		_ = c.Close()
		return nil, &handshakeError{status: nethttp.StatusSwitchingProtocols, why: "the server chose a subprotocol that wasn't offered"}
	case resp.Header.Get("Sec-WebSocket-Extensions") != "":
		_ = c.Close()
		return nil, &handshakeError{status: nethttp.StatusSwitchingProtocols, why: "the server chose an extension that wasn't offered"}
	}
	c.SetReadLimit(MaxMessage)
	return c, nil
}

type handshakeError struct {
	status int
	why    string
}

func (e *handshakeError) Error() string {
	switch {
	case e.why != "":
		return e.why
	case e.status == nethttp.StatusSwitchingProtocols:
		return "the server's WebSocket handshake is invalid"
	default:
		return "HTTP " + strconv.Itoa(e.status)
	}
}

func DialError(err error) string {
	var pe *policy.Error
	if errors.As(err, &pe) {
		return policy.Errno + ": " + pe.Error()
	}
	var he *handshakeError
	if errors.As(err, &he) {
		return "ECONNREFUSED: " + he.Error()
	}
	var ve *tls.CertificateVerificationError
	if errors.As(err, &ve) && ve.Err != nil {
		msg := ve.Err.Error()
		if !strings.HasPrefix(msg, "x509: ") {
			msg = "x509: " + msg
		}
		return "ECONNREFUSED: " + msg
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout() {
		return "ECONNREFUSED: the handshake timed out"
	}
	return "ECONNREFUSED: " + err.Error()
}
