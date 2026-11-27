//go:build !wasip1

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	ossignal "os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	guarded "github.com/ai-ecoverse/slicc-cli/internal/caps/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/lease"
	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
	"golang.org/x/term"
)

const leaseUsage = `Signed leases allow goto and scoped top-document snapshots only. No clicks, fills or form submissions.
usage: slicc lease review --to jkt:<thumbprint> --allow <HTTPS rule> --for <duration>
       slicc lease grant --no-input --link <label> --browser <loopback HTTP origin> --to jkt:<thumbprint> --allow <rule> --for <duration>
       slicc lease grant <request> --add <rule> --keep-expiry
       slicc lease status | deny <request> | revoke <lease>
       slicc lease pair --link <label> --peer <verified Seven SHA-256 fingerprint>
`

type leaseRoute struct {
	path   string
	status *ctl.Status
	lease.Snapshot
}

func leaseRoutes() ([]leaseRoute, error) {
	ctx := context.Background()
	entries, e := ctl.ListLocal(ctx)
	if e != nil {
		return nil, e
	}
	out := []leaseRoute{}
	for _, ent := range entries {
		st := ent.Status
		if st == nil || !slices.Equal(st.Offers, []string{lease.Cap}) {
			continue
		}
		complete := true
		for _, op := range []string{ctl.OpLeaseList, ctl.OpLeaseApprove, ctl.OpLeaseDeny, ctl.OpLeaseRevoke} {
			complete = complete && slices.Contains(st.Ops, op)
		}
		if !complete {
			continue
		}
		r, e := ctl.Call(ctx, ent.Path, ctl.Request{Op: ctl.OpLeaseList})
		if e != nil || !r.OK {
			continue
		}
		row := leaseRoute{path: ent.Path, status: st}
		if json.Unmarshal(r.Pending, &row.Pending) != nil || json.Unmarshal(r.Active, &row.Active) != nil {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}
func ownerRule(raw string) (lease.Rule, error) {
	r := lease.Rule{URL: raw, Kind: "origin"}
	if strings.HasSuffix(raw, "/**") {
		r.Kind = "path-prefix"
		r.URL = strings.TrimSuffix(raw, "/**")
		if !strings.Contains(strings.TrimPrefix(r.URL, "https://"), "/") {
			r.URL += "/"
		}
	} else if strings.ContainsAny(strings.TrimPrefix(raw, "https://"), "/?#") {
		r.Kind = "exact"
	}
	if !r.Valid(nil) {
		return r, errors.New("invalid or protected HTTPS rule")
	}
	return r, nil
}
func runLease(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" {
		fmt.Fprint(stdout, leaseUsage)
		return 0
	}
	if e := ownerLease(args, stdout, stderr); e != nil {
		fmt.Fprintf(stderr, "slicc lease: %v\n", e)
		return 1
	}
	return 0
}

type selection struct {
	path string
	p    lease.Pending
	pid  int
}
type ownerFlags struct {
	op, target, label, endpoint, to, peer string
	ttl                                   time.Duration
	keep, noInput                         bool
	scope, delta                          stringList
}

var errLeaseUsage = errors.New(strings.TrimSpace(leaseUsage))

var brokerID = regexp.MustCompile(`^[A-Za-z0-9_-]{24}$`)

func parseOwner(args []string) (ownerFlags, error) {
	f := ownerFlags{op: args[0]}
	args = args[1:]
	if len(args) > 0 && (f.op == "grant" || f.op == "deny" || f.op == "revoke") && brokerID.MatchString(args[0]) {
		f.target = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("lease", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&f.label, "link", "", "")
	fs.StringVar(&f.endpoint, "browser", "", "")
	fs.StringVar(&f.to, "to", "", "")
	fs.StringVar(&f.peer, "peer", "", "")
	fs.DurationVar(&f.ttl, "for", 0, "")
	fs.BoolVar(&f.keep, "keep-expiry", false, "")
	fs.BoolVar(&f.noInput, "no-input", false, "")
	fs.Var(&f.scope, "allow", "")
	fs.Var(&f.delta, "add", "")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		return f, errLeaseUsage
	}
	switch f.op {
	case "pair":
		if fs.NFlag() != 2 || f.target != "" || f.label == "" || f.peer == "" {
			return f, errLeaseUsage
		}
	case "status", "deny", "revoke":
		if fs.NFlag() != 0 || (f.op == "status") != (f.target == "") {
			return f, errLeaseUsage
		}
	case "review", "grant":
		if f.peer != "" {
			return f, errLeaseUsage
		}
	default:
		return f, errLeaseUsage
	}
	return f, nil
}
func (f ownerFlags) rules() ([]lease.Rule, error) {
	scope := f.scope
	if f.target != "" {
		if f.op != "grant" || !f.keep || len(f.delta) == 0 || len(scope) > 0 || f.ttl != 0 || f.to != "" {
			return nil, errLeaseUsage
		}
		scope = f.delta
	} else {
		jkt, ok := strings.CutPrefix(f.to, "jkt:")
		if !ok || !lease.ValidJKT(jkt) || f.ttl < time.Second || f.ttl > lease.MaxTTL*time.Second || f.ttl%time.Second != 0 || len(scope) == 0 || f.keep || len(f.delta) > 0 {
			return nil, errLeaseUsage
		}
		if f.op == "grant" && (!f.noInput || !guarded.LocalEndpoint(f.endpoint) || !leaseLabel.MatchString(f.label)) {
			return nil, errors.New("automation requires --no-input, an explicit browser and a paired link")
		}
	}
	rules := []lease.Rule{}
	for _, v := range scope {
		r, e := ownerRule(v)
		if e != nil {
			return nil, e
		}
		if f.target != "" && r.Kind != "exact" {
			return nil, errors.New("--add requires an exact HTTPS URL, including its path")
		}
		rules = append(rules, r)
	}
	return rules, nil
}
func (f ownerFlags) matches(p lease.Pending, rules []lease.Rule) bool {
	if f.label != "" && p.Link != f.label || f.endpoint != "" && p.Browser != f.endpoint || !slices.Equal(rules, p.Allow) {
		return false
	}
	if f.target != "" {
		return p.Request == f.target && p.Parent != ""
	}
	return p.JKT == strings.TrimPrefix(f.to, "jkt:") && p.TTL == int(f.ttl/time.Second) && p.Parent == ""
}
func (f ownerFlags) ownerOp(routes []leaseRoute, out io.Writer) error {
	for _, r := range routes {
		switch f.op {
		case "status":
			b, _ := json.Marshal(r.Snapshot)
			fmt.Fprintln(out, string(b))
		case "revoke":
			if slices.ContainsFunc(r.Active, func(a lease.Active) bool { return a.Lease == f.target }) {
				return ownerCall(r.path, ctl.Request{Op: ctl.OpLeaseRevoke, Lease: f.target})
			}
		case "deny":
			if slices.ContainsFunc(r.Pending, func(p lease.Pending) bool { return p.Request == f.target }) {
				return ownerCall(r.path, ctl.Request{Op: ctl.OpLeaseDeny, Request: f.target})
			}
		}
	}
	if f.op == "status" {
		return nil
	}
	return errors.New("unknown or stale ID")
}
func ownerLease(args []string, out, errOut io.Writer) error {
	f, e := parseOwner(args)
	if e != nil {
		return e
	}
	if f.op == "pair" {
		return pairPin(f.label, f.peer)
	}
	if f.op == "review" && (!term.IsTerminal(int(os.Stdin.Fd())) || f.noInput) {
		return errors.New("review requires a local terminal; nothing was issued")
	}
	routes, e := leaseRoutes()
	if e != nil {
		return e
	}
	if f.op != "review" && f.op != "grant" {
		return f.ownerOp(routes, out)
	}
	rules, e := f.rules()
	if e != nil {
		return e
	}
	rd := bufio.NewReaderSize(os.Stdin, 2048)
	if len(routes) == 0 && f.op == "review" {
		return f.coldReview(rules, rd, errOut)
	}
	var choices []selection
	for _, r := range routes {
		if r.status.State != ctl.StateConnected {
			continue
		}
		for _, p := range r.Pending {
			if f.matches(p, rules) {
				choices = append(choices, selection{path: r.path, p: p})
			}
		}
	}
	_, e = f.decide(choices, rd, errOut)
	return e
}
func (f ownerFlags) decide(choices []selection, rd *bufio.Reader, errOut io.Writer) (string, error) {
	if len(choices) == 0 {
		return "", errors.New("no matching proven request on a connected lease-only link; nothing issued")
	}
	chosen := 0
	if f.op == "review" {
		for i, c := range choices {
			fmt.Fprintf(errOut, "[%d] Link %s · Browser %s · one new tab · holder jkt:%s\n", i+1, c.p.Link, c.p.Browser, c.p.JKT)
		}
		if len(choices) > 1 {
			n := line(rd, errOut, "Select the browser and link by number: ")
			v, e := strconv.Atoi(n)
			if e != nil || v < 1 || v > len(choices) {
				return "", errors.New("no selection")
			}
			chosen = v - 1
		}
		c := choices[chosen]
		for _, r := range c.p.Allow {
			fmt.Fprintf(errOut, "%s: %s\n", r.Kind, r.URL)
		}
		fmt.Fprintf(errOut, "For %s from approval. Operates as you in the selected profile. This broker can navigate and read the top document; other tabs stay private.\n", (time.Duration(c.p.TTL) * time.Second).String())
		switch word := line(rd, errOut, "Type APPROVE to issue, DENY to reject, or Enter to cancel: "); word {
		case "APPROVE":
		case "DENY":
			if c.pid != 0 {
				return word, nil
			}
			return word, c.call(ctl.Request{Op: ctl.OpLeaseDeny, Request: c.p.Request})
		default:
			return word, errNotApproved
		}
	} else if len(choices) != 1 {
		return "", errors.New("ambiguous request; select a single paired link and browser")
	}
	c := choices[chosen]
	if c.pid != 0 {
		return "APPROVE", nil
	}
	return "APPROVE", c.call(ctl.Request{Op: ctl.OpLeaseApprove, Request: c.p.Request})
}
func (cp *coldPair) childCall(c selection, q ctl.Request) error {
	cp.mu.Lock()
	child := cp.child
	cp.mu.Unlock()
	if child == nil || !child.alive() {
		return errLeaseLinkStopped
	}
	return c.call(q)
}
func (cp *coldPair) beginApprove() bool {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if cp.interrupted {
		return false
	}
	cp.approving = true
	return true
}

type approveFinish int

const (
	approveClear approveFinish = iota
	approveGranted
	approveUnknown
)

func (cp *coldPair) finishApprove(outcome approveFinish) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.approving = false
	switch outcome {
	case approveGranted:
		cp.kept = true
		cp.approveUnknown = false
	case approveUnknown:
		cp.kept = true
		cp.approveUnknown = true
	}
}

var afterApproveReply func()

func (cp *coldPair) approve(c selection) error {
	cp.mu.Lock()
	child := cp.child
	cp.mu.Unlock()
	if child == nil || !child.alive() {
		return errLeaseLinkStopped
	}
	if !cp.beginApprove() {
		return errInterrupted
	}
	e := c.call(ctl.Request{Op: ctl.OpLeaseApprove, Request: c.p.Request})
	if afterApproveReply != nil {
		afterApproveReply()
	}
	if e == nil {
		cp.finishApprove(approveGranted)
		fmt.Fprintf(cp.out, "Approved for %s from approval.\n", (time.Duration(c.p.TTL) * time.Second).String())
		return nil
	}
	if errors.Is(e, errDecisionRefused) {
		cp.finishApprove(approveClear)
		return e
	}
	cp.finishApprove(approveUnknown)
	fmt.Fprintln(cp.out, "The approval result is unknown; check slicc lease status and slicc lease revoke <lease-id>; the lease-only link was kept.")
	return nil
}
func (c selection) call(q ctl.Request) error {
	if c.pid == 0 {
		return ownerCall(c.path, q)
	}
	return ownerResult(ctl.CallPID(context.Background(), c.path, c.pid, q))
}
func ownerCall(path string, q ctl.Request) error {
	return ownerResult(ctl.Call(context.Background(), path, q))
}
func ownerResult(r *ctl.Reply, e error) error {
	if e != nil {
		return e
	}
	if !r.OK {
		return fmt.Errorf("%w: %s", errDecisionRefused, r.Error)
	}
	return nil
}
func line(r *bufio.Reader, out io.Writer, prompt string) string {
	fmt.Fprint(out, prompt)
	v, e := r.ReadSlice('\n')
	if e != nil {
		return ""
	}
	return strings.TrimSpace(string(v))
}

const (
	leaseDiscovery     = 15 * time.Second
	leaseDecisionFlush = 3 * time.Second
)

var (
	errNotApproved      = errors.New("not approved")
	errDecisionRefused  = errors.New("decision refused")
	errLeaseLinkStopped = errors.New("the lease-only link stopped")
	errInterrupted      = errors.New("interrupted")
)

type leaseChild struct {
	proc   *os.Process
	exited chan struct{}
	pin    string
	mu     sync.Mutex
	path   string
	code   int
}

type coldPair struct {
	rd             *bufio.Reader
	out            io.Writer
	secret         func(func()) ([]byte, error)
	mu             sync.Mutex
	child          *leaseChild
	kept           bool
	approveUnknown bool
	approving      bool
	interrupted    bool
	restore        func()
	raw            bool
	restored       bool
}

func (f ownerFlags) coldReview(rules []lease.Rule, rd *bufio.Reader, out io.Writer) error {
	if !ctl.PeerPIDs {
		return errors.New("a first pair needs the control socket peer check, which this platform lacks; nothing was dialed or issued")
	}
	cp := &coldPair{rd: rd, out: out}
	cp.secret = func(show func()) ([]byte, error) { return readHidden(os.Stdin, show, cp) }
	done := cp.watchSignals()
	defer done()
	return cp.review(f, rules)
}

func readHidden(f *os.File, show func(), cp *coldPair) ([]byte, error) {
	fd, e := fileFD(f)
	if e != nil {
		return nil, e
	}
	old, e := term.GetState(fd)
	if e != nil {
		return nil, e
	}
	restore := sync.OnceFunc(func() {
		cp.mu.Lock()
		defer cp.mu.Unlock()
		_ = term.Restore(fd, old)
		cp.raw = false
		cp.restored = true
	})
	undoStop := ignoreStopSignals()
	defer func() {
		restore()
		undoStop()
		cp.hold(nil)
	}()
	cp.mu.Lock()
	if cp.interrupted || cp.restored {
		cp.mu.Unlock()
		return nil, errInterrupted
	}
	cp.restore = restore
	cp.restored = false
	_, e = term.MakeRaw(fd)
	cp.raw = e == nil
	cp.mu.Unlock()
	if e != nil {
		return nil, e
	}
	if afterMakeRaw != nil {
		afterMakeRaw(fd)
	}
	if on, err := termiosEcho(fd); err != nil || on {
		return nil, errors.New("terminal echo could not be disabled")
	}
	if e := writePasteDisable(f); e != nil {
		return nil, e
	}
	cp.mu.Lock()
	if cp.interrupted {
		cp.mu.Unlock()
		return nil, errInterrupted
	}
	cp.mu.Unlock()
	show()
	cp.mu.Lock()
	if cp.interrupted {
		cp.mu.Unlock()
		return nil, errInterrupted
	}
	cp.mu.Unlock()
	return readHiddenLine(f, fd)
}

var afterMakeRaw func(int)
var afterInterruptSnap func()

func endHiddenLine(fd int, over bool) error {
	flushLeaseInput(fd)
	quietDrainLeaseInput(fd)
	if over {
		return errors.New("join URL too long")
	}
	return nil
}
func refuseHiddenControl(f *os.File, fd int, b []byte) error {
	for {
		if n, e := f.Read(b); e != nil || n == 0 {
			break
		}
		switch b[0] {
		case 3:
			flushLeaseInput(fd)
			quietDrainLeaseInput(fd)
			return errInterrupted
		case 4:
			flushLeaseInput(fd)
			quietDrainLeaseInput(fd)
			return io.EOF
		case '\r', '\n':
			flushLeaseInput(fd)
			quietDrainLeaseInput(fd)
			return errors.New("join URL refused")
		}
	}
	flushLeaseInput(fd)
	quietDrainLeaseInput(fd)
	return errors.New("join URL refused")
}
func readHiddenLine(f *os.File, fd int) ([]byte, error) {
	var out []byte
	over := false
	b := make([]byte, 1)
	for {
		if n, e := f.Read(b); e != nil || n == 0 {
			flushLeaseInput(fd)
			quietDrainLeaseInput(fd)
			return nil, io.EOF
		}
		c := b[0]
		switch c {
		case '\r', '\n':
			if e := endHiddenLine(fd, over); e != nil {
				return nil, e
			}
			return out, nil
		case 3:
			flushLeaseInput(fd)
			quietDrainLeaseInput(fd)
			return nil, errInterrupted
		case 4:
			flushLeaseInput(fd)
			quietDrainLeaseInput(fd)
			return nil, io.EOF
		case 8, 127:
			if !over && len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			if c < 0x20 {
				return nil, refuseHiddenControl(f, fd, b)
			}
			if over || len(out) >= 4096 {
				over = true
				continue
			}
			out = append(out, c)
		}
	}
}
func fileFD(f *os.File) (int, error) {
	rc, e := f.SyscallConn()
	if e != nil {
		return 0, e
	}
	fd := -1
	if e = rc.Control(func(h uintptr) { fd = int(h) }); e != nil {
		return 0, e
	}
	return fd, nil
}
func (cp *coldPair) review(f ownerFlags, rules []lease.Rule) error {
	return cp.afterDecision(cp.pair(f, rules))
}
func (cp *coldPair) afterDecision(word string, e error) error {
	switch {
	case e == nil && word == "APPROVE":
		return nil
	case e == nil && word == "DENY":
		time.Sleep(leaseDecisionFlush)
		cp.abandon()
		fmt.Fprintln(cp.out, "Denied. Stopped the lease-only link this review started; nothing was issued.")
		return nil
	case e == nil:
		e = errNotApproved
	}
	if cp.abandon() {
		return fmt.Errorf("%w; stopped the lease-only link this review started; nothing was issued", e)
	}
	return fmt.Errorf("%w; nothing was issued", e)
}
func (cp *coldPair) pair(f ownerFlags, rules []lease.Rule) (string, error) {
	endpoint := line(cp.rd, cp.out, "No lease-only link. Enter the chosen browser's literal loopback HTTP origin: ")
	if !guarded.LocalEndpoint(endpoint) {
		return "", errors.New("no browser selected")
	}
	label := line(cp.rd, cp.out, "Local link label: ")
	if !leaseLabel.MatchString(label) {
		return "", errors.New("invalid local link label")
	}
	hub, e := reviewHubOrigin(line(cp.rd, cp.out, "Tray hub origin of the requester's Seven (the hub it joined, https://www.sliccy.ai in production; not the Seven page address): "))
	if e != nil {
		return "", e
	}
	pin, e := leaseDevice(hub)
	if e != nil {
		return "", e
	}
	if _, e = fmt.Fprintf(cp.out, "Broker SHA-256 fingerprint for %s: %s\n", hub, pin); e != nil {
		return "", errors.New("could not show the broker fingerprint; nothing was dialed")
	}
	if _, e = fmt.Fprintln(cp.out, "Nothing has been dialed yet. The requester's Seven pins this fingerprint; confirm it in your trusted task conversation."); e != nil {
		return "", errors.New("could not show the broker fingerprint; nothing was dialed")
	}
	sevenPrompt := "Expected Seven DTLS fingerprint from your trusted task conversation (not the hub): "
	if _, e = fmt.Fprint(cp.out, sevenPrompt); e != nil {
		return "", errors.New("could not show the Seven fingerprint prompt; nothing was dialed")
	}
	sevenPin, e := cp.rd.ReadSlice('\n')
	if e != nil {
		return "", errors.New("no Seven fingerprint")
	}
	if e := pairPin(label, strings.TrimSpace(string(sevenPin))); e != nil {
		return "", e
	}
	secret, e := cp.secret(func() {
		fmt.Fprint(cp.out, "In the requester's Seven, choose Network → Link a device. Paste its join URL here (hidden): ")
	})
	fmt.Fprintln(cp.out)
	if errors.Is(e, errInterrupted) {
		return "", e
	}
	if e != nil || !tray.IsJoinURL(string(secret)) {
		return "", errors.New("no valid join URL")
	}
	if !sameLeaseHub(string(secret), hub) {
		return "", fmt.Errorf("the join URL is not on %s; nothing was dialed", hub)
	}
	if tray.HubOrigin(string(secret)) != leaseCertKey(hub) {
		return "", errors.New("enter the hub origin as the join URL spells the host (lower case, no trailing dot, ASCII/punycode form); nothing was dialed")
	}
	c, e := cp.start(endpoint, label, pin, secret)
	if e != nil {
		return "", e
	}
	if line(cp.rd, cp.out, "After your job confirms pairing and submits its request, type REVIEW: ") != "REVIEW" {
		return "", errNotApproved
	}
	choice, e := c.discover(f, rules)
	if e != nil {
		return "", e
	}
	word, e := f.decide([]selection{choice}, cp.rd, cp.out)
	if e != nil {
		return word, e
	}
	switch word {
	case "APPROVE":
		return word, cp.approve(choice)
	case "DENY":
		return word, cp.childCall(choice, ctl.Request{Op: ctl.OpLeaseDeny, Request: choice.p.Request})
	default:
		return word, nil
	}
}
func (cp *coldPair) start(endpoint, label, pin string, secret []byte) (*leaseChild, error) {
	if e := leaseSlotFree(pin, 0); e != nil {
		return nil, e
	}
	exe, e := os.Executable()
	if e != nil {
		return nil, e
	}
	cmd := exec.Command(exe, "-", "follow", "--lease-browser", endpoint, "--name", label)
	cmd.Env = append(os.Environ(), "SLICC_LEASE_DEVICE="+pin)
	cmd.Stdin = bytes.NewReader(append(slices.Clone(secret), '\n'))
	detachLease(cmd)
	cp.mu.Lock()
	if cp.interrupted {
		cp.mu.Unlock()
		return nil, errInterrupted
	}
	if e := cmd.Start(); e != nil {
		cp.mu.Unlock()
		return nil, e
	}
	c := &leaseChild{proc: cmd.Process, exited: make(chan struct{}), pin: pin}
	cp.child = c
	race := cp.interrupted
	cp.mu.Unlock()
	go func() {
		err := cmd.Wait()
		c.mu.Lock()
		defer c.mu.Unlock()
		if err == nil {
			c.code = 0
		} else if ee, ok := err.(*exec.ExitError); ok {
			c.code = ee.ExitCode()
		} else {
			c.code = -1
		}
		close(c.exited)
	}()
	if race {
		c.stop()
		cp.mu.Lock()
		if cp.child == c {
			cp.child = nil
		}
		cp.mu.Unlock()
		return nil, errInterrupted
	}
	return c, c.await()
}
func (cp *coldPair) hold(restore func()) {
	cp.mu.Lock()
	cp.restore = restore
	if restore != nil {
		cp.restored = false
	}
	cp.mu.Unlock()
}
func (cp *coldPair) abandon() bool {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if cp.child == nil || cp.kept {
		return false
	}
	cp.child.stop()
	cp.child = nil
	return true
}
func (cp *coldPair) watchSignals() func() {
	ch := make(chan os.Signal, 1)
	done := make(chan struct{})
	ossignal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		select {
		case <-ch:
			fmt.Fprintf(cp.out, "\nslicc lease: %s\n", cp.onInterrupt())
			os.Exit(1)
		case <-done:
		}
	}()
	return func() { ossignal.Stop(ch); close(done) }
}
func (cp *coldPair) onInterrupt() string {
	_ = afterInterruptSnap
	cp.mu.Lock()
	cp.interrupted = true
	restore := cp.restore
	if cp.approving {
		cp.kept = true
		cp.approveUnknown = true
		cp.mu.Unlock()
		if restore != nil {
			restore()
		}
		return "interrupted during approval; the result is unknown; check slicc lease status and slicc lease revoke <lease-id>; the lease-only link was kept"
	}
	kept, unknown := cp.kept, cp.approveUnknown
	cp.mu.Unlock()
	if restore != nil {
		restore()
	}
	switch {
	case kept && unknown:
		return "interrupted during approval; the result is unknown; check slicc lease status and slicc lease revoke <lease-id>; the lease-only link was kept"
	case kept:
		return "interrupted; the lease was approved; the lease-only link this review started was kept"
	case cp.abandon():
		return "interrupted; stopped the lease-only link this review started; nothing was issued"
	}
	return "interrupted; nothing was issued"
}
func (c *leaseChild) socket() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.path
}
func (c *leaseChild) trust(path string) {
	c.mu.Lock()
	c.path = path
	c.mu.Unlock()
}
func (c *leaseChild) await() error {
	dir, e := ctl.Dir()
	if e != nil {
		return e
	}
	path := filepath.Join(dir, strconv.Itoa(c.proc.Pid)+".sock")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for {
		if !c.alive() {
			return c.exitError()
		}
		st, e := ctl.QueryPID(ctx, path, c.proc.Pid)
		if errors.Is(e, ctl.ErrPeer) {
			return errors.New("another process answers the started broker's control socket; it was not trusted")
		}
		if e == nil && slices.Equal(st.Offers, []string{lease.Cap}) && c.alive() {
			c.trust(path)
			if st.Device != c.pin {
				return errLeaseDeviceMismatch
			}
			return nil
		}
		select {
		case <-c.exited:
			return c.exitError()
		case <-ctx.Done():
			return errors.New("background lease broker did not start")
		case <-tick.C:
		}
	}
}
func (c *leaseChild) exitError() error {
	c.mu.Lock()
	code := c.code
	c.mu.Unlock()
	switch code {
	case leaseClaimBusyExit:
		return errLeaseClaimHeld
	case leaseDeviceMismatchExit:
		return errLeaseDeviceMismatch
	case leaseClaimErrorExit:
		return errLeaseClaimFailed
	}
	return errors.New("background lease broker exited")
}
func (c *leaseChild) discover(f ownerFlags, rules []lease.Rule) (selection, error) {
	ctx, cancel := context.WithTimeout(context.Background(), leaseDiscovery)
	defer cancel()
	miss := fmt.Errorf("no matching proven request from %s within %s; ask the job for a fresh request and review again", f.to, leaseDiscovery)
	for {
		found, e := c.matching(ctx, f, rules)
		switch {
		case e != nil:
			return selection{}, e
		case len(found) > 1:
			return selection{}, errors.New("more than one matching request; ambiguous")
		case len(found) == 1:
			if ctx.Err() != nil {
				return selection{}, miss
			}
			return found[0], nil
		case ctx.Err() != nil:
			return selection{}, miss
		}
		select {
		case <-c.exited:
			return selection{}, errors.New("the lease-only link stopped")
		case <-ctx.Done():
			return selection{}, miss
		case <-time.After(250 * time.Millisecond):
		}
	}
}
func (c *leaseChild) matching(parent context.Context, f ownerFlags, rules []lease.Rule) ([]selection, error) {
	if !c.alive() {
		return nil, errors.New("the lease-only link stopped")
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	path, pid := c.socket(), c.proc.Pid
	st, e := ctl.QueryPID(ctx, path, pid)
	if e == nil && st.Device != c.pin {
		return nil, errors.New("the started broker changed its fingerprint")
	}
	var r *ctl.Reply
	if e == nil && st.State == ctl.StateConnected {
		r, e = ctl.CallPID(ctx, path, pid, ctl.Request{Op: ctl.OpLeaseList})
	}
	if errors.Is(e, ctl.ErrPeer) {
		c.trust("")
		return nil, errors.New("another process answers the started broker's control socket; it was not trusted")
	}
	if parent.Err() != nil {
		return nil, nil
	}
	var pending []lease.Pending
	if e != nil || r == nil || !r.OK || json.Unmarshal(r.Pending, &pending) != nil {
		return nil, nil
	}
	var out []selection
	for _, p := range pending {
		if f.matches(p, rules) {
			out = append(out, selection{path: path, p: p, pid: pid})
		}
	}
	return out, nil
}
func (c *leaseChild) alive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.exited:
		return false
	default:
		return true
	}
}
func (c *leaseChild) stop() {
	if c.proc == nil {
		return
	}
	if c.alive() {
		_ = signalLeaseStop(c.proc)
		select {
		case <-c.exited:
			return
		case <-time.After(3 * time.Second):
		}
	}
	if c.alive() {
		_ = c.proc.Kill()
		select {
		case <-c.exited:
		case <-time.After(5 * time.Second):
		}
	}
}
