package driver

import (
	"testing"
)

func TestNewDriver_Claude(t *testing.T) {
	d, err := NewDriver("claude")
	if err != nil {
		t.Fatalf("NewDriver(%q) returned error: %v", "claude", err)
	}
	if _, ok := d.(*ClaudeDriver); !ok {
		t.Errorf("NewDriver(%q) returned %T, want *ClaudeDriver", "claude", d)
	}
}

func TestNewDriver_GeminiNotYetSupported(t *testing.T) {
	// Gemini driver is not yet implemented; NewDriver should return an error.
	_, err := NewDriver("gemini")
	if err == nil {
		t.Error("NewDriver(\"gemini\") expected error (not yet implemented), got nil")
	}
}

func TestNewDriver_PathHandling(t *testing.T) {
	d, err := NewDriver("/usr/bin/claude")
	if err != nil {
		t.Fatalf("NewDriver(%q) returned error: %v", "/usr/bin/claude", err)
	}
	if _, ok := d.(*ClaudeDriver); !ok {
		t.Errorf("NewDriver(%q) returned %T, want *ClaudeDriver", "/usr/bin/claude", d)
	}
}

func TestNewDriver_Unknown(t *testing.T) {
	_, err := NewDriver("unknown")
	if err == nil {
		t.Error("NewDriver(\"unknown\") expected error, got nil")
	}
}
