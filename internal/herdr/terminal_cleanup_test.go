package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
	"github.com/khaiql/parley/internal/paths"
	runtime "github.com/khaiql/parley/internal/runtime"
)

func TestReconcileTerminalPreservesMovedTerminal(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "ev-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "herdr.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var request struct {
			Method string `json:"method"`
		}
		if json.NewDecoder(conn).Decode(&request) == nil && request.Method == "session.snapshot" {
			_, _ = conn.Write([]byte(`{"result":{"type":"session_snapshot","snapshot":{"panes":[{"pane_id":"w:p-new","terminal_id":"term_original"}]}}}` + "\n"))
		}
	}()
	h := &HostOperations{Store: NewStore(filepath.Join(dir, "plugin")), Client: &Client{LocalMachineID: "local", HerdrSocket: socket}}
	binding := Binding{RoomID: "room-a", Target: AgentTarget{MachineID: "self", HerdrSocket: socket, PaneID: "w:p-old", TerminalID: "term_original", Kind: "codex"}, SessionID: "psn_original", ParticipantName: "owned", StateDir: dir, JoinPhase: "complete", JoinRequestID: "first", Status: "online"}
	if err := h.saveBinding(binding); err != nil {
		t.Fatal(err)
	}
	if err := h.ReconcileTerminal(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := h.Store.Binding(HostBindingKey(binding.RoomID, binding.Target))
	if err != nil || got != binding {
		t.Fatalf("moved terminal binding changed: %#v, %v", got, err)
	}
}

func TestReconcileTerminalIgnoresForeignRecords(t *testing.T) {
	for _, owner := range []struct{ name, machine, socket string }{
		{"remote", "remote", ""},
		{"other-session", "local", "/tmp/foreign-herdr.sock"},
		{"self-other-session", "self", "/tmp/foreign-herdr.sock"},
	} {
		t.Run(owner.name, func(t *testing.T) {
			h, req, _ := brokerFixture(t)
			foreign := req.Target
			foreign.MachineID = owner.machine
			if owner.socket != "" {
				foreign.HerdrSocket = owner.socket
			}
			binding := Binding{RoomID: req.RoomID, Target: foreign, SessionID: "psn_foreign", ParticipantName: "foreign", StateDir: "/tmp/foreign-runtime", JoinPhase: "complete", JoinRequestID: req.ID, Status: "online"}
			room := Room{ID: req.RoomID, Host: foreign, HostSessionID: "psn_foreign"}
			if err := h.Store.saveBinding(binding); err != nil {
				t.Fatal(err)
			}
			if err := h.Store.saveRoom(room); err != nil {
				t.Fatal(err)
			}
			if err := h.ReconcileTerminal(context.Background()); err != nil {
				t.Fatalf("local cleanup inspected foreign records: %v", err)
			}
			got, err := h.Store.Binding(HostBindingKey(binding.RoomID, binding.Target))
			if err != nil || got != binding {
				t.Fatalf("foreign binding changed: %#v, %v", got, err)
			}
			gotRoom, err := h.Store.Room(room.ID)
			if err != nil || gotRoom != room {
				t.Fatalf("foreign room changed: %#v, %v", gotRoom, err)
			}
		})
	}
}

func TestReconcileTerminalLeavesOnlyGoneTerminalParticipant(t *testing.T) {
	h, req, root := brokerFixture(t)
	binding := Binding{RoomID: req.RoomID, Target: req.Target, SessionID: "psn_original", ParticipantName: "owned", StateDir: root, JoinPhase: "complete", JoinRequestID: req.ID, Status: "online"}
	originalFixture(t, root, binding, false)
	if err := h.Store.saveBinding(binding); err != nil {
		t.Fatal(err)
	}
	foreign := binding
	foreign.Target.MachineID = "remote"
	foreign.Target.NativeSession = "/native/foreign"
	foreign.ParticipantName = "foreign"
	foreign.StateDir = "/tmp/foreign-runtime"
	if err := h.Store.saveBinding(foreign); err != nil {
		t.Fatal(err)
	}
	h.Client.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		if len(args) != 3 || args[0] != "leave" || args[1] != "--session" || args[2] != binding.SessionID {
			t.Fatalf("leave args = %v", args)
		}
		store, err := runtime.ParticipantStore(paths.New(root), binding.RoomID, binding.ParticipantName)
		if err != nil {
			return nil, err
		}
		meta, err := store.LoadMeta()
		if err != nil {
			return nil, err
		}
		meta.Status = "left"
		if err = store.SaveMeta(meta); err != nil {
			return nil, err
		}
		return []byte(`{"status":"left"}`), nil
	}
	if err := h.ReconcileTerminal(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := h.Store.Binding(HostBindingKey(binding.RoomID, binding.Target))
	if err != nil || got.Status != "left" || got.CleanupError != "" {
		t.Fatalf("binding after cleanup = %#v, %v", got, err)
	}
	got, err = h.Store.Binding(HostBindingKey(foreign.RoomID, foreign.Target))
	if err != nil || got != foreign {
		t.Fatalf("foreign binding changed during local cleanup: %#v, %v", got, err)
	}
}

func TestReconcileTerminalAcceptsManualLeave(t *testing.T) {
	h, req, root := brokerFixture(t)
	binding := Binding{RoomID: req.RoomID, Target: req.Target, SessionID: "psn_original", ParticipantName: "owned", StateDir: root, JoinPhase: "complete", JoinRequestID: req.ID, Status: "online"}
	originalFixture(t, root, binding, false)
	store, _ := runtime.ParticipantStore(paths.New(root), binding.RoomID, binding.ParticipantName)
	meta, _ := store.LoadMeta()
	meta.Status = "left"
	if err := store.SaveMeta(meta); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.saveBinding(binding); err != nil {
		t.Fatal(err)
	}
	calls := 0
	h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) {
		calls++
		return nil, fmt.Errorf("adapter already stopped")
	}
	if err := h.ReconcileTerminal(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := h.Store.Binding(HostBindingKey(binding.RoomID, binding.Target))
	if err != nil || got.Status != "left" || got.CleanupError != "" || calls != 0 {
		t.Fatalf("manual leave not reconciled: %#v, calls=%d, err=%v", got, calls, err)
	}
}

func TestTerminalSnapshotFailurePreservesBindings(t *testing.T) {
	h, req, root := brokerFixture(t)
	// Use a missing socket to make this session's authoritative inventory unavailable.
	h.Client.HerdrSocket = filepath.Join(root, "missing.sock")
	req.Target.HerdrSocket = h.Client.HerdrSocket
	binding := Binding{RoomID: req.RoomID, Target: req.Target, SessionID: "psn_original", ParticipantName: "owned", StateDir: root, JoinPhase: "complete", JoinRequestID: req.ID, Status: "online"}
	originalFixture(t, root, binding, false)
	if err := h.Store.saveBinding(binding); err != nil {
		t.Fatal(err)
	}
	if err := h.ReconcileTerminal(context.Background()); err == nil {
		t.Fatal("missing snapshot accepted")
	}
	got, err := h.Store.Binding(HostBindingKey(binding.RoomID, binding.Target))
	if err != nil || got.Status != "online" {
		t.Fatalf("unverified terminal was cleaned up: %#v, %v", got, err)
	}
	metaStore, _ := runtime.ParticipantStore(paths.New(root), binding.RoomID, binding.ParticipantName)
	meta, _ := metaStore.LoadMeta()
	if meta.Status == "left" {
		t.Fatal("participant left without terminal absence proof")
	}
}

func TestReconcileTerminalStopsExactHostedRoom(t *testing.T) {
	h, req, root := brokerFixture(t)
	if err := h.Store.PinRuntimeRoot(root); err != nil {
		t.Fatal(err)
	}
	room := Room{ID: "room-a", Host: req.Target, HostSessionID: "psn_host", Source: Endpoints{Descriptor: "parley://127.0.0.1:49001/room-a", ArtifactURL: "http://127.0.0.1:49002/rooms/room-a/artifacts"}}
	if err := h.Store.saveRoom(room); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SaveSession(paths.New(root), runtime.Session{ID: room.HostSessionID, RoomID: room.ID, Name: "host"}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SaveRoomRuntime(paths.New(root), runtime.RoomRuntime{RoomID: room.ID, LocalHost: "127.0.0.1", LocalPort: 49001, ArtifactLocalPort: 49002, ArtifactPath: "/rooms/room-a/artifacts"}); err != nil {
		t.Fatal(err)
	}
	h.Client.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		if len(args) != 3 || args[0] != "stop" || args[1] != "--session" || args[2] != room.HostSessionID {
			t.Fatalf("stop args = %v", args)
		}
		log := eventlog.New(runtime.RoomEventsPath(paths.New(root), room.ID))
		_, err := log.Append(model.Event{Type: model.EventRoomStopped, RoomID: room.ID, Actor: "host"})
		return []byte(`{"status":"stopping"}`), err
	}
	if err := h.ReconcileTerminal(context.Background()); err != nil {
		t.Fatal(err)
	}
	closed, err := h.Store.Room(room.ID)
	if err != nil || !closed.Closed || closed.CleanupStatus != "complete" || closed.CleanupError != "" {
		t.Fatalf("room after cleanup = %#v, %v", closed, err)
	}
}

func TestStopRoomRejectsChangedHostIncarnation(t *testing.T) {
	h, req, root := brokerFixture(t)
	if err := h.Store.PinRuntimeRoot(root); err != nil {
		t.Fatal(err)
	}
	current := Room{ID: "room-a", Host: req.Target, HostSessionID: "psn_new", Source: Endpoints{Descriptor: "parley://127.0.0.1:49001/room-a", ArtifactURL: "http://127.0.0.1:49002/rooms/room-a/artifacts"}}
	if err := h.Store.saveRoom(current); err != nil {
		t.Fatal(err)
	}
	old := current
	old.HostSessionID = "psn_old"
	calls := 0
	h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) { calls++; return nil, nil }
	if err := h.stopRoom(context.Background(), old); err == nil || calls != 0 {
		t.Fatalf("changed room incarnation accepted: err=%v calls=%d", err, calls)
	}
}

func TestRoomCleanupRecordDoesNotOverwriteReplacement(t *testing.T) {
	h, req, _ := brokerFixture(t)
	old := Room{ID: "room-a", Host: req.Target, HostSessionID: "psn_old", Source: Endpoints{Descriptor: "parley://127.0.0.1:49001/room-a", ArtifactURL: "http://127.0.0.1:49002/rooms/room-a/artifacts"}}
	newer := old
	newer.HostSessionID = "psn_new"
	if err := h.Store.saveRoom(newer); err != nil {
		t.Fatal(err)
	}
	if err := h.recordRoomCleanup(old, "pending", "stale", false); err == nil {
		t.Fatal("stale cleanup changed replacement room")
	}
	got, err := h.Store.Room(old.ID)
	if err != nil || got.HostSessionID != newer.HostSessionID || got.CleanupError != "" {
		t.Fatalf("replacement room changed: %#v, %v", got, err)
	}
}
