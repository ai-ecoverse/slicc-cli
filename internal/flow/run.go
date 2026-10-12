package flow

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/kernelin"
	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
	"github.com/ai-ecoverse/slicc-cli/internal/telemetry"
)

const DefaultPage = "https://seven.sliccy.ai/"

type Browser struct {
	ID           string
	Name         string
	Version      string
	CDP          string
	WebSocketURL string
	Seven        bool
	Launched     bool
}

type Config struct {
	Page     string
	Origins  []string
	Profile  string
	Profiles map[string]string
	Headless bool
	Kernel   int
	Name     string
	Version  string
	Allow    policy.Rules
	Deny     policy.Rules
	Browser  browser.Options
	Chosen   bool
	Register func(ctx context.Context, n *node.Node, pol *policy.Policy, browsers []Browser) (Caps, error)
	Compute  string
	Emit     func(Event)
	Logf     func(format string, args ...any)
	Protect  []uint16
	OnClient func(*cdp.Client) func(cdp.TargetInfo)
}

type launched struct {
	id string
	b  *browser.Browser
}

type run struct {
	cfg      Config
	origins  []string
	mu       sync.Mutex
	launched []launched
	browsers []Browser
	client   *cdp.Client
	allowed  *signal.Origins
}

func PageOrigins(page string, extra []string) ([]string, error) {
	u, err := url.Parse(page)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("--page %q is not an http(s) URL", page)
	}
	origins := append([]string(nil), extra...)
	if o := browser.OriginOf(page); !browser.IsSevenOrigin(o, extra) {
		origins = append(origins, o)
	}
	if _, err := signal.NewOrigins(origins); err != nil {
		return nil, err
	}
	return origins, nil
}

func Go(ctx context.Context, cfg Config, sel []browser.Entry) error {
	if cfg.Page == "" {
		cfg.Page = DefaultPage
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Register == nil {
		cfg.Register = func(context.Context, *node.Node, *policy.Policy, []Browser) (Caps, error) { return Caps{}, nil }
	}
	r := &run{cfg: cfg}
	r.cfg.Emit = r.serialize(cfg.Emit)
	err := r.start(ctx, sel)
	if err != nil && ctx.Err() == nil {
		r.emit(Event{Event: Error, Message: err.Error()})
	} else {
		err = nil
	}
	r.cleanup()
	return err
}

func (r *run) serialize(emit func(Event)) func(Event) {
	if emit == nil {
		return func(Event) {}
	}
	var mu sync.Mutex
	return func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		e.Schema = Schema
		if e.Time.IsZero() {
			e.Time = time.Now()
		}
		emit(e)
	}
}

func (r *run) emit(e Event) { r.cfg.Emit(e) }

func (r *run) start(ctx context.Context, sel []browser.Entry) error {
	if err := Check(sel); err != nil {
		return err
	}
	origins, err := PageOrigins(r.cfg.Page, r.cfg.Origins)
	if err != nil {
		return err
	}
	r.origins = origins
	r.allowed, _ = signal.NewOrigins(origins)
	if err := r.setupHost(ctx, sel[0]); err != nil {
		telemetry.ReportError(telemetry.Browser, reportable(sel[0], err))
		return err
	}
	for _, e := range sel[1:] {
		if err := r.setupExtra(ctx, e); err != nil {
			telemetry.ReportError(telemetry.Browser, reportable(e, err))
			return err
		}
	}
	return r.serve(ctx)
}

func (r *run) profileFor(e browser.Entry, host bool) string {
	if p := r.cfg.Profiles[e.ID]; p != "" {
		return p
	}
	if host {
		return r.cfg.Profile
	}
	return ""
}

func (r *run) launch(ctx context.Context, e browser.Entry, role, page string) (*browser.Browser, error) {
	opts := r.cfg.Browser
	opts.Origins = r.origins
	var b *browser.Browser
	var err error
	if e.Six != nil {
		b, err = browser.LaunchSix(ctx, *e.Installed, *e.Six, browser.LaunchOptions{Options: opts, URL: page, Headless: r.cfg.Headless})
	} else {
		b, err = browser.Launch(ctx, *e.Installed, browser.LaunchOptions{Options: opts, Profile: r.profileFor(e, role == RoleSeven), URL: page, Headless: r.cfg.Headless})
	}
	if err != nil {
		return nil, fmt.Errorf("launch %s: %w", e.Name, err)
	}
	r.mu.Lock()
	r.launched = append(r.launched, launched{id: e.ID, b: b})
	r.mu.Unlock()
	r.emit(Event{
		Event: Launched, Browser: e.ID, Name: e.Name, Version: e.Version, Role: role, PID: b.PID,
		Profile: b.Profile, ProfileDir: b.ProfileDir, CDP: b.CDP, WebSocketURL: b.WebSocketURL,
	})
	return b, nil
}

func (r *run) setupHost(ctx context.Context, e browser.Entry) error {
	e, err := r.resolveSix(ctx, e)
	if err != nil {
		return err
	}
	br := Browser{ID: e.ID, Name: e.Name, Version: e.Version, Seven: true}
	if e.Kind == browser.KindInstalled {
		b, err := r.launch(ctx, e, RoleSeven, r.cfg.Page)
		if err != nil {
			return err
		}
		br.CDP, br.WebSocketURL, br.Launched = b.CDP, b.WebSocketURL, true
	} else {
		br.CDP, br.WebSocketURL = e.Endpoint.CDP, e.Endpoint.WebSocketURL
	}
	target := br.WebSocketURL
	if target == "" {
		target = br.CDP
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := cdp.Dial(dctx, target)
	if err != nil {
		return fmt.Errorf("attach to %s: %w", e.Name, err)
	}
	r.client = client
	r.browsers = append(r.browsers, br)
	r.emit(Event{Event: Attached, Browser: e.ID, Name: e.Name, Version: e.Version, Role: RoleSeven, Launched: br.Launched, CDP: br.CDP, WebSocketURL: br.WebSocketURL})
	if br.Launched {
		return nil
	}
	opened, err := r.ensureTab(ctx)
	if err != nil {
		return err
	}
	switch {
	case IsSix(e):
		r.emit(Event{Event: ForeignBrowser, Browser: e.ID, Name: e.Name, Profile: SixLabel(e), CDP: br.CDP, OpenedTab: &opened})
	case r.foreign(e):
		r.emit(Event{Event: ForeignBrowser, Browser: e.ID, Name: e.Name, Profile: ProfileLabel(*e.Endpoint), CDP: br.CDP, OpenedTab: &opened})
	}
	return nil
}

func (r *run) foreign(e browser.Entry) bool {
	if r.cfg.Chosen || e.Endpoint == nil || e.Endpoint.Profile != "" {
		return false
	}
	key := browser.EndpointKey(e.Endpoint.CDP)
	for _, raw := range r.cfg.Browser.CDPURLs {
		if browser.EndpointKey(raw) == key {
			return false
		}
	}
	return true
}

func ProfileLabel(ep browser.Endpoint) string {
	switch {
	case ep.Browser != "":
		return "default"
	case ep.UserDataDir != "":
		return filepath.Base(ep.UserDataDir)
	}
	return ""
}

func (r *run) ensureTab(ctx context.Context) (bool, error) {
	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if tab := r.sevenTab(tctx); tab != "" {
		return false, nil
	}
	if err := r.client.Call(tctx, "", "Target.createTarget", map[string]any{"url": r.cfg.Page}, nil); err != nil {
		return false, fmt.Errorf("open %s: %w", r.cfg.Page, err)
	}
	r.emit(Event{Event: Status, Message: "opened " + r.cfg.Page + " in a new tab"})
	return true, nil
}

func (r *run) sevenTab(ctx context.Context) string {
	targets, err := r.client.Targets(ctx)
	if err != nil {
		return ""
	}
	for _, t := range targets {
		if t.Type == "page" && r.allowed.AllowedURL(t.URL) {
			return t.URL
		}
	}
	return ""
}

func (r *run) setupExtra(ctx context.Context, e browser.Entry) error {
	e, err := r.resolveSix(ctx, e)
	if err != nil {
		return err
	}
	br := Browser{ID: e.ID, Name: e.Name, Version: e.Version}
	if e.Kind == browser.KindInstalled {
		b, err := r.launch(ctx, e, RoleExtra, "")
		if err != nil {
			return err
		}
		br.CDP, br.WebSocketURL, br.Launched = b.CDP, b.WebSocketURL, true
	} else {
		br.CDP, br.WebSocketURL = e.Endpoint.CDP, e.Endpoint.WebSocketURL
	}
	r.browsers = append(r.browsers, br)
	r.emit(Event{Event: Attached, Browser: e.ID, Name: e.Name, Version: e.Version, Role: RoleExtra, Launched: br.Launched, CDP: br.CDP, WebSocketURL: br.WebSocketURL})
	return nil
}

func (r *run) policy() *policy.Policy {
	var ports []uint16
	for _, b := range r.browsers {
		if p := portOf(b.CDP); p != 0 {
			ports = append(ports, p)
		}
	}
	if r.cfg.Kernel > 0 {
		ports = append(ports, uint16(r.cfg.Kernel))
	}
	ports = append(ports, r.cfg.Protect...)
	return policy.Local(ports...).With(r.cfg.Allow, r.cfg.Deny)
}

func portOf(raw string) uint16 {
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	p, _ := strconv.ParseUint(u.Port(), 10, 16)
	return uint16(p)
}

func (r *run) serve(ctx context.Context) error {
	pol := r.policy()
	n := node.New(node.Config{Name: r.cfg.Name, Host: r.cfg.Name, Version: r.cfg.Version, Mode: "local", Policy: pol, Exit: true, Logf: r.cfg.Logf, Status: r.cfg.Logf})
	caps, err := r.cfg.Register(ctx, n, pol, append([]Browser(nil), r.browsers...))
	if err != nil {
		return err
	}
	if caps.Release != nil {
		defer caps.Release()
	}
	defer r.watchBrowsers(ctx, caps.Live)()
	q := newQueue()
	unsubscribe := n.Subscribe(q.push)
	defer r.serveKernel(n)()
	pumped := make(chan struct{})
	go func() {
		defer close(pumped)
		r.pump(q, n, pol)
	}()
	status := func(format string, a ...any) { r.emit(Event{Event: Status, Message: fmt.Sprintf(format, a...)}) }
	var onTarget func(cdp.TargetInfo)
	if r.cfg.OnClient != nil {
		onTarget = r.cfg.OnClient(r.client)
	}
	sig := signal.NewCDP(signal.CDPOptions{Client: r.client, Origins: r.allowed, Logf: r.cfg.Logf, Status: status, OnTarget: onTarget})
	r.emit(Event{Event: Serving, Caps: n.Caps(), Policy: pol.Summary(), Compute: r.cfg.Compute})
	err = n.Serve(ctx, sig)
	unsubscribe()
	q.close()
	<-pumped
	_ = sig.Close()
	select {
	case <-r.client.Done():
		if ctx.Err() == nil {
			r.emit(Event{Event: Status, Message: r.browsers[0].Name + " closed"})
			return nil
		}
	default:
	}
	_ = r.client.Close()
	return err
}

type Caps struct {
	Release func()
	Live    func() map[string]bool
}

const browserCheck = 2 * time.Second

func (r *run) watchBrowsers(ctx context.Context, live func() map[string]bool) func() {
	if live == nil {
		return func() {}
	}
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		gone := map[string]bool{}
		t := time.NewTicker(browserCheck / 2)
		defer t.Stop()
		for {
			select {
			case <-wctx.Done():
				return
			case <-t.C:
			}
			r.checkBrowsers(live(), gone)
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (r *run) checkBrowsers(now, gone map[string]bool) {
	for _, b := range r.browsers {
		switch {
		case !now[b.ID] && !gone[b.ID]:
			gone[b.ID] = true
			r.emit(Event{Event: BrowserGone, Browser: b.ID, Name: b.Name, Role: roleOf(b)})
		case now[b.ID] && gone[b.ID]:
			delete(gone, b.ID)
			r.emit(Event{Event: BrowserBack, Browser: b.ID, Name: b.Name, Role: roleOf(b)})
		}
	}
}

func roleOf(b Browser) string {
	if b.Seven {
		return RoleSeven
	}
	return RoleExtra
}

func (r *run) serveKernel(n *node.Node) func() {
	if r.cfg.Kernel <= 0 {
		return func() {}
	}
	status := func(format string, a ...any) { r.emit(Event{Event: Status, Message: fmt.Sprintf(format, a...)}) }
	kern := kernelin.New(kernelin.Config{Port: r.cfg.Kernel, Logf: r.cfg.Logf, Status: status})
	unsubscribe := n.Subscribe(func(e node.Event) {
		if e.Kind != node.EventLinked {
			return
		}
		go func() {
			kern.Add(e.Link)
			if a, ok := kern.Addr().(*net.TCPAddr); ok {
				r.emit(Event{Event: Kernel, Kernel: kernelin.URL("<port>", a.Port)})
			}
		}()
	})
	return func() {
		unsubscribe()
		_ = kern.Close()
	}
}

func (r *run) pump(q *queue, n *node.Node, pol *policy.Policy) {
	for {
		ev, ok := q.pop()
		if !ok {
			return
		}
		switch ev.Kind {
		case node.EventConnecting:
			r.emit(Event{Event: Connecting})
		case node.EventLinked:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			tab := r.sevenTab(ctx)
			cancel()
			r.emit(Event{Event: Linked, Peer: ev.Peer.Name, Tab: tab, Caps: n.Caps(), Policy: pol.Summary()})
		case node.EventLinkDown:
			e := Event{Event: LinkLost, Reason: ev.Reason}
			if ev.Err != nil {
				e.Message = ev.Err.Error()
				telemetry.ReportError(telemetry.Link, ev.Err)
			}
			r.emit(e)
		case node.EventStreamOpen:
			r.emit(Event{Event: StreamOpen, Stream: ev.Stream})
		case node.EventStreamClose:
			r.emit(Event{Event: StreamDone, Stream: ev.Stream})
		}
	}
}

func (r *run) cleanup() {
	if r.client != nil {
		_ = r.client.Close()
	}
	r.mu.Lock()
	list := r.launched
	r.launched = nil
	r.mu.Unlock()
	var ids []string
	var wg sync.WaitGroup
	errs := make([]error, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			errs[i] = list[i].b.Close(ctx)
		}(i)
	}
	wg.Wait()
	for i, l := range list {
		if errs[i] != nil {
			r.emit(Event{Event: Error, Browser: l.id, Message: errs[i].Error()})
			continue
		}
		ids = append(ids, l.id)
	}
	r.emit(Event{Event: Closed, Browsers: ids})
}

type queue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []node.Event
	closed bool
}

func newQueue() *queue {
	q := &queue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *queue) push(e node.Event) {
	q.mu.Lock()
	if !q.closed {
		q.items = append(q.items, e)
	}
	q.mu.Unlock()
	q.cond.Signal()
}

func (q *queue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cond.Broadcast()
}

func (q *queue) pop() (node.Event, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return node.Event{}, false
	}
	e := q.items[0]
	q.items = q.items[1:]
	return e, true
}
