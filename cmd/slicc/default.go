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

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	hostfs "github.com/ai-ecoverse/slicc-cli/internal/caps/fs"
	httpcap "github.com/ai-ecoverse/slicc-cli/internal/caps/http"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/kernelin"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/resolve"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/tcp"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
	"github.com/ai-ecoverse/slicc-cli/internal/tui"
)

const defaultUsage = "usage: slicc [flags]\n" + defaultHelp

const defaultHelp = `
With no command, slicc finds browsers, launches or attaches to the best one,
opens seven in it and links it. In a terminal it shows a picker and then a
dashboard. Without a terminal, or with --yes, --browser, --plain or --json,
it takes the best match and prints status lines on stderr. q, Ctrl-C or
SIGTERM says bye to seven, closes the browsers this run launched and exits.

flags for slicc with no command:
  --browser <id>      use this browser from "slicc browsers" (repeatable;
                      the first one hosts seven)
  --cdp <url>         also consider this CDP endpoint (repeatable)
  --page <url>        the seven page to open (default ` + flow.DefaultPage + `);
                      its origin is accepted too
  --origin <origin>   also accept seven on this origin (repeatable)
  --profile <name>    SLICC profile for a launched browser (default: its id)
  --headless          launch browsers without a window
  --name <name>       the name and host this link shows in seven
  --kernel-port <n>   serve http://<port>.kernel.localhost on this local
                      port instead of 80
  --no-kernel         don't serve <port>.kernel.localhost at all
  --mount <path>[:<name>][:ro]
                      share this folder with seven's kernel (repeatable)
  --allow <rule>      allow CIDR or CIDR:port (repeatable, comma-separated)
  --deny <rule>       deny CIDR or CIDR:port (repeatable, comma-separated)
  --yes               no picker: take the best match
  --plain             no picker and no dashboard: plain status lines
  --json              NDJSON events on stdout (see README.md)
` + computeUsage + `
SLICC six's flags (such as --electron, --lead, --serve-only or -v) print
how to run six with npx sliccy@6, and exit 2.
`

type defaultFlags struct {
	browsers []string
	cdp      []string
	page     string
	origins  []string
	profile  string
	headless bool
	kernel   int
	noKernel bool
	name     string
	allow    policy.Rules
	deny     policy.Rules
	yes      bool
	plain    bool
	json     bool
	compute  computeFlags
	mounts   stringList
}

func isDefault(args []string) bool {
	return len(args) == 0 || (strings.HasPrefix(args[0], "-") && !quietCommands[args[0]])
}

func parseDefault(args []string, stderr io.Writer) (defaultFlags, int) {
	var f defaultFlags
	given := args
	args, compute, err := extractCompute(args)
	if err != nil {
		fmt.Fprintf(stderr, "slicc: %v\n\n%s", err, defaultUsage)
		return f, 2
	}
	f.compute = compute
	fs := flag.NewFlagSet("slicc", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f.compute.register(fs)
	fs.Var((*multi)(&f.browsers), "browser", "")
	fs.Var((*multi)(&f.cdp), "cdp", "")
	fs.StringVar(&f.page, "page", flow.DefaultPage, "")
	fs.Var((*multi)(&f.origins), "origin", "")
	fs.StringVar(&f.profile, "profile", "", "")
	fs.BoolVar(&f.headless, "headless", false, "")
	fs.StringVar(&f.name, "name", "", "")
	fs.IntVar(&f.kernel, "kernel-port", kernelin.DefaultPort, "")
	fs.BoolVar(&f.noKernel, "no-kernel", false, "")
	fs.Var(&f.allow, "allow", "")
	fs.Var(&f.deny, "deny", "")
	fs.BoolVar(&f.yes, "yes", false, "")
	fs.BoolVar(&f.plain, "plain", false, "")
	fs.BoolVar(&f.json, "json", false, "")
	fs.Var(&f.mounts, "mount", "")
	var six string
	registerSix(fs, &six)
	err = fs.Parse(args)
	if six != "" {
		return f, sixPointer(stderr, six, given)
	}
	if err != nil {
		fmt.Fprintf(stderr, "slicc: %v\n\n%s", err, defaultUsage)
		return f, 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "slicc: unexpected argument %q\n\n%s", fs.Arg(0), defaultUsage)
		return f, 2
	}
	if err := f.compute.validate(); err != nil {
		fmt.Fprintf(stderr, "slicc: %v\n\n%s", err, defaultUsage)
		return f, 2
	}
	if code := checkMounts(f.mounts, "slicc", stderr); code >= 0 {
		return f, code
	}
	if f.kernel < 1 || f.kernel > 65535 {
		fmt.Fprintf(stderr, "slicc: --kernel-port must be 1-65535\n")
		return f, 2
	}
	if f.noKernel {
		f.kernel = 0
	}
	if f.profile != "" && !browser.ValidProfileName(f.profile) {
		fmt.Fprintf(stderr, "slicc: invalid profile name %q\n", f.profile)
		return f, 2
	}
	if _, err := flow.PageOrigins(f.page, f.origins); err != nil {
		fmt.Fprintf(stderr, "slicc: %v\n", err)
		return f, 2
	}
	return f, -1
}

func registerCaps(cf computeFlags, logf func(string, ...any), mounts []hostfs.Mount) func(context.Context, *node.Node, *policy.Policy, []flow.Browser) (flow.Caps, error) {
	return func(ctx context.Context, n *node.Node, pol *policy.Policy, browsers []flow.Browser) (flow.Caps, error) {
		tcp.Register(n)
		resolve.Register(n)
		httpcap.Register(n)
		closeMounts := serveMounts(n, mounts, logf)
		var seven string
		var extra, order []string
		for _, b := range browsers {
			if b.Seven {
				seven = b.CDP
				order = append([]string{b.ID}, order...)
			} else {
				extra = append(extra, b.CDP)
				order = append(order, b.ID)
			}
		}
		reg, err := cdpBrowsers(ctx, seven, extra, pol, nil, logf)
		if err != nil {
			closeMounts()
			return flow.Caps{}, err
		}
		closeSSH, err := cf.serve(n, logf)
		if err != nil {
			closeMounts()
			return flow.Caps{}, err
		}
		ids := map[string]string{}
		for i, b := range reg.List() {
			if i < len(order) {
				ids[b.ID] = order[i]
			}
		}
		wctx, cancel := context.WithCancel(ctx)
		serveBrowsers(wctx, n, reg)
		live := func() map[string]bool {
			out := map[string]bool{}
			for _, b := range reg.List() {
				if id, ok := ids[b.ID]; ok {
					out[id] = true
				}
			}
			return out
		}
		return flow.Caps{Release: func() { cancel(); closeSSH(); closeMounts() }, Live: live}, nil
	}
}

func (cf *computeFlags) dashboard() string {
	if len(cf.runner) == 0 {
		return ""
	}
	return "ssh via " + strings.Join(cf.runner, " ")
}

func isTerminal(w any) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func runDefault(args []string, stdout, stderr io.Writer) int {
	f, code := parseDefault(args, stderr)
	if code >= 0 {
		return code
	}
	interactive := !f.json && !f.plain && !f.yes && len(f.browsers) == 0 && isTerminal(os.Stdin) && isTerminal(stdout)
	logf := func(string, ...any) {}
	if os.Getenv("SLICC_DEBUG") != "" && !interactive {
		logf = func(format string, a ...any) { fmt.Fprintf(stderr, "slicc debug: "+format+"\n", a...) }
	}
	mounts := hostfs.Load(f.mounts, func(format string, a ...any) { fmt.Fprintf(stderr, "slicc: "+format+"\n", a...) })
	cfg := flow.Config{
		Page: f.page, Origins: f.origins, Profile: f.profile, Headless: f.headless, Kernel: f.kernel, Name: f.name, Version: version,
		Allow: f.allow, Deny: f.deny, Register: registerCaps(f.compute, logf, mounts), Compute: f.compute.dashboard(), Logf: logf,
	}
	origins, _ := flow.PageOrigins(f.page, f.origins)
	cfg.Browser = browser.Options{CDPURLs: f.cdp, Origins: origins}
	if interactive {
		cfg.Chosen = true
		return runTUI(cfg, stdout, stderr)
	}
	cfg.Chosen = len(f.browsers) > 0
	return runPlain(cfg, f, stdout, stderr)
}

func runPlain(cfg flow.Config, f defaultFlags, stdout, stderr io.Writer) int {
	flushNotice(stderr)
	out := plainOut{stdout: stdout, stderr: stderr, json: f.json}
	cfg.Emit = out.event
	ctx, stop := ossignal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	r := browser.Discover(ctx, cfg.Browser)
	for _, err := range r.Errors {
		fmt.Fprintf(stderr, "slicc: %v\n", err)
	}
	out.event(flow.Event{Schema: flow.Schema, Event: flow.Discovered, Entries: r.Entries})
	sel, err := flow.Resolve(r.Entries, f.browsers)
	if err != nil {
		out.event(flow.Event{Schema: flow.Schema, Event: flow.Error, Message: err.Error()})
		return 1
	}
	if err := flow.Go(ctx, cfg, sel); err != nil {
		return 1
	}
	return 0
}

type plainOut struct {
	mu     sync.Mutex
	stdout io.Writer
	stderr io.Writer
	json   bool
}

func (o *plainOut) event(e flow.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.json && flow.Public[e.Event] {
		b, _ := json.Marshal(e)
		fmt.Fprintf(o.stdout, "%s\n", b)
	}
	if line := plainLine(e); line != "" && (!o.json || e.Event == flow.Error || e.Event == flow.ForeignBrowser) {
		fmt.Fprintf(o.stderr, "slicc: %s\n", line)
	}
}

func named(e flow.Event) string {
	s := e.Name
	if e.Version != "" {
		s += " " + e.Version
	}
	return s
}

func plainLine(e flow.Event) string {
	switch e.Event {
	case flow.Discovered:
		return fmt.Sprintf("found %d browsers", len(e.Entries))
	case flow.Launched:
		return fmt.Sprintf("launched %s (profile %s)", named(e), e.ProfileDir)
	case flow.Attached:
		role := "hosts seven"
		if e.Role == flow.RoleExtra {
			role = "extra browser"
		}
		return fmt.Sprintf("attached to %s at %s (%s)", named(e), e.CDP, role)
	case flow.Serving:
		line := fmt.Sprintf("offering %s; policy: %s", strings.Join(e.Caps, ", "), e.Policy)
		if e.Compute != "" {
			line += "; compute: " + e.Compute
		}
		return line
	case flow.BrowserGone:
		return e.Name + " quit; it is no longer listed for cdp"
	case flow.BrowserBack:
		return e.Name + " is back and listed for cdp again"
	case flow.Linked:
		return fmt.Sprintf("linked with %q in %s", firstNonEmpty(e.Peer, "seven"), firstNonEmpty(e.Tab, "the seven tab"))
	case flow.LinkLost:
		return "link lost: " + firstNonEmpty(e.Message, e.Reason, "closed")
	case flow.Status:
		return e.Message
	case flow.ForeignBrowser:
		return foreignLine(e)
	case flow.Error:
		return "error: " + e.Message
	case flow.Closed:
		if len(e.Browsers) == 0 {
			return "done"
		}
		return "closed " + strings.Join(e.Browsers, ", ")
	}
	return ""
}

func foreignLine(e flow.Event) string {
	who := e.Name
	if e.Profile != "" {
		who += " (profile " + e.Profile + ")"
	}
	what := "found a seven tab"
	if e.OpenedTab != nil && *e.OpenedTab {
		what = "opened a new seven tab"
	}
	return fmt.Sprintf("using %s at %s, which slicc didn't launch; %s. Use --browser or --cdp to choose another.", who, e.CDP, what)
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func runTUI(cfg flow.Config, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var p *tea.Program
	var started sync.WaitGroup
	var failed error
	tcfg := tui.Config{
		Profile:  cfg.Profile,
		Color:    os.Getenv("NO_COLOR") == "",
		Notice:   strings.TrimSpace(takeNotice()),
		Profiles: func(e browser.Entry) []string { return flow.Profiles(cfg.Browser, *e.Installed) },
		Stop:     cancel,
	}
	tcfg.Start = func(c tui.Choice) {
		fc := cfg
		fc.Profiles = c.Profiles
		events := make(chan tea.Msg, 4096)
		fc.Emit = func(e flow.Event) { events <- tui.EventMsg(e) }
		started.Add(1)
		go func() {
			for m := range events {
				p.Send(m)
			}
		}()
		go func() {
			defer started.Done()
			err := flow.Go(ctx, fc, c.Entries)
			failed = err
			events <- tui.DoneMsg{Err: err}
			close(events)
		}()
	}
	p = tea.NewProgram(tui.New(tcfg), tea.WithOutput(stdout))
	go func() {
		r := browser.Discover(ctx, cfg.Browser)
		p.Send(tui.EntriesMsg(r.Entries))
	}()
	_, err := p.Run()
	cancel()
	started.Wait()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) && !errors.Is(err, tea.ErrInterrupted) {
		fmt.Fprintf(stderr, "slicc: %v\n", err)
		return 1
	}
	if failed != nil {
		fmt.Fprintf(stderr, "slicc: %v\n", failed)
		return 1
	}
	return 0
}
