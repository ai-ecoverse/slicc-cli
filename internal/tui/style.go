package tui

import (
	"charm.land/lipgloss/v2"
)

type styles struct {
	title    lipgloss.Style
	faint    lipgloss.Style
	cursor   lipgloss.Style
	selected lipgloss.Style
	good     lipgloss.Style
	warn     lipgloss.Style
	bad      lipgloss.Style
	accent   lipgloss.Style
	section  lipgloss.Style
	key      lipgloss.Style
	row      lipgloss.Style
}

func newStyles(color, dark bool) styles {
	if !color {
		p := lipgloss.NewStyle()
		return styles{title: p, faint: p, cursor: p, selected: p, good: p, warn: p, bad: p, accent: p, section: p, key: p, row: p}
	}
	pick := lipgloss.LightDark(dark)
	faint := lipgloss.NewStyle().Faint(true)
	if !dark {
		faint = lipgloss.NewStyle().Foreground(lipgloss.Color("#57606a"))
	}
	return styles{
		title:    lipgloss.NewStyle().Bold(true),
		faint:    faint,
		cursor:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
		selected: lipgloss.NewStyle().Bold(true).Foreground(pick(lipgloss.Color("2"), lipgloss.Color("10"))),
		good:     lipgloss.NewStyle().Foreground(pick(lipgloss.Color("2"), lipgloss.Color("10"))),
		warn:     lipgloss.NewStyle().Foreground(pick(lipgloss.Color("3"), lipgloss.Color("11"))),
		bad:      lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		accent:   lipgloss.NewStyle().Foreground(lipgloss.Color("12")),
		section:  lipgloss.NewStyle().Bold(true).Foreground(pick(lipgloss.Color("#5f6b7a"), lipgloss.Color("#9aa0b4"))),
		key:      lipgloss.NewStyle().Bold(true),
		row:      lipgloss.NewStyle().Background(pick(lipgloss.Color("#dce9f8"), lipgloss.Color("#26364a"))),
	}
}
