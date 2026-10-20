package herdr

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/khaiql/parley/internal/paths"
	runtime "github.com/khaiql/parley/internal/runtime"
)

func TestReadFormCancelAndInput(t *testing.T) {
	for _, input := range []string{"\x1b", "", "\n", strings.Repeat("x", 513) + "\n", "bad\x00\n", "\xff\n"} {
		if _, err := ReadForm(strings.NewReader(input), "start"); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	for _, input := range []string{"room-1\n", "room-X\b1\r", "room-X\x7f1\n"} {
		got, err := ReadForm(strings.NewReader(input), "join")
		if err != nil || got != "room-1" {
			t.Fatalf("%q %v", got, err)
		}
	}
	got, err := ReadForm(strings.NewReader("café\x7f!\n"), "start")
	if err != nil || got != "caf!" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := ReadForm(strings.NewReader("bad/id\n"), "join"); err == nil {
		t.Fatal("unsafe id accepted")
	}
}

func TestPopupTarget(t *testing.T) {
	socket := "/tmp/original.sock"
	target := `{"machine_id":"self","herdr_socket":"/tmp/original.sock","workspace_id":"w1","pane_id":"w1:p2","terminal_id":"term_original","kind":"codex","native_session":"original"}`
	got, err := DecodeTarget(target, socket)
	if err != nil || got.TerminalID != "term_original" || got.PaneID != "w1:p2" {
		t.Fatalf("%+v %v", got, err)
	}
	for _, input := range []string{strings.Replace(target, "original.sock", "other.sock", 1), strings.Replace(target, `"machine_id"`, `"root":"/tmp/evil","machine_id"`, 1), target + target} {
		if _, err := DecodeTarget(input, socket); err == nil {
			t.Fatal("untrusted target accepted")
		}
	}
}

func TestDirectFormUnavailableRoomCompletesClaim(t *testing.T) {
	operations, req, _ := brokerFixture(t)
	if err := operations.Store.Enqueue(req); err != nil {
		t.Fatal(err)
	}
	host := HostSession{Store: operations.Store}
	owner := SessionConfig{MachineID: "local", SessionKey: "session"}
	response, err := host.directForm(context.Background(), *operations, owner, req, Room{ID: req.RoomID, Closed: true})
	if err == nil {
		t.Fatal("expected closed-room error")
	}
	outcome, outcomeErr := operations.Store.readOutcome(req.ID)
	if outcomeErr != nil || outcome.Phase != "complete" || outcome.Response.Code != "room_closed" || response.Code != "room_closed" {
		t.Fatalf("response=%+v outcome=%+v err=%v", response, outcome, outcomeErr)
	}
	pending, err := operations.Store.Pending()
	if err != nil || len(pending) != 0 {
		t.Fatalf("completed failure retained pending request: %+v, %v", pending, err)
	}
	owner.ControllerID = "activated-controller"
	response, err = host.directForm(context.Background(), *operations, owner, req, Room{})
	if err != nil || response.Code != "room_closed" {
		t.Fatalf("completed failure cannot replay after activation: %+v, %v", response, err)
	}
}

func TestDirectFormJoinErrorRetainsOriginalParticipant(t *testing.T) {
	operations, req, root := brokerFixture(t)
	room := brokerRoom(t, operations, req)
	if err := operations.Store.Enqueue(req); err != nil {
		t.Fatal(err)
	}
	calls := 0
	operations.Client.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		if args[0] != "join" {
			return nil, fmt.Errorf("unexpected command %v", args)
		}
		calls++
		binding, err := operations.Store.Binding(HostBindingKey(req.RoomID, req.Target))
		if err != nil {
			t.Fatal(err)
		}
		originalFixture(t, root, binding, true)
		return []byte(`{"status":"joined","room_id":"room-a","session_id":"invalid/session"}`), nil
	}
	host := HostSession{Store: operations.Store}
	owner := SessionConfig{MachineID: "local", ControllerID: "controller"}
	response, err := host.directForm(context.Background(), *operations, owner, req, room)
	if err == nil || response.Code != "operation_uncertain" {
		t.Fatalf("mutation error lost uncertainty: %+v, %v", response, err)
	}
	outcome, err := operations.Store.readOutcome(req.ID)
	if err != nil || outcome.Phase != "uncertain" || outcome.Response.Code != "operation_uncertain" {
		t.Fatalf("mutation error left no durable uncertainty: %+v, %v", outcome, err)
	}
	closed := room
	closed.Closed = true
	_, _ = host.directForm(context.Background(), *operations, owner, req, closed)
	outcome, err = operations.Store.readOutcome(req.ID)
	if err != nil || outcome.Phase != "uncertain" || outcome.Response.Code != "operation_uncertain" {
		t.Fatalf("room failure discarded mutation uncertainty: %+v, %v", outcome, err)
	}
	response, err = host.directForm(context.Background(), *operations, owner, req, room)
	if err != nil || response.Binding == nil || response.Binding.SessionID != "psn_original" || calls != 1 {
		t.Fatalf("original participant not recovered: %+v, %v, joins=%d", response, err, calls)
	}
	store, err := runtime.ParticipantStore(paths.New(root), req.RoomID, response.Binding.ParticipantName)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.LoadMeta()
	if err != nil || meta.LastSeenSeq != 17 {
		t.Fatalf("participant cursor changed: %+v, %v", meta, err)
	}
}
