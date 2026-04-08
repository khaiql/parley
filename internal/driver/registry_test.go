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

func TestNewDriver_Rovodev(t *testing.T) {
	d, err := NewDriver("rovodev")
	if err != nil {
		t.Fatalf("NewDriver(%q) returned error: %v", "rovodev", err)
	}
	if _, ok := d.(*RovodevDriver); !ok {
		t.Errorf("NewDriver(%q) returned %T, want *RovodevDriver", "rovodev", d)
	}
}

func TestNewDriver_CaseInsensitive(t *testing.T) {
	for _, name := range []string{"Claude", "CLAUDE", "Gemini", "ROVODEV", "Rovodev"} {
		d, err := NewDriver(name)
		if err != nil {
			t.Errorf("NewDriver(%q) returned error: %v", name, err)
		}
		if d == nil {
			t.Errorf("NewDriver(%q) returned nil", name)
		}
	}
}

func TestNewDriver_Unknown(t *testing.T) {
	_, err := NewDriver("unknown")
	if err == nil {
		t.Error("NewDriver(\"unknown\") expected error, got nil")
	}
}

func TestConsumesInitialMessage(t *testing.T) {
	tests := []struct {
		agentType string
		want      bool
	}{
		{"claude", false},
		{"CLAUDE", false},
		{"gemini", true},
		{"GEMINI", true},
		{"rovodev", true},
		{"Rovodev", true},
	}
	for _, tt := range tests {
		if got := ConsumesInitialMessage(tt.agentType); got != tt.want {
			t.Errorf("ConsumesInitialMessage(%q) = %v, want %v", tt.agentType, got, tt.want)
		}
	}
}
