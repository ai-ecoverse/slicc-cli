package signal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	BindingName  = "sliccLinkSend"
	ReceiverName = "sliccLinkReceive"
	SetupTimeout = 10 * time.Second
	QuietEnv     = "SLICC_SEVEN_QUIET"
	QuietTimeout = 15 * time.Second
	NoSevenTab   = "still no seven tab. Open seven in this browser. If it's open and slicc can't see it, the browser hides it from debugging clients, so link it through seven's Link a device with slicc <join-url> follow"
)

type CDPOptions struct {
	Client  *cdp.Client
	Origins *Origins
	Logf    func(format string, args ...any)
	Status  func(format string, args ...any)
}

type CDP struct {
	opts CDPOptions

	mu      sync.Mutex
	session *cdp.Session
	ctxID   int
	nonce   string
	origin  string

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
	ctx, cancel := context.WithCancel(context.Background())
	c := &CDP{opts: opts, in: make(chan Message, 64), done: make(chan struct{}), ran: make(chan struct{}), stop: cancel}
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
		close(c.done)
	})
}

func (c *CDP) emit(ctx context.Context, m Message) {
	select {
	case c.in <- m:
	case <-ctx.Done():
	}
}

func (c *CDP) run(ctx context.Context) {
	sub := c.opts.Client.Subscribe()
	defer sub.Close()
	for {
		target, err := c.findTarget(ctx, sub)
		if err != nil {
			c.finish(err)
			return
		}
		if err := c.serveTarget(ctx, sub, target); err != nil {
			if ctx.Err() == nil && c.gone(ctx, target.TargetID) {
				c.opts.Logf("signal: the seven tab went away during setup: %v", err)
				continue
			}
			c.finish(err)
			return
		}
	}
}

func (c *CDP) gone(ctx context.Context, targetID string) bool {
	targets, err := c.opts.Client.Targets(ctx)
	if err != nil {
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
	for _, t := range targets {
		if t.Type == "page" && c.opts.Origins.AllowedURL(t.URL) {
			return t, nil
		}
	}
	c.opts.Status("waiting for a seven tab")
	quiet := time.After(quietFor())
	for {
		select {
		case <-quiet:
			c.opts.Status("%s", NoSevenTab)
		case ev, ok := <-sub.Events():
			if !ok {
				return cdp.TargetInfo{}, cl.Err()
			}
			if ev.Method != "Target.targetCreated" && ev.Method != "Target.targetInfoChanged" {
				continue
			}
			var p struct {
				TargetInfo cdp.TargetInfo `json:"targetInfo"`
			}
			if json.Unmarshal(ev.Params, &p) == nil && p.TargetInfo.Type == "page" && c.opts.Origins.AllowedURL(p.TargetInfo.URL) {
				return p.TargetInfo, nil
			}
		case <-ctx.Done():
			return cdp.TargetInfo{}, ctx.Err()
		}
	}
}

func quietFor() time.Duration {
	if d, err := time.ParseDuration(os.Getenv(QuietEnv)); err == nil && d > 0 {
		return d
	}
	return QuietTimeout
}

func (c *CDP) serveTarget(ctx context.Context, sub *cdp.Subscription, t cdp.TargetInfo) error {
	s, err := c.setup(ctx, t)
	if err != nil {
		return err
	}
	w := &watch{c: c, s: s, targetID: t.TargetID, mainFrame: t.TargetID}
	err = w.loop(ctx, sub)
	c.detach(s)
	return err
}

func bounded(ctx context.Context, fn func(context.Context) error) error {
	cctx, cancel := context.WithTimeout(ctx, SetupTimeout)
	defer cancel()
	return fn(cctx)
}

func (c *CDP) setup(ctx context.Context, t cdp.TargetInfo) (*cdp.Session, error) {
	var s *cdp.Session
	if err := bounded(ctx, func(cctx context.Context) (err error) {
		s, err = c.opts.Client.Attach(cctx, t.TargetID)
		return err
	}); err != nil {
		return nil, err
	}
	c.opts.Status("found seven at %s", protect.Redact(t.URL))
	c.grantLocalNetwork(ctx, t.URL)
	if err := bounded(ctx, func(cctx context.Context) error { return s.AddBinding(cctx, BindingName) }); err != nil {
		c.detach(s)
		return nil, err
	}
	c.mu.Lock()
	c.session = s
	c.mu.Unlock()
	if err := bounded(ctx, func(cctx context.Context) error { return s.Call(cctx, "Runtime.enable", nil, nil) }); err != nil {
		c.detach(s)
		return nil, err
	}
	return s, nil
}

func (c *CDP) grantLocalNetwork(ctx context.Context, rawURL string) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	origin := u.Scheme + "://" + u.Host
	for _, name := range localNetworkPermissions {
		params := map[string]any{"permission": map[string]any{"name": name}, "setting": "granted", "origin": origin}
		if err := bounded(ctx, func(cctx context.Context) error {
			return c.opts.Client.Call(cctx, "", "Browser.setPermission", params, nil)
		}); err != nil {
			c.opts.Logf("signal: grant %s to %s: %v", name, origin, err)
		}
	}
}

type watch struct {
	c         *CDP
	s         *cdp.Session
	targetID  string
	mainFrame string
	cancel    context.CancelFunc
}

var errTargetGone = errors.New("signal: seven tab is gone")

func (w *watch) loop(ctx context.Context, sub *cdp.Subscription) error {
	defer w.drop(ctx, "seven tab closed")
	for {
		select {
		case ev, ok := <-sub.Events():
			if !ok {
				return w.c.opts.Client.Err()
			}
			err := w.handle(ctx, ev)
			if errors.Is(err, errTargetGone) {
				w.c.opts.Status("seven tab closed")
				return nil
			}
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (w *watch) handle(ctx context.Context, ev cdp.Event) error {
	if ev.SessionID == "" {
		return w.handleBrowser(ev)
	}
	if ev.SessionID != w.s.ID {
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
	case "Runtime.executionContextsCleared":
		w.drop(ctx, "page navigated")
	case "Runtime.bindingCalled":
		var p cdp.BindingCalled
		if json.Unmarshal(ev.Params, &p) == nil {
			w.bindingCalled(ctx, p)
		}
	}
	return nil
}

func (w *watch) handleBrowser(ev cdp.Event) error {
	switch ev.Method {
	case "Target.detachedFromTarget":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(ev.Params, &p) == nil && p.SessionID == w.s.ID {
			return errTargetGone
		}
	case "Target.targetDestroyed", "Target.targetCrashed":
		var p struct {
			TargetID string `json:"targetId"`
		}
		if json.Unmarshal(ev.Params, &p) == nil && p.TargetID == w.targetID {
			return errTargetGone
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
	if BlankOrigin(ec.Origin) {
		w.c.opts.Logf("signal: ignoring the blank context %d of a new tab", ec.ID)
		return
	}
	if !w.c.opts.Origins.Allowed(ec.Origin) {
		w.c.opts.Status("the seven tab left seven")
		return
	}
	w.drop(ctx, "page navigated")
	nonce := NewNonce()
	w.c.mu.Lock()
	w.c.ctxID = ec.ID
	w.c.origin = ec.Origin
	w.c.nonce = nonce
	w.c.mu.Unlock()
	hctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	go w.deliverHello(hctx, ec.ID, nonce)
}

func (w *watch) contextGone(ctx context.Context, id int) {
	w.c.mu.Lock()
	current := w.c.ctxID
	w.c.mu.Unlock()
	if id == current {
		w.drop(ctx, "page navigated")
	}
}

func (w *watch) drop(ctx context.Context, reason string) {
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	w.c.mu.Lock()
	nonce := w.c.nonce
	had := w.c.ctxID != 0
	w.c.ctxID = 0
	w.c.nonce = ""
	w.c.mu.Unlock()
	if had {
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
		w.c.opts.Logf("signal: %s from the page", m.T)
		m.Origin = origin
		w.c.emit(ctx, m)
	default:
		w.c.opts.Logf("signal: ignoring %q from the page", strings.ToValidUTF8(m.T, "?"))
	}
}

var _ Conn = (*CDP)(nil)
