package room

import "testing"

func TestAssignColour_ReturnsFromPalette(t *testing.T) {
	colour := AssignColour(nil)
	if colour == "" {
		t.Fatal("expected a colour, got empty string")
	}
	found := false
	for _, c := range AgentPalette {
		if c == colour {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("colour %q not in AgentPalette", colour)
	}
}

func TestAssignColour_AvoidsUsed(t *testing.T) {
	// Use all but one colour
	used := make([]string, len(AgentPalette)-1)
	copy(used, AgentPalette[:len(AgentPalette)-1])

	colour := AssignColour(used)
	if colour != AgentPalette[len(AgentPalette)-1] {
		t.Errorf("expected last palette colour %q, got %q", AgentPalette[len(AgentPalette)-1], colour)
	}
}

func TestAssignColour_FallbackWhenAllUsed(t *testing.T) {
	used := make([]string, len(AgentPalette))
	copy(used, AgentPalette)

	colour := AssignColour(used)
	if colour == "" {
		t.Fatal("expected a fallback colour, got empty string")
	}
	// Should still return something from the palette (wraps around)
	found := false
	for _, c := range AgentPalette {
		if c == colour {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("fallback colour %q not in AgentPalette", colour)
	}
}

func TestAssignColour_UniqueFor8Participants(t *testing.T) {
	var used []string
	seen := make(map[string]bool)
	for i := 0; i < len(AgentPalette); i++ {
		colour := AssignColour(used)
		if seen[colour] {
			t.Fatalf("duplicate colour %q on participant %d", colour, i)
		}
		seen[colour] = true
		used = append(used, colour)
	}
}
