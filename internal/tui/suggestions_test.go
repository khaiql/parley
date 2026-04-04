package tui

import (
	"fmt"
	"strings"
	"testing"
)

func TestSuggestions_SetItems_ReplacesAll(t *testing.T) {
	s := NewSuggestions(80)
	s.SetItems([]SuggestionItem{
		{Label: "/info", Description: "Room info"},
		{Label: "/save", Description: "Save state"},
	})

	if len(s.filtered) != 2 {
		t.Fatalf("expected 2 filtered items, got %d", len(s.filtered))
	}
	if !s.Visible() {
		t.Error("expected suggestions to be visible after SetItems")
	}
}

func TestSuggestions_Filter_PrefixMatch(t *testing.T) {
	s := NewSuggestions(80)
	s.SetItems([]SuggestionItem{
		{Label: "/info", Description: "Room info"},
		{Label: "/save", Description: "Save state"},
		{Label: "/send_command", Description: "Send to agent"},
	})
	s.Filter("sa")

	if len(s.filtered) != 1 {
		t.Fatalf("expected 1 match for 'sa', got %d", len(s.filtered))
	}
	if s.filtered[0].Label != "/save" {
		t.Errorf("expected /save, got %s", s.filtered[0].Label)
	}
}

func TestSuggestions_Filter_CaseInsensitive(t *testing.T) {
	s := NewSuggestions(80)
	s.SetItems([]SuggestionItem{
		{Label: "@Claude", Description: "agent"},
	})
	s.Filter("cl")

	if len(s.filtered) != 1 {
		t.Fatalf("expected 1 match for 'cl', got %d", len(s.filtered))
	}
}

func TestSuggestions_Filter_EmptyQuery_ShowsAll(t *testing.T) {
	s := NewSuggestions(80)
	s.SetItems([]SuggestionItem{
		{Label: "/info", Description: "Room info"},
		{Label: "/save", Description: "Save state"},
	})
	s.Filter("")

	if len(s.filtered) != 2 {
		t.Fatalf("expected 2 items with empty query, got %d", len(s.filtered))
	}
}

func TestSuggestions_Filter_NoMatch_Hides(t *testing.T) {
	s := NewSuggestions(80)
	s.SetItems([]SuggestionItem{
		{Label: "/info", Description: "Room info"},
	})
	s.Filter("xyz")

	if len(s.filtered) != 0 {
		t.Fatalf("expected 0 matches for 'xyz', got %d", len(s.filtered))
	}
	if s.Visible() {
		t.Error("expected suggestions to hide when no matches")
	}
}

func TestSuggestions_MoveDown_Wraps(t *testing.T) {
	s := NewSuggestions(80)
	s.SetItems([]SuggestionItem{
		{Label: "/a", Description: "A"},
		{Label: "/b", Description: "B"},
		{Label: "/c", Description: "C"},
	})

	s.MoveDown()
	if s.cursor != 1 {
		t.Errorf("expected cursor 1 after MoveDown, got %d", s.cursor)
	}
	s.MoveDown()
	s.MoveDown() // wraps
	if s.cursor != 0 {
		t.Errorf("expected cursor 0 after wrap, got %d", s.cursor)
	}
}

func TestSuggestions_MoveUp_Wraps(t *testing.T) {
	s := NewSuggestions(80)
	s.SetItems([]SuggestionItem{
		{Label: "/a", Description: "A"},
		{Label: "/b", Description: "B"},
	})

	s.MoveUp() // wraps to end
	if s.cursor != 1 {
		t.Errorf("expected cursor 1 after MoveUp wrap, got %d", s.cursor)
	}
}

func TestSuggestions_Selected_ReturnsCursorItem(t *testing.T) {
	s := NewSuggestions(80)
	s.SetItems([]SuggestionItem{
		{Label: "/info", Description: "Info"},
		{Label: "/save", Description: "Save"},
	})
	s.MoveDown()

	sel := s.Selected()
	if sel.Label != "/save" {
		t.Errorf("expected /save, got %s", sel.Label)
	}
}

func TestSuggestions_View_ContainsLabels(t *testing.T) {
	s := NewSuggestions(60)
	s.SetItems([]SuggestionItem{
		{Label: "/info", Description: "Room info"},
		{Label: "/save", Description: "Save state"},
	})

	view := stripANSI(s.View())
	if !strings.Contains(view, "/info") {
		t.Errorf("view should contain /info, got:\n%s", view)
	}
	if !strings.Contains(view, "/save") {
		t.Errorf("view should contain /save, got:\n%s", view)
	}
	if !strings.Contains(view, "Room info") {
		t.Errorf("view should contain description, got:\n%s", view)
	}
}

func TestSuggestions_View_MaxVisible(t *testing.T) {
	s := NewSuggestions(60)
	items := make([]SuggestionItem, 8)
	for i := range items {
		items[i] = SuggestionItem{Label: fmt.Sprintf("/cmd%d", i), Description: "desc"}
	}
	s.SetItems(items)

	view := stripANSI(s.View())
	// Should show at most 5 items.
	count := 0
	for i := 0; i < 8; i++ {
		if strings.Contains(view, fmt.Sprintf("/cmd%d", i)) {
			count++
		}
	}
	if count > maxSuggestionItems {
		t.Errorf("expected at most %d visible items, got %d", maxSuggestionItems, count)
	}
}

func TestSuggestions_View_Hidden_Empty(t *testing.T) {
	s := NewSuggestions(60)
	// Not visible by default.
	if s.View() != "" {
		t.Errorf("expected empty view when not visible, got: %q", s.View())
	}
}

func TestSuggestions_Height(t *testing.T) {
	s := NewSuggestions(60)
	if s.Height() != 0 {
		t.Errorf("expected height 0 when not visible, got %d", s.Height())
	}

	s.SetItems([]SuggestionItem{
		{Label: "/a", Description: "A"},
		{Label: "/b", Description: "B"},
	})
	// 2 items + 2 border lines
	if s.Height() != 4 {
		t.Errorf("expected height 4, got %d", s.Height())
	}
}
