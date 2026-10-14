package herdr

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestIntegrationNativeFormsAndActivation(t *testing.T) {
	operations, start, root := brokerFixture(t)
	binary := filepath.Join(root, "bin", "parley")
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(root, "skills", "parley")
	if err := os.MkdirAll(skillDir, 0700); err != nil {
		t.Fatal(err)
	}
	skill, err := os.ReadFile("../../skills/parley/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), skill, 0600); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", binary, "../../cmd/parley")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	operations.Client.ParleyBinary = binary
	host := HostSession{Config: SessionConfig{HerdrSocket: operations.Client.HerdrSocket}, ConfigDir: filepath.Join(root, "config"), Store: operations.Store}
	host.Config.SessionKey, _ = SessionKey(host.Config.HerdrSocket)
	start.Op = "start"
	start.RoomID = ""
	start.Topic = "Native form before activation"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := host.SubmitForm(ctx, operations.Client, start)
	if err != nil || result.Room == nil || result.Binding == nil {
		t.Fatal(result, err)
	}
	if result.Handoff == nil || result.Handoff.Status != "submitted" {
		t.Fatal("room created without agent handoff", result.Handoff)
	}
	run := func(args ...string) []byte {
		t.Helper()
		runCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		cmd := exec.CommandContext(runCtx, binary, args...)
		cmd.Env = appendEnvironment(os.Environ(), "PARLEY_STATE_DIR", root)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s %v", args, out, err)
		}
		return out
	}
	t.Cleanup(func() { run("stop", "--session", result.Binding.SessionID) })
	room, err := host.Store.Room(result.Room.ID)
	if err != nil || room.Host.MachineID != "self" {
		t.Fatal(room, err)
	}
	state, err := host.Store.Read()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range state.Bindings {
		if b.Target.MachineID != "self" {
			t.Fatal("host alias persisted", b)
		}
	}
	// This is participant activation, without manual participant setup.
	participant := host.Config
	participant.Role = "participant"
	participant.MachineID = "remote-profile"
	participant.ControllerID = "unique-controller"
	if err = privateDir(host.ConfigDir); err != nil {
		t.Fatal(err)
	}
	if err = writeJSON(filepath.Join(host.ConfigDir, "controller.json"), participant); err != nil {
		t.Fatal(err)
	}
	operations.Client.LocalMachineID = participant.MachineID
	replay, err := host.SubmitForm(ctx, operations.Client, start)
	if err != nil || replay.Binding == nil || replay.Binding.SessionID != result.Binding.SessionID {
		t.Fatal(replay, err)
	}
	raw, err := host.Dispatch(ctx, RPCRequest{Operation: "room", Session: "native", SessionKey: participant.SessionKey, ControllerID: participant.ControllerID, RoomID: room.ID})
	if err != nil || raw.(Room).Host.MachineID != participant.MachineID {
		t.Fatal(raw, err)
	}
	join := start
	join.ID = "original-host-join"
	join.Op = "join"
	join.Topic = ""
	join.RoomID = room.ID
	joined, err := host.SubmitForm(ctx, operations.Client, join)
	if err != nil || joined.Binding == nil || joined.Binding.SessionID != result.Binding.SessionID {
		t.Fatal(joined, err)
	}
	if joined.Handoff == nil || joined.Handoff.Status != "submitted" {
		t.Fatal("Join missed handoff receipt", joined.Handoff)
	}
	outcome, err := host.Store.readOutcome(join.ID)
	if err != nil || outcome.Phase != "complete" {
		t.Fatal(outcome, err)
	}
	// Completed original delivery survives changed controller ownership and lost form output.
	participant.ControllerID = "later-controller"
	if err = writeJSON(filepath.Join(host.ConfigDir, "controller.json"), participant); err != nil {
		t.Fatal(err)
	}
	again, err := host.SubmitForm(ctx, operations.Client, join)
	if err != nil || again.Binding == nil || again.Binding.SessionID != joined.Binding.SessionID {
		t.Fatal(again, err)
	}
	incomplete := join
	incomplete.ID = "incomplete-old-owner"
	if err = host.Store.Enqueue(incomplete); err != nil {
		t.Fatal(err)
	}
	claimed, err := host.Store.Claim(incomplete.ID, hostDigest(incomplete), "previous-controller")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = host.SubmitForm(ctx, operations.Client, incomplete); err == nil {
		t.Fatal("incomplete ownership was stolen")
	}
	retained, err := host.Store.readOutcome(incomplete.ID)
	if err != nil || !reflect.DeepEqual(retained, claimed) {
		t.Fatal(retained, err)
	}
	sessions := run("sessions")
	var decoded any
	if err = json.Unmarshal(sessions, &decoded); err != nil {
		t.Fatal(err)
	}
	run("send", "native form message", "--session", result.Binding.SessionID)
}

func TestIntegrationEmptyRemoteRequestArray(t *testing.T) {
	c := &Client{LocalMachineID: "local", Run: func(context.Context, string, []string, []byte) ([]byte, error) { return []byte("[]"), nil }}
	broker := Broker{Store: NewStore(t.TempDir()), Client: c}
	peer := Peer{Machine: Machine{ID: "remote", Target: "fixture", Session: "native", Enabled: true}, Binary: "/installed/bin/parley", Config: SessionConfig{ControllerID: "controller", HerdrSocket: "/tmp/native.sock"}}
	peer.Config.SessionKey, _ = SessionKey(peer.Config.HerdrSocket)
	if err := broker.Drain(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationRemoteQueueRejectsWrongShape(t *testing.T) {
	for _, body := range []string{"null", "{}", "\"requests\""} {
		client := &Client{LocalMachineID: "local", Run: func(context.Context, string, []string, []byte) ([]byte, error) { return []byte(body), nil }}
		broker := Broker{Store: NewStore(t.TempDir()), Client: client}
		peer := Peer{Machine: Machine{ID: "remote", Target: "fixture", Session: "native", Enabled: true}, Binary: "/installed/bin/parley", Config: SessionConfig{ControllerID: "controller", HerdrSocket: "/tmp/native.sock"}}
		peer.Config.SessionKey, _ = SessionKey(peer.Config.HerdrSocket)
		if err := broker.Drain(context.Background(), peer); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestDirectBrokerSharedClaimOrders(t *testing.T) {
	for _, first := range []string{"direct", "broker"} {
		t.Run(first, func(t *testing.T) {
			operations, req, root := brokerFixture(t)
			room := brokerRoom(t, operations, req)
			host := HostSession{Config: SessionConfig{HerdrSocket: operations.Client.HerdrSocket}, ConfigDir: filepath.Join(root, "config"), Store: operations.Store}
			host.Config.SessionKey, _ = SessionKey(host.Config.HerdrSocket)
			owner, err := host.Setup(context.Background(), operations.Client)
			if err != nil {
				t.Fatal(err)
			}
			peer := Peer{Machine: Machine{ID: owner.MachineID}, Config: owner}
			broker := Broker{Store: host.Store, Client: operations.Client, Tunnels: &TunnelManager{}}
			defer broker.Tunnels.Close()
			calls := 0
			operations.Client.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
				if args[0] != "join" {
					t.Fatalf("unexpected %v", args)
				}
				calls++
				binding, err := host.Store.Binding(HostBindingKey(req.RoomID, req.Target))
				if err != nil {
					t.Fatal(err)
				}
				originalFixture(t, root, binding, true)
				return []byte(`{"status":"joined","room_id":"room-a","session_id":"psn_original"}`), nil
			}
			if err = host.Store.Enqueue(req); err != nil {
				t.Fatal(err)
			}
			normalized, err := normalize(req, peer)
			if err != nil {
				t.Fatal(err)
			}
			if first == "direct" {
				if _, err = host.Store.Claim(req.ID, hostDigest(normalized), owner.ControllerID); err != nil {
					t.Fatal(err)
				}
				if err = broker.Drain(context.Background(), peer); err != nil {
					t.Fatal(err)
				}
				saved, err := host.Store.readOutcome(req.ID)
				if err != nil || saved.Phase != "complete" {
					t.Fatal("direct claim stranded by drain", saved, err)
				}
			} else {
				locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
				go func() {
					done <- host.Store.WithBinding(context.Background(), "join:"+terminalBindingKey(req.RoomID, req.Target), func() error { close(locked); <-release; return nil })
				}()
				<-locked
				attempt, stop := context.WithCancel(context.Background())
				handled := make(chan error, 1)
				go func() { _, e := broker.Handle(attempt, peer, req); handled <- e }()
				deadline := time.Now().Add(time.Second)
				for {
					if _, e := host.Store.readOutcome(req.ID); e == nil {
						break
					}
					if time.Now().After(deadline) {
						stop()
						close(release)
						<-done
						t.Fatal("broker did not claim")
					}
					time.Sleep(time.Millisecond)
				}
				stop()
				<-handled
				close(release)
				if err = <-done; err != nil {
					t.Fatal(err)
				}
			}
			response, err := host.SubmitForm(context.Background(), operations.Client, req)
			if err != nil || response.Binding == nil || response.Binding.SessionID != "psn_original" {
				t.Fatal(response, err)
			}
			replay, err := broker.Handle(context.Background(), peer, req)
			if err != nil || replay.Binding == nil || replay.Binding.SessionID != "psn_original" || calls != 1 {
				t.Fatal(replay, err, calls)
			}
			outcome, err := host.Store.readOutcome(req.ID)
			if err != nil || outcome.Phase != "complete" {
				t.Fatal(outcome, err, room)
			}
		})
	}
}
