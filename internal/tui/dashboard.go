package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/flow"
)

const recentLines = 6

type dashBrowser struct {
	id       string
	name     string
	version  string
	role     string
	cdp      string
	launched bool
}

type streamCount struct {
	open  int
	total int
}

type dashboard struct {
	browsers []dashBrowser
	tab      string
	kernel   string
	state    string
	since    time.Time
	peer     string
	everUp   bool
	caps     []string
	policy   string
	streams  map[string]*streamCount
	order    []string
	recent   []string
	notice   string
	closed   []string
	failed   string
}

const (
	StateStarting     = "starting"
	StateWaiting      = "waiting for seven"
	StateConnecting   = "connecting"
	StateLinked       = "linked"
	StateReconnecting = "reconnecting"
	StateClosed       = "closed"
)

func newDashboard(now time.Time) dashboard {
	return dashboard{state: StateStarting, since: now, streams: map[string]*streamCount{}}
}

func (d *dashboard) browser(id string) *dashBrowser {
	for i := range d.browsers {
		if d.browsers[i].id == id {
			return &d.browsers[i]
		}
	}
	d.browsers = append(d.browsers, dashBrowser{id: id})
	return &d.browsers[len(d.browsers)-1]
}

func (d *dashboard) apply(e flow.Event, now time.Time) {
	switch e.Event {
	case flow.Launched, flow.Attached:
		b := d.browser(e.Browser)
		b.name, b.version, b.role, b.cdp = e.Name, e.Version, e.Role, e.CDP
		b.launched = b.launched || e.Launched || e.Event == flow.Launched
		d.log(now, fmt.Sprintf("%s %s", e.Event, e.Name))
	case flow.Serving:
		d.caps, d.policy = e.Caps, e.Policy
		d.setState(StateWaiting, now)
	case flow.Connecting:
		d.setState(StateConnecting, now)
	case flow.Linked:
		d.peer, d.tab, d.everUp = e.Peer, e.Tab, true
		d.caps, d.policy = e.Caps, e.Policy
		d.setState(StateLinked, now)
		d.log(now, fmt.Sprintf("linked with %q", e.Peer))
	case flow.LinkLost:
		if d.everUp {
			d.setState(StateReconnecting, now)
		} else {
			d.setState(StateWaiting, now)
		}
		d.log(now, "link lost: "+firstOf(e.Message, e.Reason, "closed"))
	case flow.StreamOpen:
		c := d.count(e.Stream)
		c.open++
		c.total++
	case flow.StreamDone:
		if c := d.count(e.Stream); c.open > 0 {
			c.open--
		}
	case flow.Kernel:
		d.kernel = e.Kernel
	case flow.Status:
		d.log(now, e.Message)
	case flow.Error:
		d.failed = e.Message
		d.log(now, "error: "+e.Message)
	case flow.Closed:
		d.closed = e.Browsers
		d.setState(StateClosed, now)
	}
}

func firstOf(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func (d *dashboard) setState(s string, now time.Time) {
	if d.state != s {
		d.state, d.since = s, now
	}
}

func (d *dashboard) count(kind string) *streamCount {
	c, ok := d.streams[kind]
	if !ok {
		c = &streamCount{}
		d.streams[kind] = c
		d.order = append(d.order, kind)
	}
	return c
}

func (d *dashboard) log(now time.Time, line string) {
	d.recent = append(d.recent, now.Format("15:04:05")+"  "+line)
	if len(d.recent) > recentLines {
		d.recent = slices.Clone(d.recent[len(d.recent)-recentLines:])
	}
}

func uptime(d time.Duration) string {
	d = d.Truncate(time.Second)
	h := int(d.Hours())
	mnt := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm%02ds", h, mnt, s)
	case mnt > 0:
		return fmt.Sprintf("%dm%02ds", mnt, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

func (d dashboard) stateLine(st styles, now time.Time) string {
	since := uptime(now.Sub(d.since))
	switch d.state {
	case StateLinked:
		peer := d.peer
		if peer == "" {
			peer = "seven"
		}
		return st.good.Render("linked") + fmt.Sprintf(" with %q · up %s", peer, since)
	case StateReconnecting:
		return st.warn.Render("reconnecting") + " · waiting for seven to offer again · " + since
	case StateConnecting:
		return st.warn.Render("connecting") + " · " + since
	case StateClosed:
		return "closed"
	default:
		return d.state + " · " + since
	}
}

func (d dashboard) view(st styles, mode Mode, now time.Time) string {
	var b strings.Builder
	head := "q quit"
	if mode == ModeClosing {
		head = "closing…"
	}
	fmt.Fprintf(&b, "%s · %s    %s\n\n", st.title.Render("slicc"), d.state, st.faint.Render(head))
	b.WriteString(st.title.Render("browsers") + "\n")
	if len(d.browsers) == 0 {
		b.WriteString("  starting…\n")
	}
	for _, br := range d.browsers {
		how := "attached"
		if br.launched {
			how = "launched"
		}
		role := "hosts seven"
		if br.role == flow.RoleExtra {
			role = "extra cdp"
		}
		name := br.name
		if br.version != "" {
			name += " " + shortVersion(br.version)
		}
		fmt.Fprintf(&b, "  %-26s %s · CDP %s · %s\n", name, how, hostPort(br.cdp), role)
	}
	fmt.Fprintf(&b, "\n%-8s %s\n", "seven", firstOf(d.tab, st.faint.Render("no tab yet")))
	fmt.Fprintf(&b, "%-8s %s\n", "link", d.stateLine(st, now))
	if len(d.caps) > 0 {
		fmt.Fprintf(&b, "%-8s %s\n", "caps", strings.Join(d.caps, " · "))
	}
	if d.policy != "" {
		fmt.Fprintf(&b, "%-8s %s\n", "policy", d.policy)
	}
	if d.kernel != "" {
		fmt.Fprintf(&b, "%-8s %s\n", "kernel", d.kernel)
	}
	fmt.Fprintf(&b, "%-8s %s\n", "streams", d.streamLine())
	if len(d.recent) > 0 {
		b.WriteString("\n" + st.title.Render("recent") + "\n")
		for _, l := range d.recent {
			b.WriteString("  " + st.faint.Render(l) + "\n")
		}
	}
	if d.notice != "" {
		b.WriteString("\n" + st.warn.Render(d.notice) + "\n")
	}
	return b.String()
}

func (d dashboard) streamLine() string {
	if len(d.order) == 0 {
		return "none yet"
	}
	parts := make([]string, 0, len(d.order))
	for _, k := range d.order {
		c := d.streams[k]
		parts = append(parts, fmt.Sprintf("%s %d open, %d total", k, c.open, c.total))
	}
	return strings.Join(parts, " · ")
}
