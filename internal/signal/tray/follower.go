package tray

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	MaxRedirects          = 5
	DefaultRuntime        = "slicc-link/1"
	DefaultPollInterval   = time.Second
	DefaultOfferTimeout   = 30 * time.Second
	defaultRetryAfter     = time.Second
	maxWaitInterval       = 30 * time.Second
	minWaitInterval       = 50 * time.Millisecond
	actionSignal          = "signal"
	actionWait            = "wait"
	actionFail            = "fail"
	eventOffer            = "bootstrap.offer"
	eventCandidate        = "bootstrap.ice_candidate"
	eventFailed           = "bootstrap.failed"
	bootstrapStateFailed  = "failed"
	supersedeMovedMessage = "the tray moved to a new join URL; dial again"
)

type Config struct {
	JoinURL          string
	State            *JoinState
	Runtime          string
	HTTPClient       *http.Client
	RequestTimeout   time.Duration
	RetryBudget      time.Duration
	RetryBackoff     time.Duration
	PollInterval     time.Duration
	OfferTimeout     time.Duration
	OnJoinURLChanged func(joinURL string)
	Logf             func(format string, args ...any)
}

type Follower struct {
	cfg     Config
	origin  string
	client  *client
	red     *redactor
	mu      sync.Mutex
	current string
}

func NewFollower(cfg Config) (*Follower, error) {
	if _, err := ParseJoinURL(cfg.JoinURL); err != nil {
		return nil, err
	}
	if cfg.Runtime == "" {
		cfg.Runtime = DefaultRuntime
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = DefaultRequestTimeout
	}
	if cfg.RetryBudget <= 0 {
		cfg.RetryBudget = DefaultRetryBudget
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = DefaultRetryBackoff
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.OfferTimeout <= 0 {
		cfg.OfferTimeout = DefaultOfferTimeout
	}
	red := &redactor{}
	origin := strings.TrimSpace(cfg.JoinURL)
	red.add(origin)
	current := origin
	if cfg.State != nil {
		current = cfg.State.Current(origin)
		red.add(current)
	}
	f := &Follower{cfg: cfg, origin: origin, red: red, current: current}
	f.client = &client{
		http:    newHTTPClient(cfg.HTTPClient),
		timeout: cfg.RequestTimeout,
		budget:  cfg.RetryBudget,
		backoff: cfg.RetryBackoff,
		red:     red,
		logf:    f.logf,
	}
	return f, nil
}

func (f *Follower) Redact(s string) string { return f.red.redact(s) }

func (f *Follower) Redirected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current != f.origin
}

func (f *Follower) currentURL() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current
}

func (f *Follower) logf(format string, args ...any) {
	if f.cfg.Logf != nil {
		f.cfg.Logf("%s", f.red.redact(fmt.Sprintf(format, args...)))
	}
}

func (f *Follower) advance(next string) {
	f.red.add(next)
	f.mu.Lock()
	f.current = next
	f.mu.Unlock()
	if f.cfg.State != nil {
		if err := f.cfg.State.Save(f.origin, next); err != nil {
			f.logf("tray: %v", err)
		}
	}
	if f.cfg.OnJoinURLChanged != nil {
		f.cfg.OnJoinURLChanged(next)
	}
}

func (f *Follower) Dial(ctx context.Context) (*Session, error) {
	controllerID := newUUID()
	for redirects := 0; ; {
		joinURL := f.currentURL()
		resp, err := f.attachWait(ctx, joinURL, controllerID)
		if err != nil {
			return nil, err
		}
		if resp.superseded {
			if err := f.followSupersede(resp.successor, redirects); err != nil {
				return nil, err
			}
			redirects++
			controllerID = newUUID()
			continue
		}
		res := resp.Result
		switch res.Action {
		case actionSignal:
			if res.Bootstrap == nil || res.Bootstrap.BootstrapID == "" {
				return nil, &Error{Code: CodeBadResponse, Message: "hub signalled without a bootstrap"}
			}
			if f.cfg.State != nil {
				if err := f.cfg.State.Save(f.origin, joinURL); err != nil {
					f.logf("tray: %v", err)
				}
			}
			return f.newSession(controllerID, joinURL, res.Bootstrap, resp), nil
		case actionFail:
			return nil, f.attachError(res)
		default:
			return nil, &Error{Code: CodeBadResponse, Message: fmt.Sprintf("hub answered attach with action %q", f.red.redact(res.Action))}
		}
	}
}

func (f *Follower) checkSuccessor(next string) error {
	if next == "" {
		return &Error{Code: CodeSupersededMissingJoin, Message: "the tray was superseded without a replacement join URL"}
	}
	if _, err := ParseJoinURL(next); err != nil {
		return &Error{Code: CodeBadResponse, Message: "the tray was superseded by an invalid join URL"}
	}
	return nil
}

func (f *Follower) followSupersede(next string, redirects int) error {
	if err := f.checkSuccessor(next); err != nil {
		return err
	}
	if redirects >= MaxRedirects {
		return &Error{Code: CodeSupersedeExhausted, Message: fmt.Sprintf("the tray moved %d times without settling", MaxRedirects), Retryable: true}
	}
	f.logf("tray: superseded, following redirect %d/%d", redirects+1, MaxRedirects)
	f.advance(next)
	return nil
}

func (f *Follower) attachError(res *attachResult) error {
	code := res.Code
	if code == "" {
		code = CodeBadResponse
	}
	msg := f.red.redact(res.Error)
	if msg == "" {
		msg = "attach failed"
	}
	return &Error{Code: code, Message: msg}
}

func (f *Follower) attachWait(ctx context.Context, joinURL, controllerID string) (*hubResponse, error) {
	for {
		resp, err := f.client.post(ctx, joinURL, map[string]any{"controllerId": controllerID, "runtime": f.cfg.Runtime})
		if err != nil {
			return nil, err
		}
		if resp.superseded {
			return resp, nil
		}
		if resp.Result == nil {
			return nil, &Error{Code: CodeBadResponse, Message: "hub answered attach without a result"}
		}
		if resp.Result.Action != actionWait {
			return resp, nil
		}
		wait := clampWait(resp.Result.RetryAfterMs)
		f.logf("tray: waiting for the leader (%s), next attach in %s", f.red.redact(resp.Result.Code), wait)
		if err := sleepCtx(ctx, nil, wait); err != nil {
			return nil, err
		}
	}
}

func clampWait(ms *int) time.Duration {
	if ms == nil {
		return defaultRetryAfter
	}
	d := time.Duration(*ms) * time.Millisecond
	return min(max(d, minWaitInterval), maxWaitInterval)
}

func sleepCtx(ctx context.Context, done <-chan struct{}, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return ErrClosed
	case <-t.C:
		return nil
	}
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (f *Follower) Rejoin(joinURL string) error {
	joinURL = strings.TrimSpace(joinURL)
	f.red.add(joinURL)
	next, err := ParseJoinURL(joinURL)
	if err != nil {
		return err
	}
	cur, err := ParseJoinURL(f.currentURL())
	if err != nil || !sameOrigin(cur, next) {
		return ErrJoinOrigin
	}
	if f.cfg.State != nil {
		if err := f.cfg.State.Save(f.origin, joinURL); err != nil {
			return err
		}
	}
	f.mu.Lock()
	f.current = joinURL
	f.mu.Unlock()
	if f.cfg.OnJoinURLChanged != nil {
		f.cfg.OnJoinURLChanged(joinURL)
	}
	return nil
}
