package tui

import "testing"

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
