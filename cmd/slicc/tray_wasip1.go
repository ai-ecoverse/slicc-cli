package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
	"github.com/ai-ecoverse/slicc-cli/internal/facade"
	"github.com/ai-ecoverse/slicc-cli/internal/joinurl"
)

const (
	trayAnswerWait   = time.Minute
	trayApprovalWait = 5 * time.Minute
)

const trayUsage = `usage: slicc tray [--json]
       slicc tray url
       slicc tray rotate
       slicc tray unlink <device>
       slicc tray retry

This seven's tray: the machines and sevens linked to it.

  slicc tray          the tray's state and one line per device; --json prints
                      seven's answer as JSON
  slicc tray url      print the join URL, the tray's credential. Only this
                      command prints it: slicc tray url > /dev/clipboard
  slicc tray rotate   make a new join URL; the old one stops working
  slicc tray unlink <device>
                      drop one device: its fingerprint, a unique prefix of
                      it (colons and case ignored), or its short form from
                      slicc tray
  slicc tray retry    reconnect the tray's links

slicc asks this seven's link facade (SLICC_LINK_URL), which only root and
members of sudo may use. seven may ask you to approve rotate and unlink.

exit status: 0 on success, 1 on failure, 2 for bad usage, 3 when this seven
refuses.
`

type trayOpen struct {
	Kind string `json:"kind"`
	Op   string `json:"op"`
	ID   string `json:"id,omitempty"`
}

type trayDevice struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Kind   string   `json:"kind"`
	Host   string   `json:"host"`
	Mode   string   `json:"mode"`
	Offers []string `json:"offers"`
	Exit   bool     `json:"exit"`
	State  string   `json:"state"`
}

type trayExit struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Node string `json:"node"`
}

type trayStatus struct {
	Tray    string       `json:"tray"`
	Error   string       `json:"error"`
	Hub     string       `json:"hub"`
	Exit    *trayExit    `json:"exit"`
	Devices []trayDevice `json:"devices"`
}

func runTray(args []string, stdout, stderr io.Writer) int {
	op := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		op, args = args[0], args[1:]
	}
	asJSON := false
	var rest []string
	for _, a := range args {
		switch a {
		case "--help", "-h":
			fmt.Fprint(stdout, trayUsage)
			return 0
		case "--json":
			asJSON = true
		default:
			rest = append(rest, a)
		}
	}
	usage := func(msg string) int {
		fmt.Fprintf(stderr, "slicc tray: %s\n\n%s", msg, trayUsage)
		return 2
	}
	switch {
	case op != "status" && op != "url" && op != "rotate" && op != "unlink" && op != "retry":
		return usage(fmt.Sprintf("unknown command %q", op))
	case asJSON && op != "status":
		return usage("--json only goes with slicc tray")
	case op == "unlink" && len(rest) != 1:
		return usage("name one device")
	case op != "unlink" && len(rest) > 0:
		return usage(fmt.Sprintf("unexpected argument %q", rest[0]))
	}
	raw := os.Getenv(facade.EnvURL)
	if _, err := facade.ParseURL(raw); err != nil {
		return trayFail(stderr, err)
	}
	if err := facade.Ready(); err != nil {
		return trayFail(stderr, err)
	}
	t := trayClient{url: raw}
	switch op {
	case "url":
		return t.joinURL(stdout, stderr)
	case "rotate":
		if _, err := t.call("rotate", "", trayApprovalWait); err != nil {
			return trayFail(stderr, err)
		}
		fmt.Fprintln(stderr, "slicc: made a new join URL; the old one no longer works (slicc tray url prints it)")
		return 0
	case "unlink":
		return t.unlink(rest[0], stderr)
	case "retry":
		if _, err := t.call("retry", "", trayAnswerWait); err != nil {
			return trayFail(stderr, err)
		}
		fmt.Fprintln(stderr, "slicc: reconnecting the tray's links")
		return 0
	}
	return t.status(asJSON, stdout, stderr)
}

type trayClient struct{ url string }

func (t trayClient) call(op, id string, wait time.Duration) (json.RawMessage, error) {
	return facade.Call(context.Background(), t.url, trayOpen{Kind: "tray", Op: op, ID: id}, wait, facade.Dial)
}

func (t trayClient) fetch() (json.RawMessage, trayStatus, error) {
	var s trayStatus
	raw, err := t.call("status", "", trayAnswerWait)
	if err != nil {
		return nil, s, err
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, s, errors.New("seven's link facade sent a malformed tray status")
	}
	return raw, s, nil
}

func (t trayClient) status(asJSON bool, stdout, stderr io.Writer) int {
	raw, s, err := t.fetch()
	if err != nil {
		return trayFail(stderr, err)
	}
	if asJSON {
		var v any
		_ = json.Unmarshal(raw, &v)
		body, _ := json.Marshal(scrubJSON(v))
		fmt.Fprintf(stdout, "%s\n", body)
		return 0
	}
	printTray(stdout, s)
	return 0
}

func printTray(w io.Writer, s trayStatus) {
	switch {
	case s.Tray == "" || s.Tray == "idle":
		fmt.Fprintln(w, "tray off")
	case s.Hub != "":
		fmt.Fprintf(w, "tray %s, hub %s\n", scrub(s.Tray), scrub(s.Hub))
	default:
		fmt.Fprintf(w, "tray %s\n", scrub(s.Tray))
	}
	if s.Error != "" {
		fmt.Fprintf(w, "error: %s\n", scrub(s.Error))
	}
	if e := s.Exit; e != nil {
		switch e.Kind {
		case "link":
			fmt.Fprintf(w, "exit: %s\n", exitName(s.Devices, e.ID))
		case "tailnet":
			fmt.Fprintf(w, "exit: tailnet %s\n", scrub(e.Node))
		}
	}
	if len(s.Devices) == 0 {
		fmt.Fprintln(w, "no devices")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "DEVICE\tNAME\tHOST\tMODE\tSTATE\tOFFERS")
	for _, d := range s.Devices {
		offers := strings.Join(d.Offers, ",")
		if d.Exit {
			offers = strings.TrimPrefix(offers+",exit", ",")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", shortID(d.ID), dash(scrub(d.Name)), dash(scrub(d.Host)), dash(scrub(d.Mode)), dash(scrub(d.State)), dash(scrub(offers)))
	}
	_ = tw.Flush()
}

func exitName(devices []trayDevice, id string) string {
	for _, d := range devices {
		if d.ID == id {
			return scrub(d.Name) + " (" + shortID(id) + ")"
		}
	}
	return shortID(id)
}

func shortID(id string) string {
	id = scrub(id)
	if len(id) <= 12 {
		return dash(id)
	}
	return id[:4] + "…" + id[len(id)-4:]
}

var joinURLs = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]*/join/[A-Za-z0-9._~%+-]+`)

var secretKeys = map[string]bool{"url": true, "join": true, "joinUrl": true, "joinCommand": true, "followUrl": true, "webhookUrl": true}

func scrub(s string) string {
	s = strings.Join(strings.Fields(agentctl.Clean(s)), " ")
	return joinURLs.ReplaceAllString(s, "<join-url>")
}

func scrubJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if secretKeys[k] {
				delete(x, k)
				continue
			}
			x[k] = scrubJSON(e)
		}
	case []any:
		for i, e := range x {
			x[i] = scrubJSON(e)
		}
	case string:
		return joinURLs.ReplaceAllString(agentctl.Clean(x), "<join-url>")
	}
	return v
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (t trayClient) joinURL(stdout, stderr io.Writer) int {
	raw, err := t.call("url", "", trayAnswerWait)
	if err != nil {
		return trayFail(stderr, err)
	}
	var u struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(raw, &u) != nil || u.URL == "" {
		return trayFail(stderr, errors.New("seven's link facade sent no join URL"))
	}
	if _, err := joinurl.Parse(u.URL); err != nil || strings.ContainsFunc(u.URL, unicode.IsControl) {
		return trayFail(stderr, errors.New("seven's link facade sent something that isn't a join URL"))
	}
	fmt.Fprintln(stdout, strings.TrimSpace(u.URL))
	return 0
}

func (t trayClient) unlink(arg string, stderr io.Writer) int {
	_, s, err := t.fetch()
	if err != nil {
		return trayFail(stderr, err)
	}
	var found []trayDevice
	for _, d := range s.Devices {
		if matchDevice(d.ID, arg) {
			found = append(found, d)
		}
	}
	switch len(found) {
	case 0:
		fmt.Fprintf(stderr, "slicc tray: no device matches %s\n", scrub(arg))
		return 1
	case 1:
	default:
		fmt.Fprintf(stderr, "slicc tray: more than one device matches %s; give more of the fingerprint\n", scrub(arg))
		return 1
	}
	d := found[0]
	if _, err := t.call("unlink", d.ID, trayApprovalWait); err != nil {
		return trayFail(stderr, err)
	}
	fmt.Fprintf(stderr, "slicc: unlinked %s (%s)\n", shortID(d.ID), scrub(d.Name))
	return 0
}

func matchDevice(id, arg string) bool {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, ":", "")) }
	id, arg = norm(id), strings.ReplaceAll(norm(arg), "...", "…")
	if arg == "" {
		return false
	}
	if strings.Contains(arg, "…") {
		return len(id) > 12 && arg == shortID(id)
	}
	return strings.HasPrefix(id, arg)
}

var trayCodes = map[string]string{
	"EACCES":     "this seven refused",
	"ENOTCONN":   "this seven's tray isn't connected",
	"ENOENT":     "this seven has no such device",
	"EOPNOTSUPP": "this seven's link facade has no tray",
}

func trayFail(stderr io.Writer, err error) int {
	var re *agentctl.ResetError
	if errors.As(err, &re) && re.Remote {
		msg := scrub(re.Message)
		if msg == "" {
			msg = trayCodes[re.Code]
		}
		if msg == "" {
			msg = scrub(re.Code)
		}
		fmt.Fprintf(stderr, "slicc tray: %s\n", msg)
		if re.Code == "EACCES" {
			return 3
		}
		return 1
	}
	fmt.Fprintf(stderr, "slicc tray: %v\n", err)
	return 1
}
