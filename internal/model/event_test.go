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

func TestMessagePayloadArtifactsPreserveOrder(t *testing.T) {
	payload := MessagePayload{
		Text: "compare these",
		Artifacts: []ArtifactMetadata{
			{ID: "art_before", Name: "before.log", Size: 3, SHA256: "abc"},
			{ID: "art_after", Name: "after.log", Size: 5, SHA256: "def"},
		},
	}

	if len(payload.Artifacts) != 2 {
		t.Fatalf("artifacts = %#v, want two entries", payload.Artifacts)
	}
	if payload.Artifacts[0].ID != "art_before" || payload.Artifacts[1].ID != "art_after" {
		t.Fatalf("artifact order = %#v, want CLI order", payload.Artifacts)
	}
}
