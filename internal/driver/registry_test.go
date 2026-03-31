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

func TestNewDriver_Gemini(t *testing.T) {
	d, err := NewDriver("gemini")
	if err != nil {
		t.Fatalf("NewDriver(%q) returned error: %v", "gemini", err)
	}
	if _, ok := d.(*GeminiDriver); !ok {
		t.Errorf("NewDriver(%q) returned %T, want *GeminiDriver", "gemini", d)
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

func TestNewDriver_GeminiPathHandling(t *testing.T) {
	d, err := NewDriver("/usr/local/bin/gemini")
	if err != nil {
		t.Fatalf("NewDriver(%q) returned error: %v", "/usr/local/bin/gemini", err)
	}
	if _, ok := d.(*GeminiDriver); !ok {
		t.Errorf("NewDriver(%q) returned %T, want *GeminiDriver", "/usr/local/bin/gemini", d)
	}
}

func TestNewDriver_Unknown(t *testing.T) {
	_, err := NewDriver("unknown")
	if err == nil {
		t.Error("NewDriver(\"unknown\") expected error, got nil")
	}
}
