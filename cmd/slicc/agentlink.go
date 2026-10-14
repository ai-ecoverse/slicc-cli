//go:build !wasip1

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
	"github.com/ai-ecoverse/slicc-cli/internal/telemetry"
	"github.com/pion/webrtc/v4"
)

type fatalError struct{ msg string }

func (e *fatalError) Error() string { return e.msg }

func linkedEvents(n *node.Node) <-chan *link.Link {
	ch := make(chan *link.Link, 8)
	n.Subscribe(func(e node.Event) {
		if e.Kind != node.EventLinked {
			return
		}
		select {
		case ch <- e.Link:
		default:
		}
	})
	return ch
}

func freshLink(ch <-chan *link.Link) (*link.Link, bool) {
	for {
		select {
		case l := <-ch:
			select {
			case <-l.Done():
				continue
			default:
				return l, true
			}
		default:
			return nil, false
		}
	}
}

type localLinks struct {
	conn   signal.Conn
	client *cdp.Client
	what   string
	links  <-chan *link.Link
	served chan error
	cancel context.CancelFunc
}

func newLocalLinks(ctx context.Context, cdpURL string, origins *signal.Origins, logf func(string, ...any)) (*localLinks, error) {
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := cdp.Dial(dctx, cdpURL)
	if err != nil {
		return nil, err
	}
	conn := signal.NewCDP(signal.CDPOptions{Client: client, Origins: origins, Logf: logf, Status: logf})
	return serveLocal(conn, client, "the CDP connection closed", logf), nil
}

func newStdioLinks(stdin io.Reader, stdout io.Writer, logf func(string, ...any)) (*localLinks, error) {
	conn, err := signal.NewStdio(stdin, stdout)
	if err != nil {
		return nil, err
	}
	return serveLocal(conn, nil, "stdin closed", logf), nil
}

func serveLocal(conn signal.Conn, client *cdp.Client, what string, logf func(string, ...any)) *localLinks {
	n := node.New(node.Config{Version: version, Mode: "local", Logf: logf, Status: logf})
	sctx, scancel := context.WithCancel(context.Background())
	ll := &localLinks{conn: conn, client: client, what: what, links: linkedEvents(n), served: make(chan error, 1), cancel: scancel}
	go func() { ll.served <- n.Serve(sctx, conn) }()
	return ll
}

func (ll *localLinks) next(ctx context.Context) (agentctl.Link, error) {
	if l, ok := freshLink(ll.links); ok {
		return agentLink(l), nil
	}
	select {
	case l := <-ll.links:
		return agentLink(l), nil
	case err := <-ll.served:
		ll.served <- err
		if err == nil {
			err = errors.New(ll.what)
		}
		return nil, &fatalError{msg: err.Error()}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (ll *localLinks) close() {
	ll.cancel()
	select {
	case <-ll.served:
	case <-time.After(5 * time.Second):
	}
	_ = ll.conn.Close()
	if ll.client != nil {
		_ = ll.client.Close()
	}
}

type remoteLinks struct {
	f      *tray.Follower
	node   *node.Node
	links  <-chan *link.Link
	logf   func(string, ...any)
	status func(string, ...any)
	stop   func()
}

func newRemoteLinks(joinURL string, logf, status func(string, ...any)) (*remoteLinks, error) {
	state, err := tray.OpenDefaultState()
	if err != nil {
		return nil, errors.New("no user config directory")
	}
	certs, err := tray.OpenDefaultCerts()
	if err != nil {
		return nil, errors.New("no user config directory")
	}
	f, err := tray.NewFollower(tray.Config{JoinURL: joinURL, State: state, Logf: logf})
	if err != nil {
		return nil, err
	}
	logf = redactingLogf(f, logf)
	pemData, _, err := certs.Load(tray.HubOrigin(joinURL))
	if err != nil {
		return nil, err
	}
	cert, err := webrtc.CertificateFromPEM(pemData)
	if err != nil {
		return nil, fmt.Errorf("the device certificate: %w", err)
	}
	n := node.New(node.Config{Version: version, Mode: "remote", Logf: logf, Status: logf, Certificates: []webrtc.Certificate{*cert}})
	return &remoteLinks{f: f, node: n, links: linkedEvents(n), logf: logf, status: status, stop: func() {}}, nil
}

func (rl *remoteLinks) next(ctx context.Context) (agentctl.Link, error) {
	rl.stop()
	rl.stop = func() {}
	backoff := followBackoffBase
	for {
		l, err := rl.once(ctx)
		if err == nil {
			return agentLink(l), nil
		}
		var fe *fatalError
		if ctx.Err() != nil || errors.As(err, &fe) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		wait := jitter(backoff)
		rl.status("%s; trying again in %s", rl.f.Redact(err.Error()), wait.Round(100*time.Millisecond))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		backoff = min(backoff*2, followBackoffMax)
	}
}

func (rl *remoteLinks) once(ctx context.Context) (*link.Link, error) {
	sess, err := rl.f.Dial(ctx)
	if err != nil {
		return nil, rl.classify(err)
	}
	conn := tray.NewConn(sess)
	sctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- rl.node.Serve(sctx, conn) }()
	stop := func() {
		cancel()
		select {
		case <-served:
		case <-time.After(5 * time.Second):
		}
		_ = conn.Close()
	}
	timer := time.NewTimer(followSetupTimeout)
	defer timer.Stop()
	for {
		select {
		case l := <-rl.links:
			select {
			case <-l.Done():
				continue
			default:
			}
			conn.Settle()
			rl.stop = stop
			return l, nil
		case err := <-served:
			stop()
			if err == nil {
				err = errors.New("seven closed the setup")
			}
			return nil, rl.classify(err)
		case <-timer.C:
			stop()
			return nil, errors.New("the link didn't come up")
		case <-ctx.Done():
			stop()
			return nil, ctx.Err()
		}
	}
}

func (rl *remoteLinks) classify(err error) error {
	code := tray.ErrorCode(err)
	if code == "UNLINKED" {
		return &fatalError{msg: "seven unlinked this device"}
	}
	if msg, ok := fatalFollowCodes[code]; ok {
		telemetry.ReportError(telemetry.Signal, err)
		return &fatalError{msg: fmt.Sprintf("%s (%s)", msg, code)}
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		telemetry.ReportError(telemetry.Signal, err)
	}
	return errors.New(rl.f.Redact(err.Error()))
}

func (rl *remoteLinks) close() {
	rl.stop()
	rl.stop = func() {}
}

func peerName(l *link.Link) string {
	h, _ := l.PeerHello()
	return strings.TrimSpace(leaderName(h))
}

func agentLink(l *link.Link) agentctl.Link {
	return agentctl.FromLink(l, peerName(l))
}
