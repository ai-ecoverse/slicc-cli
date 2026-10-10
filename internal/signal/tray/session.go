package tray

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type EventKind int

const (
	EventOffer EventKind = iota + 1
	EventCandidate
	EventRestart
)

func (k EventKind) String() string {
	switch k {
	case EventOffer:
		return "offer"
	case EventCandidate:
		return "candidate"
	case EventRestart:
		return "restart"
	default:
		return "unknown"
	}
}

type Event struct {
	Kind       EventKind
	SDP        string
	Candidate  Candidate
	Attempt    int
	ICEServers []ICEServer
}

type Session struct {
	f            *Follower
	controllerID string
	joinURL      string

	recvMu   sync.Mutex
	queue    []Event
	lastPoll time.Time
	cursor   int
	offered  bool
	started  time.Time

	mu          sync.Mutex
	bootstrapID string
	attempt     int
	iceServers  []ICEServer
	trust       string
	closed      bool
	done        chan struct{}

	trickleMu sync.Mutex
	answered  bool
	pending   []Candidate
}

func (f *Follower) newSession(controllerID, joinURL string, b *bootstrapStatus, resp *hubResponse) *Session {
	return &Session{
		f:            f,
		controllerID: controllerID,
		joinURL:      joinURL,
		bootstrapID:  b.BootstrapID,
		attempt:      b.Attempt,
		iceServers:   resp.IceServers,
		trust:        resp.Trust,
		started:      time.Now(),
		done:         make(chan struct{}),
	}
}

func (s *Session) Trust() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trust
}

func (s *Session) ICEServers() []ICEServer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ICEServer(nil), s.iceServers...)
}

func (s *Session) Attempt() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempt
}

func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.done)
	}
	return nil
}

func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Session) current() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bootstrapID
}

func (s *Session) Recv(ctx context.Context) (Event, error) {
	s.recvMu.Lock()
	defer s.recvMu.Unlock()
	for {
		if s.isClosed() {
			return Event{}, ErrClosed
		}
		if len(s.queue) > 0 {
			ev := s.queue[0]
			s.queue = s.queue[1:]
			return ev, nil
		}
		if !s.offered && time.Since(s.started) > s.f.cfg.OfferTimeout {
			return Event{}, &Error{Code: CodeOfferTimeout, Message: fmt.Sprintf("no offer from the tray leader within %s", s.f.cfg.OfferTimeout), Retryable: true}
		}
		if err := sleepCtx(ctx, s.done, time.Until(s.lastPoll.Add(s.f.cfg.PollInterval))); err != nil {
			return Event{}, err
		}
		if err := s.poll(ctx); err != nil {
			return Event{}, err
		}
	}
}

func (s *Session) poll(ctx context.Context) error {
	bootstrapID := s.current()
	s.lastPoll = time.Now()
	resp, err := s.request(ctx, map[string]any{
		"action":       "poll",
		"controllerId": s.controllerID,
		"bootstrapId":  bootstrapID,
		"cursor":       s.cursor,
	})
	if err != nil {
		return err
	}
	status := resp.Bootstrap
	attempt := status.Attempt
	for _, ev := range resp.Events {
		s.cursor = max(s.cursor, ev.Sequence)
		switch ev.Type {
		case eventOffer:
			if ev.Offer != nil && ev.Offer.SDP != "" {
				s.offered = true
				s.queue = append(s.queue, Event{Kind: EventOffer, SDP: ev.Offer.SDP, Attempt: attempt})
			}
		case eventCandidate:
			if ev.Candidate != nil {
				s.queue = append(s.queue, Event{Kind: EventCandidate, Candidate: *ev.Candidate, Attempt: attempt})
			}
		case eventFailed:
			return s.failed(ctx, status, ev.Failure)
		}
	}
	s.cursor = max(s.cursor, status.Cursor)
	if status.State == bootstrapStateFailed {
		return s.failed(ctx, status, status.Failure)
	}
	return nil
}

func (s *Session) failed(ctx context.Context, status *bootstrapStatus, failure *bootstrapFailure) error {
	if failure == nil {
		failure = status.Failure
	}
	if failure == nil {
		failure = &bootstrapFailure{Code: CodeBadResponse, Message: "bootstrap failed"}
	}
	if !failure.Retryable || status.RetriesRemaining <= 0 {
		return &Error{Code: failure.Code, Message: s.f.red.redact(failure.Message), Retryable: true}
	}
	s.queue = nil
	wait := clampWait(failure.RetryAfterMs)
	if failure.RetryAfterMs == nil {
		wait = clampWait(status.RetryAfterMs)
	}
	s.f.logf("tray: bootstrap failed (%s), retrying in %s", s.f.red.redact(failure.Code), wait)
	if err := sleepCtx(ctx, s.done, wait); err != nil {
		return err
	}
	return s.retry(ctx, status.BootstrapID, failure)
}

func (s *Session) retry(ctx context.Context, oldID string, failure *bootstrapFailure) error {
	resp, err := s.request(ctx, map[string]any{
		"action":       "retry",
		"controllerId": s.controllerID,
		"bootstrapId":  oldID,
		"runtime":      s.f.cfg.Runtime,
	})
	if err != nil {
		return err
	}
	next := resp.Bootstrap
	if next.BootstrapID == oldID {
		return &Error{Code: failure.Code, Message: s.f.red.redact(failure.Message), Retryable: true}
	}
	s.mu.Lock()
	s.bootstrapID = next.BootstrapID
	s.attempt = next.Attempt
	if len(resp.IceServers) > 0 {
		s.iceServers = resp.IceServers
	}
	ice := append([]ICEServer(nil), s.iceServers...)
	s.mu.Unlock()
	s.trickleMu.Lock()
	s.answered = false
	s.pending = nil
	s.trickleMu.Unlock()
	s.cursor = 0
	s.offered = false
	s.started = time.Now()
	s.lastPoll = time.Time{}
	s.queue = []Event{{Kind: EventRestart, Attempt: next.Attempt, ICEServers: ice}}
	return nil
}

func (s *Session) SendAnswer(ctx context.Context, sdp string) error {
	if strings.TrimSpace(sdp) == "" {
		return &Error{Code: "INVALID_ANSWER", Message: "empty answer"}
	}
	s.trickleMu.Lock()
	defer s.trickleMu.Unlock()
	bootstrapID := s.current()
	if _, err := s.request(ctx, map[string]any{
		"action":       "answer",
		"controllerId": s.controllerID,
		"bootstrapId":  bootstrapID,
		"answer":       map[string]any{"type": "answer", "sdp": sdp},
	}); err != nil {
		return err
	}
	s.answered = true
	pending := s.pending
	s.pending = nil
	for _, c := range pending {
		if err := s.postCandidate(ctx, bootstrapID, c); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) SendCandidate(ctx context.Context, c Candidate) error {
	if strings.TrimSpace(c.Candidate) == "" {
		return nil
	}
	s.trickleMu.Lock()
	if !s.answered {
		s.pending = append(s.pending, c)
		s.trickleMu.Unlock()
		return nil
	}
	s.trickleMu.Unlock()
	bootstrapID := s.current()
	return s.postCandidate(ctx, bootstrapID, c)
}

func (s *Session) postCandidate(ctx context.Context, bootstrapID string, c Candidate) error {
	_, err := s.request(ctx, map[string]any{
		"action":       "ice-candidate",
		"controllerId": s.controllerID,
		"bootstrapId":  bootstrapID,
		"candidate":    c,
	})
	return err
}

func (s *Session) request(ctx context.Context, body map[string]any) (*hubResponse, error) {
	if s.isClosed() {
		return nil, ErrClosed
	}
	resp, err := s.f.client.post(ctx, s.joinURL, body)
	if err != nil {
		return nil, err
	}
	if resp.superseded {
		return nil, s.superseded(resp.successor)
	}
	if resp.Result != nil && resp.Result.Action == actionFail {
		return nil, s.f.attachError(resp.Result)
	}
	if resp.Bootstrap == nil || resp.Bootstrap.BootstrapID == "" {
		return nil, &Error{Code: CodeBadResponse, Message: "hub answered without a bootstrap", Retryable: true}
	}
	s.mu.Lock()
	if len(resp.IceServers) > 0 && resp.Bootstrap.BootstrapID == s.bootstrapID {
		s.iceServers = resp.IceServers
	}
	s.mu.Unlock()
	return resp, nil
}

func (s *Session) superseded(next string) error {
	if err := s.f.checkSuccessor(next); err != nil {
		return err
	}
	s.f.logf("tray: superseded during the bootstrap")
	s.f.advance(next)
	return &Error{Code: CodeSuperseded, Message: supersedeMovedMessage, Retryable: true}
}
