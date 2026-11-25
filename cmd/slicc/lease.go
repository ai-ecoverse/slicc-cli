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
	"slices"
	"strconv"
	"strings"
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
}
type ownerFlags struct {
	op, target, label, endpoint, to, peer string
	ttl                                   time.Duration
	keep, noInput                         bool
	scope, delta                          stringList
}

var errLeaseUsage = errors.New(strings.TrimSpace(leaseUsage))

func parseOwner(args []string) (ownerFlags, error) {
	f := ownerFlags{op: args[0]}
	args = args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
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
		if e := bootstrapLease(rd, errOut); e != nil {
			return e
		}
		routes, e = leaseRoutes()
		if e != nil {
			return e
		}
	}
	var choices []selection
	for _, r := range routes {
		if r.status.State != ctl.StateConnected {
			continue
		}
		for _, p := range r.Pending {
			if f.matches(p, rules) {
				choices = append(choices, selection{r.path, p})
			}
		}
	}
	return f.decide(choices, rules, rd, errOut)
}
func (f ownerFlags) decide(choices []selection, rules []lease.Rule, rd *bufio.Reader, errOut io.Writer) error {
	if len(choices) == 0 {
		return errors.New("no matching proven request on a connected lease-only link; nothing issued")
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
				return errors.New("no selection")
			}
			chosen = v - 1
		}
		c := choices[chosen]
		for _, r := range rules {
			fmt.Fprintf(errOut, "%s: %s\n", r.Kind, r.URL)
		}
		fmt.Fprintf(errOut, "Until %s. Operates as you in the selected profile. This broker can navigate and read the top document; other tabs stay private.\n", time.Now().Add(f.ttl).Format(time.RFC3339))
		switch line(rd, errOut, "Type APPROVE to issue, DENY to reject, or Enter to cancel: ") {
		case "APPROVE":
		case "DENY":
			return ownerCall(c.path, ctl.Request{Op: ctl.OpLeaseDeny, Request: c.p.Request})
		default:
			return errors.New("not approved")
		}
	} else if len(choices) != 1 {
		return errors.New("ambiguous request; select a single paired link and browser")
	}
	c := choices[chosen]
	return ownerCall(c.path, ctl.Request{Op: ctl.OpLeaseApprove, Request: c.p.Request})
}
func ownerCall(path string, q ctl.Request) error {
	r, e := ctl.Call(context.Background(), path, q)
	if e != nil {
		return e
	}
	if !r.OK {
		return fmt.Errorf("decision refused: %s", r.Error)
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
func bootstrapLease(rd *bufio.Reader, out io.Writer) error {
	endpoint := line(rd, out, "No lease-only link. Enter the chosen browser's literal loopback HTTP origin: ")
	if !guarded.LocalEndpoint(endpoint) {
		return errors.New("no browser selected")
	}
	label := line(rd, out, "Local link label: ")
	pin := line(rd, out, "Expected Seven DTLS fingerprint from your trusted task conversation (not the hub): ")
	if e := pairPin(label, pin); e != nil {
		return e
	}
	fmt.Fprint(out, "In the requester's Seven, choose Network → Link a device. Paste its join URL here (hidden): ")
	secret, e := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(out)
	if e != nil || !tray.IsJoinURL(string(secret)) {
		return errors.New("no valid join URL")
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	cmd := exec.Command(exe, "-", "follow", "--lease-browser", endpoint, "--name", label)
	cmd.Stdin = strings.NewReader(string(secret) + "\n")
	detachLease(cmd)
	if e := cmd.Start(); e != nil {
		return e
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	status, err := waitLeaseSocket(pid)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Compare broker SHA-256 fingerprint %s in the task conversation. The other side must pin it.\n", status.Device)
	if line(rd, out, "After your job confirms pairing and submits its request, type REVIEW: ") != "REVIEW" {
		return errors.New("not approved")
	}
	return nil
}
func waitLeaseSocket(pid int) (*ctl.Status, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for {
		entries, _ := ctl.ListLocal(ctx)
		for _, e := range entries {
			if e.PID == pid && e.Status != nil && slices.Equal(e.Status.Offers, []string{lease.Cap}) {
				return e.Status, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, errors.New("background lease broker did not start")
		case <-tick.C:
		}
	}
}
