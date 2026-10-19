package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	hostfs "github.com/ai-ecoverse/slicc-cli/internal/caps/fs"
)

type Folder struct {
	Path     string
	Name     string
	ReadOnly bool
	Off      bool
}

type Settings struct {
	Jerk    bool
	Shell   []string
	Kernel  int
	Folders []Folder
}

func (s Settings) clone() Settings {
	s.Shell = slices.Clone(s.Shell)
	s.Folders = slices.Clone(s.Folders)
	return s
}

func (s Settings) shared() []Folder {
	var out []Folder
	for _, f := range s.Folders {
		if !f.Off {
			out = append(out, f)
		}
	}
	return out
}

type change struct {
	text   string
	relink bool
}

func changes(from, to Settings, live bool) []change {
	var out []change
	if from.Jerk != to.Jerk {
		out = append(out, change{sign(to.Jerk) + " jerk", true})
	}
	switch a, b := strings.Join(from.Shell, " "), strings.Join(to.Shell, " "); {
	case a == b:
	case a == "":
		out = append(out, change{"+ shell " + b, true})
	case b == "":
		out = append(out, change{"- shell", true})
	default:
		out = append(out, change{"shell " + a + " → " + b, true})
	}
	switch {
	case from.Kernel == to.Kernel:
	case from.Kernel == 0:
		out = append(out, change{fmt.Sprintf("+ kernel :%d", to.Kernel), true})
	case to.Kernel == 0:
		out = append(out, change{"- kernel", true})
	default:
		out = append(out, change{fmt.Sprintf("kernel :%d → :%d", from.Kernel, to.Kernel), true})
	}
	old := from.shared()
	relink := !live || len(old) == 0
	was := map[string]Folder{}
	for _, f := range old {
		was[f.Name] = f
	}
	now := map[string]bool{}
	for _, f := range to.shared() {
		now[f.Name] = true
		o, ok := was[f.Name]
		switch {
		case !ok:
			out = append(out, change{"+ folder " + f.Name, relink})
		case o.Path != f.Path:
			out = append(out, change{"folder " + f.Name + " → " + homeRel(f.Path), relink})
		case o.ReadOnly != f.ReadOnly:
			out = append(out, change{"folder " + f.Name + " " + access(f.ReadOnly), relink})
		}
	}
	for _, f := range old {
		if !now[f.Name] {
			out = append(out, change{"- folder " + f.Name, relink})
		}
	}
	return out
}

func sign(on bool) string {
	if on {
		return "+"
	}
	return "-"
}

func access(ro bool) string {
	if ro {
		return "read-only"
	}
	return "read-write"
}

func relinks(cs []change) bool {
	return slices.ContainsFunc(cs, func(c change) bool { return c.relink })
}

type rowKind int

const (
	rowJerk rowKind = iota
	rowShell
	rowKernel
	rowFolder
	rowAdd
)

type capRow struct {
	kind   rowKind
	folder int
}

type inputKind int

const (
	inputNone inputKind = iota
	inputShell
	inputKernel
	inputFolder
)

type capsEditor struct {
	draft   Settings
	cursor  int
	editing inputKind
	index   int
	input   textinput.Model
	confirm bool
	runner  []string
	kernel  int
	back    Mode
}

func (e capsEditor) rows() []capRow {
	rows := []capRow{{kind: rowJerk}, {kind: rowShell}, {kind: rowKernel}}
	for i := range e.draft.Folders {
		rows = append(rows, capRow{kind: rowFolder, folder: i})
	}
	return append(rows, capRow{kind: rowAdd})
}

func (e capsEditor) row() capRow {
	rows := e.rows()
	return rows[min(e.cursor, len(rows)-1)]
}

func (m Model) liveMounts() bool {
	return m.cfg.LiveMounts != nil && m.cfg.LiveMounts()
}

func (m Model) pending() []change {
	return changes(m.settings, m.caps.draft, m.liveMounts())
}

func (m *Model) openCaps() {
	m.message = ""
	m.caps.draft = m.settings.clone()
	m.caps.cursor = 0
	m.caps.editing = inputNone
	m.caps.confirm = false
	m.caps.back = m.mode
	if len(m.settings.Shell) > 0 {
		m.caps.runner = slices.Clone(m.settings.Shell)
	} else if m.caps.runner == nil {
		m.caps.runner = slices.Clone(m.cfg.Runner)
	}
	if m.settings.Kernel > 0 {
		m.caps.kernel = m.settings.Kernel
	} else if m.caps.kernel == 0 {
		m.caps.kernel = 80
	}
	m.mode = ModeCaps
}

func (m Model) capsKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.caps.editing != inputNone {
		return m.capsInputKey(k)
	}
	if m.caps.confirm {
		return m.confirmKey(k)
	}
	m.message = ""
	rows := m.caps.rows()
	r := m.caps.row()
	switch k.String() {
	case "up", "k":
		if m.caps.cursor > 0 {
			m.caps.cursor--
		}
	case "down", "j":
		if m.caps.cursor < len(rows)-1 {
			m.caps.cursor++
		}
	case "home", "g":
		m.caps.cursor = 0
	case "end", "G":
		m.caps.cursor = len(rows) - 1
	case "space":
		return m.toggleCap(r)
	case "enter":
		return m.editCap(r)
	case "a", "+":
		return m.startInput(inputFolder, -1, "")
	case "r":
		if r.kind == rowFolder {
			f := &m.caps.draft.Folders[r.folder]
			f.ReadOnly = !f.ReadOnly
		}
	case "x", "d", "delete":
		if r.kind == rowFolder {
			m.caps.draft.Folders = slices.Delete(m.caps.draft.Folders, r.folder, r.folder+1)
		}
	case "u":
		m.caps.draft = m.settings.clone()
		m.message = "changes undone"
	case "s":
		if len(m.pending()) == 0 {
			m.message = "nothing to apply"
			return m, nil
		}
		return m.applyCaps()
	case "esc", "q", "c":
		if len(m.pending()) > 0 {
			m.caps.confirm = true
			return m, nil
		}
		m.mode = m.caps.back
	}
	return m, nil
}

func (m Model) confirmKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y", "s", "enter":
		m.caps.confirm = false
		return m.applyCaps()
	case "n":
		m.caps.confirm = false
		m.caps.draft = m.settings.clone()
		m.mode = m.caps.back
	case "esc":
		m.caps.confirm = false
	}
	return m, nil
}

func (m Model) toggleCap(r capRow) (tea.Model, tea.Cmd) {
	d := &m.caps.draft
	switch r.kind {
	case rowJerk:
		if m.cfg.JerkVersion == "" && !d.Jerk {
			m.message = "slicc-jerk isn't installed here; run slicc install jerk"
			return m, nil
		}
		d.Jerk = !d.Jerk
	case rowShell:
		if len(d.Shell) > 0 {
			m.caps.runner = d.Shell
			d.Shell = nil
			return m, nil
		}
		if len(m.caps.runner) == 0 {
			return m.startInput(inputShell, 0, "")
		}
		d.Shell = slices.Clone(m.caps.runner)
	case rowKernel:
		if d.Kernel > 0 {
			m.caps.kernel = d.Kernel
			d.Kernel = 0
			return m, nil
		}
		d.Kernel = m.caps.kernel
	case rowFolder:
		f := &d.Folders[r.folder]
		f.Off = !f.Off
	case rowAdd:
		return m.startInput(inputFolder, -1, "")
	}
	return m, nil
}

func (m Model) editCap(r capRow) (tea.Model, tea.Cmd) {
	d := m.caps.draft
	switch r.kind {
	case rowJerk:
		return m.toggleCap(r)
	case rowShell:
		runner := d.Shell
		if len(runner) == 0 {
			runner = m.caps.runner
		}
		return m.startInput(inputShell, 0, strings.Join(runner, " "))
	case rowKernel:
		port := d.Kernel
		if port == 0 {
			port = m.caps.kernel
		}
		return m.startInput(inputKernel, 0, strconv.Itoa(port))
	case rowFolder:
		return m.startInput(inputFolder, r.folder, spec(d.Folders[r.folder]))
	}
	return m.startInput(inputFolder, -1, "")
}

func spec(f Folder) string {
	s := homeRel(f.Path)
	if f.Name != filepath.Base(f.Path) {
		s += ":" + f.Name
	}
	if f.ReadOnly {
		s += ":ro"
	}
	return s
}

func (m Model) startInput(kind inputKind, index int, value string) (tea.Model, tea.Cmd) {
	if m.mode != ModeCaps {
		m.openCaps()
	}
	in := textinput.New()
	in.CharLimit = 1024
	in.SetWidth(56)
	switch kind {
	case inputShell:
		in.Placeholder = "bash -c"
	case inputKernel:
		in.Placeholder = "80"
		in.CharLimit = 5
	case inputFolder:
		in.Placeholder = "~/path/to/folder[:name][:ro]"
		in.ShowSuggestions = true
	}
	in.SetValue(value)
	in.CursorEnd()
	m.caps.input = in
	m.caps.editing = kind
	m.caps.index = index
	m.message = ""
	m.suggest()
	return m, m.caps.input.Focus()
}

func (m *Model) suggest() {
	if m.caps.editing == inputFolder {
		m.caps.input.SetSuggestions(dirSuggestions(m.caps.input.Value()))
	}
}

func (m Model) capsInputKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.caps.editing = inputNone
		m.message = ""
		return m, nil
	case "enter":
		if err := m.commitInput(); err != nil {
			m.message = err.Error()
			return m, nil
		}
		m.caps.editing = inputNone
		m.message = ""
		return m, nil
	}
	var cmd tea.Cmd
	m.caps.input, cmd = m.caps.input.Update(k)
	m.suggest()
	return m, cmd
}

func (m *Model) commitInput() error {
	value := strings.TrimSpace(m.caps.input.Value())
	d := &m.caps.draft
	switch m.caps.editing {
	case inputShell:
		argv := strings.Fields(value)
		if len(argv) == 0 {
			return errors.New("type the runner's command, such as bash -c")
		}
		if err := m.checkRunner(argv); err != nil {
			return err
		}
		m.caps.runner = argv
		d.Shell = slices.Clone(argv)
	case inputKernel:
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("%q is not a port: use 1-65535", value)
		}
		m.caps.kernel = port
		d.Kernel = port
	case inputFolder:
		f, err := m.parseFolder(value, m.caps.index)
		if err != nil {
			return err
		}
		if m.caps.index < 0 {
			d.Folders = append(d.Folders, f)
			m.caps.cursor = len(m.caps.rows()) - 2
		} else {
			d.Folders[m.caps.index] = f
		}
	}
	return nil
}

func (m Model) checkRunner(argv []string) error {
	if m.cfg.CheckRunner != nil {
		return m.cfg.CheckRunner(argv)
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return fmt.Errorf("%s: program not found", argv[0])
	}
	return nil
}

func (m Model) parseFolder(value string, index int) (Folder, error) {
	if value == "" {
		return Folder{}, errors.New("type a folder's path; tab completes it")
	}
	path, name, ro := hostfs.ParseSpec(value)
	root, err := filepath.Abs(expandHome(path))
	if err != nil {
		return Folder{}, err
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return Folder{}, fmt.Errorf("%s is not an existing folder", path)
	}
	if hostfs.Fenced(root) {
		return Folder{}, fmt.Errorf("slicc never shares %s: it holds slicc's keys or a credential tool's files", path)
	}
	if name == "" {
		name = filepath.Base(root)
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return Folder{}, fmt.Errorf("%q is not a folder name", name)
	}
	for i, f := range m.caps.draft.Folders {
		if i != index && f.Name == name {
			return Folder{}, fmt.Errorf("the name %s is taken; add :<name> to pick another", name)
		}
	}
	return Folder{Path: root, Name: name, ReadOnly: ro}, nil
}

func (m Model) applyCaps() (tea.Model, tea.Cmd) {
	cs := m.pending()
	relink := relinks(cs)
	m.settings = m.caps.draft.clone()
	m.mode = m.caps.back
	texts := make([]string, len(cs))
	for i, c := range cs {
		texts[i] = c.text
	}
	how := "live"
	if relink {
		how = "relinking seven"
	}
	m.dash.log(m.cfg.Now(), how+": "+strings.Join(texts, ", "))
	if relink {
		m.dash.setState(StateRelinking, m.cfg.Now())
	}
	if m.cfg.Apply != nil {
		m.cfg.Apply(m.settings.clone(), relink)
	}
	return m, nil
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if h := homeDir(); h != "" {
			return h + p[1:]
		}
	}
	return p
}

func homeRel(p string) string {
	h := homeDir()
	if h != "" && (p == h || strings.HasPrefix(p, h+string(filepath.Separator))) {
		return "~" + p[len(h):]
	}
	return p
}

const maxSuggestions = 200

func dirSuggestions(value string) []string {
	if strings.Contains(strings.TrimPrefix(value, filepath.VolumeName(value)), ":") {
		return nil
	}
	i := strings.LastIndexAny(value, "/"+string(filepath.Separator))
	if i < 0 {
		return nil
	}
	dir, base := value[:i+1], value[i+1:]
	entries, err := os.ReadDir(expandHome(dir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(name), strings.ToLower(base)) {
			continue
		}
		if !e.IsDir() {
			fi, err := os.Stat(filepath.Join(expandHome(dir), name))
			if err != nil || !fi.IsDir() {
				continue
			}
		}
		out = append(out, dir+name+"/")
		if len(out) == maxSuggestions {
			break
		}
	}
	return out
}

var mutableCaps = map[string]bool{"jerk": true, "jerk-seal": true, "ssh": true, "fs": true}

type span struct {
	st   lipgloss.Style
	text string
}

func (m Model) rowOf(cur bool, spans ...span) string {
	base := lipgloss.NewStyle()
	if cur {
		base = m.st.row
	}
	s := "  "
	if cur {
		s = "› "
		if m.cfg.Color {
			s = m.st.cursor.Inherit(base).Render("▌") + base.Render(" ")
		}
	}
	for _, sp := range spans {
		s += sp.st.Inherit(base).Render(sp.text)
	}
	if cur && m.width > 0 && m.cfg.Color {
		s += base.Render(strings.Repeat(" ", max(m.width-lipgloss.Width(s), 0)))
	}
	return s
}

func (m Model) capsView() string {
	st := m.st
	cs := m.pending()
	right := st.faint.Render("nothing pending")
	if len(cs) > 0 {
		right = st.warn.Render(fmt.Sprintf("%d %s pending", len(cs), pluralize(len(cs), "change", "changes")))
	}
	b := []string{m.spread(st.title.Render("slicc")+st.faint.Render(" · capabilities"), right), ""}
	b = append(b, m.sectionLine("THIS MACHINE", "what seven may use here"))
	d := m.caps.draft
	for i, r := range m.caps.rows() {
		if r.kind == rowFolder && r.folder == 0 || r.kind == rowAdd && len(d.Folders) == 0 {
			b = append(b, "", m.sectionLine("FOLDERS", "shared with seven's kernel"))
		}
		b = append(b, m.capLine(i, r))
	}
	var always []string
	for _, c := range m.dash.caps {
		if !mutableCaps[c] {
			always = append(always, c)
		}
	}
	if len(always) > 0 {
		b = append(b, "", m.sectionLine("ALWAYS OFFERED", strings.Join(always, " · ")))
	}
	b = append(b, "")
	if m.caps.editing != inputNone {
		b = append(b, "  "+st.cursor.Render("↵")+" "+m.inputLabel(), "  "+m.caps.input.View())
	} else {
		l1, l2 := m.capDetail()
		b = append(b, "  "+st.cursor.Render("↵")+" "+l1)
		if l2 != "" {
			b = append(b, "    "+st.faint.Render(l2))
		}
	}
	if len(cs) > 0 {
		texts := make([]string, len(cs))
		for i, c := range cs {
			texts[i] = c.text
		}
		how := "applies live: seven gets the new folder list, nothing relinks"
		if relinks(cs) {
			how = "applying relinks seven: open streams close, the browsers stay up"
		}
		b = append(b, "", "  "+st.warn.Render("PENDING")+"  "+strings.Join(texts, st.faint.Render(" · ")), "           "+st.faint.Render(how))
	}
	if m.message != "" {
		b = append(b, "", "  "+st.bad.Render(m.message))
	}
	b = append(b, "", m.capKeys())
	return strings.Join(b, "\n") + "\n"
}

func pluralize(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (m Model) capKeys() string {
	switch {
	case m.caps.confirm:
		return "  " + m.st.cursor.Render("apply these changes?") + m.keyBar([2]string{"y", "apply"}, [2]string{"n", "discard"}, [2]string{"esc", "keep editing"})
	case m.caps.editing == inputFolder:
		return m.keyBar([2]string{"tab", "complete"}, [2]string{"↑↓", "other matches"}, [2]string{"↵", "done"}, [2]string{"esc", "cancel"})
	case m.caps.editing != inputNone:
		return m.keyBar([2]string{"↵", "done"}, [2]string{"esc", "cancel"})
	}
	pairs := [][2]string{{"↑↓", "move"}, {"space", "on/off"}, {"↵", "edit"}}
	if m.caps.row().kind == rowFolder {
		pairs = append(pairs, [2]string{"r", "read-only"}, [2]string{"x", "remove"})
	}
	pairs = append(pairs, [2]string{"a", "add folder"})
	if len(m.pending()) > 0 {
		pairs = append(pairs, [2]string{"s", "apply"}, [2]string{"u", "undo"})
	}
	return m.keyBar(append(pairs, [2]string{"esc", "back"})...)
}

func (m Model) changed(r capRow) string {
	a, d := m.settings, m.caps.draft
	switch r.kind {
	case rowJerk:
		if a.Jerk != d.Jerk {
			return "changed"
		}
	case rowShell:
		if !slices.Equal(a.Shell, d.Shell) {
			return "changed"
		}
	case rowKernel:
		if a.Kernel != d.Kernel {
			return "changed"
		}
	case rowFolder:
		f := d.Folders[r.folder]
		i := slices.IndexFunc(a.Folders, func(o Folder) bool { return o.Name == f.Name })
		if i < 0 {
			return "new"
		}
		if a.Folders[i] != f {
			return "changed"
		}
	}
	return ""
}

func (m Model) capLine(i int, r capRow) string {
	d, st := m.caps.draft, m.st
	cur := i == m.caps.cursor
	plain := lipgloss.NewStyle()
	mark := func(on bool) span {
		if on {
			return span{st.selected, "● "}
		}
		return span{st.faint, "○ "}
	}
	tail := func(spans []span) []span {
		if c := m.changed(r); c != "" {
			spans = append(spans, span{plain, "  "}, span{st.warn, c})
		}
		return spans
	}
	cell := func(on bool, name, value, desc string) string {
		vs := plain
		if !on {
			vs = st.faint
		}
		return m.rowOf(cur, tail([]span{mark(on), {st.title, fmt.Sprintf("%-8s", name)}, {vs, fmt.Sprintf("%-14s", value)}, {st.faint, desc}})...)
	}
	switch r.kind {
	case rowJerk:
		switch {
		case m.cfg.JerkVersion == "":
			return cell(false, "jerk", "not installed", "slicc install jerk")
		case d.Jerk:
			return cell(true, "jerk", m.cfg.JerkVersion, "sign-ins and tokens for seven's vault")
		}
		return cell(false, "jerk", "off", "seven can't ask slicc-jerk for tokens")
	case rowShell:
		if len(d.Shell) > 0 {
			return cell(true, "shell", strings.Join(d.Shell, " "), "seven can ssh into this machine")
		}
		return cell(false, "shell", "off", "seven can't run commands here")
	case rowKernel:
		if d.Kernel > 0 {
			desc := "<port>.kernel.localhost reaches seven's servers"
			if d.Kernel == m.settings.Kernel && m.dash.kernOff != "" {
				return m.rowOf(cur, tail([]span{{st.bad, "● "}, {st.title, fmt.Sprintf("%-8s", "kernel")}, {plain, fmt.Sprintf("%-14s", fmt.Sprintf(":%d", d.Kernel))}, {st.bad, "off: " + m.dash.kernOff}})...)
			}
			return cell(true, "kernel", fmt.Sprintf(":%d", d.Kernel), desc)
		}
		return cell(false, "kernel", "off", "seven's servers aren't reachable from here")
	case rowFolder:
		f := d.Folders[r.folder]
		how := span{st.faint, fmt.Sprintf("%-14s", "read-write")}
		switch {
		case f.Off:
			how = span{st.faint, fmt.Sprintf("%-14s", "not shared")}
		case f.ReadOnly:
			how = span{st.warn, fmt.Sprintf("%-14s", "read-only")}
		}
		ns := st.title
		if f.Off {
			ns = st.faint
		}
		return m.rowOf(cur, tail([]span{mark(!f.Off), {ns, fmt.Sprintf("%-8s", f.Name) + " "}, how, {st.faint, homeRel(f.Path)}})...)
	}
	return m.rowOf(cur, span{st.accent, "+ "}, span{st.accent, "add a folder…"})
}

func (m Model) capDetail() (string, string) {
	d, st := m.caps.draft, m.st
	r := m.caps.row()
	switch r.kind {
	case rowJerk:
		if m.cfg.JerkVersion == "" {
			return "slicc-jerk isn't installed", "run slicc install jerk, then restart slicc"
		}
		return "turn slicc-jerk " + onOffWord(!d.Jerk) + " for seven", "jerk signs in on this machine and seals tokens into seven's vault"
	case rowShell:
		runner := firstOf(strings.Join(d.Shell, " "), strings.Join(m.caps.runner, " "), "bash -c")
		return "change the runner: each command seven runs becomes " + st.title.Render(runner) + " <command>", "space turns the shell on or off; seven reaches it over ssh"
	case rowKernel:
		return "change the port for <port>.kernel.localhost", "space turns kernel services on or off; ports below 1024 need root on macOS and Linux"
	case rowFolder:
		f := d.Folders[r.folder]
		return "edit " + st.title.Render(f.Name) + " as path[:name][:ro]", homeRel(f.Path) + " · space shares or unshares · r read-only · x removes"
	}
	return "share a folder with seven's kernel", "the same path[:name][:ro] as --mount; tab completes"
}

func onOffWord(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func (m Model) inputLabel() string {
	switch m.caps.editing {
	case inputShell:
		return "runner: each command seven runs becomes <runner> <command>"
	case inputKernel:
		return "port for <port>.kernel.localhost"
	}
	if m.caps.index >= 0 {
		return "folder: path[:name][:ro]"
	}
	return "share a folder: path[:name][:ro]"
}
