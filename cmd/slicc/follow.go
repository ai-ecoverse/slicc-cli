//go:build !wasip1

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	ossignal "os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	capcdp "github.com/ai-ecoverse/slicc-cli/internal/caps/cdp"
	hostfs "github.com/ai-ecoverse/slicc-cli/internal/caps/fs"
	httpcap "github.com/ai-ecoverse/slicc-cli/internal/caps/http"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/resolve"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/tcp"
	udpcap "github.com/ai-ecoverse/slicc-cli/internal/caps/udp"
	wscap "github.com/ai-ecoverse/slicc-cli/internal/caps/ws"
	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
	"github.com/ai-ecoverse/slicc-cli/internal/telemetry"
	"github.com/pion/webrtc/v4"
)

const (
	defaultSignalOnlyTimeout = 2 * time.Minute
	followSetupTimeout       = 45 * time.Second
	followDropGrace          = 3 * time.Second
	capJoin                  = "join"
)

const followUsage = `usage: slicc <join-url> follow [flags]
       slicc - follow [flags]
       slicc @<file> follow [flags]

Follow a seven's tray from this machine. slicc attaches to the tray hub with
the join URL, answers seven's offer with a WebRTC link, and serves the link
until interrupted, reconnecting when it drops. Status lines go to stderr.
The join URL is never printed. With - slicc reads it from the first line of
stdin, and with @<file> from the first line of that file, so it stays out of
the command line, where ps shows it to every user of this machine.

flags:
  --exit               offer this machine's network to seven (tcp, resolve,
                       http, ws and udp streams) under the remote policy:
                       public addresses only
  --allow <rule>       allow CIDR or CIDR:port (repeatable, comma-separated)
  --deny <rule>        deny CIDR or CIDR:port (repeatable, comma-separated)
` + exposeUsage + `  --name <name>        the name and host this link shows in seven
  --cdp <url>          let seven drive this browser over the link (cdp,
                       repeatable); seven gets full control of it, except
                       tabs, cookies and storage on sliccy.ai and the
                       browser's privileged pages (chrome://, extension
                       pages, file:// and the like), and its endpoint and
                       port are denied to tcp, http, ws, udp and ssh
` + jerkFollowUsage + mountUsage + computeUsage + `
With --runner the link also offers ssh: seven's kernel reaches this machine's
SSH server, which never listens on a port, and its forwarding goes through
the remote policy. With --mount it also offers fs: the seven on the other
machine can read, and unless :ro, change the folders listed.
` + debugPortsHelp

type followFlags struct {
	exit       bool
	name       string
	allow      policy.Rules
	deny       policy.Rules
	expose     policy.Ports
	signalOnly bool
	timeout    time.Duration
	compute    computeFlags
	hosts      hostsFlag
	cdp        stringList
	mounts     stringList
	jerk       bool
	jerkSeal   bool
}

func joinCommand(args []string, stdout, stderr io.Writer) int {
	if _, err := tray.ParseJoinURL(args[0]); err != nil {
		fmt.Fprintf(stderr, "slicc: %v\n", err)
		return 2
	}
	if len(args) >= 2 {
		switch args[1] {
		case "follow":
			return follow(args[0], args[2:], stdout, stderr)
		case "prompt", "watch", "wait", "abort":
			return runAgent(args[1], args[0], args[2:], agentIO{stdin: os.Stdin, stdout: stdout, stderr: stderr})
		}
	}
	fmt.Fprint(stderr, "slicc: a join URL must be followed by a command: follow, prompt, watch, wait or abort\n")
	return 2
}

func parseFollow(args []string, stdout, stderr io.Writer) (followFlags, int) {
	var f followFlags
	args, compute, err := extractCompute(args)
	if err != nil {
		fmt.Fprintf(stderr, "slicc follow: %v\n\n%s", err, followUsage)
		return f, 2
	}
	f.compute = compute
	fs := flag.NewFlagSet("follow", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&f.exit, "exit", false, "")
	fs.StringVar(&f.name, "name", "", "")
	fs.Var(&f.allow, "allow", "")
	fs.Var(&f.deny, "deny", "")
	fs.Var(&f.expose, "expose-debug-port", "")
	fs.Var(&f.hosts, "hosts", "")
	fs.Var(&f.cdp, "cdp", "")
	fs.Var(&f.mounts, "mount", "")
	fs.BoolVar(&f.jerk, "jerk", false, "")
	fs.BoolVar(&f.jerkSeal, "jerk-seal", false, "")
	fs.BoolVar(&f.signalOnly, "signal-only", false, "")
	fs.DurationVar(&f.timeout, "timeout", defaultSignalOnlyTimeout, "")
	f.compute.register(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, followUsage)
			return f, 0
		}
		fmt.Fprintf(stderr, "slicc follow: %v\n\n%s", err, followUsage)
		return f, 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "slicc follow: unexpected arguments\n\n%s", followUsage)
		return f, 2
	}
	if err := f.compute.validate(); err != nil {
		fmt.Fprintf(stderr, "slicc follow: %v\n\n%s", err, followUsage)
		return f, 2
	}
	if code := f.compute.checkRunner(stderr); code >= 0 {
		return f, code
	}
	if code := checkMounts(f.mounts, "slicc follow", stderr); code >= 0 {
		return f, code
	}
	return f, -1
}

func follow(joinURL string, args []string, stdout, stderr io.Writer) int {
	f, code := parseFollow(args, stdout, stderr)
	if code >= 0 {
		return code
	}
	ctx, stop := ossignal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if f.signalOnly {
		ctx, cancel := context.WithTimeout(ctx, f.timeout)
		defer cancel()
		return signalOnlyRun(ctx, joinURL, stdout, stderr)
	}
	r, err := newFollowRun(joinURL, f, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "slicc follow: %v\n", err)
		return 1
	}
	defer r.closeSSH()
	return r.run(ctx)
}

type followEventKind int

const (
	evUp followEventKind = iota + 1
	evDown
	evUnlink
)

type followEvent struct {
	kind followEventKind
	name string
	conn *tray.Conn
}

type followRun struct {
	f        *tray.Follower
	joinURL  string
	hub      string
	state    *tray.JoinState
	certs    *tray.Certs
	node     *node.Node
	cdp      *capcdp.Registry
	status   func(format string, a ...any)
	events   chan followEvent
	closeSSH func()
	ctl      *ctl.Holder
	device   string

	mu   sync.Mutex
	conn *tray.Conn
}

func newFollowRun(joinURL string, ff followFlags, stderr io.Writer) (*followRun, error) {
	status := func(format string, a ...any) { fmt.Fprintf(stderr, "slicc: "+format+"\n", a...) }
	logf := func(string, ...any) {}
	if os.Getenv("SLICC_DEBUG") != "" {
		logf = func(format string, a ...any) { fmt.Fprintf(stderr, "slicc debug: "+format+"\n", a...) }
	}
	r := &followRun{joinURL: joinURL, hub: tray.HubOrigin(joinURL), status: status, events: make(chan followEvent, 16)}
	var err error
	if r.state, err = tray.OpenDefaultState(); err != nil {
		return nil, errors.New("no user config directory")
	}
	if r.certs, err = tray.OpenDefaultCerts(); err != nil {
		return nil, errors.New("no user config directory")
	}
	if r.f, err = tray.NewFollower(tray.Config{JoinURL: joinURL, State: r.state, Logf: logf}); err != nil {
		return nil, err
	}
	logf = redactingLogf(r.f, logf)
	cert, err := r.certificate()
	if err != nil {
		return nil, err
	}
	pol := policy.Remote().With(ff.allow, ff.deny)
	guardCtx, stopGuard := context.WithCancel(context.Background())
	if err := browser.Guard(guardCtx, pol, browser.Options{Status: status}, cdpPorts(ff.cdp...), ff.expose, logf); err != nil {
		stopGuard()
		return nil, err
	}
	if len(ff.cdp) > 0 {
		if r.cdp, err = cdpBrowsers(context.Background(), "", ff.cdp, nil, pol, ff.hosts.hosts, logf); err != nil {
			stopGuard()
			return nil, err
		}
	}
	r.node = node.New(node.Config{
		Name: ff.name, Host: ff.name, Version: version, Mode: "remote", Policy: pol, Hosts: ff.hosts.hosts, Exit: ff.exit,
		Logf: logf, Status: r.nodeStatus, Certificates: []webrtc.Certificate{cert},
	})
	r.node.Subscribe(func(e node.Event) {
		if e.Kind == node.EventConnecting {
			r.onLink(e.Link)
		}
	})
	if ff.exit {
		tcp.Register(r.node)
		resolve.Register(r.node)
		httpcap.Register(r.node)
		wscap.Register(r.node)
		udpcap.Register(r.node)
		status("exit on (tcp, resolve, http, ws, udp), %s", pol.Summary())
	} else {
		status("exit off")
	}
	closeSSH, err := ff.compute.serve(r.node, status)
	if err != nil {
		stopGuard()
		return nil, err
	}
	if ff.jerk || ff.jerkSeal {
		serveJerk(r.node, ff.jerkSeal, true, status)
	}
	closeMounts := serveMounts(r.node, hostfs.Load(ff.mounts, status), status)
	r.closeSSH = func() { stopGuard(); closeMounts(); closeSSH() }
	if r.cdp != nil {
		followBrowsers(r.node, r.cdp, status)
	}
	r.node.Handle(capJoin, "join", r.handleJoin)
	r.node.Handle(capJoin, "unlink", r.handleUnlink)
	h, closeCtl := serveCtl("follow", r.node, 0, logf)
	h.Update(func(s *ctl.Status) { s.Hub, s.Device, s.Seven = r.hub, r.device, r.certs.Leader(r.hub) })
	r.ctl = h
	closeRest := r.closeSSH
	r.closeSSH = func() { closeCtl(); closeRest() }
	return r, nil
}

func redactingLogf(f *tray.Follower, logf func(string, ...any)) func(string, ...any) {
	return func(format string, a ...any) { logf("%s", f.Redact(fmt.Sprintf(format, a...))) }
}

func (r *followRun) certificate() (webrtc.Certificate, error) {
	pemData, created, err := r.certs.Load(r.hub)
	if err != nil {
		return webrtc.Certificate{}, err
	}
	cert, err := webrtc.CertificateFromPEM(pemData)
	if err != nil {
		return webrtc.Certificate{}, fmt.Errorf("the device certificate: %w", err)
	}
	fps, err := cert.GetFingerprints()
	if err != nil || len(fps) == 0 {
		return webrtc.Certificate{}, errors.New("the device certificate has no fingerprint")
	}
	r.device = strings.ToLower(strings.ReplaceAll(fps[0].Value, ":", ""))
	verb := "device"
	if created {
		verb = "new device"
	}
	r.status("%s, fingerprint %s %s", verb, fps[0].Algorithm, strings.ToUpper(fps[0].Value))
	return *cert, nil
}

func (r *followRun) nodeStatus(format string, a ...any) {
	if strings.HasPrefix(format, "link up with") {
		return
	}
	msg := r.f.Redact(fmt.Sprintf(format, a...))
	if msg == "link closed by seven: restart" {
		msg = "the tray hub restarted the setup"
	}
	r.status("%s", msg)
}

func (r *followRun) emit(ev followEvent) {
	select {
	case r.events <- ev:
	default:
	}
}

func (r *followRun) currentConn() *tray.Conn {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conn
}

func (r *followRun) onLink(l *link.Link) {
	conn := r.currentConn()
	if conn == nil {
		return
	}
	nonce := conn.Session().BootstrapID()
	r.ctl.SetState(ctl.StateConnecting)
	go func() {
		timer := time.NewTimer(followSetupTimeout)
		defer timer.Stop()
		select {
		case <-l.Ready():
			conn.Settle()
			h, _ := l.PeerHello()
			name := leaderName(h)
			_ = r.certs.SetLeader(r.hub, name)
			r.emit(followEvent{kind: evUp, name: name, conn: conn})
			<-l.Done()
			r.emit(followEvent{kind: evDown, conn: conn})
		case <-l.Done():
			if conn.Session().BootstrapID() == nonce {
				r.emit(followEvent{kind: evDown, conn: conn})
			}
		case <-timer.C:
			_ = l.Close()
			r.emit(followEvent{kind: evDown, conn: conn})
		}
	}()
}

func leaderName(h link.Hello) string {
	if h.Name != "" {
		return h.Name
	}
	return "seven"
}

func (r *followRun) handleJoin(s *link.Stream) {
	var open struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(s.OpenPayload(), &open)
	if err := r.f.Rejoin(open.URL); err != nil {
		_ = s.Reset("EACCES: not a join URL on the hub this device follows")
		return
	}
	r.status("seven sent a new join URL, used from the next reconnect")
	if s.Accept(nil) == nil {
		_ = s.CloseWrite()
	}
}

func (r *followRun) handleUnlink(s *link.Stream) {
	h, _ := s.Link().PeerHello()
	if s.Accept(nil) == nil {
		_ = s.CloseWrite()
	}
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
	}
	r.emit(followEvent{kind: evUnlink, name: leaderName(h), conn: r.currentConn()})
}

func (r *followRun) unlink(name string) int {
	if name == "" {
		name = r.certs.Leader(r.hub)
	}
	if name == "" {
		name = "seven"
	}
	_ = r.state.ForgetHub(r.joinURL)
	_ = r.certs.Delete(r.hub)
	r.ctl.Update(func(s *ctl.Status) { s.State, s.Code, s.RetryAt = ctl.StateStopped, "UNLINKED", nil })
	r.status("unlinked by %s", name)
	return 0
}

type followOutcome int

const (
	outcomeReconnect followOutcome = iota
	outcomeUp
	outcomeStop
)

func (r *followRun) run(ctx context.Context) int {
	if r.cdp != nil {
		go r.cdp.Watch(ctx, 2*time.Second)
	}
	backoff := followBackoffBase
	attempt := 0
	for {
		r.ctl.Update(func(s *ctl.Status) { s.State, s.RetryAt = ctl.StateDialing, nil; s.Dials++ })
		r.status("connecting to the tray hub")
		code, outcome := r.once(ctx)
		if outcome == outcomeStop {
			return code
		}
		if outcome == outcomeUp {
			backoff = followBackoffBase
			attempt = 0
		}
		wait := jitter(backoff)
		attempt++
		at := time.Now().Add(wait).UTC()
		r.ctl.Update(func(s *ctl.Status) { s.State, s.Attempt, s.RetryAt = ctl.StateReconnecting, attempt, &at })
		r.status("reconnecting in %s", wait.Round(100*time.Millisecond))
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(wait):
		}
		if outcome != outcomeUp {
			backoff = min(backoff*2, followBackoffMax)
		}
	}
}

func (r *followRun) once(ctx context.Context) (int, followOutcome) {
	sess, err := r.f.Dial(ctx)
	if err != nil {
		return r.failed(ctx, err, false)
	}
	r.ctl.SetState(ctl.StateOffer)
	conn := tray.NewConn(sess)
	r.mu.Lock()
	r.conn = conn
	r.mu.Unlock()
	defer func() { _ = conn.Close() }()
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- r.node.Serve(sctx, conn) }()
	up := false
	for {
		select {
		case <-ctx.Done():
			_ = stopServing(cancel, served)
			return 0, outcomeStop
		case err := <-served:
			if err == nil {
				return 0, r.after(up)
			}
			return r.failed(ctx, err, up)
		case ev := <-r.events:
			if ev.conn != conn {
				continue
			}
			switch ev.kind {
			case evUp:
				up = true
				r.ctl.Update(func(s *ctl.Status) {
					now := time.Now().UTC()
					s.State, s.Seven, s.Attempt, s.Code, s.Error = ctl.StateConnected, ev.name, 0, "", ""
					s.Links++
					s.LastLinked = &now
				})
				r.status("linked to %s", ev.name)
			case evDown:
				if !up {
					if code, outcome, ok := r.drainEarlyDrop(ctx, served); ok {
						return code, outcome
					}
				}
				_ = stopServing(cancel, served)
				return 0, r.after(up)
			case evUnlink:
				_ = stopServing(cancel, served)
				return r.unlink(ev.name), outcomeStop
			}
		}
	}
}

func (r *followRun) drainEarlyDrop(ctx context.Context, served <-chan error) (int, followOutcome, bool) {
	select {
	case err := <-served:
		if err == nil {
			return 0, outcomeReconnect, true
		}
		code, outcome := r.failed(ctx, err, false)
		return code, outcome, true
	case <-time.After(followDropGrace):
		return 0, 0, false
	case <-ctx.Done():
		return 0, 0, false
	}
}

func (r *followRun) after(up bool) followOutcome {
	if up {
		return outcomeUp
	}
	return outcomeReconnect
}

var fatalFollowCodes = map[string]string{
	tray.CodeInvalidJoin:           "the join URL is not valid; get a new one from seven",
	tray.CodeTrayExpired:           "seven's tray has expired; get a new join URL from seven",
	tray.CodeSupersededMissingJoin: "seven's tray moved without saying where; get a new join URL from seven",
	"UNSUPPORTED_RUNTIME":          "seven does not support this slicc version; update slicc or seven",
	"UNTRUSTED":                    "seven does not trust this device; link it again from seven",
	"NO_FINGERPRINT":               "internal error: seven found no SHA-256 fingerprint in this device's answer; please report this",
}

func (r *followRun) failed(ctx context.Context, err error, up bool) (int, followOutcome) {
	if ctx.Err() != nil {
		return 0, outcomeStop
	}
	code := tray.ErrorCode(err)
	if code == "UNLINKED" {
		return r.unlink(""), outcomeStop
	}
	telemetry.ReportError(telemetry.Signal, err)
	r.ctl.Update(func(s *ctl.Status) { s.Code, s.Error = code, r.f.Redact(err.Error()) })
	if msg, ok := fatalFollowCodes[code]; ok {
		r.ctl.Update(func(s *ctl.Status) { s.State, s.RetryAt = ctl.StateStopped, nil })
		r.status("%s (%s)", msg, code)
		return 1, outcomeStop
	}
	r.status("%s", r.f.Redact(err.Error()))
	return 0, r.after(up)
}

func signalOnlyRun(ctx context.Context, joinURL string, stdout, stderr io.Writer) int {
	state, err := tray.OpenDefaultState()
	if err != nil {
		fmt.Fprint(stderr, "slicc follow: no config directory, the join URL is not remembered\n")
		state = nil
	}
	f, err := tray.NewFollower(tray.Config{
		JoinURL: joinURL,
		State:   state,
		Logf:    func(format string, a ...any) { fmt.Fprintf(stderr, format+"\n", a...) },
	})
	if err != nil {
		fmt.Fprintf(stderr, "slicc follow: %v\n", err)
		return 2
	}
	fail := func(err error) int {
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.New("timed out waiting for an offer")
		}
		fmt.Fprintf(stderr, "slicc follow: %s\n", f.Redact(err.Error()))
		return 1
	}
	sess, err := f.Dial(ctx)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = sess.Close() }()
	if f.Redirected() {
		fmt.Fprint(stdout, "join url: superseded, the replacement is remembered\n")
	}
	fmt.Fprintf(stdout, "attached: bootstrap attempt %d\n", sess.Attempt())
	for {
		ev, err := sess.Recv(ctx)
		if err != nil {
			return fail(err)
		}
		switch ev.Kind {
		case tray.EventRestart:
			fmt.Fprintf(stdout, "restart: bootstrap attempt %d\n", ev.Attempt)
		case tray.EventOffer:
			fmt.Fprintf(stdout, "offer: %s\n", tray.SummarizeSDP(ev.SDP))
			printICEServers(stdout, sess.ICEServers())
			return 0
		}
	}
}

func printICEServers(w io.Writer, servers []tray.ICEServer) {
	fmt.Fprintf(w, "ice servers: %d\n", len(servers))
	for _, s := range servers {
		auth := ""
		if s.Username != "" || s.Credential != "" {
			auth = " (with credentials)"
		}
		fmt.Fprintf(w, "  %s%s\n", strings.Join(s.URLs, " "), auth)
	}
}

func stopServing(cancel context.CancelFunc, served <-chan error) error {
	cancel()
	return <-served
}
