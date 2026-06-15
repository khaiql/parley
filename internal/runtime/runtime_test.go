package runtime

import (
	"testing"

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
	if invite.JoinCommandTemplate != `parley join "parley://127.0.0.1:49231/room-1" --name <participant-name> --role <participant-role>` {
		t.Fatalf("join_command_template = %q", invite.JoinCommandTemplate)
	}
	if invite.AgentInstruction != "Use your Parley skill to join this room: parley://127.0.0.1:49231/room-1" {
		t.Fatalf("agent_instruction = %q", invite.AgentInstruction)
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
	want := `parley join "parley://[::1]:49231/room-1" --name <participant-name> --role <participant-role>`
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
