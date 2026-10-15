package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestTerminalParticipantReusesLegacyBinding(t *testing.T) {
	h, req, root := brokerFixture(t)
	legacy := Binding{RoomID: req.RoomID, Target: req.Target, SessionID: "psn_original", ParticipantName: "original", StateDir: root, Routed: Endpoints{Descriptor: "parley://127.0.0.1:49001/room-a", ArtifactURL: "http://127.0.0.1:49002/rooms/room-a/artifacts"}, JoinPhase: "complete", JoinRequestID: "legacy"}
	originalFixture(t, root, legacy, true)
	if err := h.saveBinding(legacy); err != nil {
		t.Fatal(err)
	}
	req.Target.NativeSession = ""
	h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) {
		t.Fatal("created a duplicate participant")
		return nil, nil
	}
	result, err := h.Join(context.Background(), req, legacy.Routed)
	if err != nil || result.Binding == nil || result.Binding.SessionID != legacy.SessionID {
		t.Fatal(result, err)
	}
	state, _ := h.Store.Read()
	if len(state.Bindings) != 1 {
		t.Fatal("legacy binding rewritten or duplicated", state.Bindings)
	}
}

func TestTerminalParticipantRecoversLegacyIntent(t *testing.T) {
	for _, phase := range []string{"intent", "uncertain"} {
		t.Run(phase, func(t *testing.T) {
			h, req, root := brokerFixture(t)
			legacy := Binding{RoomID: req.RoomID, Target: req.Target, ParticipantName: "legacy", StateDir: root, Routed: Endpoints{Descriptor: "parley://127.0.0.1:49001/room-a", ArtifactURL: "http://127.0.0.1:49002/rooms/room-a/artifacts"}, JoinPhase: phase, JoinRequestID: req.ID}
			if err := h.journal(req); err != nil {
				t.Fatal(err)
			}
			originalFixture(t, root, legacy, true)
			if err := h.saveBinding(legacy); err != nil {
				t.Fatal(err)
			}
			req.ID, req.Target.NativeSession = "retry", ""
			h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) {
				t.Fatal("created a duplicate participant")
				return nil, nil
			}
			result, err := h.Join(context.Background(), req, legacy.Routed)
			if err != nil || result.Binding == nil || result.Binding.SessionID != "psn_original" {
				t.Fatal(result, err)
			}
			binding, err := h.Store.Binding(HostBindingKey(legacy.RoomID, legacy.Target))
			if err != nil || binding.JoinPhase != "complete" {
				t.Fatal("legacy key was not recovered", binding, err)
			}
		})
	}
}

func TestLegacyJoinUsesTerminalTransactionLock(t *testing.T) {
	h, req, root := brokerFixture(t)
	terminal := req.Target
	terminal.NativeSession = ""
	locked, release, lockDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		lockDone <- h.Store.WithBinding(context.Background(), "join:"+HostBindingKey(req.RoomID, terminal), func() error { close(locked); <-release; return nil })
	}()
	<-locked
	var calls atomic.Int32
	h.Client.Run = func(_ context.Context, _ string, _ []string, _ []byte) ([]byte, error) {
		calls.Add(1)
		binding, _ := h.Store.Binding(HostBindingKey(req.RoomID, req.Target))
		originalFixture(t, root, binding, true)
		return []byte(`{"status":"joined","room_id":"room-a","session_id":"psn_original"}`), nil
	}
	result := make(chan Response, 1)
	go func() {
		response, _ := h.Join(context.Background(), req, Endpoints{Descriptor: "parley://127.0.0.1:49001/room-a", ArtifactURL: "http://127.0.0.1:49002/rooms/room-a/artifacts"})
		result <- response
	}()
	time.Sleep(100 * time.Millisecond)
	before := calls.Load()
	close(release)
	if err := <-lockDone; err != nil {
		t.Fatal(err)
	}
	response := <-result
	if before != 0 {
		t.Fatal("legacy request bypassed terminal lock")
	}
	if response.Binding == nil || calls.Load() != 1 {
		t.Fatal(response, calls.Load())
	}
}

func TestOldRemoteHelperRejectedBeforeClaim(t *testing.T) {
	h, req, _ := brokerFixture(t)
	key, _ := SessionKey(req.Target.HerdrSocket)
	p := Peer{Machine: Machine{ID: "remote", Enabled: true, Target: "remote", Session: "native"}, Config: SessionConfig{MachineID: "remote", ControllerID: "controller", SessionKey: key, HerdrSocket: req.Target.HerdrSocket}, Binary: "/opt/parley/bin/parley"}
	req.Target.MachineID = p.Machine.ID
	h.Client.managed.Store(p.Machine.ID, p)
	h.Client.Run = func(_ context.Context, _ string, _ []string, data []byte) ([]byte, error) {
		var rpc RPCRequest
		if err := json.Unmarshal(data, &rpc); err != nil {
			t.Fatal(err)
		}
		if rpc.Operation != "probe" {
			t.Fatal("old helper mutated", rpc.Operation)
		}
		return json.Marshal(ProbeResult{Version: 1, Config: p.Config, Agent: &req.Target})
	}
	b := Broker{Store: h.Store, Client: h.Client}
	result, err := b.Handle(context.Background(), p, req)
	if err != nil || result.Code != "agent_unavailable" {
		t.Fatal(result, err)
	}
	state, _ := h.Store.Read()
	if len(state.Requests)+len(state.Bindings) != 0 {
		t.Fatal("old helper claimed request", state)
	}
}

func TestQueuedJoinHandoffAfterPopupTimeout(t *testing.T) {
	operations, req, root := brokerFixture(t)
	skill := filepath.Join(root, "skills", "parley", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("Parley skill"), 0600); err != nil {
		t.Fatal(err)
	}
	operations.Client.ParleyBinary = filepath.Join(root, "bin", "parley")
	host := HostSession{Config: SessionConfig{HerdrSocket: operations.Client.HerdrSocket}, ConfigDir: filepath.Join(root, "config"), Store: operations.Store}
	host.Config.SessionKey, _ = SessionKey(host.Config.HerdrSocket)
	owner := host.Config
	owner.Role, owner.ControllerID, owner.MachineID = "participant", "controller", "local"
	if err := privateDir(host.ConfigDir); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(host.ConfigDir, "controller.json"), owner); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	result, err := host.SubmitForm(ctx, operations.Client, req)
	if err != nil || result.Status != "pending" {
		t.Fatal(result, err)
	}
	room := brokerRoom(t, operations, req)
	operations.Client.Run = func(_ context.Context, _ string, _ []string, _ []byte) ([]byte, error) {
		binding, err := host.Store.Binding(HostBindingKey(req.RoomID, req.Target))
		if err != nil {
			return nil, err
		}
		originalFixture(t, root, binding, true)
		return []byte(`{"status":"joined","room_id":"room-a","session_id":"psn_original"}`), nil
	}
	broker := Broker{Store: host.Store, Client: operations.Client, Tunnels: &TunnelManager{}}
	defer broker.Tunnels.Close()
	if err := broker.Drain(context.Background(), Peer{Machine: Machine{ID: owner.MachineID}, Config: owner}); err != nil {
		t.Fatal(err)
	}
	outcome, err := host.Store.readOutcome(req.ID)
	if err != nil || outcome.Phase != "complete" || outcome.Response.Handoff == nil || outcome.Response.Handoff.Status != "submitted" {
		t.Fatal(outcome, err, room)
	}
	statuses, err := host.Store.HandoffStatuses()
	if err != nil || statuses["psn_original"].Status != "submitted" {
		t.Fatal(statuses, err)
	}
}

func TestTerminalParticipantIgnoresConversationReport(t *testing.T) {
	h, req, _ := brokerFixture(t)
	req.Target.NativeSession = "unrelated-reported-conversation"
	live, _, err := h.Client.Agent(context.Background(), req.Target)
	if err != nil || live.TerminalID != req.Target.TerminalID || live.NativeSession != req.Target.NativeSession {
		t.Fatal(live, err)
	}
	req.Target.NativeSession = ""
	live, _, err = h.Client.Agent(context.Background(), req.Target)
	if err != nil || live.NativeSession != "" {
		t.Fatal(live, err)
	}
}

func TestLocalSelectionIgnoresUnrelatedPeerFailure(t *testing.T) {
	h, req, _ := brokerFixture(t)
	room := brokerRoom(t, h, req)
	h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) {
		return nil, fmt.Errorf("unrelated peer unavailable")
	}
	b := Broker{Store: h.Store, Client: h.Client}
	if err := b.CheckRoomChoice(context.Background(), req.Target, room); err != nil {
		t.Fatal("confirmed local selection blocked", err)
	}
}

func TestSelectedRoomRejectsChangedIncarnation(t *testing.T) {
	h, req, _ := brokerFixture(t)
	room := brokerRoom(t, h, req)
	selected := RoomHostReference(room, h.Client.LocalMachineID)
	room.HostSessionID = "psn_replaced"
	if err := h.Store.saveRoom(room); err != nil {
		t.Fatal(err)
	}
	b := Broker{Store: h.Store, Client: h.Client}
	if _, err := b.ResolveSelectedRoom(context.Background(), room.ID, selected); err == nil {
		t.Fatal("selected room incarnation replaced while request waited")
	}
}

func TestCoordinatorProbeDoesNotMutate(t *testing.T) {
	h, req, _ := brokerFixture(t)
	req.Op, req.RoomID = "probe", ""
	b := Broker{Store: h.Store, Client: h.Client}
	response, err := b.Handle(context.Background(), b.localPeer(), req)
	if err != nil || response.Status != "ready" {
		t.Fatal(response, err)
	}
	state, _ := h.Store.Read()
	if len(state.Requests)+len(state.Bindings)+len(state.Rooms) != 0 {
		t.Fatal("probe mutated state", state)
	}
}
