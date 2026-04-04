package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const maxSuggestionItems = 5

// SuggestionItem is a single autocomplete option.
type SuggestionItem struct {
	Label       string // displayed and inserted text (e.g., "/save", "@claude")
	Description string // shown next to label (e.g., "Save room state")
}

// Suggestions renders a filtered list of autocomplete options.
type Suggestions struct {
	items    []SuggestionItem
	filtered []SuggestionItem
	query    string
	cursor   int
	visible  bool
	width    int
}

// NewSuggestions creates a Suggestions component with the given width.
func NewSuggestions(width int) Suggestions {
	return Suggestions{width: width}
}

// SetItems replaces the full item list, resets filter and cursor, and shows the list.
func (s *Suggestions) SetItems(items []SuggestionItem) {
	s.items = items
	s.query = ""
	s.cursor = 0
	s.filtered = make([]SuggestionItem, len(items))
	copy(s.filtered, items)
	s.visible = len(s.filtered) > 0
}

// Filter narrows the list by case-insensitive prefix match on Label.
// The prefix is the part of the label after the trigger character.
// For example, filtering "/save" with query "sa" matches because
// we strip the first character (trigger) before comparing.
func (s *Suggestions) Filter(query string) {
	s.query = query
	s.filtered = s.filtered[:0]
	q := strings.ToLower(query)
	for _, item := range s.items {
		// Strip the trigger character (first char) from label for matching.
		label := item.Label
		if len(label) > 1 {
			label = label[1:]
		}
		if strings.HasPrefix(strings.ToLower(label), q) {
			s.filtered = append(s.filtered, item)
		}
	}
	s.cursor = 0
	s.visible = len(s.filtered) > 0
}

// Visible reports whether the suggestion list is showing.
func (s Suggestions) Visible() bool {
	return s.visible
}

// Hide closes the suggestion list.
func (s *Suggestions) Hide() {
	s.visible = false
}

// Selected returns the item at the cursor position.
func (s Suggestions) Selected() SuggestionItem {
	if len(s.filtered) == 0 {
		return SuggestionItem{}
	}
	return s.filtered[s.cursor]
}

// SetWidth updates the rendering width.
func (s *Suggestions) SetWidth(width int) {
	s.width = width
}

// MoveDown advances the cursor, wrapping at the end.
func (s *Suggestions) MoveDown() {
	if len(s.filtered) == 0 {
		return
	}
	s.cursor = (s.cursor + 1) % len(s.filtered)
}

// MoveUp moves the cursor back, wrapping to the end.
func (s *Suggestions) MoveUp() {
	if len(s.filtered) == 0 {
		return
	}
	s.cursor = (s.cursor - 1 + len(s.filtered)) % len(s.filtered)
}

// Height returns the total rendered height (0 when hidden).
func (s Suggestions) Height() int {
	if !s.visible || len(s.filtered) == 0 {
		return 0
	}
	n := len(s.filtered)
	if n > maxSuggestionItems {
		n = maxSuggestionItems
	}
	return n + 2 // items + top/bottom border
}

// View renders the suggestion list.
func (s Suggestions) View() string {
	if !s.visible || len(s.filtered) == 0 {
		return ""
	}

	// Determine the visible window of items.
	n := len(s.filtered)
	start := 0
	visible := n
	if visible > maxSuggestionItems {
		visible = maxSuggestionItems
		// Scroll so the cursor is always visible.
		if s.cursor >= start+visible {
			start = s.cursor - visible + 1
		}
		if s.cursor < start {
			start = s.cursor
		}
	}

	labelStyle := lipgloss.NewStyle().Foreground(colorText).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(colorDimText)
	selectedStyle := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)

	var rows []string
	for i := start; i < start+visible && i < n; i++ {
		item := s.filtered[i]
		if i == s.cursor {
			row := selectedStyle.Render(item.Label + "  " + item.Description)
			rows = append(rows, row)
		} else {
			row := labelStyle.Render(item.Label) + "  " + descStyle.Render(item.Description)
			rows = append(rows, row)
		}
	}

	content := strings.Join(rows, "\n")

	boxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(colorBorder).
		Padding(0, 1).
		Width(s.width)

	return boxStyle.Render(content)
}
