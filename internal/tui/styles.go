package tui

import (
	"hash/fnv"

	"github.com/charmbracelet/lipgloss"
	"github.com/khaiql/parley/internal/room"
)

var (
	colorPrimary     = lipgloss.Color("#58a6ff")
	colorHuman       = lipgloss.Color("#f0883e")
	colorSystem      = lipgloss.Color("#8b949e")
	colorText        = lipgloss.Color("#e1e4e8")
	colorDimText     = lipgloss.Color("#6e7681")
	colorBorder      = lipgloss.Color("#3b3f47")
	colorSeparator   = lipgloss.Color("#21262d")
	colorSidebarBg   = lipgloss.Color("#161b22")
	colorStatusBarBg = lipgloss.Color("#30363d")
)

// ColorForSender returns the display colour for a participant.
// If assignedColor is non-empty, it is used directly (server-assigned).
// Humans always get colorHuman. Agents with no assigned colour fall back
// to FNV hash of name (for 9+ participants or legacy data).
func ColorForSender(name string, isHuman bool, assignedColor string) lipgloss.Color {
	if isHuman {
		return colorHuman
	}
	if assignedColor != "" {
		return lipgloss.Color(assignedColor)
	}
	h := fnv.New32a()
	h.Write([]byte(name))
	idx := int(h.Sum32()) % len(room.AgentPalette)
	return lipgloss.Color(room.AgentPalette[idx])
}

// agentNameStyleFor returns a bold style with the given foreground color.
func agentNameStyleFor(c lipgloss.Color) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(c)
}

// agentBadgeStyleFor returns a badge style with the status bar background and the given foreground.
func agentBadgeStyleFor(c lipgloss.Color) lipgloss.Style {
	return lipgloss.NewStyle().
		Background(colorStatusBarBg).
		Foreground(c).
		Padding(0, 1)
}

var (
	topBarStyle = lipgloss.NewStyle().
			Background(colorStatusBarBg).
			Foreground(colorText).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(colorPrimary).
			Padding(0, 1).
			Bold(true)

	sidebarStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderLeft(true).
			BorderForeground(colorBorder).
			Foreground(colorText).
			Padding(0, 1).
			Background(colorSidebarBg)

	inputStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(colorBorder).
			Padding(0, 1)

	humanNameStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorHuman)

	systemMsgStyle = lipgloss.NewStyle().
			Foreground(colorSystem).
			Italic(true)

	timestampStyle = lipgloss.NewStyle().
			Foreground(colorDimText)

	offlineNameStyle = lipgloss.NewStyle().
				Foreground(colorDimText).
				Italic(true)

	separatorStyle = lipgloss.NewStyle().
			Foreground(colorSeparator)

	modalStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary).
			Padding(0, 1)

	modalTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			MarginBottom(1)

	modalFooterStyle = lipgloss.NewStyle().
				Foreground(colorDimText).
				Italic(true).
				MarginTop(1)
)
