package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	ossignal "os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	httpcap "github.com/ai-ecoverse/slicc-cli/internal/caps/http"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/resolve"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/tcp"
	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
)

const attachUsage = `usage: slicc attach --cdp <url> [flags]

Link a seven that is open in a running CDP browser. slicc finds the seven
tab, sets up a WebRTC link with it (seven offers, slicc answers), and serves
the link until interrupted. Status lines go to stderr.

flags:
  --cdp <url>          the browser's CDP endpoint: http://host:port or ws://...
                       seven can drive this browser over the link (cdp)
  --cdp-extra <url>    also let seven drive this browser (repeatable)
  --origin <origin>    also accept seven on this origin (repeatable); the
                       default is https://<label>.sliccy.ai, except www
  --signal cdp|stdio   carry the setup messages over CDP (default), or as
                       NDJSON on stdin and stdout for a harness that relays
                       them itself; --cdp is then optional
  --name <name>        the name and host this link shows in seven
  --allow <rule>       allow CIDR or CIDR:port (repeatable, comma-separated)
  --deny <rule>        deny CIDR or CIDR:port (repeatable, comma-separated)
` + computeUsage + `
The link offers the net capability (tcp and resolve streams) and the http
capability (fetches without redirects) under the local policy: public
addresses, this machine and the local network, but not link-local addresses
or the browsers' debugging ports. With --runner it also offers ssh: seven's
kernel reaches this machine's SSH server, which never listens on a port, and
its forwarding goes through the same policy. With --cdp or --cdp-extra it
also offers cdp: seven gets full control of every browser listed.
`

type attachFlags struct {
	cdp     string
	extra   []string
	origins []string
	signal  string
	name    string
	allow   policy.Rules
	deny    policy.Rules
	compute computeFlags
	hosts   hostsFlag
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func parseAttach(args []string, stderr io.Writer) (attachFlags, int) {
	var f attachFlags
	args, compute, err := extractCompute(args)
	if err != nil {
		fmt.Fprintf(stderr, "slicc attach: %v\n\n%s", err, attachUsage)
		return f, 2
	}
	f.compute = compute
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Var(singleCDP{&f.cdp}, "cdp", "")
	fs.Var((*multi)(&f.extra), "cdp-extra", "")
	fs.Var((*multi)(&f.origins), "origin", "")
	fs.StringVar(&f.signal, "signal", "cdp", "")
	fs.StringVar(&f.name, "name", "", "")
	fs.Var(&f.allow, "allow", "")
	fs.Var(&f.deny, "deny", "")
	f.compute.register(fs)
	fs.Var(&f.hosts, "hosts", "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stderr, attachUsage)
			return f, 0
		}
		fmt.Fprintf(stderr, "slicc attach: %v\n\n%s", err, attachUsage)
		return f, 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "slicc attach: unexpected argument %q\n\n%s", fs.Arg(0), attachUsage)
		return f, 2
	}
	if err := f.compute.validate(); err != nil {
		fmt.Fprintf(stderr, "slicc attach: %v\n\n%s", err, attachUsage)
		return f, 2
	}
	switch {
	case f.signal != "cdp" && f.signal != "stdio":
		fmt.Fprintf(stderr, "slicc attach: --signal must be cdp or stdio\n")
		return f, 2
	case f.signal == "cdp" && f.cdp == "":
		fmt.Fprintf(stderr, "slicc attach: --cdp is required\n\n%s", attachUsage)
		return f, 2
	}
	return f, -1
}

func attach(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Fprint(stdout, attachUsage)
			return 0
		}
	}
	f, code := parseAttach(args, stderr)
	if code >= 0 {
		return code
	}
	status := func(format string, a ...any) { fmt.Fprintf(stderr, "slicc: "+format+"\n", a...) }
	logf := func(string, ...any) {}
	if os.Getenv("SLICC_DEBUG") != "" {
		logf = func(format string, a ...any) { fmt.Fprintf(stderr, "slicc debug: "+format+"\n", a...) }
	}
	origins, err := signal.NewOrigins(f.origins)
	if err != nil {
		status("%v", err)
		return 2
	}
	ctx, stop := ossignal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	conn, closer, err := openSignal(ctx, f, origins, stdin, stdout, status, logf)
	if err != nil {
		status("%v", err)
		return 1
	}
	defer closer()
	browsers, ports, err := cdpBrowsers(ctx, f.cdp, f.extra, logf)
	if err != nil {
		status("%v", err)
		return 1
	}
	pol := policy.Local(ports...).With(f.allow, f.deny)
	n := node.New(node.Config{Name: f.name, Host: f.name, Version: version, Mode: "local", Policy: pol, Hosts: f.hosts.hosts, Exit: true, Logf: logf, Status: status})
	tcp.Register(n)
	resolve.Register(n)
	httpcap.Register(n)
	closeSSH, err := f.compute.serve(n, status)
	if err != nil {
		status("%v", err)
		return 1
	}
	defer closeSSH()
	serveBrowsers(ctx, n, browsers)
	if err := n.Serve(ctx, conn); err != nil && ctx.Err() == nil {
		status("%v", err)
		return 1
	}
	return 0
}

func openSignal(ctx context.Context, f attachFlags, origins *signal.Origins, stdin io.Reader, stdout io.Writer, status, logf func(string, ...any)) (signal.Conn, func(), error) {
	if f.signal == "stdio" {
		s, err := signal.NewStdio(stdin, stdout)
		if err != nil {
			return nil, nil, err
		}
		status("setup messages on stdin and stdout")
		return s, func() { _ = s.Close() }, nil
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := cdp.Dial(dctx, f.cdp)
	if err != nil {
		return nil, nil, err
	}
	status("connected to %s", f.cdp)
	s := signal.NewCDP(signal.CDPOptions{Client: client, Origins: origins, Logf: logf, Status: status})
	return s, func() { _ = s.Close(); _ = client.Close() }, nil
}

func debugPort(raw string) uint16 {
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "http", "ws":
			port = "80"
		case "https", "wss":
			port = "443"
		}
	}
	p, _ := strconv.ParseUint(port, 10, 16)
	return uint16(p)
}
