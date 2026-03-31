package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/khaiql/parley/internal/protocol"
)

// Sidebar renders the participant list panel.
type Sidebar struct {
	participants []protocol.Participant
	statuses     map[string]string // per-participant status text
	width        int
	height       int
}

// NewSidebar creates an empty Sidebar.
func NewSidebar() Sidebar {
	return Sidebar{statuses: make(map[string]string)}
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

// SetParticipantStatus updates the activity status for a named participant.
// An empty status string means the participant is idle (nothing is shown).
func (s *Sidebar) SetParticipantStatus(name, status string) {
	if s.statuses == nil {
		s.statuses = make(map[string]string)
	}
	s.statuses[name] = status
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

		// Show per-participant status when non-empty.
		if status := s.statuses[p.Name]; status != "" {
			lines = append(lines, participantStatusStyle.Render("  "+status))
		}

		if p.AgentType != "" {
			lines = append(lines, systemMsgStyle.Render("  "+p.AgentType))
		}
		if p.Directory != "" {
			dir := p.Directory
			maxLen := innerWidth - 2
			if maxLen > 4 && len(dir) > maxLen {
				dir = "…" + dir[len(dir)-(maxLen-1):]
			}
			lines = append(lines, timestampStyle.Render("  "+dir))
		}
		lines = append(lines, "") // blank line between participants
	}

	content := strings.Join(lines, "\n")
	return sidebarStyle.Width(s.width).Height(s.height).Render(content)
}
