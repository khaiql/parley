package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorPrimary   = lipgloss.Color("#58a6ff")
	colorHuman     = lipgloss.Color("#f0883e")
	colorAgent     = lipgloss.Color("#a5d6ff")
	colorSystem    = lipgloss.Color("#8b949e")
	colorRoleBadge = lipgloss.Color("#388bfd")
	colorBorder    = lipgloss.Color("#30363d")
	colorText      = lipgloss.Color("#c9d1d9")
	colorDimText   = lipgloss.Color("#484f58")
)

var (
	topBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#161b22")).
			Foreground(colorText).
			Padding(0, 1).
			Bold(true)

	sidebarStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderLeft(true).
			BorderForeground(colorBorder).
			Foreground(colorText).
			Padding(0, 1)

	sidebarTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorPrimary).
				MarginBottom(1)

	inputStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(colorBorder).
			Padding(0, 1)

	humanNameStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorHuman)

	agentNameStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAgent)

	roleBadgeStyle = lipgloss.NewStyle().
			Background(colorRoleBadge).
			Foreground(lipgloss.Color("#ffffff")).
			Padding(0, 1)

	systemMsgStyle = lipgloss.NewStyle().
			Foreground(colorSystem).
			Italic(true)

	timestampStyle = lipgloss.NewStyle().
			Foreground(colorDimText)

	participantStatusStyle = lipgloss.NewStyle().
				Foreground(colorSystem).
				Italic(true)
)
