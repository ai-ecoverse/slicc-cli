//go:build !wasip1

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	ossignal "os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	hostfs "github.com/ai-ecoverse/slicc-cli/internal/caps/fs"
	httpcap "github.com/ai-ecoverse/slicc-cli/internal/caps/http"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/kernelin"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/resolve"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/tcp"
	udpcap "github.com/ai-ecoverse/slicc-cli/internal/caps/udp"
	wscap "github.com/ai-ecoverse/slicc-cli/internal/caps/ws"
	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
	"github.com/ai-ecoverse/slicc-cli/internal/signal/loopback"
	"github.com/ai-ecoverse/slicc-cli/internal/telemetry"
	"github.com/pion/webrtc/v4"
)

const (
	openCommandEnv   = "SLICC_OPEN_COMMAND"
	loopbackPortsEnv = "SLICC_LOOPBACK_PORTS"
)

const openUsage = `usage: slicc open [flags]
       slicc pair [flags]
       slicc --default-browser [flags]

Link a seven that is open in your everyday browser, any browser, without
CDP or remote debugging. slicc listens on 127.0.0.1 and [::1], port 17117
or the next free one up to 17126, and prints a pairing URL on stdout:
seven's page with #slicc-pair=<port>.<token>.<fingerprint>. slicc open
(and slicc --default-browser) also opens it in the default browser;
slicc pair only prints it. seven confirms, pairs once, and links again by
itself after a reload or a restart of slicc. The token is good once, for
10 minutes; press Enter for a new pairing URL. Ctrl-C or SIGTERM stops
slicc. slicc unpair forgets paired sevens.

flags:
  --page <url>         the seven page to pair (default ` + flow.DefaultPage + `);
                       its origin is accepted too
  --origin <origin>    also accept seven on this origin (repeatable)
  --name <name>        the name and host this link shows in seven
  --allow <rule>       allow CIDR or CIDR:port (repeatable, comma-separated)
  --deny <rule>        deny CIDR or CIDR:port (repeatable, comma-separated)
` + exposeUsage + `  --kernel-port <n>    serve http://<port>.kernel.localhost on this local
                       port instead of 80
  --no-kernel          don't serve <port>.kernel.localhost at all
` + jerkLocalUsage + mountUsage + computeUsage + `
The link offers net (tcp and resolve), http and ws under the local policy:
public addresses, this machine and the local network, but not link-local
addresses, the debugging ports (below), or slicc's own listeners. There is
no cdp: slicc controls no browser here.
` + debugPortsHelp

type openFlags struct {
	page       string
	origins    []string
	name       string
	allow      policy.Rules
	deny       policy.Rules
	expose     policy.Ports
	kernelPort int
	noKernel   bool
	mounts     stringList
	noJerk     bool
	compute    computeFlags
	hosts      hostsFlag
	loaded     []hostfs.Mount
}

type openOptions struct {
	mode   string
	open   bool
	json   bool
	notice string
}

func parseOpen(verb string, args []string, stdout, stderr io.Writer) (openFlags, int) {
	f := openFlags{page: flow.DefaultPage}
	prog := "slicc " + verb
	given := args
	args, compute, err := extractCompute(args)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n\n%s", prog, err, openUsage)
		return f, 2
	}
	f.compute = compute
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&f.page, "page", flow.DefaultPage, "")
	fs.Var((*multi)(&f.origins), "origin", "")
	fs.StringVar(&f.name, "name", "", "")
	fs.Var(&f.allow, "allow", "")
	fs.Var(&f.deny, "deny", "")
	fs.Var(&f.expose, "expose-debug-port", "")
	fs.IntVar(&f.kernelPort, "kernel-port", kernelin.DefaultPort, "")
	fs.BoolVar(&f.noKernel, "no-kernel", false, "")
	fs.Var(&f.mounts, "mount", "")
	fs.BoolVar(&f.noJerk, "no-jerk", false, "")
	fs.Var(&f.hosts, "hosts", "")
	f.compute.register(fs)
	var six string
	registerSix(fs, &six)
	err = fs.Parse(args)
	switch {
	case six != "":
		return f, sixPointer(stderr, six, given)
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(stdout, openUsage)
		return f, 0
	case err != nil:
		fmt.Fprintf(stderr, "%s: %v\n\n%s", prog, err, openUsage)
		return f, 2
	case fs.NArg() > 0:
		fmt.Fprintf(stderr, "%s: unexpected argument %q\n\n%s", prog, fs.Arg(0), openUsage)
		return f, 2
	case f.kernelPort < 1 || f.kernelPort > 65535:
		fmt.Fprintf(stderr, "%s: --kernel-port must be 1-65535\n", prog)
		return f, 2
	}
	if err := f.compute.validate(); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n\n%s", prog, err, openUsage)
		return f, 2
	}
	if code := f.compute.checkRunner(stderr); code >= 0 {
		return f, code
	}
	if code := checkMounts(f.mounts, prog, stderr); code >= 0 {
		return f, code
	}
	if _, err := flow.PageOrigins(f.page, f.origins); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return f, 2
	}
	return f, -1
}

func defaultBrowserValue(arg string) (on, isFlag, ok bool) {
	if !strings.HasPrefix(arg, "-") {
		return false, false, true
	}
	name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
	if name != "default-browser" {
		return false, false, true
	}
	if !hasValue {
		return true, true, true
	}
	b, err := strconv.ParseBool(value)
	return b, true, err == nil
}

func defaultBrowserArgs(args []string) ([]string, bool) {
	rest := make([]string, 0, len(args))
	on := false
	for i := 0; i < len(args); i++ {
		if name, ok := argvFlagName(args[i]); ok && !strings.HasSuffix(name, "=") {
			j := i + 1
			for j < len(args) && args[j] != "--" {
				j++
			}
			end := min(j+1, len(args))
			rest = append(rest, args[i:end]...)
			i = end - 1
			continue
		}
		if args[i] == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		v, isFlag, ok := defaultBrowserValue(args[i])
		if !ok {
			return nil, false
		}
		if isFlag {
			on = v
			continue
		}
		rest = append(rest, args[i])
	}
	return rest, on
}

type openRun struct {
	opts   openOptions
	f      openFlags
	outMu  sync.Mutex
	status func(string, ...any)
	logf   func(string, ...any)
	stdout io.Writer
	ln     *loopback.Listener
	id     *loopback.Identity
	node   *node.Node
	pol    *policy.Policy
	host   string
	token  string
}

func runOpen(verb string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	f, code := parseOpen(verb, args, stdout, stderr)
	if code >= 0 {
		return code
	}
	return serveOpen(f, openOptions{mode: verb, open: verb == "open"}, stdin, stdout, stderr)
}

func serveOpen(f openFlags, opts openOptions, stdin io.Reader, stdout, stderr io.Writer) int {
	r := &openRun{opts: opts, f: f, stdout: stdout}
	r.status = func(format string, a ...any) { fmt.Fprintf(stderr, "slicc: "+format+"\n", a...) }
	r.logf = func(string, ...any) {}
	if os.Getenv("SLICC_DEBUG") != "" {
		r.logf = func(format string, a ...any) { fmt.Fprintf(stderr, "slicc debug: "+format+"\n", a...) }
	}
	ctx, stop := ossignal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if opts.notice != "" {
		r.status("%s", opts.notice)
		r.event(map[string]any{"schema": flow.Schema, "event": "mode", "mode": "loopback", "reason": "no-browser"})
	}
	if !r.f.noKernel {
		r.f.kernelPort = kernelin.Pick(r.f.kernelPort, r.status)
	}
	if err := r.listen(); err != nil {
		telemetry.ReportError(telemetry.Signal, err)
		r.status("%v", err)
		return 1
	}
	defer func() { _ = r.ln.Close() }()
	release, err := r.serveCaps(ctx)
	if err != nil {
		r.status("%v", err)
		return 1
	}
	defer release()
	if err := r.pair(); err != nil {
		r.status("%v", err)
		return 1
	}
	go r.readStdin(stdin)
	_ = r.ln.Serve(ctx, r.node.Serve)
	r.event(map[string]any{"schema": flow.Schema, "event": flow.Closed, "browsers": []string{}})
	return 0
}

func (r *openRun) linkEvent(e node.Event) {
	if !r.opts.json || e.Link == nil {
		return
	}
	base := map[string]any{"schema": flow.Schema, "mode": "loopback", "device": loopback.PeerFingerprint(e.Link)}
	switch e.Kind {
	case node.EventLinked:
		base["event"] = flow.Linked
		if e.Peer.Name != "" {
			base["peer"] = e.Peer.Name
		}
		base["caps"] = r.node.Caps()
		base["policy"] = r.pol.Summary()
	case node.EventLinkDown:
		base["event"] = flow.LinkLost
		if e.Reason != "" {
			base["reason"] = e.Reason
		} else if e.Err != nil {
			base["message"] = e.Err.Error()
		}
	default:
		return
	}
	r.event(base)
}

func loopbackPorts() (int, int) {
	first, last, ok := strings.Cut(os.Getenv(loopbackPortsEnv), "-")
	a, errA := strconv.Atoi(first)
	b, errB := strconv.Atoi(last)
	if !ok || errA != nil || errB != nil || a < 1 || b > 65535 || b < a {
		return loopback.FirstPort, loopback.LastPort
	}
	return a, b
}

func (r *openRun) listen() error {
	origins, err := flow.PageOrigins(r.f.page, r.f.origins)
	if err != nil {
		return err
	}
	allowed, err := signal.NewOrigins(origins)
	if err != nil {
		return err
	}
	dir, err := loopback.DefaultDir()
	if err != nil {
		return errors.New("no user config directory")
	}
	if r.id, err = loopback.LoadIdentity(dir); err != nil {
		return err
	}
	r.host = r.f.name
	if r.host == "" {
		r.host = node.DefaultHost()
	}
	first, last := loopbackPorts()
	var avoid []int
	if !r.f.noKernel {
		avoid = append(avoid, r.f.kernelPort)
	}
	r.ln, err = loopback.Listen(loopback.Config{
		Avoid: avoid, Identity: r.id, Peers: loopback.OpenPeers(dir), Origins: allowed, FirstPort: first, LastPort: last,
		Name: r.host, NodeID: signal.NodeID(), Logf: r.logf, Status: r.status,
	})
	if err != nil {
		return err
	}
	verb := "this computer"
	if r.id.Created {
		verb = "new loopback identity for this computer"
	}
	r.status("%s: %s (%s), fingerprint sha-256 %s", verb, r.host, loopback.ShortFingerprint(r.id.Fingerprint), loopback.Colons(r.id.Fingerprint))
	r.status("listening on %s", strings.Join(r.ln.Addrs(), " and "))
	return nil
}

func (r *openRun) ownPorts() []uint16 {
	ports := []uint16{uint16(r.ln.Port())}
	if !r.f.noKernel {
		ports = append(ports, uint16(r.f.kernelPort))
	}
	return ports
}

func (r *openRun) serveCaps(ctx context.Context) (func(), error) {
	r.pol = policy.Local(r.ownPorts()...).With(r.f.allow, r.f.deny)
	loopLog := func(format string, a ...any) { r.logf("loopback: "+format, a...) }
	if err := browser.Guard(ctx, r.pol, browser.Options{Status: r.status}, nil, r.f.expose, loopLog); err != nil {
		return nil, err
	}
	n := node.New(node.Config{
		Name: r.f.name, Host: r.f.name, Version: version, Mode: "local", Policy: r.pol, Hosts: r.f.hosts.hosts, Exit: true,
		Logf: r.logf, Status: r.status, Certificates: []webrtc.Certificate{r.id.Certificate},
	})
	r.node = n
	tcp.Register(n)
	resolve.Register(n)
	httpcap.Register(n)
	wscap.Register(n)
	udpcap.Register(n)
	if !r.f.noJerk {
		serveJerk(n, true, false, r.status)
	}
	closeSSH, err := r.f.compute.serve(n, r.status)
	if err != nil {
		return nil, err
	}
	mounts := r.f.loaded
	if mounts == nil {
		mounts = hostfs.Load(r.f.mounts, r.status)
	}
	closeMounts := serveMounts(n, mounts, r.status)
	closeKernel := func() {}
	if !r.f.noKernel {
		n.OnHello(kernelin.Announce(r.f.kernelPort))
		kern := kernelin.New(kernelin.Config{Port: r.f.kernelPort, Logf: r.logf, Status: r.status})
		closeKernel = func() { _ = kern.Close() }
		n.Subscribe(func(e node.Event) {
			if e.Kind == node.EventLinked {
				go kern.Add(e.Link)
			}
		})
	}
	n.Handle(loopback.CapUnlink, loopback.KindUnlink, r.ln.Unlink)
	n.Subscribe(func(e node.Event) {
		r.linkEvent(e)
		switch {
		case e.Link == nil:
		case e.Kind == node.EventConnecting:
			r.ln.LinkUp(e.Link)
		case e.Kind == node.EventLinkDown:
			go r.ln.LinkDown(e.Link)
		}
	})
	n.API()
	r.status("offering %s; policy: %s", strings.Join(n.Caps(), ", "), r.pol.Summary())
	kernelPort := r.f.kernelPort
	if r.f.noKernel {
		kernelPort = 0
	}
	h, closeCtl := serveCtl(r.opts.mode, n, kernelPort, r.logf)
	h.Update(func(s *ctl.Status) { s.Device = r.id.Fingerprint })
	unwatch := watchLocal(h, n, kernelPort)
	return func() { unwatch(); closeCtl(); closeKernel(); closeMounts(); closeSSH() }, nil
}

func (r *openRun) mint() (string, error) {
	tok := r.ln.Mint()
	u, err := r.ln.PairingURL(r.f.page, tok)
	if err != nil {
		return "", err
	}
	r.outMu.Lock()
	r.token = tok
	r.outMu.Unlock()
	time.AfterFunc(loopback.TokenLifetime, func() {
		r.outMu.Lock()
		current := r.token == tok
		r.outMu.Unlock()
		if unused := r.ln.Expire(tok); current && unused {
			r.status("the pairing URL expired; press Enter for a new one")
		}
	})
	return u, nil
}

func (r *openRun) pair() error {
	u, err := r.mint()
	if err != nil {
		return err
	}
	r.status("to link seven, open the pairing URL below; seven asks to link %s (%s). It works once, for 10 minutes; press Enter for a new one", r.host, loopback.ShortFingerprint(r.id.Fingerprint))
	r.printURL(u)
	if r.opts.open {
		if err := openBrowser(u); err != nil {
			r.status("could not open the default browser (%v); open the URL above yourself", err)
		} else {
			r.status("opened the pairing URL in the default browser")
		}
	}
	return nil
}

func (r *openRun) readStdin(stdin io.Reader) {
	if stdin == nil {
		return
	}
	sc := bufio.NewScanner(stdin)
	for sc.Scan() {
		u, err := r.mint()
		if err != nil {
			return
		}
		r.status("a new pairing URL, good once for 10 minutes:")
		r.printURL(u)
	}
}

func (r *openRun) event(e map[string]any) {
	if !r.opts.json {
		return
	}
	b, _ := json.Marshal(e)
	r.outMu.Lock()
	defer r.outMu.Unlock()
	fmt.Fprintf(r.stdout, "%s\n", b)
}

func (r *openRun) printURL(u string) {
	if r.opts.json {
		r.event(map[string]any{"schema": flow.Schema, "event": "pairing", "url": u})
		return
	}
	r.outMu.Lock()
	defer r.outMu.Unlock()
	fmt.Fprintln(r.stdout, u)
}

func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch custom := os.Getenv(openCommandEnv); {
	case custom != "":
		cmd = exec.Command(custom, u)
	case runtime.GOOS == "darwin":
		cmd = exec.Command("open", u)
	case runtime.GOOS == "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		return nil
	}
}
