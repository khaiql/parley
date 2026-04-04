package tui

import (
	"strings"
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
