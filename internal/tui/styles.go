package tui

import (
	"hash/fnv"

	"github.com/charmbracelet/lipgloss"
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
	colorConnected   = lipgloss.Color("#3fb950")
	colorStatusBarBg = lipgloss.Color("#30363d")
)

var agentPalette = []lipgloss.Color{
	"#a78bfa", "#7dd3fc", "#34d399", "#fbbf24",
	"#f472b6", "#60a5fa", "#a3e635", "#fb923c",
}

// ColorForSender returns a deterministic color for a participant.
// Humans always get colorHuman; agents get a color from the palette
// based on an FNV hash of their name.
func ColorForSender(name string, isHuman bool) lipgloss.Color {
	if isHuman {
		return colorHuman
	}
	h := fnv.New32a()
	h.Write([]byte(name))
	idx := int(h.Sum32()) % len(agentPalette)
	return agentPalette[idx]
}

// ColorForIndex returns the palette color for a server-assigned color index.
// The index wraps around so any integer (including negative) is safe.
func ColorForIndex(idx int) lipgloss.Color {
	n := len(agentPalette)
	return agentPalette[((idx%n)+n)%n]
}

// agentNameStyleFor returns a bold style with the given foreground color.
func agentNameStyleFor(c lipgloss.Color) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(c)
}

// agentBadgeStyleFor returns a badge style with bg #30363d and the given foreground.
func agentBadgeStyleFor(c lipgloss.Color) lipgloss.Style {
	return lipgloss.NewStyle().
		Background(lipgloss.Color("#30363d")).
		Foreground(c).
		Padding(0, 1)
}

var (
	topBarStyle = lipgloss.NewStyle().
			Background(colorSidebarBg).
			Foreground(colorText).
			Padding(0, 1).
			Bold(true)

	sidebarStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderLeft(true).
			BorderForeground(colorBorder).
			Foreground(colorText).
			Padding(0, 1).
			Background(colorSidebarBg)

	sidebarTitleStyle = lipgloss.NewStyle().
				Foreground(colorDimText).
				MarginBottom(1)

	sidebarBrandStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorPrimary).
				Align(lipgloss.Center)

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

	participantStatusStyle = lipgloss.NewStyle().
				Foreground(colorSystem).
				Italic(true)

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
