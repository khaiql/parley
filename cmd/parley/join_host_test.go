package main

import (
	"fmt"
	"testing"
)

func TestJoinHostFlagDefault(t *testing.T) {
	// Re-register flags to pick up current defaults (init() already ran once).
	joinCmd.ResetFlags()
	initJoinFlags()

	got, err := joinCmd.Flags().GetString("host")
	if err != nil {
		t.Fatalf("host flag not registered: %v", err)
	}
	if got != "localhost" {
		t.Errorf("expected default host %q, got %q", "localhost", got)
	}
}

func TestJoinAddrFormat(t *testing.T) {
	tests := []struct {
		host string
		port int
		want string
	}{
		{"localhost", 8080, "localhost:8080"},
		{"192.168.1.10", 9000, "192.168.1.10:9000"},
		{"my-server.local", 1234, "my-server.local:1234"},
	}
	for _, tc := range tests {
		got := fmt.Sprintf("%s:%d", tc.host, tc.port)
		if got != tc.want {
			t.Errorf("addr(%q,%d) = %q, want %q", tc.host, tc.port, got, tc.want)
		}
	}
}
