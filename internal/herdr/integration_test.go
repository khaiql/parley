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
	binary := filepath.Join(root, "parley")
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
