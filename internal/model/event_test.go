package model

import "testing"

func TestEventIsTranscript(t *testing.T) {
	tests := []struct {
		typ  EventType
		want bool
	}{
		{EventRoomStarted, true},
		{EventRoomStopped, true},
		{EventParticipantJoined, true},
		{EventParticipantLeft, true},
		{EventMessage, true},
		{EventType("unknown"), false},
	}
	for _, tt := range tests {
		if got := tt.typ.IsTranscript(); got != tt.want {
			t.Errorf("%s IsTranscript = %v, want %v", tt.typ, got, tt.want)
		}
	}
}

func TestMessagePayloadMentions(t *testing.T) {
	payload := MessagePayload{Text: "@codex please inspect", Mentions: []string{"codex"}}
	if payload.Text == "" {
		t.Fatal("expected text to be stored")
	}
	if len(payload.Mentions) != 1 || payload.Mentions[0] != "codex" {
		t.Fatalf("mentions = %#v, want [codex]", payload.Mentions)
	}
}
