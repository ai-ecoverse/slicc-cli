package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m Model) pairKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q":
		return m.quit()
	case "enter":
		if m.cfg.NewPairing != nil {
			m.cfg.NewPairing()
		}
	case "y":
		if m.dash.pairURL != "" {
			m.dash.log(m.cfg.Now(), "copied the pairing URL")
			return m, tea.SetClipboard(m.dash.pairURL)
		}
	}
	return m, nil
}

func (m Model) pairLink(u string) []string {
	w := m.width - 14
	var parts []string
	for w >= 20 && len(u) > w {
		parts, u = append(parts, u[:w]), u[w:]
	}
	parts = append(parts, u)
	if !m.cfg.Color {
		return parts
	}
	link := lipgloss.NewStyle().Hyperlink(m.dash.pairURL)
	for i, p := range parts {
		parts[i] = link.Render(p)
	}
	return parts
}

func (m Model) pairView(b []string) string {
	d, st := m.dash, m.st
	b = append(b, m.sectionLine("BROWSER", ""))
	b = append(b, fmt.Sprintf("  %s %s  %s  %s", st.good.Render("●"), st.title.Render("Default browser"), st.faint.Render("paired over loopback · no cdp"), st.accent.Render("hosts seven")))
	b = append(b, "", m.sectionLine("SEVEN", ""))
	if d.pairURL != "" && d.state != StateLinked && d.state != StateClosed {
		for i, l := range m.pairLink(d.pairURL) {
			if i == 0 {
				b = append(b, m.field("pairing", l))
			} else {
				b = append(b, m.field("", l))
			}
		}
		b = append(b, m.field("", st.faint.Render("opens in the default browser; works once, for 10 minutes")))
	} else if d.state != StateClosed {
		b = append(b, m.field("pairing", st.faint.Render("↵ for a URL to pair another seven")))
	}
	b = append(b, m.field("link", m.stateLine()))
	if d.policy != "" {
		b = append(b, m.field("policy", d.policy))
	}
	b = append(b, m.field("streams", m.streamText()))
	b = append(b, "", m.sectionLine("CAPABILITIES", st.faint.Render("restart slicc with flags to change them")))
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
		pairs := [][2]string{{"↵", "new pairing URL"}}
		if d.pairURL != "" {
			pairs = append(pairs, [2]string{"y", "copy it"})
		}
		b = append(b, m.keyBar(append(pairs, [2]string{"q", "quit"})...))
	}
	return strings.Join(b, "\n") + "\n"
}
