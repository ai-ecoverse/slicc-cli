package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
)

type group int

const (
	groupNone group = iota - 1
	groupRunning
	groupLaunch
	groupSix
	groupHeadless
	groupElectron
)

func groupOf(e browser.Entry) group {
	switch {
	case flow.IsSix(e):
		return groupSix
	case flow.IsHeadless(e) && e.Endpoint.Source != browser.SourceFlag:
		return groupHeadless
	case e.Kind == browser.KindEndpoint:
		return groupRunning
	case e.Installed != nil && e.Installed.Engine == browser.EngineElectron:
		return groupElectron
	default:
		return groupLaunch
	}
}

func (g group) folds() bool { return g == groupHeadless || g == groupElectron }

type item struct {
	entry int
	group group
}

type line struct {
	text string
	at   int
}

func (m *Model) setEntries(entries []browser.Entry) {
	entries = flow.Demoted(entries)
	m.last = m.lastEntry(entries)
	if i := slices.IndexFunc(entries, func(e browser.Entry) bool { return e.ID == m.last }); i > 0 {
		entries = append([]browser.Entry{entries[i]}, slices.Delete(slices.Clone(entries), i, i+1)...)
	}
	m.entries = entries
	m.order = nil
	var folded []group
	seen := map[group]bool{}
	for _, e := range entries {
		g := groupOf(e)
		if seen[g] {
			continue
		}
		seen[g] = true
		if g.folds() {
			folded = append(folded, g)
		} else {
			m.order = append(m.order, g)
		}
	}
	slices.Sort(folded)
	if len(m.order) == 0 {
		for _, g := range folded {
			m.open[g] = true
		}
	}
	m.order = append(m.order, folded...)
	m.cursor = m.firstEntry()
	m.used = map[string]time.Time{}
	for _, e := range entries {
		if e.Six != nil {
			if t, ok := lastUsed(e.Six.Dir); ok {
				m.used[e.ID] = t
			}
		}
	}
	m.loadProfiles()
	m.applyLast()
}

func (m Model) lastEntry(entries []browser.Entry) string {
	l := m.cfg.Last
	i := slices.IndexFunc(entries, func(e browser.Entry) bool { return l.ID != "" && e.ID == l.ID })
	if i < 0 {
		return ""
	}
	e := entries[i]
	if e.Kind != browser.KindInstalled || m.cfg.Dir == nil {
		return e.ID
	}
	dir := m.cfg.Dir(e, l.Profile)
	for _, ep := range entries {
		if dir != "" && ep.Kind == browser.KindEndpoint && ep.Endpoint.UserDataDir != "" && browser.SameDir(ep.Endpoint.UserDataDir, dir) {
			return ep.ID
		}
	}
	return e.ID
}

func lastUsed(dir string) (time.Time, bool) {
	for _, p := range []string{filepath.Join(dir, "Local State"), dir} {
		if fi, err := os.Stat(p); err == nil {
			return fi.ModTime(), true
		}
	}
	return time.Time{}, false
}

func usedAgo(now, t time.Time) string {
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.In(now.Location()).Date()
	days := int(time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC).Sub(time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC)).Hours() / 24)
	switch {
	case days <= 0:
		return "used today"
	case days == 1:
		return "used yesterday"
	case days < 14:
		return fmt.Sprintf("used %d days ago", days)
	case days < 60:
		return fmt.Sprintf("used %d weeks ago", days/7)
	default:
		return fmt.Sprintf("used %d months ago", days/30)
	}
}

func (m Model) items() []item {
	var out []item
	for _, g := range m.order {
		if g.folds() {
			out = append(out, item{entry: -1, group: g})
			if !m.open[g] {
				continue
			}
		}
		for i, e := range m.entries {
			if groupOf(e) == g {
				out = append(out, item{entry: i, group: g})
			}
		}
	}
	return out
}

func (m Model) firstEntry() int {
	for i, it := range m.items() {
		if it.entry >= 0 {
			return i
		}
	}
	return 0
}

func (m Model) at() (item, bool) {
	items := m.items()
	if m.cursor < 0 || m.cursor >= len(items) {
		return item{entry: -1, group: groupNone}, false
	}
	return items[m.cursor], true
}

func (m Model) current() (browser.Entry, bool) {
	it, ok := m.at()
	if !ok || it.entry < 0 {
		return browser.Entry{}, false
	}
	return m.entries[it.entry], true
}

func (m Model) onFold() (group, bool) {
	it, ok := m.at()
	return it.group, ok && it.entry < 0
}

func (m *Model) fold(g group) {
	if !slices.Contains(m.order, g) {
		return
	}
	cur, _ := m.at()
	if m.open[g] && cur.group == g {
		cur = item{entry: -1, group: g}
	}
	m.open[g] = !m.open[g]
	m.cursor = max(slices.Index(m.items(), cur), 0)
}

func (m Model) pickKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.message, m.note = "", ""
	switch k.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.moved = true
		}
	case "down", "j":
		if m.cursor < len(m.items())-1 {
			m.cursor++
			m.moved = true
		}
	case "space":
		if g, ok := m.onFold(); ok {
			m.fold(g)
		} else {
			m.toggle()
		}
	case "e":
		m.fold(groupElectron)
	case "h":
		m.fold(groupHeadless)
	case "p":
		m.openProfiles()
	case "enter":
		if g, ok := m.onFold(); ok && len(m.selected) == 0 {
			m.fold(g)
			return m, nil
		}
		return m.goChoice()
	case "q", "esc":
		return m.quit()
	}
	return m, nil
}

func (m *Model) toggle() {
	e, ok := m.current()
	if !ok {
		return
	}
	if i := slices.Index(m.selected, e.ID); i >= 0 {
		m.selected = slices.Delete(m.selected, i, i+1)
		return
	}
	if err := flow.CanExtra(e); err != nil {
		m.note = err.Error() + ", so it can't be picked"
		return
	}
	m.selected = append(m.selected, e.ID)
}

func (m Model) choice() []browser.Entry {
	if len(m.selected) == 0 {
		if e, ok := m.current(); ok {
			return []browser.Entry{e}
		}
		return nil
	}
	var out []browser.Entry
	for _, id := range m.selected {
		for _, e := range m.entries {
			if e.ID == id {
				out = append(out, e)
			}
		}
	}
	return out
}

func (m Model) goChoice() (tea.Model, tea.Cmd) {
	sel := m.choice()
	if len(m.selected) == 0 && !m.moved && len(sel) == 1 && flow.IsSix(sel[0]) {
		m.message = sixUnpicked
		return m, nil
	}
	if err := flow.Check(sel); err != nil {
		m.message = err.Error()
		return m, nil
	}
	profiles := map[string]string{}
	for _, e := range sel {
		if p := m.profiles[e.ID]; p != "" {
			profiles[e.ID] = p
		}
	}
	notice := m.dash.notice
	m.went = true
	m.mode = ModeDashboard
	m.dash = newDashboard(m.cfg.Now())
	m.dash.notice = notice
	if m.cfg.Start != nil {
		m.cfg.Start(Choice{Entries: sel, Profiles: profiles})
	}
	return m, tick()
}

func (m Model) pickView() string {
	top := []string{m.header(), ""}
	var body []line
	if len(m.entries) == 0 {
		body = append(body, line{text: "  no browsers found; install Chrome, or start one with --remote-debugging-port", at: -1})
	}
	items := m.items()
	w := m.nameWidth()
	cw := map[group]int{}
	for _, g := range m.order {
		cw[g] = m.chipWidth(items, g)
	}
	prev := groupNone
	for i, it := range items {
		if it.entry < 0 {
			if len(body) > 0 && (!prev.folds() || m.open[prev]) {
				body = append(body, line{at: -1})
			}
			body = append(body, line{text: m.foldRow(i, it.group), at: i})
			prev = it.group
			continue
		}
		if it.group != prev && !it.group.folds() {
			if len(body) > 0 {
				body = append(body, line{at: -1})
			}
			body = append(body, line{text: m.sectionHeader(it.group), at: -1})
		}
		prev = it.group
		body = append(body, line{text: m.entryRow(i, it.entry, w, cw[it.group]), at: i})
	}
	bottom := []string{""}
	if len(m.entries) > 0 {
		l1, l2 := m.detail()
		bottom = append(bottom, l1)
		if l2 != "" {
			bottom = append(bottom, "  "+m.st.faint.Render(l2))
		}
	}
	if m.message != "" {
		bottom = append(bottom, "", m.st.bad.Render(m.message))
	}
	if m.note != "" {
		bottom = append(bottom, "", "  "+m.st.faint.Render(m.note))
	}
	bottom = append(bottom, "", m.keys())
	var b strings.Builder
	for _, l := range top {
		b.WriteString(l + "\n")
	}
	for _, l := range m.window(body, m.height-len(top)-len(bottom)) {
		b.WriteString(l + "\n")
	}
	for _, l := range bottom {
		b.WriteString(l + "\n")
	}
	return b.String()
}

func (m Model) window(body []line, avail int) []string {
	out := make([]string, 0, len(body))
	if m.height <= 0 || len(body) <= avail {
		for _, l := range body {
			out = append(out, l.text)
		}
		return out
	}
	visible := max(avail-2, 3)
	ci := max(slices.IndexFunc(body, func(l line) bool { return l.at == m.cursor }), 0)
	start := min(max(ci-visible/2, 0), max(len(body)-visible, 0))
	end := min(start+visible, len(body))
	if start > 0 {
		out = append(out, m.st.faint.Render(fmt.Sprintf("  ↑ %d more", start)))
	}
	for _, l := range body[start:end] {
		out = append(out, l.text)
	}
	if end < len(body) {
		out = append(out, m.st.faint.Render(fmt.Sprintf("  ↓ %d more", len(body)-end)))
	}
	return out
}

func (m Model) header() string {
	left := m.st.title.Render("slicc") + m.st.faint.Render(" · choose where seven runs")
	var counts []string
	add := func(n int, one, many string) {
		switch {
		case n == 1:
			counts = append(counts, "1 "+one)
		case n > 1:
			counts = append(counts, strconv.Itoa(n)+" "+many)
		}
	}
	n := map[group]int{}
	for _, e := range m.entries {
		n[groupOf(e)]++
	}
	add(n[groupRunning], "running", "running")
	add(n[groupLaunch], "to launch", "to launch")
	add(n[groupSix], "six profile", "six profiles")
	right := m.st.faint.Render(strings.Join(counts, " · "))
	gap := 4
	if m.width > 0 {
		gap = max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 4)
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) sectionHeader(g group) string {
	label, desc := "", ""
	switch g {
	case groupRunning:
		label, desc = "Running", "attach over CDP"
	case groupLaunch:
		label, desc = "Launch", "in a SLICC profile of its own, for testing SLICC seven"
	case groupSix:
		return m.sectionLine("SLICC six profiles", "") + "  " + m.risk().Render("re-use existing SLICC profile (might lose data)")
	}
	return m.sectionLine(label, desc)
}

func (m Model) groupCount(g group) int {
	n := 0
	for _, e := range m.entries {
		if groupOf(e) == g {
			n++
		}
	}
	return n
}

func (m Model) foldTitle(g group) (string, string, string) {
	n := m.groupCount(g)
	switch g {
	case groupElectron:
		hint := "extras only"
		blocked := 0
		for _, e := range m.entries {
			if groupOf(e) == g && e.Installed.DebugSupport == browser.DebugDisabled {
				blocked++
			}
		}
		if blocked > 0 {
			hint += fmt.Sprintf(" · %d %s debugging", blocked, pluralize(blocked, "blocks", "block"))
		}
		return fmt.Sprintf("%d Electron %s", n, pluralize(n, "app", "apps")), hint, "e"
	default:
		return fmt.Sprintf("%d headless %s", n, pluralize(n, "browser", "browsers")), "no window, usually left by tests", "h"
	}
}

func (m Model) foldRow(i int, g group) string {
	title, hint, key := m.foldTitle(g)
	arrow, verb := "▸", "show"
	if m.open[g] {
		arrow, verb = "▾", "hide"
	}
	sel := 0
	for _, e := range m.entries {
		if groupOf(e) == g && slices.Contains(m.selected, e.ID) {
			sel++
		}
	}
	if sel > 0 {
		hint += fmt.Sprintf(" · %d selected", sel)
	}
	plain := lipgloss.NewStyle()
	left := []span{{m.st.faint, arrow + " "}, {m.st.title, title}, {plain, "  "}, {m.st.faint, hint}}
	right := []span{{m.st.key, key}, {m.st.faint, " " + verb}}
	gap := 3
	if m.width > 0 {
		gap = max(m.width-2-spansWidth(left)-spansWidth(right)-1, 3)
	}
	return m.rowOf(i == m.cursor, append(append(left, span{plain, strings.Repeat(" ", gap)}), right...)...)
}

func spansWidth(spans []span) int {
	n := 0
	for _, sp := range spans {
		n += lipgloss.Width(sp.text)
	}
	return n
}

func (m Model) entryRow(i, ei, w, cw int) string {
	e := m.entries[ei]
	dim := groupOf(e) == groupHeadless
	st := func(s lipgloss.Style) lipgloss.Style {
		if dim {
			return m.st.faint
		}
		return s
	}
	plain := lipgloss.NewStyle()
	mark := span{st(m.st.faint), "○ "}
	if at := slices.Index(m.selected, e.ID); at >= 0 {
		mark = span{st(m.st.selected), "●" + strconv.Itoa(at+1)}
	}
	nameStyle := m.st.title
	switch groupOf(e) {
	case groupSix, groupElectron:
		nameStyle = plain
	}
	if e.ID == m.last {
		nameStyle = m.st.good.Bold(m.cfg.Color)
	}
	version := majorVersion(e.Version)
	if groupOf(e) == groupSix {
		version = ""
	}
	name := displayName(e)
	spans := []span{mark, {plain, " "}, {st(nameStyle), name}, {plain, strings.Repeat(" ", max(w-lipgloss.Width(name), 0)+2)}, {st(m.st.faint), fmt.Sprintf("%-6s", version)}, {plain, " "}}
	width := 0
	for j, c := range m.chips(ei) {
		if j > 0 {
			spans = append(spans, span{plain, "  "})
			width += 2
		}
		spans = append(spans, span{st(c.st), c.text})
		width += lipgloss.Width(c.text)
	}
	if r := m.right(e); r != "" {
		spans = append(spans, span{plain, strings.Repeat(" ", max(cw-width, 0)+2)}, span{st(m.st.faint), r})
	}
	return m.rowOf(i == m.cursor, spans...)
}

func displayName(e browser.Entry) string {
	switch {
	case e.Six != nil:
		return e.Six.Label
	case e.Endpoint != nil && e.Endpoint.App != "":
		return e.Endpoint.App
	}
	return e.Name
}

func majorVersion(v string) string {
	major, _, _ := strings.Cut(v, ".")
	if n, err := strconv.Atoi(major); err == nil && n < 100 {
		return shortVersion(v)
	}
	return major
}

func (m Model) nameWidth() int {
	w := 14
	for _, e := range m.entries {
		w = max(w, min(lipgloss.Width(displayName(e)), 36))
	}
	return w
}

func (m Model) chipWidth(items []item, g group) int {
	w := 0
	for _, it := range items {
		if it.entry < 0 || it.group != g {
			continue
		}
		n := 0
		for j, c := range m.chips(it.entry) {
			if j > 0 {
				n += 2
			}
			n += lipgloss.Width(c.text)
		}
		w = max(w, n)
	}
	return w
}

func (m Model) chips(ei int) []span {
	e := m.entries[ei]
	var out []span
	switch {
	case e.ID == m.last:
		out = append(out, span{m.st.good, "last launched"})
	case ei == 0 && len(m.selected) == 0 && !flow.IsSix(e) && flow.CanHost(e) == nil:
		out = append(out, span{m.st.accent, "best match"})
	}
	switch groupOf(e) {
	case groupRunning, groupHeadless:
		if e.Endpoint.SevenOpen {
			out = append(out, span{m.st.good, "seven is open"})
		}
		if flow.IsHeadless(e) {
			out = append(out, span{m.st.faint, "headless"})
		}
		if e.Endpoint.WSOnly {
			out = append(out, span{m.st.faint, "websocket only"})
		}
	case groupLaunch:
		p := strconv.Quote(m.profileName(e))
		if m.profileExists(e) {
			out = append(out, span{m.st.faint, "profile " + p})
		} else {
			out = append(out, span{m.st.faint, "new profile " + p})
		}
	case groupSix:
		if t, ok := m.used[e.ID]; ok {
			out = append(out, span{m.st.faint, usedAgo(m.cfg.Now(), t)})
		}
	case groupElectron:
		if e.Installed.DebugSupport == browser.DebugDisabled {
			out = append(out, span{m.st.bad, "blocks debugging"})
		}
	}
	return out
}

func (m Model) profileName(e browser.Entry) string {
	if p := m.profiles[e.ID]; p != "" {
		return p
	}
	if m.cfg.Profile != "" {
		return m.cfg.Profile
	}
	return browser.DefaultProfileName(*e.Installed)
}

func (m Model) profileExists(e browser.Entry) bool {
	return slices.Contains(m.profileList(e), m.profileName(e))
}

func (m Model) right(e browser.Entry) string {
	switch groupOf(e) {
	case groupRunning, groupHeadless:
		s := hostPort(e.Endpoint.CDP)
		if e.Endpoint.Six {
			s += " · SLICC six's profile"
		}
		return s
	case groupSix:
		if e.Installed != nil {
			return "opens in " + m.sixBrowser(e)
		}
		if e.Endpoint != nil {
			return hostPort(e.Endpoint.CDP)
		}
	}
	return ""
}

func (m Model) opens(e browser.Entry) string {
	name := m.st.title.Render(displayName(e))
	switch groupOf(e) {
	case groupLaunch:
		p := m.st.title.Render(strconv.Quote(m.profileName(e)))
		if !m.profileExists(e) {
			return "Opens seven in " + name + " " + majorVersion(e.Version) + " (create new " + e.Name + " profile " + p + " for testing SLICC seven)"
		}
		return "Opens seven in " + name + " " + majorVersion(e.Version) + " (SLICC profile " + p + ")"
	case groupRunning, groupHeadless:
		if e.Endpoint.SevenOpen {
			return "Links the seven tab in " + name + " at " + hostPort(e.Endpoint.CDP)
		}
		return "Opens seven in " + name + " at " + hostPort(e.Endpoint.CDP) + " (new tab)"
	case groupSix:
		return "Re-uses existing SLICC profile " + name + " in " + m.sixBrowser(e) + " " + m.risk().Render("(might lose data)")
	}
	return "Adds " + name + " as an extra browser"
}

func (m Model) risk() lipgloss.Style {
	return m.st.warn.Bold(m.cfg.Color && !m.dark)
}

func (m Model) sixBrowser(e browser.Entry) string {
	if e.Installed == nil {
		return "Chrome"
	}
	return strings.TrimSpace(e.Installed.Name + " " + majorVersion(e.Installed.Version))
}

func andList(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func (m Model) detail() (string, string) {
	enter := m.st.cursor.Render("↵") + " "
	if len(m.selected) > 0 {
		sel := m.choice()
		s := enter + m.opens(sel[0])
		if len(sel) > 1 {
			var extra []string
			for _, e := range sel[1:] {
				extra = append(extra, displayName(e))
			}
			s += ", with " + andList(extra) + " as " + pluralize(len(extra), "an extra browser", "extra browsers")
		}
		return s, "space adds or removes the highlighted row"
	}
	if g, ok := m.onFold(); ok {
		title, _, _ := m.foldTitle(g)
		verb := "Shows "
		if m.open[g] {
			verb = "Hides "
		}
		hint := "browsers without a window, usually left by tests; seven would run where you can't see it"
		if g == groupElectron {
			hint = "Electron apps can't host seven; select them with space as extra browsers"
		}
		return enter + verb + title, hint
	}
	e, ok := m.current()
	if !ok {
		return "", ""
	}
	switch groupOf(e) {
	case groupLaunch:
		dir := ""
		if m.cfg.Dir != nil {
			dir = tilde(m.cfg.Dir(e, m.profileName(e)))
		}
		return enter + m.opens(e), joinNonEmpty(dir, "p picks another profile")
	case groupRunning, groupHeadless:
		ctx := e.Endpoint.UserDataDir
		if flow.IsHeadless(e) {
			ctx = joinNonEmpty("headless: it has no window you can see", tilde(ctx))
		}
		return enter + m.opens(e), tilde(ctx)
	case groupSix:
		dir := ""
		if e.Six != nil {
			dir = e.Six.Dir
		}
		return enter + m.opens(e), tilde(dir)
	default:
		if e.Installed.DebugSupport == browser.DebugDisabled {
			return "  " + m.st.faint.Render(displayName(e)+" blocks remote debugging, so it can't be picked"), tilde(e.Installed.Path)
		}
		return "  " + m.opens(e) + " with " + m.st.key.Render("space") + "; Electron apps can't host seven", tilde(e.Installed.Path)
	}
}

func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

func (m Model) keys() string {
	pairs := [][2]string{{"↑↓", "move"}, {"↵", "go"}, {"space", "select"}, {"p", "profile"}}
	if slices.Contains(m.order, groupElectron) {
		pairs = append(pairs, [2]string{"e", "apps"})
	}
	if slices.Contains(m.order, groupHeadless) {
		pairs = append(pairs, [2]string{"h", "headless"})
	}
	return m.keyBar(append(pairs, [2]string{"q", "quit"})...)
}
