package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testHost(t *testing.T) HostSession {
	t.Helper()
	socket := "/tmp/test-herdr.sock"
	key, _ := SessionKey(socket)
	root := t.TempDir()
	return HostSession{Config: SessionConfig{HerdrSocket: socket, SessionKey: key}, ConfigDir: filepath.Join(root, "config"), Store: NewStore(filepath.Join(root, "state"))}
}
func TestManagePeerOwnership(t *testing.T) {
	h := testHost(t)
	r := RPCRequest{Operation: "claim-controller", Session: "agents", SessionKey: h.Config.SessionKey, ControllerID: "local", MachineID: "peer"}
	if _, e := h.Dispatch(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(h.ConfigDir, "controller.json"))
	r.ControllerID = "other"
	if _, e := h.Dispatch(context.Background(), r); e == nil || !strings.Contains(e.Error(), "controller_conflict") {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(h.ConfigDir, "controller.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("ownership replaced")
	}
	if e := os.WriteFile(filepath.Join(h.ConfigDir, "controller.json"), []byte(`{"bad":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := h.Dispatch(context.Background(), r); e == nil {
		t.Fatal("corrupt owner accepted")
	}
}
func TestHerdrRPCNormalizedClaim(t *testing.T) {
	h := testHost(t)
	r := RPCRequest{Operation: "claim-controller", Session: "agents", SessionKey: h.Config.SessionKey, ControllerID: "local", MachineID: "peer"}
	if _, e := h.Dispatch(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	request := Request{ID: "one", Op: "join", RoomID: "room", Target: AgentTarget{MachineID: "self", HerdrSocket: h.Config.HerdrSocket, PaneID: "w1:p1", TerminalID: "term", Kind: "codex", NativeSession: "native"}}
	if e := h.Store.Enqueue(request); e != nil {
		t.Fatal(e)
	}
	r.Operation = "requests"
	r.MachineID = ""
	v, e := h.Dispatch(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	requests := v.([]Request)
	if len(requests) != 1 || requests[0].Target.MachineID != "peer" {
		t.Fatal(v)
	}
	r.Operation = "claim-request"
	r.RequestID = "one"
	r.Digest = RequestDigest(request)
	if _, e = h.Dispatch(context.Background(), r); e == nil {
		t.Fatal("raw self digest accepted")
	}
	r.Digest = RequestDigest(requests[0])
	if _, e = h.Dispatch(context.Background(), r); e != nil {
		t.Fatal(e)
	}
}
func TestHerdrRPCRejectFields(t *testing.T) {
	for _, raw := range []string{`{"operation":"probe","session":"agents","root":"/tmp"}`, `{"operation":"probe","session":"agents","socket":"/tmp/a"}`, `{"operation":"probe","session":"agents","method":"agent.prompt"}`, `{"operation":"probe","session":"agents","room_id":"room"}`, `{"operation":"probe","session":"../../other"}`, `{"operation":"start","session":"agents"}`, `{"operation":"probe","session":"agents"} {}`} {
		if _, e := DecodeRPC(strings.NewReader(raw)); e == nil {
			t.Fatal(raw)
		}
	}
}
func TestQuietStartup(t *testing.T) {
	h := testHost(t)
	r := RPCRequest{Operation: "probe", Session: "agents"}
	if _, e := h.Dispatch(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(h.ConfigDir); !os.IsNotExist(e) {
		t.Fatal("probe touched state", e)
	}
}
func TestHerdrRPCCommandData(t *testing.T) {
	key, _ := SessionKey("/tmp/a")
	topic := "literal `touch nope` $(touch nope) ' \"\nnext"
	r := RPCRequest{Operation: "outcome", Session: "agents", SessionKey: key, ControllerID: "local", RequestID: "one", Outcome: &Outcome{Digest: strings.Repeat("a", 64), ControllerID: "local", Phase: "complete", Response: Response{Message: topic}}}
	p := Peer{Machine: Machine{ID: "peer", Target: "host", Session: "agents"}, Binary: "/opt/a ' b/bin/parley"}
	c := Client{Run: func(_ context.Context, b string, args []string, in []byte) ([]byte, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, topic) || strings.Contains(joined, "touch") || strings.Contains(joined, "-L") || strings.Contains(joined, "-R") {
			t.Fatal(args)
		}
		var got RPCRequest
		if e := json.Unmarshal(in, &got); e != nil || got.Outcome.Response.Message != topic {
			t.Fatal(string(in), e)
		}
		return []byte("{}"), nil
	}}
	if _, e := c.Call(context.Background(), p, r); e != nil {
		t.Fatal(e)
	}
}
func TestHerdrRPCRejectCorruptRoom(t *testing.T) {
	h := testHost(t)
	r := RPCRequest{Operation: "claim-controller", Session: "agents", SessionKey: h.Config.SessionKey, ControllerID: "local", MachineID: "peer"}
	if _, e := h.Dispatch(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	if e := h.Store.Update(func(s *State) error { s.Rooms["room"] = Room{ID: "../outside"}; return nil }); e != nil {
		t.Fatal(e)
	}
	r.Operation = "rooms"
	r.MachineID = ""
	if _, e := h.Dispatch(context.Background(), r); e == nil {
		t.Fatal("invalid plugin record exposed")
	}
}
