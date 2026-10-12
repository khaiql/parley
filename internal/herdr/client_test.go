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
	c := Client{HerdrBinary: "herdr", LocalMachineID: "local", Run: func(_ context.Context, _ string, a []string, in []byte) ([]byte, error) {
		if len(a) > 0 && a[0] == "-T" {
			key, _ := SessionKey("/tmp/herdr.sock")
			return json.Marshal(ProbeResult{Config: SessionConfig{HerdrSocket: "/tmp/herdr.sock", SessionKey: key}})
		}
		if !reflect.DeepEqual(a, []string{"--machine", "peer", "agent", "get", "term_a"}) {
			t.Fatal(a)
		}
		return []byte(`{"result":{"agent":{"pane_id":"w2:p9","workspace_id":"w2","terminal_id":"term_a","agent":"codex","agent_status":"idle","agent_session":{"kind":"id","value":"conversation_a"}}}}`), nil
	}}
	key, _ := SessionKey("/tmp/herdr.sock")
	c.managed.Store("peer", Peer{Machine: Machine{ID: "peer", Target: "host", Session: "agents"}, Binary: "/opt/parley", Config: SessionConfig{HerdrSocket: "/tmp/herdr.sock", SessionKey: key}})
	got, state, e := c.Agent(context.Background(), target)
	if e != nil || state != "idle" || got.PaneID != "w2:p9" {
		t.Fatal(got, state, e)
	}
	target.NativeSession = "different"
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
			return json.Marshal(ProbeResult{Version: 1, Config: SessionConfig{HerdrSocket: "/tmp/peer.sock", SessionKey: key}, Capabilities: RequiredCapabilities})
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
				if r.Method == "agent.get" {
					io.WriteString(conn, `{"result":{"agent":{"terminal_id":"term_a","pane_id":"w1:p2","workspace_id":"w1","agent":"codex","agent_status":"idle","agent_session":{"kind":"id","value":"conversation_a"}}}}`+"\n")
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
	if e != nil || got.HerdrSocket != socket || got.NativeSession != "conversation_a" || calls != 0 {
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
	c := Client{LocalMachineID: "local", Run: func(_ context.Context, _ string, a []string, _ []byte) ([]byte, error) {
		if a[0] == "-T" {
			key, _ := SessionKey("/tmp/herdr.sock")
			return json.Marshal(ProbeResult{Config: SessionConfig{HerdrSocket: "/tmp/herdr.sock", SessionKey: key}})
		}
		return []byte(`{"result":{"agent":{"terminal_id":"term","agent":"codex"}}}`), nil
	}}
	key, _ := SessionKey("/tmp/herdr.sock")
	c.managed.Store("peer", Peer{Machine: Machine{ID: "peer", Target: "host", Session: "agents"}, Binary: "/opt/parley", Config: SessionConfig{HerdrSocket: "/tmp/herdr.sock", SessionKey: key}})
	if _, _, e := c.Agent(context.Background(), AgentTarget{MachineID: "peer", PaneID: "w1:p1"}); e == nil || !strings.Contains(e.Error(), "agent_identity_required") {
		t.Fatal(e)
	}
}
