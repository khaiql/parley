package runtime

import (
	"strings"
	"testing"

	"github.com/khaiql/parley/internal/artifact"
	"github.com/khaiql/parley/internal/paths"
)

func TestInviteFromRuntimeMetadata(t *testing.T) {
	p := paths.New(t.TempDir())
	meta := RoomRuntime{
		RoomID:    "room-1",
		LocalHost: "127.0.0.1",
		LocalPort: 49231,
	}
	if err := SaveRoomRuntime(p, meta); err != nil {
		t.Fatalf("SaveRoomRuntime: %v", err)
	}
	invite, err := Invite(p, "room-1")
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if invite.Descriptor != "parley://127.0.0.1:49231/room-1" {
		t.Fatalf("descriptor = %q", invite.Descriptor)
	}
	if invite.JoinCommandTemplate != `parley join "parley://127.0.0.1:49231/room-1" --role <participant-role>` {
		t.Fatalf("join_command_template = %q", invite.JoinCommandTemplate)
	}
	if invite.AgentInstruction != "Use your Parley skill to join this room: parley://127.0.0.1:49231/room-1" {
		t.Fatalf("agent_instruction = %q", invite.AgentInstruction)
	}
}

func TestArtifactRoomRuntimeRoundTrip(t *testing.T) {
	p := paths.New(t.TempDir())
	meta := RoomRuntime{
		RoomID:            "room-1",
		LocalHost:         "127.0.0.1",
		LocalPort:         49231,
		ArtifactLocalPort: 49232,
		ArtifactPath:      "/rooms/room-1/artifacts",
		ArtifactLimits:    artifact.DefaultLimits(),
	}
	if err := SaveRoomRuntime(p, meta); err != nil {
		t.Fatalf("SaveRoomRuntime: %v", err)
	}
	got, err := LoadRoomRuntime(p, "room-1")
	if err != nil {
		t.Fatalf("LoadRoomRuntime: %v", err)
	}
	if got.ArtifactLocalPort != 49232 || got.ArtifactPath != "/rooms/room-1/artifacts" {
		t.Fatalf("artifact endpoint = port %d path %q, want 49232 /rooms/room-1/artifacts", got.ArtifactLocalPort, got.ArtifactPath)
	}
	if got.ArtifactLimits != artifact.DefaultLimits() {
		t.Fatalf("artifact limits = %#v, want defaults", got.ArtifactLimits)
	}
}

func TestInviteIncludesArtifactEndpointMetadataAndLimits(t *testing.T) {
	p := paths.New(t.TempDir())
	meta := RoomRuntime{
		RoomID:            "room-1",
		LocalHost:         "127.0.0.1",
		LocalPort:         49231,
		ArtifactLocalPort: 49232,
		ArtifactPath:      "/rooms/room-1/artifacts",
		ArtifactLimits:    artifact.DefaultLimits(),
	}
	if err := SaveRoomRuntime(p, meta); err != nil {
		t.Fatalf("SaveRoomRuntime: %v", err)
	}
	invite, err := Invite(p, "room-1")
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if invite.ArtifactLocalPort != 49232 || invite.ArtifactPath != "/rooms/room-1/artifacts" {
		t.Fatalf("invite artifact endpoint = port %d path %q", invite.ArtifactLocalPort, invite.ArtifactPath)
	}
	if invite.ArtifactLimits != artifact.DefaultLimits() {
		t.Fatalf("invite limits = %#v, want defaults", invite.ArtifactLimits)
	}
	if !strings.Contains(invite.AgentInstruction, "room protocol port 49231") || !strings.Contains(invite.AgentInstruction, "artifact HTTP port 49232") {
		t.Fatalf("agent instruction does not mention both ports: %q", invite.AgentInstruction)
	}
}

func TestInviteQuotesIPv6DescriptorInJoinTemplate(t *testing.T) {
	p := paths.New(t.TempDir())
	meta := RoomRuntime{
		RoomID:    "room-1",
		LocalHost: "::1",
		LocalPort: 49231,
	}
	if err := SaveRoomRuntime(p, meta); err != nil {
		t.Fatalf("SaveRoomRuntime: %v", err)
	}
	invite, err := Invite(p, "room-1")
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	want := `parley join "parley://[::1]:49231/room-1" --role <participant-role>`
	if invite.JoinCommandTemplate != want {
		t.Fatalf("join_command_template = %q, want %q", invite.JoinCommandTemplate, want)
	}
}

func TestActiveParticipationRoundTrip(t *testing.T) {
	p := paths.New(t.TempDir())
	active := ActiveParticipation{RoomID: "room-1", Name: "codex"}
	if err := SaveActive(p, active); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	got, err := LoadActive(p)
	if err != nil {
		t.Fatalf("LoadActive: %v", err)
	}
	if got != active {
		t.Fatalf("active = %#v, want %#v", got, active)
	}
}

func TestSessionRoundTrip(t *testing.T) {
	p := paths.New(t.TempDir())
	id, err := NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	session := Session{ID: id, RoomID: "room-1", Name: "codex"}
	if err := SaveSession(p, session); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	got, err := LoadSession(p, id)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got != session {
		t.Fatalf("session = %#v, want %#v", got, session)
	}
}

func TestListSessionsReturnsSortedSessions(t *testing.T) {
	p := paths.New(t.TempDir())
	sessions := []Session{
		{ID: "psn_b", RoomID: "room-2", Name: "bob"},
		{ID: "psn_a", RoomID: "room-1", Name: "alice"},
	}
	for _, session := range sessions {
		if err := SaveSession(p, session); err != nil {
			t.Fatalf("SaveSession %s: %v", session.ID, err)
		}
	}

	got, err := ListSessions(p)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("sessions = %#v, want 2", got)
	}
	if got[0].ID != "psn_a" || got[1].ID != "psn_b" {
		t.Fatalf("sessions = %#v, want sorted by id", got)
	}
}
