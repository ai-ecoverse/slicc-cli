package link

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/signal"
	"github.com/pion/ice/v4"
	pionlogging "github.com/pion/logging"
	"github.com/pion/webrtc/v4"
)

const ReceiveBuffer = 4 << 20

type APIOptions struct {
	Logf     func(format string, args ...any)
	Loopback bool
}

func NewAPI(opts APIOptions) *webrtc.API {
	se := webrtc.SettingEngine{}
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeQueryOnly)
	se.SetSCTPMaxReceiveBufferSize(ReceiveBuffer)
	se.SetIncludeLoopbackCandidate(opts.Loopback)
	se.SetICETimeouts(5*time.Second, 15*time.Second, 2*time.Second)
	se.LoggerFactory = PionLoggerFactory(opts.Logf)
	return webrtc.NewAPI(webrtc.WithSettingEngine(se))
}

func PionLoggerFactory(logf func(format string, args ...any)) pionlogging.LoggerFactory {
	return pionFactory{logf: logf}
}

type pionFactory struct {
	logf func(format string, args ...any)
}

func (f pionFactory) NewLogger(scope string) pionlogging.LeveledLogger {
	return pionLogger{scope: scope, logf: f.logf}
}

type pionLogger struct {
	scope string
	logf  func(format string, args ...any)
}

func (l pionLogger) emit(level, msg string) {
	if l.logf != nil {
		l.logf("pion %s %s: %s", l.scope, level, msg)
	}
}

func (l pionLogger) emitf(level, format string, args ...any) {
	if l.logf != nil {
		l.emit(level, fmt.Sprintf(format, args...))
	}
}

func (l pionLogger) Trace(string)                   {}
func (l pionLogger) Tracef(string, ...any)          {}
func (l pionLogger) Debug(string)                   {}
func (l pionLogger) Debugf(string, ...any)          {}
func (l pionLogger) Info(msg string)                { l.emit("info", msg) }
func (l pionLogger) Infof(format string, a ...any)  { l.emitf("info", format, a...) }
func (l pionLogger) Warn(msg string)                { l.emit("warn", msg) }
func (l pionLogger) Warnf(format string, a ...any)  { l.emitf("warn", format, a...) }
func (l pionLogger) Error(msg string)               { l.emit("error", msg) }
func (l pionLogger) Errorf(format string, a ...any) { l.emitf("error", format, a...) }

func Answer(ctx context.Context, api *webrtc.API, offer signal.Message, send func(context.Context, signal.Message) error, opts Options) (*Link, error) {
	cfg := webrtc.Configuration{Certificates: opts.Certificates}
	for _, s := range offer.ICEServers {
		cfg.ICEServers = append(cfg.ICEServers, webrtc.ICEServer{URLs: s.URLs, Username: s.Username, Credential: s.Credential})
	}
	pc, err := api.NewPeerConnection(cfg)
	if err != nil {
		return nil, err
	}
	l, err := New(pc, RoleNode, opts)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	out := newOutbox(ctx, l.done, send, l.opts.Logf)
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		m := signal.Message{T: signal.TypeCandidate, Nonce: offer.Nonce}
		if c != nil {
			init := c.ToJSON()
			m.Candidate = &signal.Candidate{Candidate: init.Candidate, SDPMid: init.SDPMid, SDPMLineIndex: init.SDPMLineIndex, UsernameFragment: init.UsernameFragment}
		}
		out.push(m)
	})
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer.SDP}); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("link: offer: %w", err)
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("link: answer: %w", err)
	}
	out.push(signal.Message{T: signal.TypeAnswer, Nonce: offer.Nonce, SDP: answer.SDP})
	if err := pc.SetLocalDescription(answer); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("link: answer: %w", err)
	}
	return l, nil
}

func (l *Link) AddCandidate(c *signal.Candidate) error {
	if c == nil || c.Candidate == "" {
		return nil
	}
	return l.pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: c.Candidate, SDPMid: c.SDPMid, SDPMLineIndex: c.SDPMLineIndex, UsernameFragment: c.UsernameFragment})
}

type outbox struct {
	mu    sync.Mutex
	queue []signal.Message
	wake  chan struct{}
}

func newOutbox(ctx context.Context, done <-chan struct{}, send func(context.Context, signal.Message) error, logf func(string, ...any)) *outbox {
	o := &outbox{wake: make(chan struct{}, 1)}
	go func() {
		for {
			o.mu.Lock()
			if len(o.queue) == 0 {
				o.mu.Unlock()
				select {
				case <-o.wake:
					continue
				case <-done:
					return
				case <-ctx.Done():
					return
				}
			}
			m := o.queue[0]
			o.queue = o.queue[1:]
			o.mu.Unlock()
			if err := send(ctx, m); err != nil {
				logf("link: signal %s: %v", m.T, err)
			}
		}
	}()
	return o
}

func (o *outbox) push(m signal.Message) {
	o.mu.Lock()
	o.queue = append(o.queue, m)
	o.mu.Unlock()
	select {
	case o.wake <- struct{}{}:
	default:
	}
}
