package signal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	BindingName   = "sliccLinkSend"
	ReceiverName  = "sliccLinkReceive"
	SetupTimeout  = 10 * time.Second
	RetryMin      = time.Second
	RetryMax      = 30 * time.Second
	ReloadTimeout = 30 * time.Second
	CrashReloads  = 3
	CrashWindow   = 5 * time.Minute
	QuietEnv      = "SLICC_SEVEN_QUIET"
	QuietTimeout  = 15 * time.Second
	NoSevenTab    = "still no seven tab. Open seven in this browser. If it's open and slicc can't see it, the browser hides it from debugging clients, so link it through seven's Link a device with slicc <join-url> follow"
	KeepsCrashing = "the seven tab keeps crashing. Reload it and slicc links it again"
	ReadyEnv      = "SLICC_SEVEN_READY"
	ReadyTimeout  = 5 * time.Second
	WaitNoTab     = "no-seven-tab"
	WaitNotReady  = "no-ready-seven"
	MovedTab      = "moved to another seven tab"
)

type Waiting struct {
	Reason     string
	Candidates int
	Message    string
}

func NotReady(n int) string {
	if n <= 1 {
		return "the seven tab isn't ready to link yet; waiting for it. Reload it if this lasts"
	}
	return fmt.Sprintf("%d seven tabs are open and none is ready to link yet; waiting for one. seven runs in one tab per profile, so close the extra tabs or reload the one you use if this lasts", n)
}

const probeTimeout = 2 * time.Second

const probeExpr = "[typeof globalThis." + ReceiverName + " === 'function', document.visibilityState === 'visible']"

const crashGrace = time.Second

type CDPOptions struct {
	Client        *cdp.Client
	Origins       *Origins
	Tab           func(url string) bool
	Logf          func(format string, args ...any)
	Status        func(format string, args ...any)
	Waiting       func(Waiting)
	SetupTimeout  time.Duration
	ReadyTimeout  time.Duration
	RetryMin      time.Duration
	RetryMax      time.Duration
	ReloadTimeout time.Duration
}

type CDP struct {
	opts CDPOptions

	mu         sync.Mutex
	session    *cdp.Session
	ctxID      int
	nonce      string
	origin     string
	hostTarget string

	reloads []time.Time
	avoid   string
	prefer  string
	waited  string

	wake      chan struct{}
	due       string
	retryWait time.Duration
	retryAt   *time.Timer

	in   chan Message
	err  error
	done chan struct{}
	ran  chan struct{}
	once sync.Once
	stop context.CancelFunc
}

var localNetworkPermissions = []string{"local-network-access", "local-network", "loopback-network"}

func NewCDP(opts CDPOptions) *CDP {
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	if opts.Status == nil {
		opts.Status = func(string, ...any) {}
	}
	if opts.SetupTimeout <= 0 {
		opts.SetupTimeout = SetupTimeout
	}
	if opts.RetryMin <= 0 {
		opts.RetryMin = RetryMin
	}
	if opts.RetryMax < opts.RetryMin {
		opts.RetryMax = max(RetryMax, opts.RetryMin)
	}
	if opts.ReloadTimeout <= 0 {
		opts.ReloadTimeout = ReloadTimeout
	}
	if opts.ReadyTimeout <= 0 {
		opts.ReadyTimeout = readyFor()
	}
	if opts.Tab == nil {
		opts.Tab = func(string) bool { return true }
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &CDP{opts: opts, in: make(chan Message, 64), done: make(chan struct{}), ran: make(chan struct{}), stop: cancel, wake: make(chan struct{}, 1), retryWait: opts.RetryMin}
	go func() {
		defer close(c.ran)
		c.run(ctx)
	}()
	return c
}

func (c *CDP) Recv(ctx context.Context) (Message, error) {
	select {
	case m := <-c.in:
		return m, nil
	case <-c.done:
		return Message{}, c.err
	case <-ctx.Done():
		return Message{}, ctx.Err()
	}
}

func (c *CDP) Send(ctx context.Context, m Message) error {
	c.mu.Lock()
	s, ctxID, nonce := c.session, c.ctxID, c.nonce
	c.mu.Unlock()
	if s == nil || ctxID == 0 || m.Nonce != nonce {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	expr := "globalThis." + ReceiverName + "?.(" + string(b) + ")"
	return s.Evaluate(ctx, expr, cdp.EvaluateOptions{ContextID: ctxID}, nil)
}

func (c *CDP) Close() error {
	c.finish(ErrClosed)
	<-c.ran
	return nil
}

func (c *CDP) HostTarget() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hostTarget
}

func (c *CDP) setHostTarget(id string) {
	c.mu.Lock()
	c.hostTarget = id
	c.mu.Unlock()
}

const detachTimeout = 2 * time.Second

func (c *CDP) detach(s *cdp.Session) {
	c.mu.Lock()
	if c.session == s {
		c.session = nil
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), detachTimeout)
	defer cancel()
	if err := s.Detach(ctx); err != nil {
		c.opts.Logf("signal: detach from the seven tab: %v", err)
	}
}

func (c *CDP) finish(err error) {
	c.once.Do(func() {
		c.err = err
		c.stop()
		c.mu.Lock()
		c.unschedule()
		c.mu.Unlock()
		close(c.done)
	})
}

func (c *CDP) Linked(nonce string) {
	c.mu.Lock()
	if nonce != "" && nonce == c.nonce {
		c.retryWait = c.opts.RetryMin
		c.unschedule()
	}
	c.mu.Unlock()
}

func (c *CDP) Failed(nonce string, err error) {
	c.mu.Lock()
	if nonce == "" || nonce != c.nonce || c.ctxID == 0 || c.closed() {
		c.mu.Unlock()
		return
	}
	wait := c.retryWait
	c.retryWait = min(wait*2, c.opts.RetryMax)
	c.unschedule()
	var t *time.Timer
	t = time.AfterFunc(wait, func() {
		c.mu.Lock()
		if c.retryAt != t {
			c.mu.Unlock()
			return
		}
		c.retryAt = nil
		c.due = nonce
		c.mu.Unlock()
		select {
		case c.wake <- struct{}{}:
		default:
		}
	})
	c.retryAt = t
	c.mu.Unlock()
	c.opts.Status("link failed (%v); asking seven to set it up again in %s", err, wait)
}

func (c *CDP) unschedule() {
	if c.retryAt != nil {
		c.retryAt.Stop()
		c.retryAt = nil
	}
	c.due = ""
}

func (c *CDP) closed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *CDP) emit(ctx context.Context, m Message) {
	select {
	case c.in <- m:
	case <-ctx.Done():
	}
}

func (c *CDP) run(ctx context.Context) {
	wait := c.opts.RetryMin
	for {
		linked, err := c.round(ctx)
		if end := c.ended(ctx); end != nil {
			c.finish(end)
			return
		}
		if linked {
			wait = c.opts.RetryMin
		}
		if err == nil {
			continue
		}
		c.opts.Status("couldn't link the seven tab (%v), trying again in %s", err, wait)
		c.sleep(ctx, wait)
		wait = min(wait*2, c.opts.RetryMax)
	}
}

func (c *CDP) ended(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.opts.Client.Err(); err != nil {
		return err
	}
	return nil
}

func (c *CDP) sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	case <-c.opts.Client.Done():
	}
}

func (c *CDP) round(ctx context.Context) (bool, error) {
	sub := c.opts.Client.Subscribe()
	defer sub.Close()
	target, err := c.findTarget(ctx, sub)
	if err != nil {
		return false, err
	}
	linked, err := c.serveTarget(ctx, sub, target)
	if errors.Is(err, errTargetCrashed) {
		if !linked {
			c.opts.Status("seven tab crashed")
		}
		err = c.recover(ctx, sub, target)
	}
	switch {
	case err == nil, errors.Is(err, errTargetGone), errors.Is(err, errTargetMoved):
		return linked, nil
	case ctx.Err() == nil && c.gone(ctx, target.TargetID):
		c.opts.Logf("signal: the seven tab went away during setup: %v", err)
		return linked, nil
	}
	return linked, err
}

func (c *CDP) gone(ctx context.Context, targetID string) bool {
	var targets []cdp.TargetInfo
	if err := c.bounded(ctx, func(cctx context.Context) (err error) {
		targets, err = c.opts.Client.Targets(cctx)
		return err
	}); err != nil {
		return false
	}
	for _, t := range targets {
		if t.TargetID == targetID {
			return false
		}
	}
	return true
}

func (c *CDP) findTarget(ctx context.Context, sub *cdp.Subscription) (cdp.TargetInfo, error) {
	cl := c.opts.Client
	if err := cl.Call(ctx, "", "Target.setDiscoverTargets", map[string]any{"discover": true}, nil); err != nil {
		return cdp.TargetInfo{}, err
	}
	targets, err := cl.Targets(ctx)
	if err != nil {
		return cdp.TargetInfo{}, err
	}
	if found := c.candidates(targets); len(found) > 0 {
		return c.choose(ctx, found), nil
	}
	c.opts.Status("waiting for a seven tab")
	quiet := time.After(quietFor())
	for {
		select {
		case <-quiet:
			c.waiting(Waiting{Reason: WaitNoTab, Message: NoSevenTab})
		case ev, ok := <-sub.Events():
			if !ok {
				return cdp.TargetInfo{}, cl.Err()
			}
			if t, ok := c.sevenPage(ev); ok {
				return t, nil
			}
		case <-ctx.Done():
			return cdp.TargetInfo{}, ctx.Err()
		}
	}
}

func (c *CDP) accepts(t cdp.TargetInfo) bool {
	return t.Type == "page" && c.opts.Origins.AllowedURL(t.URL) && c.opts.Tab(t.URL)
}

func (c *CDP) candidates(targets []cdp.TargetInfo) []cdp.TargetInfo {
	var out []cdp.TargetInfo
	for _, t := range targets {
		if c.accepts(t) {
			out = append(out, t)
		}
	}
	return out
}

func (c *CDP) choose(ctx context.Context, found []cdp.TargetInfo) cdp.TargetInfo {
	prefer := c.prefer
	c.prefer = ""
	for _, t := range found {
		if prefer != "" && t.TargetID == prefer {
			return t
		}
	}
	if len(found) == 1 {
		return found[0]
	}
	best, score := 0, -1
	for i, t := range found {
		s := 0
		if t.TargetID != c.avoid {
			s = 4
			r := Probe(ctx, c.opts.Client, t.TargetID)
			if r.Ready {
				s += 2
			}
			if r.Visible {
				s++
			}
		}
		if s > score {
			best, score = i, s
		}
	}
	c.opts.Logf("signal: %d seven tabs; chose #%d (ready %t, visible %t)", len(found), best+1, score&2 != 0, score&1 != 0)
	return found[best]
}

type Readiness struct {
	Ready   bool
	Visible bool
}

func Probe(ctx context.Context, cl *cdp.Client, targetID string) Readiness {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	s, err := cl.Attach(pctx, targetID)
	if err != nil {
		return Readiness{}
	}
	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), detachTimeout)
		defer cancel()
		_ = s.Detach(dctx)
	}()
	var v []bool
	if err := s.Evaluate(pctx, probeExpr, cdp.EvaluateOptions{ReturnByValue: true}, &v); err != nil || len(v) != 2 {
		return Readiness{}
	}
	return Readiness{Ready: v[0], Visible: v[1]}
}

func (c *CDP) waiting(w Waiting) {
	key := w.Reason + "/" + strconv.Itoa(w.Candidates)
	c.mu.Lock()
	same := c.waited == key
	c.waited = key
	c.mu.Unlock()
	if same {
		return
	}
	if c.opts.Waiting != nil {
		c.opts.Waiting(w)
		return
	}
	c.opts.Status("%s", w.Message)
}

func (c *CDP) ready() {
	c.mu.Lock()
	c.waited = ""
	c.mu.Unlock()
}

func (c *CDP) sevenPage(ev cdp.Event) (cdp.TargetInfo, bool) {
	if ev.SessionID != "" || (ev.Method != "Target.targetCreated" && ev.Method != "Target.targetInfoChanged") {
		return cdp.TargetInfo{}, false
	}
	var p struct {
		TargetInfo cdp.TargetInfo `json:"targetInfo"`
	}
	if json.Unmarshal(ev.Params, &p) != nil || p.TargetInfo.Type != "page" || !c.opts.Origins.AllowedURL(p.TargetInfo.URL) || !c.opts.Tab(p.TargetInfo.URL) {
		return cdp.TargetInfo{}, false
	}
	return p.TargetInfo, true
}

func quietFor() time.Duration {
	if d, err := time.ParseDuration(os.Getenv(QuietEnv)); err == nil && d > 0 {
		return d
	}
	return QuietTimeout
}

func readyFor() time.Duration {
	if d, err := time.ParseDuration(os.Getenv(ReadyEnv)); err == nil && d > 0 {
		return d
	}
	return ReadyTimeout
}

func (c *CDP) serveTarget(ctx context.Context, sub *cdp.Subscription, t cdp.TargetInfo) (bool, error) {
	s, early, err := c.setup(ctx, sub, t)
	if err != nil {
		return false, err
	}
	c.avoid = ""
	c.setHostTarget(t.TargetID)
	w := &watch{c: c, s: s, targetID: t.TargetID, mainFrame: t.TargetID}
	err = w.loop(ctx, sub, early)
	c.setHostTarget("")
	c.detach(s)
	return true, err
}

func (c *CDP) bounded(ctx context.Context, fn func(context.Context) error) error {
	cctx, cancel := context.WithTimeout(ctx, c.opts.SetupTimeout)
	defer cancel()
	return fn(cctx)
}

func (c *CDP) setup(ctx context.Context, sub *cdp.Subscription, t cdp.TargetInfo) (*cdp.Session, []cdp.Event, error) {
	sctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var sid atomic.Value
	sid.Store("")
	type result struct {
		s   *cdp.Session
		err error
	}
	done := make(chan result, 1)
	go func() {
		s, err := c.prepare(sctx, t, func(id string) { sid.Store(id) })
		done <- result{s, err}
	}()
	var early []cdp.Event
	events := sub.Events()
	for {
		select {
		case r := <-done:
			if r.err == nil {
				return r.s, early, nil
			}
			if cause := context.Cause(sctx); errors.Is(cause, errTargetCrashed) || errors.Is(cause, errTargetGone) {
				return nil, nil, cause
			}
			return nil, nil, r.err
		case ev, ok := <-events:
			if !ok {
				events = nil
				cancel(c.opts.Client.Err())
				continue
			}
			early = append(early, ev)
			if err := targetEvent(ev, t.TargetID, sid.Load().(string)); err != nil {
				cancel(err)
			}
		}
	}
}

func (c *CDP) prepare(ctx context.Context, t cdp.TargetInfo, attached func(string)) (*cdp.Session, error) {
	var s *cdp.Session
	if err := c.bounded(ctx, func(cctx context.Context) (err error) {
		s, err = c.opts.Client.Attach(cctx, t.TargetID)
		return err
	}); err != nil {
		return nil, err
	}
	attached(s.ID)
	if err := c.bounded(ctx, func(cctx context.Context) error { return s.Call(cctx, "Inspector.enable", nil, nil) }); err != nil {
		var ce *cdp.Error
		if !errors.As(err, &ce) {
			c.detach(s)
			return nil, err
		}
		c.opts.Logf("signal: Inspector.enable: %v", err)
	}
	if err := c.bounded(ctx, func(cctx context.Context) error { return s.Call(cctx, "Page.enable", nil, nil) }); err != nil {
		c.detach(s)
		return nil, err
	}
	c.grantLocalNetwork(ctx, t.URL)
	if err := c.bounded(ctx, func(cctx context.Context) error { return s.AddBinding(cctx, BindingName) }); err != nil {
		c.detach(s)
		return nil, err
	}
	c.mu.Lock()
	c.session = s
	c.mu.Unlock()
	if err := c.bounded(ctx, func(cctx context.Context) error { return s.Call(cctx, "Runtime.enable", nil, nil) }); err != nil {
		c.detach(s)
		return nil, err
	}
	c.opts.Status("found seven at %s", protect.Redact(t.URL))
	return s, nil
}

func (c *CDP) recover(ctx context.Context, sub *cdp.Subscription, t cdp.TargetInfo) error {
	var s *cdp.Session
	if err := c.bounded(ctx, func(cctx context.Context) (err error) {
		s, err = c.opts.Client.Attach(cctx, t.TargetID)
		return err
	}); err != nil {
		return err
	}
	defer c.detach(s)
	if err := c.bounded(ctx, func(cctx context.Context) error { return s.Call(cctx, "Inspector.enable", nil, nil) }); err != nil {
		return err
	}
	crashed, err := c.stillCrashed(ctx, sub, t.TargetID, s.ID)
	if err != nil || !crashed {
		return err
	}
	var limit <-chan time.Time
	if c.mayReload(time.Now()) {
		c.opts.Status("reloading the seven tab")
		go c.reload(ctx, s)
		timer := time.NewTimer(c.opts.ReloadTimeout)
		defer timer.Stop()
		limit = timer.C
	} else {
		c.opts.Status("%s", KeepsCrashing)
	}
	return c.revived(ctx, sub, t.TargetID, s.ID, limit)
}

func (c *CDP) reload(ctx context.Context, s *cdp.Session) {
	if err := c.bounded(ctx, func(cctx context.Context) error { return s.Call(cctx, "Page.reload", nil, nil) }); err != nil && ctx.Err() == nil {
		c.opts.Logf("signal: reload the seven tab: %v", err)
	}
}

func (c *CDP) mayReload(now time.Time) bool {
	kept := c.reloads[:0]
	for _, at := range c.reloads {
		if now.Sub(at) < CrashWindow {
			kept = append(kept, at)
		}
	}
	c.reloads = kept
	if len(kept) >= CrashReloads {
		return false
	}
	c.reloads = append(c.reloads, now)
	return true
}

func (c *CDP) stillCrashed(ctx context.Context, sub *cdp.Subscription, targetID, sessionID string) (bool, error) {
	grace := time.NewTimer(crashGrace)
	defer grace.Stop()
	for {
		select {
		case ev, ok := <-sub.Events():
			if !ok {
				return false, c.opts.Client.Err()
			}
			if ev.SessionID == sessionID && ev.Method == "Inspector.targetCrashed" {
				return true, nil
			}
			if err := targetEvent(ev, targetID, sessionID); errors.Is(err, errTargetGone) {
				return false, err
			}
		case <-grace.C:
			c.opts.Logf("signal: the seven tab is running again")
			return false, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

func (c *CDP) revived(ctx context.Context, sub *cdp.Subscription, targetID, sessionID string, limit <-chan time.Time) error {
	for {
		select {
		case ev, ok := <-sub.Events():
			if !ok {
				return c.opts.Client.Err()
			}
			if ev.SessionID == sessionID && ev.Method == "Inspector.targetReloadedAfterCrash" {
				c.opts.Logf("signal: the seven tab is back")
				return nil
			}
			if err := targetEvent(ev, targetID, sessionID); errors.Is(err, errTargetGone) {
				return err
			}
			if t, ok := c.sevenPage(ev); ok && t.TargetID != targetID {
				c.avoid = targetID
				return nil
			}
		case <-limit:
			c.opts.Logf("signal: the reloaded seven tab didn't come back within %s", c.opts.ReloadTimeout)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *CDP) grantLocalNetwork(ctx context.Context, rawURL string) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	origin := u.Scheme + "://" + u.Host
	for _, name := range localNetworkPermissions {
		params := map[string]any{"permission": map[string]any{"name": name}, "setting": "granted", "origin": origin}
		if err := c.bounded(ctx, func(cctx context.Context) error {
			return c.opts.Client.Call(cctx, "", "Browser.setPermission", params, nil)
		}); err != nil {
			c.opts.Logf("signal: grant %s to %s: %v", name, origin, err)
		}
	}
}

type watch struct {
	c           *CDP
	s           *cdp.Session
	targetID    string
	mainFrame   string
	cancel      context.CancelFunc
	helloDone   chan struct{}
	mainContext cdp.ExecutionContext
	delivered   atomic.Bool
	readyAt     *time.Timer
}

var (
	errTargetGone    = errors.New("signal: seven tab is gone")
	errTargetCrashed = errors.New("signal: seven tab crashed")
	errTargetMoved   = errors.New("signal: moved to another seven tab")
)

func targetEvent(ev cdp.Event, targetID, sessionID string) error {
	if ev.SessionID != "" {
		if ev.SessionID == sessionID && ev.Method == "Inspector.targetCrashed" {
			return errTargetCrashed
		}
		return nil
	}
	switch ev.Method {
	case "Target.detachedFromTarget":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		if sessionID != "" && json.Unmarshal(ev.Params, &p) == nil && p.SessionID == sessionID {
			return errTargetGone
		}
	case "Target.targetDestroyed", "Target.targetCrashed":
		var p struct {
			TargetID string `json:"targetId"`
		}
		if json.Unmarshal(ev.Params, &p) != nil || p.TargetID != targetID {
			return nil
		}
		if ev.Method == "Target.targetCrashed" {
			return errTargetCrashed
		}
		return errTargetGone
	}
	return nil
}

func (w *watch) loop(ctx context.Context, sub *cdp.Subscription, early []cdp.Event) error {
	reason := "seven tab closed"
	w.readyAt = time.NewTimer(w.c.opts.ReadyTimeout)
	defer w.readyAt.Stop()
	defer func() { w.drop(ctx, reason) }()
	end := func(err error) error {
		switch {
		case errors.Is(err, errTargetGone):
			w.c.opts.Status("seven tab closed")
			return nil
		case errors.Is(err, errTargetMoved):
			reason = MovedTab
			return err
		case errors.Is(err, errTargetCrashed):
			reason = "seven tab crashed"
			w.c.opts.Status("seven tab crashed")
		}
		return err
	}
	for _, ev := range early {
		if err := w.handle(ctx, ev); err != nil {
			return end(err)
		}
	}
	for {
		select {
		case ev, ok := <-sub.Events():
			if !ok {
				return w.c.opts.Client.Err()
			}
			if err := w.handle(ctx, ev); err != nil {
				return end(err)
			}
		case <-w.c.wake:
			w.retry(ctx)
		case <-w.readyAt.C:
			if err := w.checkReady(ctx); err != nil {
				return end(err)
			}
			w.readyAt.Reset(w.c.opts.ReadyTimeout)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (w *watch) restartReady() {
	w.delivered.Store(false)
	if w.readyAt != nil {
		w.readyAt.Reset(w.c.opts.ReadyTimeout)
	}
}

func (w *watch) checkReady(ctx context.Context) error {
	if w.delivered.Load() {
		return nil
	}
	var targets []cdp.TargetInfo
	if err := w.c.bounded(ctx, func(cctx context.Context) (err error) {
		targets, err = w.c.opts.Client.Targets(cctx)
		return err
	}); err != nil {
		w.c.opts.Logf("signal: list tabs: %v", err)
		return nil
	}
	here := false
	var others []cdp.TargetInfo
	for _, t := range w.c.candidates(targets) {
		if t.TargetID == w.targetID {
			here = true
		} else {
			others = append(others, t)
		}
	}
	for _, t := range others {
		if t.TargetID != w.c.avoid && Probe(ctx, w.c.opts.Client, t.TargetID).Ready {
			w.c.prefer = t.TargetID
			w.c.opts.Status("the seven tab isn't ready to link; moving to another seven tab that is")
			return errTargetMoved
		}
	}
	if !here && len(others) > 0 {
		w.c.prefer = others[0].TargetID
		w.c.opts.Status("the seven tab left seven; moving to another seven tab")
		return errTargetMoved
	}
	if !here {
		w.c.waiting(Waiting{Reason: WaitNoTab, Message: NoSevenTab})
		return nil
	}
	n := len(others) + 1
	w.c.waiting(Waiting{Reason: WaitNotReady, Candidates: n, Message: NotReady(n)})
	return nil
}

func (w *watch) handle(ctx context.Context, ev cdp.Event) error {
	if err := targetEvent(ev, w.targetID, w.s.ID); err != nil {
		return err
	}
	if ev.SessionID == "" || ev.SessionID != w.s.ID {
		return nil
	}
	switch ev.Method {
	case "Runtime.executionContextCreated":
		var p struct {
			Context cdp.ExecutionContext `json:"context"`
		}
		if json.Unmarshal(ev.Params, &p) == nil {
			w.contextCreated(ctx, p.Context)
		}
	case "Runtime.executionContextDestroyed":
		var p struct {
			ID int `json:"executionContextId"`
		}
		if json.Unmarshal(ev.Params, &p) == nil {
			w.contextGone(ctx, p.ID)
		}
	case "Page.navigatedWithinDocument":
		var p struct {
			FrameID string `json:"frameId"`
			URL     string `json:"url"`
		}
		if json.Unmarshal(ev.Params, &p) == nil && p.FrameID == w.mainFrame && w.mainContext.ID != 0 {
			if !w.c.opts.Tab(p.URL) {
				w.drop(ctx, "page navigated")
				w.c.opts.Status("the seven tab left seven")
			} else {
				w.c.mu.Lock()
				active := w.c.ctxID != 0
				w.c.mu.Unlock()
				if !active {
					w.contextCreated(ctx, w.mainContext)
				}
			}
		}
	case "Runtime.executionContextsCleared":
		w.mainContext = cdp.ExecutionContext{}
		w.drop(ctx, "page navigated")
	case "Runtime.bindingCalled":
		var p cdp.BindingCalled
		if json.Unmarshal(ev.Params, &p) == nil {
			w.bindingCalled(ctx, p)
		}
	}
	return nil
}

func (w *watch) contextCreated(ctx context.Context, ec cdp.ExecutionContext) {
	if !ec.AuxData.IsDefault || ec.AuxData.FrameID != w.mainFrame {
		if w.c.opts.Origins.Allowed(ec.Origin) {
			w.c.opts.Logf("signal: ignoring context %d (%s, frame %s)", ec.ID, ec.Origin, ec.AuxData.FrameID)
		} else {
			w.c.opts.Logf("signal: ignored a context on another origin")
		}
		return
	}
	w.mainContext = ec
	if BlankOrigin(ec.Origin) {
		w.c.opts.Logf("signal: ignoring the blank context %d of a new tab", ec.ID)
		return
	}
	if !w.c.opts.Origins.Allowed(ec.Origin) {
		w.c.opts.Status("the seven tab left seven")
		return
	}
	var pageURL string
	if err := w.c.bounded(ctx, func(cctx context.Context) error {
		return w.s.Evaluate(cctx, "location.href", cdp.EvaluateOptions{ContextID: ec.ID, ReturnByValue: true}, &pageURL)
	}); err != nil {
		w.c.opts.Logf("signal: could not read the URL of context %d", ec.ID)
		return
	}
	if !w.c.opts.Tab(pageURL) {
		w.c.opts.Status("the seven tab left seven")
		return
	}
	w.drop(ctx, "page navigated")
	w.c.mu.Lock()
	w.c.origin = ec.Origin
	w.c.mu.Unlock()
	w.greet(ctx, ec.ID)
}

func (w *watch) greet(ctx context.Context, ctxID int) {
	w.stopHello()
	w.restartReady()
	nonce := NewNonce()
	w.c.mu.Lock()
	w.c.ctxID = ctxID
	w.c.nonce = nonce
	w.c.mu.Unlock()
	hctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.helloDone = make(chan struct{})
	go func() {
		defer close(w.helloDone)
		w.deliverHello(hctx, ctxID, nonce)
	}()
}

func (w *watch) retry(ctx context.Context) {
	w.c.mu.Lock()
	due, nonce, ctxID := w.c.due, w.c.nonce, w.c.ctxID
	w.c.due = ""
	w.c.mu.Unlock()
	if due == "" || due != nonce || ctxID == 0 || ctxID != w.mainContext.ID {
		return
	}
	w.c.opts.Logf("signal: a new hello to context %d after a failed link", ctxID)
	w.greet(ctx, ctxID)
}

func (w *watch) contextGone(ctx context.Context, id int) {
	if id == w.mainContext.ID {
		w.mainContext = cdp.ExecutionContext{}
	}
	w.c.mu.Lock()
	current := w.c.ctxID
	w.c.mu.Unlock()
	if id == current {
		w.drop(ctx, "page navigated")
	}
}

func (w *watch) stopHello() {
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
		<-w.helloDone
		w.helloDone = nil
	}
}

func (w *watch) drop(ctx context.Context, reason string) {
	w.stopHello()
	w.c.mu.Lock()
	nonce := w.c.nonce
	had := w.c.ctxID != 0
	w.c.ctxID = 0
	w.c.nonce = ""
	w.c.unschedule()
	w.c.mu.Unlock()
	if had {
		w.restartReady()
		w.c.emit(ctx, Message{T: TypeBye, Nonce: nonce, Reason: reason})
	}
}

func (w *watch) deliverHello(ctx context.Context, ctxID int, nonce string) {
	b, _ := json.Marshal(Hello(nonce))
	expr := fmt.Sprintf("typeof globalThis.%[1]s !== 'function' ? 'binding' : typeof globalThis.%[2]s !== 'function' ? 'receiver' : (globalThis.%[2]s(%[3]s), 'ok')", BindingName, ReceiverName, b)
	delay := 100 * time.Millisecond
	for {
		var state string
		err := w.s.Evaluate(ctx, expr, cdp.EvaluateOptions{ContextID: ctxID, ReturnByValue: true}, &state)
		if err == nil && state == "ok" {
			w.c.opts.Logf("signal: hello delivered to context %d", ctxID)
			w.delivered.Store(true)
			w.c.ready()
			return
		}
		var ce *cdp.Error
		if errors.As(err, &ce) || ctx.Err() != nil {
			return
		}
		if state == "binding" {
			w.c.opts.Logf("signal: %s missing in context %d, adding it again", BindingName, ctxID)
			if err := w.s.AddBinding(ctx, BindingName); err != nil {
				w.c.opts.Logf("signal: add binding: %v", err)
			}
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return
		}
		delay = min(delay*2, time.Second)
	}
}

func (w *watch) bindingCalled(ctx context.Context, p cdp.BindingCalled) {
	if p.Name != BindingName {
		return
	}
	w.c.mu.Lock()
	ctxID, nonce, origin := w.c.ctxID, w.c.nonce, w.c.origin
	w.c.mu.Unlock()
	if ctxID == 0 || p.ExecutionContextID != ctxID {
		w.c.opts.Logf("signal: ignoring %s from context %d", BindingName, p.ExecutionContextID)
		return
	}
	m, err := Parse([]byte(p.Payload))
	if err != nil || m.Nonce != nonce {
		w.c.opts.Logf("signal: ignoring %s with a bad or stale message", BindingName)
		return
	}
	switch m.T {
	case TypeOffer, TypeCandidate, TypeBye:
		if m.T == TypeBye {
			w.c.mu.Lock()
			if w.c.nonce == nonce {
				w.c.unschedule()
			}
			w.c.mu.Unlock()
		}
		w.c.opts.Logf("signal: %s from the page", m.T)
		m.Origin = origin
		w.c.emit(ctx, m)
	default:
		w.c.opts.Logf("signal: ignoring %q from the page", strings.ToValidUTF8(m.T, "?"))
	}
}

var (
	_ Conn     = (*CDP)(nil)
	_ Relinker = (*CDP)(nil)
)
