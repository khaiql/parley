package tui

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/khaiql/parley/internal/command"
)

// Modal is a dismissable overlay component that renders a titled, scrollable
// content box centered within the terminal. Dismiss with Esc or q.
type Modal struct {
	content    *command.ModalContent
	vp         viewport.Model
	termWidth  int
	termHeight int
}

// NewModal creates a Modal sized to fit within the given terminal dimensions.
func NewModal(content *command.ModalContent, termWidth, termHeight int) Modal {
	m := Modal{content: content}
	m.applySize(termWidth, termHeight)
	return m
}

// Resize recalculates the modal dimensions for a new terminal size.
func (m *Modal) Resize(termWidth, termHeight int) {
	m.applySize(termWidth, termHeight)
}

// applySize computes box and viewport dimensions from terminal size and content hints.
func (m *Modal) applySize(termWidth, termHeight int) {
	m.termWidth = termWidth
	m.termHeight = termHeight

	boxW := termWidth * 4 / 5
	boxH := termHeight * 3 / 4
	if m.content.Width > 0 {
		boxW = m.content.Width
	}
	if m.content.Height > 0 {
		boxH = m.content.Height
	}
	// Clamp to terminal size.
	if boxW > termWidth {
		boxW = termWidth
	}
	if boxH > termHeight {
		boxH = termHeight
	}

	// Inner viewport: subtract border (2) + horizontal padding (2) for width;
	// subtract border (2) + title (1) + margin (1) + footer (1) + margin (1) for height.
	vpW := boxW - 4
	vpH := boxH - 6
	if vpW < 10 {
		vpW = 10
	}
	if vpH < 1 {
		vpH = 1
	}

	m.vp = viewport.New(vpW, vpH)
	m.vp.SetContent(m.content.Body)
}

// Update forwards scroll key events to the inner viewport.
// Dismiss keys (Esc, q) are handled by App, not here.
func (m *Modal) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return cmd
}

// View renders the modal box centered within the terminal.
func (m Modal) View() string {
	vpW := m.vp.Width

	title := modalTitleStyle.Render(m.content.Title)
	body := m.vp.View()
	footer := modalFooterStyle.Render("esc · q  close")

	inner := lipgloss.JoinVertical(lipgloss.Left, title, body, footer)
	box := modalStyle.Width(vpW).Render(inner)

	return lipgloss.Place(
		m.termWidth, m.termHeight,
		lipgloss.Center, lipgloss.Center,
		box,
	)
}
