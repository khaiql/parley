package main

import "testing"

func TestIsMentioned(t *testing.T) {
	tests := []struct {
		name     string
		mentions []string
		agent    string
		want     bool
	}{
		{
			name:     "mentioned",
			mentions: []string{"alice", "bob"},
			agent:    "bob",
			want:     true,
		},
		{
			name:     "not mentioned",
			mentions: []string{"alice", "charlie"},
			agent:    "bob",
			want:     false,
		},
		{
			name:     "empty mentions",
			mentions: []string{},
			agent:    "bob",
			want:     false,
		},
		{
			name:     "nil mentions",
			mentions: nil,
			agent:    "bob",
			want:     false,
		},
		{
			name:     "exact match required",
			mentions: []string{"bobby"},
			agent:    "bob",
			want:     false,
		},
		{
			name:     "single match",
			mentions: []string{"bob"},
			agent:    "bob",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMentioned(tt.mentions, tt.agent)
			if got != tt.want {
				t.Errorf("isMentioned(%v, %q) = %v, want %v", tt.mentions, tt.agent, got, tt.want)
			}
		})
	}
}
