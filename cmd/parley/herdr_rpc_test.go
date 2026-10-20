package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/herdr"
	"github.com/khaiql/parley/internal/model"
	"github.com/khaiql/parley/internal/paths"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
)

func TestHerdrRPCUsesHerdrBinaryOverride(t *testing.T) {
	nativeActionFixture(t, "success")
	t.Setenv("PATH", t.TempDir())
	for _, tc := range []struct{ name, request, code string }{
		{"probe", `{"operation":"probe","session":"remote-agents"}`, ""},
		{"malformed", `{bad}`, "helper_startup_failed"},
		{"missing-session", `{"operation":"probe","session":"missing"}`, "helper_transport_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newRootCmd()
			cmd.SetArgs([]string{"herdr", "rpc"})
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetIn(strings.NewReader(tc.request))
			err := cmd.Execute()
			var response struct {
				Version int                   `json:"version"`
				Error   struct{ Code string } `json:"error"`
			}
			if decodeErr := json.Unmarshal(out.Bytes(), &response); decodeErr != nil {
				t.Fatalf("response = %s, error = %v", &out, decodeErr)
			}
			if response.Error.Code != tc.code || (err != nil) != (tc.code != "") {
				t.Fatalf("response = %s, error = %v", &out, err)
			}
			if tc.code == "" && response.Version != herdr.HelperVersion {
				t.Fatalf("probe did not reach helper: %s", &out)
			}
		})
	}
}

func TestHerdrRPCCleanupLeavesParticipantAndStopsRoom(t *testing.T) {
	nativeActionFixture(t, "success")
	root := filepath.Dir(os.Getenv("HERDR_BIN_PATH"))
	binary := filepath.Join(root, "parley")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	runtimeRoot := os.Getenv("PARLEY_STATE_DIR")
	run := func(input []byte, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Stdin = bytes.NewReader(input)
		return cmd.CombinedOutput()
	}
	var started struct {
		RoomID     string `json:"room_id"`
		SessionID  string `json:"session_id"`
		Name       string
		Descriptor string
	}
	out, err := run(nil, "start", "--topic", "RPC cleanup", "--name", "owned")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}
	if err = json.Unmarshal(out, &started); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd := exec.Command(binary, "stop", "--session", started.SessionID)
		cmd.Env = append(os.Environ(), "PARLEY_STATE_DIR="+runtimeRoot)
		_ = cmd.Run()
	})
	p := paths.New(runtimeRoot)
	roomRuntime, err := parleyRuntime.LoadRoomRuntime(p, started.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	socket := os.Getenv("HERDR_SOCKET_PATH")
	configDir, stateDir, err := herdr.SessionDirs(filepath.Join(root, "config"), filepath.Join(root, "state", "herdr", "plugins", "parley"), socket)
	if err != nil {
		t.Fatal(err)
	}
	store := herdr.NewStore(stateDir)
	if err = store.PinRuntimeRoot(runtimeRoot); err != nil {
		t.Fatal(err)
	}
	target := herdr.AgentTarget{MachineID: "self", HerdrSocket: socket, TerminalID: "term_original", Kind: "codex"}
	binding := herdr.Binding{RoomID: started.RoomID, Target: target, SessionID: started.SessionID, ParticipantName: started.Name, StateDir: runtimeRoot, JoinPhase: "complete", JoinRequestID: "cleanup", Status: "online"}
	if err = store.Update(func(state *herdr.State) error {
		state.Bindings[herdr.HostBindingKey(binding.RoomID, target)] = binding
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	room := herdr.Room{ID: started.RoomID, Host: target, HostSessionID: started.SessionID, Source: herdr.Endpoints{Descriptor: started.Descriptor, ArtifactURL: fmt.Sprintf("http://%s:%d%s", roomRuntime.LocalHost, roomRuntime.ArtifactLocalPort, roomRuntime.ArtifactPath)}}
	key, err := herdr.SessionKey(socket)
	if err != nil {
		t.Fatal(err)
	}
	owner := herdr.SessionConfig{Role: "participant", ControllerID: "local", MachineID: "peer", HerdrSocket: socket, SessionKey: key}
	writeRecord := func(path string, record any) {
		t.Helper()
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeRecord(filepath.Join(configDir, "controller.json"), owner)
	// Cleanup must use the host's pinned runtime even if the SSH environment differs.
	t.Setenv("PARLEY_STATE_DIR", filepath.Join(root, "unrelated-runtime"))
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	request, err := json.Marshal(herdr.RPCRequest{Operation: "cleanup-terminals", Session: "remote-agents", SessionKey: key, ControllerID: owner.ControllerID})
	if err != nil {
		t.Fatal(err)
	}
	if out, err = run(request, "herdr", "rpc"); err != nil {
		t.Fatalf("cleanup RPC: %v\n%s", err, out)
	}
	participant, err := parleyRuntime.ParticipantStore(p, started.RoomID, started.Name)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := participant.LoadMeta()
	if err != nil || meta.Status != "left" {
		t.Fatalf("participant after cleanup = %+v, %v", meta, err)
	}
	saved, err := store.Binding(herdr.HostBindingKey(binding.RoomID, target))
	if err != nil || saved.Status != "left" || saved.CleanupStatus != "complete" {
		t.Fatalf("binding after cleanup = %+v, %v", saved, err)
	}
	writeRecord(filepath.Join(stateDir, "rooms", room.ID+".json"), room)
	if out, err = run(request, "herdr", "rpc"); err != nil {
		t.Fatalf("room cleanup RPC: %v\n%s", err, out)
	}
	closed, err := store.Room(room.ID)
	if err != nil || !closed.Closed || closed.CleanupStatus != "complete" {
		t.Fatalf("room after cleanup = %+v, %v", closed, err)
	}
	events, err := eventlog.New(parleyRuntime.RoomEventsPath(p, room.ID)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == model.EventRoomStopped {
			return
		}
	}
	t.Fatal("cleanup did not stop the real room")
}
