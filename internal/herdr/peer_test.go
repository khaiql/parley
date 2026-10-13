package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
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
	dir, e := h.Store.recordDir("rooms")
	if e != nil {
		t.Fatal(e)
	}
	if e = writeJSON(filepath.Join(dir, "room.json"), Room{ID: "../outside"}); e != nil {
		t.Fatal(e)
	}
	r.Operation = "rooms"
	r.MachineID = ""
	if _, e := h.Dispatch(context.Background(), r); e == nil {
		t.Fatal("invalid plugin record exposed")
	}
}

func TestHerdrRPCAuthoritativeRooms(t *testing.T) {
	operations, req, root := brokerFixture(t)
	req.RoomID = "room"
	liveRoom := brokerRoom(t, operations, req)
	key, _ := SessionKey(operations.Client.HerdrSocket)
	h := HostSession{Config: SessionConfig{HerdrSocket: operations.Client.HerdrSocket, SessionKey: key}, Store: operations.Store, ConfigDir: filepath.Join(root, "config")}
	ctx := context.Background()
	r := RPCRequest{Operation: "claim-controller", Session: "agents", SessionKey: h.Config.SessionKey, ControllerID: "local", MachineID: "peer"}
	if _, e := h.Dispatch(ctx, r); e != nil {
		t.Fatal(e)
	}
	room := liveRoom
	room.Topic = "authoritative"
	room.Host.MachineID = "self"
	dir := filepath.Join(h.Store.dir, "rooms")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := writeJSON(filepath.Join(dir, "room.json"), room); e != nil {
		t.Fatal(e)
	}
	r.MachineID = ""
	r.Operation = "room"
	r.RoomID = "room"
	v, e := h.Dispatch(ctx, r)
	if e != nil || v.(Room).Topic != "authoritative" {
		t.Fatal(v, e)
	}
	stale := room
	stale.Topic = "stale"
	if e = h.Store.Update(func(s *State) error { s.Rooms["room"] = stale; return nil }); e != nil {
		t.Fatal(e)
	}
	r.Operation = "rooms"
	r.RoomID = ""
	v, e = h.Dispatch(ctx, r)
	if e != nil || v.(map[string]Room)["room"].Topic != "authoritative" {
		t.Fatal(v, e)
	}
	if e = os.Remove(filepath.Join(dir, "room.json")); e != nil {
		t.Fatal(e)
	}
	r.Operation = "room"
	r.RoomID = "room"
	if _, e = h.Dispatch(ctx, r); !os.IsNotExist(e) {
		t.Fatal("stale cache used", e)
	}
}
func TestHerdrRPCRoomRecordBoundary(t *testing.T) {
	for _, kind := range []string{"mismatch", "corrupt", "symlink", "unsafe-filename"} {
		t.Run(kind, func(t *testing.T) {
			h := testHost(t)
			r := RPCRequest{Operation: "claim-controller", Session: "agents", SessionKey: h.Config.SessionKey, ControllerID: "local", MachineID: "peer"}
			if _, e := h.Dispatch(context.Background(), r); e != nil {
				t.Fatal(e)
			}
			dir := filepath.Join(h.Store.dir, "rooms")
			if e := os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(dir, "room.json")
			switch kind {
			case "mismatch":
				writeJSON(path, Room{ID: "other"})
			case "corrupt":
				os.WriteFile(path, []byte(`{"bad":true}`), 0600)
			case "symlink":
				target := filepath.Join(t.TempDir(), "room")
				os.WriteFile(target, []byte(`{"id":"room"}`), 0600)
				os.Symlink(target, path)
			case "unsafe-filename":
				os.WriteFile(filepath.Join(dir, "bad name.json"), []byte(`{"id":"room"}`), 0600)
			}
			r.Operation = "rooms"
			r.MachineID = ""
			if _, e := h.Dispatch(context.Background(), r); e == nil {
				t.Fatal("bad authoritative room accepted")
			}
			if kind != "unsafe-filename" {
				r.Operation = "room"
				r.RoomID = "room"
				if _, e := h.Dispatch(context.Background(), r); e == nil {
					t.Fatal("bad individual room accepted")
				}
			}
		})
	}
}
func TestHerdrRPCTargetedProbePinned(t *testing.T) {
	root, e := os.MkdirTemp("/tmp", "parley-probe-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root)
	root, _ = filepath.EvalSymlinks(root)
	socket := filepath.Join(root, "h.sock")
	listener, e := net.Listen("unix", socket)
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	called := make(chan string, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		var r struct {
			Method string            `json:"method"`
			Params map[string]string `json:"params"`
		}
		json.NewDecoder(conn).Decode(&r)
		called <- r.Method + ":" + r.Params["target"]
		io.WriteString(conn, `{"result":{"agent":{"pane_id":"w1:p1","workspace_id":"w1","terminal_id":"term","agent":"codex","agent_status":"idle","agent_session":{"agent":"codex","kind":"id","value":"native"}}}}`+"\n")
	}()
	h := testHost(t)
	h.Config.HerdrSocket = socket
	h.Config.SessionKey, _ = SessionKey(socket)
	r := RPCRequest{Operation: "claim-controller", Session: "agents", SessionKey: h.Config.SessionKey, ControllerID: "local", MachineID: "peer"}
	if _, e = h.Dispatch(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	r.Operation = "probe"
	r.MachineID = ""
	r.Target = "w1:p1"
	v, e := h.Dispatch(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	probe := v.(ProbeResult)
	if probe.Agent == nil || probe.Agent.MachineID != "peer" || probe.Agent.HerdrSocket != socket || probe.Agent.NativeSession != "native" || <-called != "agent.get:w1:p1" {
		t.Fatal(probe)
	}
	r.ControllerID = "other"
	if _, e = h.Dispatch(context.Background(), r); e == nil || !strings.Contains(e.Error(), "controller_conflict") {
		t.Fatal(e)
	}
}
