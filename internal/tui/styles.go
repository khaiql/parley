package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorPrimary = lipgloss.Color("#58a6ff")
	colorHuman   = lipgloss.Color("#f0883e")
	colorAgent   = lipgloss.Color("#a5d6ff")
	colorSystem  = lipgloss.Color("#8b949e")
	colorBorder  = lipgloss.Color("#30363d")
	colorText    = lipgloss.Color("#c9d1d9")
	colorDimText = lipgloss.Color("#484f58")

	// participantColors is a palette of distinct colors assigned to participants
	// by their join order. The human always gets colorHuman; agents cycle through these.
	participantColors = []lipgloss.Color{
		lipgloss.Color("#a5d6ff"), // light blue
		lipgloss.Color("#d2a8ff"), // purple
		lipgloss.Color("#7ee787"), // green
		lipgloss.Color("#f778ba"), // pink
		lipgloss.Color("#79c0ff"), // sky blue
		lipgloss.Color("#ff7b72"), // coral
		lipgloss.Color("#d8e77e"), // lime
		lipgloss.Color("#ffc680"), // peach
	}
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

	systemMsgStyle = lipgloss.NewStyle().
			Foreground(colorSystem).
			Italic(true)

	timestampStyle = lipgloss.NewStyle().
			Foreground(colorDimText)

	participantStatusStyle = lipgloss.NewStyle().
				Foreground(colorSystem).
				Italic(true)

	listeningStatusStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#3fb950")).
				Italic(true)
)
