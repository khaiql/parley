package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMachines(t *testing.T) {
	c := Client{HerdrBinary: "herdr", Run: func(_ context.Context, b string, a []string, in []byte) ([]byte, error) {
		if b != "herdr" || !reflect.DeepEqual(a, []string{"machine", "list", "--json"}) {
			t.Fatal(b, a)
		}
		return []byte(`[{"id":"peer","target":"host","session":"agents","enabled":true,"selected":true}]`), nil
	}}
	m, e := c.Machines(context.Background())
	if e != nil || len(m) != 1 || m[0].Session != "agents" {
		t.Fatal(m, e)
	}
}
func TestAgentIdentity(t *testing.T) {
	target := AgentTarget{MachineID: "peer", HerdrSocket: "/tmp/herdr.sock", PaneID: "w1:p2", TerminalID: "term_a", Kind: "codex", NativeSession: "conversation_a"}
	key, _ := SessionKey("/tmp/herdr.sock")
	c := Client{HerdrBinary: "herdr", LocalMachineID: "local", Run: func(_ context.Context, binary string, a []string, in []byte) ([]byte, error) {
		if binary != "ssh" {
			t.Fatal("mutable native lookup", binary, a)
		}
		var r RPCRequest
		if e := json.Unmarshal(in, &r); e != nil || r.Target != "term_a" || r.ControllerID != "controller" {
			t.Fatal(r, e)
		}
		live := AgentTarget{MachineID: "peer", HerdrSocket: "/tmp/herdr.sock", WorkspaceID: "w2", PaneID: "w2:p9", TerminalID: "term_a", Kind: "codex", NativeSession: "conversation_a"}
		return json.Marshal(ProbeResult{Version: HelperVersion, Config: SessionConfig{MachineID: "peer", ControllerID: "controller", HerdrSocket: "/tmp/herdr.sock", SessionKey: key}, Agent: &live, AgentStatus: "idle"})
	}}
	c.managed.Store("peer", Peer{Machine: Machine{ID: "peer", Target: "host", Session: "agents"}, Binary: "/opt/parley", Config: SessionConfig{MachineID: "peer", ControllerID: "controller", HerdrSocket: "/tmp/herdr.sock", SessionKey: key}})
	got, state, e := c.Agent(context.Background(), target)
	if e != nil || state != "idle" || got.PaneID != "w2:p9" {
		t.Fatal(got, state, e)
	}
	target.NativeSession = "different"
	if live, _, e := c.Agent(context.Background(), target); e != nil || live.NativeSession != "different" {
		t.Fatal(live, e)
	}
	target.Kind = "different"
	if _, _, e = c.Agent(context.Background(), target); e == nil || !strings.Contains(e.Error(), "agent_replaced") {
		t.Fatal(e)
	}
}
func TestManagePeer(t *testing.T) {
	key, _ := SessionKey("/tmp/peer.sock")
	c := Client{HerdrBinary: "herdr", LocalMachineID: "local", Run: func(_ context.Context, b string, a []string, in []byte) ([]byte, error) {
		if b == "herdr" {
			if !reflect.DeepEqual(a, []string{"--machine", "peer", "plugin", "list", "--json"}) {
				t.Fatal(a)
			}
			return []byte(`{"result":{"plugins":[{"plugin_id":"parley","enabled":true,"plugin_root":"/opt/my ' plugin"}]}}`), nil
		}
		if b != "ssh" || strings.Contains(strings.Join(a, " "), "-L") {
			t.Fatal(b, a)
		}
		var r RPCRequest
		if e := json.Unmarshal(in, &r); e != nil {
			t.Fatal(e)
		}
		if r.Session != "agents" {
			t.Fatal(r)
		}
		if !strings.Contains(a[len(a)-1], `'\''`) {
			t.Fatal(a)
		}
		if r.Operation == "probe" {
			return json.Marshal(ProbeResult{Version: HelperVersion, Config: SessionConfig{HerdrSocket: "/tmp/peer.sock", SessionKey: key}, Capabilities: RequiredCapabilities})
		}
		return json.Marshal(SessionConfig{ControllerID: "local", MachineID: "peer", HerdrSocket: "/tmp/peer.sock", SessionKey: key})
	}}
	p, e := ManagePeer(context.Background(), &c, NewStore(t.TempDir()), Machine{ID: "peer", Target: "host", Session: "agents", Enabled: true})
	if e != nil || p.Config.ControllerID != "local" {
		t.Fatal(p, e)
	}
}

func TestAgentIdentityLocalPinned(t *testing.T) {
	root, e := os.MkdirTemp("/tmp", "parley-agent-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	socket := filepath.Join(root, "h.sock")
	socket, _ = filepath.Abs(socket)
	socket, _ = filepath.EvalSymlinks(filepath.Dir(socket))
	socket = filepath.Join(socket, "h.sock")
	listener, e := net.Listen("unix", socket)
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				var r struct {
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				if e := json.NewDecoder(conn).Decode(&r); e != nil {
					return
				}
				if r.Method == "agent.list" {
					io.WriteString(conn, `{"result":{"agents":[{"terminal_id":"term_a","pane_id":"w1:p2"}]}}`+"\n")
				} else if r.Method == "agent.get" {
					io.WriteString(conn, `{"result":{"agent":{"terminal_id":"term_a","pane_id":"w1:p2","workspace_id":"w1","agent":"codex","agent_status":"idle","agent_session":{"agent":"codex","kind":"id","value":"conversation_a"}}}}`+"\n")
				} else if r.Method == "workspace.report_metadata" {
					io.WriteString(conn, `{"result":{"type":"ok"}}`+"\n")
				}
			}()
		}
	}()
	calls := 0
	c := Client{LocalMachineID: "local", HerdrSocket: socket, Run: func(context.Context, string, []string, []byte) ([]byte, error) {
		calls++
		return nil, fmt.Errorf("unexpected process")
	}}
	got, _, e := c.Agent(context.Background(), AgentTarget{MachineID: "self", PaneID: "w1:p2"})
	if e != nil || got.HerdrSocket != socket || got.NativeSession != "" || calls != 0 {
		t.Fatal(got, e, calls)
	}
	if e = c.SignalRequest(context.Background(), got, "request"); e != nil {
		t.Fatal(e)
	}
	got.HerdrSocket = "/tmp/other.sock"
	if e = c.SignalRequest(context.Background(), got, "request"); e == nil {
		t.Fatal("wrong signal socket accepted")
	}
}
func TestAgentIdentityAbsent(t *testing.T) {
	key, _ := SessionKey("/tmp/herdr.sock")
	c := Client{LocalMachineID: "local", Run: func(context.Context, string, []string, []byte) ([]byte, error) {
		return json.Marshal(ProbeResult{Version: HelperVersion, Config: SessionConfig{MachineID: "peer", ControllerID: "controller", HerdrSocket: "/tmp/herdr.sock", SessionKey: key}})
	}}
	c.managed.Store("peer", Peer{Machine: Machine{ID: "peer", Target: "host", Session: "agents"}, Binary: "/opt/parley", Config: SessionConfig{MachineID: "peer", ControllerID: "controller", HerdrSocket: "/tmp/herdr.sock", SessionKey: key}})
	if _, _, e := c.Agent(context.Background(), AgentTarget{MachineID: "peer", PaneID: "w1:p1"}); e == nil || !strings.Contains(e.Error(), "agent_identity_required") {
		t.Fatal(e)
	}
}
func TestAgentIdentityProfileRetarget(t *testing.T) {
	key, _ := SessionKey("/tmp/old.sock")
	p := Peer{Machine: Machine{ID: "peer", Target: "old-host", Session: "old-session"}, Binary: "/opt/parley", Config: SessionConfig{MachineID: "peer", ControllerID: "controller", HerdrSocket: "/tmp/old.sock", SessionKey: key}}
	mutableReads := 0
	c := Client{LocalMachineID: "local", Run: func(_ context.Context, binary string, args []string, in []byte) ([]byte, error) {
		if binary == "ssh" {
			if !strings.Contains(strings.Join(args, " "), "old-host") {
				t.Fatal(args)
			}
			return []byte(`{"version":` + fmt.Sprint(HelperVersion) + `,"config":{"machine_id":"peer","controller_id":"controller","herdr_socket":"/tmp/old.sock","session_key":"` + key + `"},"agent":{"machine_id":"peer","herdr_socket":"/tmp/old.sock","workspace_id":"w1","pane_id":"w1:p1","terminal_id":"old-terminal","kind":"codex","native_session":"old-conversation"},"agent_status":"idle"}`), nil
		}
		mutableReads++
		return []byte(`{"result":{"agent":{"pane_id":"w1:p1","workspace_id":"w1","terminal_id":"new-terminal","agent":"codex","agent_status":"idle","agent_session":{"agent":"codex","kind":"id","value":"new-conversation"}}}}`), nil
	}}
	c.managed.Store("peer", p)
	live, _, e := c.Agent(context.Background(), AgentTarget{MachineID: "peer", PaneID: "w1:p1"})
	if e != nil || live.NativeSession != "" || live.HerdrSocket != "/tmp/old.sock" || mutableReads != 0 {
		t.Fatal(live, e, mutableReads)
	}
}
func TestSignalRequestMaximumID(t *testing.T) {
	root, e := os.MkdirTemp("/tmp", "parley-marker-")
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
	markers := make(chan string, 2)
	go func() {
		for i := 0; i < 2; i++ {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			var wire struct {
				Params struct {
					Tokens map[string]string `json:"tokens"`
				} `json:"params"`
			}
			json.NewDecoder(conn).Decode(&wire)
			markers <- wire.Params.Tokens["parley_request"]
			io.WriteString(conn, `{"result":{"type":"ok"}}`+"\n")
			conn.Close()
		}
	}()
	c := Client{LocalMachineID: "local", HerdrSocket: socket}
	target := AgentTarget{MachineID: "local", HerdrSocket: socket, WorkspaceID: "w1"}
	id := strings.Repeat("a", 128)
	for i := 0; i < 2; i++ {
		if e = c.SignalRequest(context.Background(), target, id); e != nil {
			t.Fatal(e)
		}
	}
	first, second := <-markers, <-markers
	if len(first) > 80 || len(second) > 80 || first == second || strings.Contains(first, id) {
		t.Fatal("invalid metadata markers", len(first), len(second), first == second)
	}
}

func TestNativeTerminalIdentityFollowsPane(t *testing.T) {
	root := t.TempDir()
	socket := filepath.Join(root, "h")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var req struct {
					Method string            `json:"method"`
					Params map[string]string `json:"params"`
				}
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				switch req.Method {
				case "agent.list":
					io.WriteString(conn, `{"result":{"type":"agent_list","agents":[{"terminal_id":"term_original","pane_id":"w2:p9"}]}}`+"\n")
				case "agent.get":
					if req.Params["target"] != "w2:p9" {
						io.WriteString(conn, `{"error":{"code":"agent_not_found","message":"native agent resolver only accepts pane/name"}}`+"\n")
						return
					}
					io.WriteString(conn, `{"result":{"agent":{"terminal_id":"term_original","pane_id":"w2:p9","workspace_id":"w2","agent":"codex","agent_status":"idle","agent_session":{"agent":"codex","kind":"id","value":"native-original"}}}}`+"\n")
				}
			}()
		}
	}()
	client := Client{LocalMachineID: "local", HerdrSocket: socket}
	target := AgentTarget{MachineID: "self", HerdrSocket: socket, PaneID: "w1:p1", TerminalID: "term_original", Kind: "codex", NativeSession: "native-original"}
	got, _, err := client.Agent(context.Background(), target)
	if err != nil || got.PaneID != "w2:p9" || got.TerminalID != target.TerminalID {
		t.Fatal(got, err)
	}
}

func TestRemoteHelperUsesLoginEnvironment(t *testing.T) {
	command := helperCommand("/opt/parley/bin/parley")
	if !strings.HasPrefix(command, "sh -lc ") {
		t.Fatal("remote helper cannot see Herdr installed through the user's login PATH", command)
	}
}

func TestInstalledBinaryUsesRegisteredPlugin(t *testing.T) {
	c := &Client{HerdrBinary: "herdr", Run: func(_ context.Context, binary string, args []string, _ []byte) ([]byte, error) {
		if binary != "herdr" || strings.Join(args, " ") != "plugin list --json" {
			t.Fatalf("unexpected lookup: %s %v", binary, args)
		}
		return []byte(`{"result":{"plugins":[{"plugin_id":"parley","enabled":true,"plugin_root":"/registered/plugin"}]}}`), nil
	}}
	binary, err := c.InstalledBinary(context.Background())
	if err != nil || binary != "/registered/plugin/bin/parley" {
		t.Fatal(binary, err)
	}
}
