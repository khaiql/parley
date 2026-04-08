package main

import (
	"strings"
	"testing"
)

func TestRandomName_AdjNounFormat(t *testing.T) {
	name := randomName()
	parts := strings.SplitN(name, "-", 2)
	if len(parts) != 2 {
		t.Fatalf("expected adjective-noun format, got %q", name)
	}
	if parts[0] == "" || parts[1] == "" {
		t.Fatalf("expected non-empty adjective and noun, got %q", name)
	}
}

func TestRandomName_Uniqueness(t *testing.T) {
	// Generate 50 names and expect good variety. With 500 combinations the
	// birthday paradox makes a zero-collision requirement flaky, so we assert
	// a lower bound on unique names instead.
	seen := make(map[string]bool)
	const iterations = 50
	for i := 0; i < iterations; i++ {
		seen[randomName()] = true
	}
	if len(seen) < 30 {
		t.Errorf("expected at least 30 unique names over %d calls, got %d", iterations, len(seen))
	}
}
