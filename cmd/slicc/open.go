package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	ossignal "os/signal"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	hostfs "github.com/ai-ecoverse/slicc-cli/internal/caps/fs"
	httpcap "github.com/ai-ecoverse/slicc-cli/internal/caps/http"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/kernelin"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/resolve"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/tcp"
	wscap "github.com/ai-ecoverse/slicc-cli/internal/caps/ws"
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
	activePortsID    = "devtools-active-port"
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
  --kernel-port <n>    serve http://<port>.kernel.localhost on this local
                       port instead of 80
  --no-kernel          don't serve <port>.kernel.localhost at all
` + jerkLocalUsage + mountUsage + computeUsage + `
The link offers net (tcp and resolve), http and ws under the local policy:
public addresses, this machine and the local network, but not link-local
addresses, ports 9222-9229, the ports in any DevToolsActivePort file, or
slicc's own listeners. There is no cdp: slicc controls no browser here.
`

type openFlags struct {
	page       string
	origins    []string
	name       string
	allow      policy.Rules
	deny       policy.Rules
	kernelPort int
	noKernel   bool
	mounts     stringList
	noJerk     bool
	compute    computeFlags
	hosts      hostsFlag
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
	if code := checkMounts(f.mounts, prog, stderr); code >= 0 {
		return f, code
	}
	if _, err := flow.PageOrigins(f.page, f.origins); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return f, 2
	}
	return f, -1
}

func defaultBrowserArgs(args []string) ([]string, bool) {
	for i, a := range args {
		if a == "--" {
			break
		}
		if a == "--default-browser" || a == "-default-browser" {
			return append(slices.Clone(args[:i]), args[i+1:]...), true
		}
	}
	return nil, false
}

type openRun struct {
	verb   string
	f      openFlags
	status func(string, ...any)
	logf   func(string, ...any)
	stdout io.Writer
	ln     *loopback.Listener
	id     *loopback.Identity
	node   *node.Node
	pol    *policy.Policy
	host   string
}

func runOpen(verb string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	for _, a := range args {
		if a == "--help" || a == "-h" || a == "-help" {
			fmt.Fprint(stdout, openUsage)
			return 0
		}
	}
	f, code := parseOpen(verb, args, stdout, stderr)
	if code >= 0 {
		return code
	}
	r := &openRun{verb: verb, f: f, stdout: stdout}
	r.status = func(format string, a ...any) { fmt.Fprintf(stderr, "slicc: "+format+"\n", a...) }
	r.logf = func(string, ...any) {}
	if os.Getenv("SLICC_DEBUG") != "" {
		r.logf = func(format string, a ...any) { fmt.Fprintf(stderr, "slicc debug: "+format+"\n", a...) }
	}
	ctx, stop := ossignal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := r.listen(); err != nil {
		telemetry.ReportError(telemetry.Signal, err)
		r.status("%v", err)
		return 1
	}
	defer func() { _ = r.ln.Close() }()
	release, err := r.serveCaps()
	if err != nil {
		r.status("%v", err)
		return 1
	}
	defer release()
	go r.watchActivePorts(ctx)
	if err := r.pair(); err != nil {
		r.status("%v", err)
		return 1
	}
	go r.readStdin(stdin)
	_ = r.ln.Serve(ctx, r.node.Serve)
	return 0
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
	r.ln, err = loopback.Listen(loopback.Config{
		Identity: r.id, Peers: loopback.OpenPeers(dir), Origins: allowed, FirstPort: first, LastPort: last,
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

func (r *openRun) protectedPorts() []uint16 {
	var ports []uint16
	for p := uint16(9222); p <= 9229; p++ {
		ports = append(ports, p)
	}
	ports = append(ports, browser.ActivePorts(browser.Options{})...)
	ports = append(ports, uint16(r.ln.Port()))
	if !r.f.noKernel {
		ports = append(ports, uint16(r.f.kernelPort))
	}
	return ports
}

func (r *openRun) serveCaps() (func(), error) {
	r.pol = policy.Local(r.protectedPorts()...).With(r.f.allow, r.f.deny)
	n := node.New(node.Config{
		Name: r.f.name, Host: r.f.name, Version: version, Mode: "local", Policy: r.pol, Hosts: r.f.hosts.hosts, Exit: true,
		Logf: r.logf, Status: r.status, Certificates: []webrtc.Certificate{r.id.Certificate},
	})
	r.node = n
	tcp.Register(n)
	resolve.Register(n)
	httpcap.Register(n)
	wscap.Register(n)
	if !r.f.noJerk {
		serveJerk(n, true, false, r.status)
	}
	closeSSH, err := r.f.compute.serve(n, r.status)
	if err != nil {
		return nil, err
	}
	closeMounts := serveMounts(n, hostfs.Load(r.f.mounts, r.status), r.status)
	closeKernel := func() {}
	if !r.f.noKernel {
		kern := kernelin.New(kernelin.Config{Port: r.f.kernelPort, Logf: r.logf, Status: r.status})
		closeKernel = func() { _ = kern.Close() }
		n.Subscribe(func(e node.Event) {
			if e.Kind == node.EventLinked {
				go kern.Add(e.Link)
			}
		})
	}
	n.Handle(loopback.CapUnlink, loopback.KindUnlink, r.ln.Unlink)
	n.API()
	r.status("offering %s; policy: %s", strings.Join(n.Caps(), ", "), r.pol.Summary())
	return func() { closeKernel(); closeMounts(); closeSSH() }, nil
}

func (r *openRun) watchActivePorts(ctx context.Context) {
	known := map[uint16]bool{}
	for _, p := range r.protectedPorts() {
		known[p] = true
	}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var add []netip.AddrPort
		for _, p := range browser.ActivePorts(browser.Options{}) {
			if !known[p] {
				known[p] = true
				add = append(add, netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), p), netip.AddrPortFrom(netip.IPv6Loopback(), p))
			}
		}
		if len(add) > 0 {
			r.pol.AddProtectedEndpoints(activePortsID, add)
			r.logf("loopback: denied new DevToolsActivePort ports: %v", add)
		}
	}
}

func (r *openRun) pair() error {
	tok := r.ln.Mint()
	u, err := r.ln.PairingURL(r.f.page, tok)
	if err != nil {
		return err
	}
	r.status("to link seven, open the pairing URL below; seven asks to link %s (%s). It works once, for 10 minutes; press Enter for a new one", r.host, loopback.ShortFingerprint(r.id.Fingerprint))
	fmt.Fprintln(r.stdout, u)
	if r.verb == "open" {
		if err := openBrowser(u); err != nil {
			r.status("could not open the default browser (%v); open the URL above yourself", err)
		} else {
			r.status("opened the pairing URL in the default browser")
		}
	}
	go func() {
		time.Sleep(loopback.TokenLifetime)
		if r.ln.Valid(tok) {
			r.status("the pairing URL expired; press Enter for a new one")
		}
	}()
	return nil
}

func (r *openRun) readStdin(stdin io.Reader) {
	if stdin == nil {
		return
	}
	sc := bufio.NewScanner(stdin)
	for sc.Scan() {
		tok := r.ln.Mint()
		u, err := r.ln.PairingURL(r.f.page, tok)
		if err != nil {
			return
		}
		r.status("a new pairing URL, good once for 10 minutes:")
		fmt.Fprintln(r.stdout, u)
	}
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
