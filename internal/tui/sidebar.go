package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sle/parley/internal/protocol"
)

// Sidebar renders the participant list panel.
type Sidebar struct {
	participants []protocol.Participant
	width        int
	height       int
}

// NewSidebar creates an empty Sidebar.
func NewSidebar() Sidebar {
	return Sidebar{}
}

// SetSize updates the sidebar dimensions.
func (s *Sidebar) SetSize(width, height int) {
	s.width = width
	s.height = height
}

// SetParticipants replaces the full participant list.
func (s *Sidebar) SetParticipants(participants []protocol.Participant) {
	s.participants = participants
}

// AddParticipant appends a participant, replacing any existing entry with the
// same name.
func (s *Sidebar) AddParticipant(p protocol.Participant) {
	for i, existing := range s.participants {
		if existing.Name == p.Name {
			s.participants[i] = p
			return
		}
	}
	s.participants = append(s.participants, p)
}

// RemoveParticipant removes a participant by name.
func (s *Sidebar) RemoveParticipant(name string) {
	filtered := s.participants[:0]
	for _, p := range s.participants {
		if p.Name != name {
			filtered = append(filtered, p)
		}
	}
	s.participants = filtered
}

// View renders the sidebar as a string.
func (s Sidebar) View() string {
	innerWidth := s.width - 4 // account for border + padding
	if innerWidth < 1 {
		innerWidth = 1
	}

	title := sidebarTitleStyle.Render("participants")
	lines := []string{title}

	for _, p := range s.participants {
		var nameLine string
		if p.Source == "human" || p.Role == "human" {
			nameLine = humanNameStyle.Render(p.Name)
		} else {
			nameLine = agentNameStyle.Render(p.Name)
		}

		if p.Role != "" && p.Role != "human" {
			badge := roleBadgeStyle.Render(p.Role)
			nameLine = lipgloss.JoinHorizontal(lipgloss.Top, nameLine, " ", badge)
		}
		lines = append(lines, nameLine)

		if p.AgentType != "" {
			lines = append(lines, systemMsgStyle.Render("  "+p.AgentType))
		}
		if p.Directory != "" {
			dir := p.Directory
			if len(dir) > innerWidth-2 {
				dir = "…" + dir[len(dir)-(innerWidth-3):]
			}
			lines = append(lines, timestampStyle.Render("  "+dir))
		}
		lines = append(lines, "") // blank line between participants
	}

	content := strings.Join(lines, "\n")
	return sidebarStyle.Width(s.width).Height(s.height).Render(content)
}
