package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/ai-ecoverse/slicc-cli/internal/flow"
)

const recentLines = 6

const kernelOff = "kernel services are off: "

type dashBrowser struct {
	id       string
	name     string
	version  string
	role     string
	cdp      string
	launched bool
	gone     bool
}

type streamCount struct {
	open    int
	total   int
	dropped int
}

type dashboard struct {
	browsers []dashBrowser
	tab      string
	kernel   string
	compute  string
	state    string
	since    time.Time
	peer     string
	everUp   bool
	caps     []string
	policy   string
	streams  map[string]*streamCount
	order    []string
	recent   []logLine
	kernOff  string
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
	StateRelinking    = "relinking"
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
		d.caps, d.policy, d.compute = e.Caps, e.Policy, e.Compute
		d.kernel, d.kernOff = "", ""
		d.setState(StateWaiting, now)
	case flow.Connecting:
		d.setState(StateConnecting, now)
	case flow.Linked:
		d.peer, d.tab, d.everUp = e.Peer, e.Tab, true
		d.caps, d.policy = e.Caps, e.Policy
		d.setState(StateLinked, now)
		d.note(now, levelGood, fmt.Sprintf("linked with %q", e.Peer))
	case flow.LinkLost:
		if e.Reason == flow.RelinkReason {
			d.setState(StateRelinking, now)
			d.log(now, "said bye to seven to relink")
			return
		}
		if d.everUp {
			d.setState(StateReconnecting, now)
		} else {
			d.setState(StateWaiting, now)
		}
		d.note(now, levelWarn, "link lost: "+firstOf(e.Message, e.Reason, "closed"))
	case flow.StreamOpen:
		c := d.count(e.Stream)
		c.open++
		c.total++
	case flow.StreamDone:
		if c := d.count(e.Stream); c.open > 0 {
			c.open--
		}
	case flow.Dropped:
		d.count(e.Stream).dropped += e.Count
	case flow.BrowserGone:
		d.browser(e.Browser).gone = true
		d.note(now, levelWarn, e.Name+" quit")
	case flow.BrowserBack:
		d.browser(e.Browser).gone = false
		d.log(now, e.Name+" is back")
	case flow.BrowserSlow:
		d.log(now, flow.SlowLine(e.Name, e.Elapsed))
	case flow.Kernel:
		d.kernel, d.kernOff = e.Kernel, ""
	case flow.Status:
		if rest, ok := strings.CutPrefix(e.Message, kernelOff); ok {
			d.kernel, d.kernOff = "", strings.TrimSuffix(rest, "; try --kernel-port")
			d.note(now, levelWarn, e.Message)
			return
		}
		d.log(now, e.Message)
	case flow.Error:
		d.failed = e.Message
		d.note(now, levelBad, "error: "+e.Message)
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

type level int

const (
	levelInfo level = iota
	levelGood
	levelWarn
	levelBad
)

type logLine struct {
	at    string
	text  string
	level level
}

func (d *dashboard) log(now time.Time, line string) { d.note(now, levelInfo, line) }

func (d *dashboard) note(now time.Time, lv level, line string) {
	d.recent = append(d.recent, logLine{at: now.Format("15:04:05"), text: line, level: lv})
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

func (m Model) stateChip() string {
	d, st := m.dash, m.st
	switch d.state {
	case StateLinked:
		return st.good.Render("● linked")
	case StateReconnecting, StateRelinking, StateConnecting:
		return st.warn.Render("◌ " + d.state)
	case StateClosed:
		return st.faint.Render("closed")
	}
	return st.faint.Render("◌ " + d.state)
}

func (m Model) stateLine() string {
	d, st := m.dash, m.st
	since := uptime(m.cfg.Now().Sub(d.since))
	switch d.state {
	case StateLinked:
		peer := d.peer
		if peer == "" {
			peer = "seven"
		}
		return st.good.Render("linked") + fmt.Sprintf(" with %q · up %s", peer, since)
	case StateReconnecting:
		return st.warn.Render("reconnecting") + st.faint.Render(" · waiting for seven to offer again · "+since)
	case StateRelinking:
		return st.warn.Render("relinking") + st.faint.Render(" · seven gets the new capabilities · "+since)
	case StateConnecting:
		return st.warn.Render("connecting") + st.faint.Render(" · "+since)
	case StateClosed:
		return "closed"
	}
	return d.state + st.faint.Render(" · "+since)
}

func (m Model) spread(left, right string) string {
	gap := 4
	if m.width > 0 {
		gap = max(m.width-lipgloss.Width(left)-lipgloss.Width(right)-1, 4)
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) sectionLine(label, desc string) string {
	s := "  " + m.st.section.Render(label)
	if desc != "" {
		s += "  " + m.st.faint.Render(desc)
	}
	return s
}

func (m Model) field(label, value string) string {
	return fmt.Sprintf("  %s %s", m.st.faint.Render(fmt.Sprintf("%-9s", label)), value)
}

func (m Model) keyBar(pairs ...[2]string) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, m.st.key.Render(p[0])+" "+m.st.faint.Render(p[1]))
	}
	return "  " + strings.Join(parts, "   ")
}

func (m Model) logText(l logLine) string {
	switch l.level {
	case levelGood:
		return m.st.good.Render(l.text)
	case levelWarn:
		return m.st.warn.Render(l.text)
	case levelBad:
		return m.st.bad.Render(l.text)
	}
	return l.text
}

func (m Model) dashView() string {
	d, st := m.dash, m.st
	var b []string
	right := ""
	if d.state == StateLinked {
		right = st.faint.Render("up " + uptime(m.cfg.Now().Sub(d.since)))
	}
	b = append(b, m.spread(st.title.Render("slicc")+st.faint.Render(" · ")+m.stateChip(), right), "")
	b = append(b, m.sectionLine("BROWSERS", ""))
	if len(d.browsers) == 0 {
		b = append(b, "  "+st.faint.Render("starting…"))
	}
	w := 14
	for _, br := range d.browsers {
		w = max(w, min(lipgloss.Width(br.name), 36))
	}
	for _, br := range d.browsers {
		how := "attached"
		if br.launched {
			how = "launched"
		}
		role := st.accent.Render("hosts seven")
		if br.role == flow.RoleExtra {
			role = st.faint.Render("extra cdp")
		}
		mark := st.good.Render("●")
		if br.gone {
			mark = st.bad.Render("○")
		}
		line := fmt.Sprintf("  %s %s%s  %s  %s  %s", mark, st.title.Render(br.name), strings.Repeat(" ", max(w-lipgloss.Width(br.name), 0)),
			st.faint.Render(fmt.Sprintf("%-6s", shortVersion(br.version))), st.faint.Render(how+" · CDP "+hostPort(br.cdp)), role)
		if br.gone {
			line += "  " + st.bad.Render("gone")
		}
		b = append(b, line)
	}
	b = append(b, "", m.sectionLine("SEVEN", ""))
	b = append(b, m.field("tab", firstOf(d.tab, st.faint.Render("no tab yet"))))
	b = append(b, m.field("link", m.stateLine()))
	if d.policy != "" {
		b = append(b, m.field("policy", d.policy))
	}
	b = append(b, m.field("streams", m.streamText()))
	b = append(b, "", m.sectionLine("CAPABILITIES", st.key.Render("c")+st.faint.Render(" change")))
	b = append(b, m.dashCaps()...)
	if len(d.recent) > 0 {
		b = append(b, "", m.sectionLine("RECENT", ""))
		for _, l := range d.recent {
			b = append(b, "  "+st.faint.Render(l.at)+"  "+m.logText(l))
		}
	}
	if d.notice != "" {
		b = append(b, "", "  "+st.warn.Render(d.notice))
	}
	b = append(b, "")
	if m.mode == ModeClosing {
		b = append(b, "  "+st.warn.Render("closing…"))
	} else {
		b = append(b, m.keyBar([2]string{"c", "capabilities"}, [2]string{"m", "share a folder"}, [2]string{"q", "quit"}))
	}
	return strings.Join(b, "\n") + "\n"
}

func (m Model) dashCaps() []string {
	d, st := m.dash, m.st
	has := func(c string) bool { return slices.Contains(d.caps, c) }
	var always []string
	for _, c := range d.caps {
		if !mutableCaps[c] {
			always = append(always, c)
		}
	}
	var out []string
	if len(always) > 0 {
		out = append(out, m.field("network", strings.Join(always, " · ")))
	}
	row := func(on bool, name, value string) string {
		mark := st.faint.Render("○")
		if on {
			mark = st.good.Render("●")
		}
		return fmt.Sprintf("  %s %s %s", mark, fmt.Sprintf("%-7s", name), value)
	}
	off := st.faint.Render("off")
	jerk := off
	if has("jerk") {
		jerk = "slicc-jerk " + firstOf(m.cfg.JerkVersion, "on the link")
	}
	out = append(out, row(has("jerk"), "jerk", jerk))
	shell := off
	switch {
	case d.compute != "":
		shell = d.compute
	case has("ssh") && len(m.settings.Shell) > 0:
		shell = "ssh via " + quoteArgv(m.settings.Shell)
	}
	out = append(out, row(shell != off, "shell", shell))
	kernel, on := off, false
	switch {
	case d.kernel != "":
		kernel, on = d.kernel, true
	case d.kernOff != "":
		kernel = st.bad.Render("off: "+d.kernOff) + "  " + st.key.Render("c") + st.faint.Render(" pick a port")
	case m.settings.Kernel > 0:
		kernel = st.faint.Render(fmt.Sprintf(":%d · waiting for seven", m.settings.Kernel))
	}
	out = append(out, row(on, "kernel", kernel))
	folders := m.settings.shared()
	if len(folders) == 0 {
		out = append(out, row(false, "folders", off+"  "+st.key.Render("m")+st.faint.Render(" share one")))
		return out
	}
	parts := make([]string, len(folders))
	for i, f := range folders {
		parts[i] = st.title.Render(f.Name) + " " + st.faint.Render(tilde(f.Path))
		if f.ReadOnly {
			parts[i] += " " + st.warn.Render("read-only")
		}
	}
	return append(out, row(true, "folders", strings.Join(parts, st.faint.Render(" · "))))
}

func (m Model) streamText() string {
	d, st := m.dash, m.st
	if len(d.order) == 0 {
		return st.faint.Render("none yet")
	}
	parts := make([]string, 0, len(d.order))
	for _, k := range d.order {
		c := d.streams[k]
		open := fmt.Sprintf("%d open", c.open)
		if c.open > 0 {
			open = st.accent.Render(open)
		}
		part := st.title.Render(k) + " " + open + st.faint.Render(fmt.Sprintf(", %d total", c.total))
		if c.dropped > 0 {
			part += st.bad.Render(fmt.Sprintf(", %d dropped", c.dropped))
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "   ")
}
