package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/khaiql/parley/internal/adapter"
	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/paths"
	runtime "github.com/khaiql/parley/internal/runtime"
	"github.com/khaiql/parley/internal/server"
	"io"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmptyQueueDoesNotDiscoverRooms(t *testing.T) {
	calls := 0
	c := &Client{Run: func(context.Context, string, []string, []byte) ([]byte, error) {
		calls++
		return nil, fmt.Errorf("unexpected subprocess")
	}}
	b := &Broker{Store: NewStore(t.TempDir()), Client: c}
	if err := b.Drain(context.Background(), Peer{Machine: Machine{ID: "local"}}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("quiet queue performed %d calls", calls)
	}
}
func TestBindingKey(t *testing.T) {
	a := target()
	b := a
	b.PaneID = "moved"
	if BindingKey("room", a) != BindingKey("room", b) {
		t.Fatal("pane move changed binding")
	}
	b.MachineID = "other"
	if BindingKey("room", a) == BindingKey("room", b) {
		t.Fatal("machine aliases collided")
	}
}
func TestBrokerNativePathReference(t *testing.T) {
	r := request("path")
	r.Target.Kind = "gemini"
	r.Target.NativeSession = "/home/user/.native/a session.json"
	if e := validateRequest(r); e != nil {
		t.Fatal(e)
	}
}

func brokerFixture(t *testing.T) (*HostOperations, Request, string) {
	t.Helper()
	root, e := os.MkdirTemp("/tmp", "hb-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	socket := filepath.Join(root, "h")
	listener, e := net.Listen("unix", socket)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				var r struct {
					Method string `json:"method"`
				}
				if json.NewDecoder(conn).Decode(&r) != nil {
					return
				}
				if r.Method == "agent.get" {
					io.WriteString(conn, `{"result":{"agent":{"terminal_id":"term_a","pane_id":"w:p","workspace_id":"w","agent":"gemini","agent_status":"idle","agent_session":{"agent":"gemini","kind":"path","value":"/native/a session.json"}}}}`+"\n")
				} else {
					io.WriteString(conn, `{"result":{}}`+"\n")
				}
			}()
		}
	}()
	c := &Client{LocalMachineID: "local", HerdrSocket: socket, RuntimeRoot: root, ParleyBinary: "parley"}
	r := Request{ID: "first", Op: "join", RoomID: "room-a", Target: AgentTarget{MachineID: "self", HerdrSocket: socket, TerminalID: "term_a", Kind: "gemini", NativeSession: "/native/a session.json"}}
	return &HostOperations{Store: NewStore(filepath.Join(root, "plugin")), Client: c}, r, root
}
func originalFixture(t *testing.T, root string, b Binding, online bool) {
	t.Helper()
	p := paths.New(root)
	if e := runtime.SaveSession(p, runtime.Session{ID: "psn_original", RoomID: b.RoomID, Name: b.ParticipantName}); e != nil {
		t.Fatal(e)
	}
	store, e := runtime.ParticipantStore(p, b.RoomID, b.ParticipantName)
	if e != nil {
		t.Fatal(e)
	}
	status := "offline"
	if online {
		status = "online"
	}
	if e = store.SaveMeta(adapter.Meta{RoomID: b.RoomID, Name: b.ParticipantName, Status: status, LastSeenSeq: 17}); e != nil {
		t.Fatal(e)
	}
	if online {
		l, e := net.Listen("unix", runtime.ParticipantSocketPath(p, b.RoomID, b.ParticipantName))
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { l.Close() })
		go func() {
			for {
				c, e := l.Accept()
				if e != nil {
					return
				}
				c.Close()
			}
		}()
	}
}
func TestConcurrentJoinSameBinding(t *testing.T) {
	h, r, root := brokerFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	h.Client.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		if args[0] != "join" {
			t.Errorf("unexpected command %v", args)
		}
		calls.Add(1)
		close(entered)
		<-release
		b, _ := h.Store.Binding(HostBindingKey(r.RoomID, r.Target))
		originalFixture(t, root, b, true)
		return []byte(`{"status":"joined","room_id":"room-a","session_id":"psn_original"}`), nil
	}
	endpoints := Endpoints{Descriptor: "parley://127.0.0.1:49001/room-a", ArtifactURL: "http://127.0.0.1:49002/rooms/room-a/artifacts"}
	results := make(chan Response, 2)
	errs := make(chan error, 2)
	go func() { out, e := h.Join(context.Background(), r, endpoints); results <- out; errs <- e }()
	<-entered
	second := r
	second.ID = "second"
	second.Target.MachineID = "local"
	go func() { out, e := h.Join(context.Background(), second, endpoints); results <- out; errs <- e }()
	close(release)
	for range 2 {
		out := <-results
		if e := <-errs; e != nil || out.Binding == nil || out.Binding.SessionID != "psn_original" {
			t.Fatal(out, e)
		}
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
}
func TestHostJoinUncertainDisconnectedCursor(t *testing.T) {
	for _, online := range []bool{true, false} {
		t.Run(fmt.Sprint(online), func(t *testing.T) {
			h, r, root := brokerFixture(t)
			calls := 0
			h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) {
				calls++
				b, _ := h.Store.Binding(HostBindingKey(r.RoomID, r.Target))
				originalFixture(t, root, b, online)
				return nil, io.EOF
			}
			endpoints := Endpoints{Descriptor: "parley://127.0.0.1:49001/room-a", ArtifactURL: "http://127.0.0.1:49002/rooms/room-a/artifacts"}
			first, e := h.Join(context.Background(), r, endpoints)
			if e != nil {
				t.Fatal(e)
			}
			r.ID = "retry"
			second, e := h.Join(context.Background(), r, endpoints)
			if e != nil || calls != 1 {
				t.Fatal(second, e, calls)
			}
			if online {
				if first.Binding == nil || second.Binding == nil || second.Binding.SessionID != "psn_original" {
					t.Fatal(first, second)
				}
			} else {
				if second.Code != "participant_disconnected" {
					t.Fatal(second)
				}
			}
			b, _ := h.Store.Binding(HostBindingKey(r.RoomID, r.Target))
			store, _ := runtime.ParticipantStore(paths.New(root), b.RoomID, b.ParticipantName)
			meta, _ := store.LoadMeta()
			if meta.LastSeenSeq != 17 {
				t.Fatal(meta)
			}
		})
	}
}
func TestHostStart(t *testing.T) {
	h, r, _ := brokerFixture(t)
	r.Op = "start"
	r.RoomID = ""
	r.Topic = "debug parser"
	calls := 0
	h.Client.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		calls++
		if args[0] != "start" || args[1] != "--topic" || args[3] != "--name" {
			t.Fatal(args)
		}
		return []byte(`{"status":"started","room_id":"room-a","session_id":"psn_host","descriptor":"parley://127.0.0.1:49001/room-a","artifact_local_port":49002,"artifact_path":"/rooms/room-a/artifacts"}`), nil
	}
	out, e := h.Start(context.Background(), r)
	if e != nil || out.Binding == nil || out.Room == nil || calls != 1 {
		t.Fatal(out, e)
	}
	room, e := h.Store.Room("room-a")
	if e != nil || room.Host.Kind != "gemini" {
		t.Fatal(room, e)
	}
}
func TestBrokerForgedOriginNoSSH(t *testing.T) {
	h, r, _ := brokerFixture(t)
	r.Target.MachineID = "forged"
	calls := 0
	h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) { calls++; return nil, io.EOF }
	b := Broker{Store: h.Store, Client: h.Client}
	out, e := b.Handle(context.Background(), b.localPeer(), r)
	if e != nil || out.Code != "invalid_request" || calls != 0 {
		t.Fatal(out, e, calls)
	}
}

func TestHostJoinRequestConflict(t *testing.T) {
	h, r, _ := brokerFixture(t)
	if e := h.Store.Enqueue(r); e != nil {
		t.Fatal(e)
	}
	r.Target.NativeSession = "/native/changed"
	if e := h.journal(r); e == nil {
		t.Fatal("changed body accepted")
	}
}
func TestHostRuntimeRootPin(t *testing.T) {
	h, _, root := brokerFixture(t)
	if e := h.Store.PinRuntimeRoot(root); e != nil {
		t.Fatal(e)
	}
	other := &HostOperations{Store: h.Store, Client: &Client{RuntimeRoot: "/different/ssh/environment"}}
	p, e := other.runtimePaths()
	if e != nil || p.Root != root {
		t.Fatal(p, e)
	}
	if e = h.Store.PinRuntimeRoot("/different"); e == nil {
		t.Fatal("pin changed")
	}
}
func TestHostJoinProcessHelper(t *testing.T) {
	root := os.Getenv("HERDR_BROKER_TEST_ROOT")
	if root == "" {
		return
	}
	var r Request
	if e := json.Unmarshal([]byte(os.Getenv("HERDR_BROKER_TEST_REQUEST")), &r); e != nil {
		t.Fatal(e)
	}
	h := &HostOperations{Store: NewStore(filepath.Join(root, "plugin")), Client: &Client{LocalMachineID: "local", HerdrSocket: r.Target.HerdrSocket, RuntimeRoot: root, ParleyBinary: "parley"}}
	h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) {
		f, e := os.OpenFile(filepath.Join(root, "mutations"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			return nil, e
		}
		_, e = f.WriteString("join\n")
		f.Close()
		if e != nil {
			return nil, e
		}
		b, e := h.Store.Binding(HostBindingKey(r.RoomID, r.Target))
		if e != nil {
			return nil, e
		}
		p := paths.New(root)
		if e = runtime.SaveSession(p, runtime.Session{ID: "psn_original", RoomID: b.RoomID, Name: b.ParticipantName}); e != nil {
			return nil, e
		}
		store, e := runtime.ParticipantStore(p, b.RoomID, b.ParticipantName)
		if e != nil {
			return nil, e
		}
		if e = store.SaveMeta(adapter.Meta{RoomID: b.RoomID, Name: b.ParticipantName, Status: "online", LastSeenSeq: 17}); e != nil {
			return nil, e
		}
		if e = os.WriteFile(filepath.Join(root, "entered"), []byte("intent-and-mutation"), 0600); e != nil {
			return nil, e
		}
		for {
			if _, e = os.Stat(filepath.Join(root, "release")); e == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		return []byte(`{"status":"joined","room_id":"room-a","session_id":"psn_original"}`), nil
	}
	out, e := h.Join(context.Background(), r, Endpoints{Descriptor: "parley://127.0.0.1:49001/room-a", ArtifactURL: "http://127.0.0.1:49002/rooms/room-a/artifacts"})
	if e != nil {
		t.Fatal(e)
	}
	data, _ := json.Marshal(out)
	if e = os.WriteFile(filepath.Join(root, r.ID+"-result"), data, 0600); e != nil {
		t.Fatal(e)
	}
}
func awaitBrokerFile(t *testing.T, path string) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		if _, e := os.Stat(path); e == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout: %s", path)
}
func TestConcurrentJoinSeparateProcesses(t *testing.T) {
	for _, kill := range []bool{false, true} {
		t.Run(fmt.Sprint(kill), func(t *testing.T) {
			h, r, root := brokerFixture(t)
			name := "h_" + HostBindingKey(r.RoomID, r.Target)[:16]
			p := paths.New(root)
			if _, e := p.EnsureRoomDir(r.RoomID); e != nil {
				t.Fatal(e)
			}
			if e := os.MkdirAll(filepath.Dir(runtime.ParticipantSocketPath(p, r.RoomID, name)), 0700); e != nil {
				t.Fatal(e)
			}
			l, e := net.Listen("unix", runtime.ParticipantSocketPath(p, r.RoomID, name))
			if e != nil {
				t.Fatal(e)
			}
			defer l.Close()
			go func() {
				for {
					c, e := l.Accept()
					if e != nil {
						return
					}
					c.Close()
				}
			}()
			launch := func(req Request) *exec.Cmd {
				data, _ := json.Marshal(req)
				cmd := exec.Command(os.Args[0], "-test.run=^TestHostJoinProcessHelper$", "-test.count=1")
				cmd.Env = append(os.Environ(), "HERDR_BROKER_TEST_ROOT="+root, "HERDR_BROKER_TEST_REQUEST="+string(data))
				cmd.Stdout = &bytes.Buffer{}
				cmd.Stderr = &bytes.Buffer{}
				if e = cmd.Start(); e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { _ = cmd.Process.Kill() })
				return cmd
			}
			first := launch(r)
			awaitBrokerFile(t, filepath.Join(root, "entered"))
			r.ID = "second"
			r.Target.MachineID = "local"
			second := launch(r)
			if kill {
				if e = first.Process.Kill(); e != nil {
					t.Fatal(e)
				}
				_ = first.Wait()
			} else {
				if e = os.WriteFile(filepath.Join(root, "release"), nil, 0600); e != nil {
					t.Fatal(e)
				}
				if e = first.Wait(); e != nil {
					t.Fatal(e, first.Stderr)
				}
			}
			if e = second.Wait(); e != nil {
				t.Fatal(e, second.Stderr)
			}
			data, e := os.ReadFile(filepath.Join(root, "mutations"))
			if e != nil || string(data) != "join\n" {
				t.Fatal(string(data), e)
			}
			var out Response
			data, e = os.ReadFile(filepath.Join(root, "second-result"))
			if e != nil || json.Unmarshal(data, &out) != nil || out.Binding == nil || out.Binding.SessionID != "psn_original" {
				t.Fatal(string(data), e)
			}
			b, e := h.Store.Binding(HostBindingKey(r.RoomID, r.Target))
			if e != nil || b.JoinPhase != "complete" {
				t.Fatal(b, e)
			}
			store, _ := runtime.ParticipantStore(p, b.RoomID, b.ParticipantName)
			meta, _ := store.LoadMeta()
			if meta.LastSeenSeq != 17 {
				t.Fatal(meta)
			}
		})
	}
}
func TestHostRuntimePinCorruption(t *testing.T) {
	h, _, root := brokerFixture(t)
	if e := h.Store.PinRuntimeRoot(root); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "plugin", "runtime-root.json"), []byte(`{"root":"relative"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := h.runtimePaths(); e == nil {
		t.Fatal("corrupt root accepted")
	}
	if e := os.WriteFile(filepath.Join(root, "plugin", "runtime-root.json"), []byte(`{`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := h.runtimePaths(); e == nil {
		t.Fatal("malformed root replaced")
	}
}
func TestHostQueuedJoinUsesImmutableRequest(t *testing.T) {
	h, r, root := brokerFixture(t)
	if e := h.Store.Enqueue(r); e != nil {
		t.Fatal(e)
	}
	h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) {
		b, _ := h.Store.Binding(HostBindingKey(r.RoomID, r.Target))
		originalFixture(t, root, b, true)
		return []byte(`{"status":"joined","room_id":"room-a","session_id":"psn_original"}`), nil
	}
	normalized, e := normalize(r, (&Broker{Client: h.Client}).localPeer())
	if e != nil {
		t.Fatal(e)
	}
	out, e := h.Join(context.Background(), normalized, Endpoints{Descriptor: "parley://127.0.0.1:49001/room-a", ArtifactURL: "http://127.0.0.1:49002/rooms/room-a/artifacts"})
	if e != nil || out.Binding == nil {
		t.Fatal(out, e)
	}
}
func TestHostJoinKnownFailureDoesNotRetry(t *testing.T) {
	h, r, _ := brokerFixture(t)
	calls := 0
	h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) {
		calls++
		return []byte(`{"error":{"code":"invalid_descriptor","message":"no effect"}}`), nil
	}
	endpoints := Endpoints{Descriptor: "parley://127.0.0.1:49001/room-a", ArtifactURL: "http://127.0.0.1:49002/rooms/room-a/artifacts"}
	first, e := h.Join(context.Background(), r, endpoints)
	if e != nil {
		t.Fatal(e)
	}
	second, e := h.Join(context.Background(), r, endpoints)
	if e != nil || calls != 1 || second.Code != first.Code {
		t.Fatal(first, second, e, calls)
	}
}
func TestHostStartSharedRootSameIDNeverRecoversSibling(t *testing.T) {
	h, r, root := brokerFixture(t)
	r.Op = "start"
	r.RoomID = ""
	r.Topic = "topic"
	siblingName := reservedStartName("/different/herdr.sock", r.ID)
	if siblingName == reservedStartName(r.Target.HerdrSocket, r.ID) {
		t.Fatal("namespace collision")
	}
	p := paths.New(root)
	if e := runtime.SaveSession(p, runtime.Session{ID: "psn_sibling", Name: siblingName, RoomID: "sibling"}); e != nil {
		t.Fatal(e)
	}
	h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) { return nil, io.EOF }
	out, e := h.Start(context.Background(), r)
	if e != nil || out.Code != "operation_uncertain" || out.Room != nil {
		t.Fatal(out, e)
	}
	rooms, e := h.Store.Rooms()
	if e != nil || len(rooms) != 0 {
		t.Fatal(rooms, e)
	}
}
func brokerRoom(t *testing.T, h *HostOperations, r Request) Room {
	t.Helper()
	srv, e := server.New("127.0.0.1:0", server.Config{RoomID: r.RoomID, Log: eventlog.New(t.TempDir() + "/events"), ArtifactStore: untouchedRepository{}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { srv.Close() })
	http := httptest.NewServer(srv.ArtifactHandler())
	t.Cleanup(http.Close)
	room := Room{ID: r.RoomID, Host: r.Target, HostSessionID: "psn_host", Source: Endpoints{Descriptor: fmt.Sprintf("parley://127.0.0.1:%d/%s", srv.Port(), r.RoomID), ArtifactURL: http.URL + "/rooms/" + r.RoomID + "/artifacts"}}
	room.Host.MachineID = "local"
	u, _ := url.Parse(room.Source.ArtifactURL)
	port, _ := strconv.Atoi(u.Port())
	p := paths.New(h.Client.RuntimeRoot)
	if e = h.Store.PinRuntimeRoot(p.Root); e != nil {
		t.Fatal(e)
	}
	if e = runtime.SaveRoomRuntime(p, runtime.RoomRuntime{RoomID: room.ID, LocalHost: "127.0.0.1", LocalPort: srv.Port(), ArtifactLocalPort: port, ArtifactPath: u.Path}); e != nil {
		t.Fatal(e)
	}
	if e = h.Store.saveRoom(room); e != nil {
		t.Fatal(e)
	}
	return room
}
func TestResolveRoomDurableWithoutTokens(t *testing.T) {
	h, r, _ := brokerFixture(t)
	room := brokerRoom(t, h, r)
	h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) { return []byte(`[]`), nil }
	b := Broker{Store: h.Store, Client: h.Client}
	found, e := b.ResolveRoom(context.Background(), room.ID)
	if e != nil || found.ID != room.ID {
		t.Fatal(found, e)
	}
	b.Store = NewStore(h.Store.dir)
	found, e = b.ResolveRoom(context.Background(), room.ID)
	if e != nil || found.Host.NativeSession != room.Host.NativeSession {
		t.Fatal(found, e)
	}
}
func TestResolveRoomAmbiguousIncomplete(t *testing.T) {
	for _, unreachable := range []bool{false, true} {
		t.Run(fmt.Sprint(unreachable), func(t *testing.T) {
			h, r, _ := brokerFixture(t)
			room := brokerRoom(t, h, r)
			key, _ := SessionKey("/remote/herdr.sock")
			p := Peer{Machine: Machine{ID: "remote", Target: "host", Session: "agents", Enabled: true}, Config: SessionConfig{MachineID: "remote", ControllerID: "local", HerdrSocket: "/remote/herdr.sock", SessionKey: key}, Binary: "/opt/parley"}
			if e := h.Store.Update(func(s *State) error { s.Peers["remote"] = p; return nil }); e != nil {
				t.Fatal(e)
			}
			h.Client.managed.Store("remote", p)
			remoteRoom := room
			remoteRoom.Host.MachineID = "remote"
			remoteRoom.Host.HerdrSocket = p.Config.HerdrSocket
			h.Client.Run = func(_ context.Context, binary string, _ []string, in []byte) ([]byte, error) {
				if binary != "ssh" {
					return []byte(`[]`), nil
				}
				if unreachable {
					return nil, io.EOF
				}
				var req RPCRequest
				if e := json.Unmarshal(in, &req); e != nil {
					return nil, e
				}
				switch req.Operation {
				case "rooms":
					return json.Marshal(map[string]Room{room.ID: remoteRoom})
				case "probe":
					return json.Marshal(ProbeResult{Config: p.Config, Agent: &remoteRoom.Host, AgentStatus: "idle"})
				case "room":
					return json.Marshal(remoteRoom)
				case "probe-route":
					return []byte(`{"status":"ready"}`), nil
				}
				return nil, fmt.Errorf("unexpected %s", req.Operation)
			}
			b := Broker{Store: h.Store, Client: h.Client}
			_, e := b.ResolveRoom(context.Background(), room.ID)
			expected := "room_ambiguous"
			if unreachable {
				expected = "room_resolution_incomplete"
			}
			if e == nil || e.Error() != expected {
				t.Fatal(e)
			}
		})
	}
}
func TestQueueRecoveryDrainsAllAndReusesOneLease(t *testing.T) {
	h, r, root := brokerFixture(t)
	room := brokerRoom(t, h, r)
	var calls atomic.Int32
	h.Client.Run = func(_ context.Context, binary string, args []string, _ []byte) ([]byte, error) {
		if args[0] != "join" {
			t.Fatalf("unexpected %s %v", binary, args)
		}
		calls.Add(1)
		b, _ := h.Store.Binding(HostBindingKey(r.RoomID, r.Target))
		originalFixture(t, root, b, true)
		return []byte(`{"status":"joined","room_id":"room-a","session_id":"psn_original"}`), nil
	}
	b := Broker{Store: h.Store, Client: h.Client}
	for _, id := range []string{"first", "second"} {
		r.ID = id
		if e := h.Store.Enqueue(r); e != nil {
			t.Fatal(e)
		}
	}
	if e := b.Drain(context.Background(), b.localPeer()); e != nil {
		t.Fatal(e)
	}
	pending, e := h.Store.Pending()
	if e != nil || len(pending) != 0 || calls.Load() != 1 {
		t.Fatal(pending, e, calls.Load())
	}
	state, _ := h.Store.Read()
	if state.Requests["first"].Response.Binding.SessionID != state.Requests["second"].Response.Binding.SessionID {
		t.Fatal(state.Requests)
	}
	if len(b.Tunnels.leases) != 1 {
		t.Fatal(b.Tunnels.leases)
	}
	for _, lease := range b.Tunnels.leases {
		if len(lease.bindings) != 1 {
			t.Fatal(lease.bindings)
		}
	}
	r.ID = "first"
	normalized, _ := normalize(r, b.localPeer())
	if out, e := b.Handle(context.Background(), b.localPeer(), normalized); e != nil || out.Binding == nil || calls.Load() != 1 {
		t.Fatal(out, e)
	}
	_ = room
}
func TestHostJoinRuntimeErrorRetainsIntent(t *testing.T) {
	h, r, _ := brokerFixture(t)
	calls := 0
	h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) {
		calls++
		return []byte(`{"error":{"code":"runtime_error","message":"save session failed after mutation"}}`), nil
	}
	endpoints := Endpoints{Descriptor: "parley://127.0.0.1:49001/room-a", ArtifactURL: "http://127.0.0.1:49002/rooms/room-a/artifacts"}
	out, e := h.Join(context.Background(), r, endpoints)
	if e != nil || out.Code != "operation_uncertain" {
		t.Fatal(out, e)
	}
	r.ID = "second"
	out, e = h.Join(context.Background(), r, endpoints)
	if e != nil || out.Code != "operation_uncertain" || calls != 1 {
		t.Fatal(out, e, calls)
	}
	binding, e := h.Store.Binding(HostBindingKey(r.RoomID, r.Target))
	if e != nil || binding.JoinPhase != "uncertain" {
		t.Fatal(binding, e)
	}
}
func TestBrokerIndividualBindingReadRejectsWrongKey(t *testing.T) {
	h, r, _ := brokerFixture(t)
	if e := h.Store.Update(func(s *State) error {
		s.Bindings[HostBindingKey(r.RoomID, r.Target)] = Binding{RoomID: "different", Target: r.Target}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := h.Store.Binding(HostBindingKey(r.RoomID, r.Target)); e == nil {
		t.Fatal("corrupt binding accepted")
	}
}
func TestBrokerCallHasFiniteDeadline(t *testing.T) {
	key, _ := SessionKey("/tmp/remote.sock")
	p := Peer{Machine: Machine{ID: "remote", Target: "host", Session: "agents"}, Config: SessionConfig{ControllerID: "local", SessionKey: key}, Binary: "/opt/parley"}
	c := &Client{Run: func(ctx context.Context, _ string, _ []string, _ []byte) ([]byte, error) {
		if d, ok := ctx.Deadline(); !ok || time.Until(d) > 30*time.Second {
			t.Fatal("unbounded helper")
		}
		return []byte(`[]`), nil
	}}
	if _, e := c.Call(context.Background(), p, RPCRequest{Operation: "requests", SessionKey: key, ControllerID: "local"}); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\nexec sleep 60\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, e := (&Client{}).Call(ctx, p, RPCRequest{Operation: "requests", SessionKey: key, ControllerID: "local"}); e == nil || time.Since(start) > time.Second {
		t.Fatal(e, time.Since(start))
	}
}
func TestBrokerRemoteJoinLostOutcomeKeepsOneParticipantAndLease(t *testing.T) {
	local, r, _ := brokerFixture(t)
	room := brokerRoom(t, local, r)
	remote, remoteReq, remoteRoot := brokerFixture(t)
	remote.Client.LocalMachineID = "peer"
	remoteReq.Target.MachineID = "peer"
	remoteReq.RoomID = room.ID
	key, _ := SessionKey(remote.Client.HerdrSocket)
	p := Peer{Machine: Machine{ID: "peer", Target: "host", Session: "agents", Enabled: true}, Config: SessionConfig{MachineID: "peer", ControllerID: "local", HerdrSocket: remote.Client.HerdrSocket, SessionKey: key}, Binary: "/opt/parley"}
	if e := remote.Store.PinRuntimeRoot(remoteRoot); e != nil {
		t.Fatal(e)
	}
	if e := remote.Store.Enqueue(remoteReq); e != nil {
		t.Fatal(e)
	}
	if e := local.Store.Update(func(s *State) error { s.Peers[p.Machine.ID] = p; return nil }); e != nil {
		t.Fatal(e)
	}
	local.Client.managed.Store("peer", p)
	cliCalls := 0
	remote.Client.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		cliCalls++
		if args[0] != "join" {
			t.Fatal(args)
		}
		binding, _ := remote.Store.Binding(HostBindingKey(room.ID, remoteReq.Target))
		originalFixture(t, remoteRoot, binding, true)
		return []byte(`{"status":"joined","room_id":"room-a","session_id":"psn_original"}`), nil
	}
	publications := 0
	local.Client.Run = func(ctx context.Context, binary string, args []string, in []byte) ([]byte, error) {
		if binary != "ssh" {
			return json.Marshal([]Machine{p.Machine})
		}
		var rpc RPCRequest
		if e := json.Unmarshal(in, &rpc); e != nil {
			return nil, e
		}
		switch rpc.Operation {
		case "rooms":
			return []byte(`{}`), nil
		case "probe":
			live := remoteReq.Target
			return json.Marshal(ProbeResult{Config: p.Config, Agent: &live, AgentStatus: "idle"})
		case "claim-request":
			out, e := remote.Store.Claim(rpc.RequestID, rpc.Digest, rpc.ControllerID)
			if e != nil {
				return nil, e
			}
			return json.Marshal(out)
		case "binding":
			binding, e := remote.Store.Binding(rpc.BindingKey)
			if errors.Is(e, os.ErrNotExist) {
				return []byte(`{"error":{"code":"binding_missing","message":"not yet joined"}}`), nil
			}
			if e != nil {
				return nil, e
			}
			return json.Marshal(binding)
		case "join":
			out, e := remote.Join(ctx, *rpc.Request, *rpc.Endpoints)
			if e != nil {
				return nil, e
			}
			return json.Marshal(out)
		case "outcome":
			if rpc.Outcome.Phase == "complete" {
				publications++
				if publications == 1 {
					return nil, io.EOF
				}
			}
			if e := remote.Store.Complete(rpc.RequestID, *rpc.Outcome); e != nil {
				return nil, e
			}
			return json.Marshal(rpc.Outcome)
		}
		return nil, fmt.Errorf("unexpected helper %s %v", rpc.Operation, args)
	}
	forwardCalls := []string{}
	manager := &TunnelManager{Masters: map[string]*Master{"peer:" + key: {ControlPath: "/tmp/owned/master", Target: "host", done: make(chan struct{})}}}
	port := 51000
	manager.Run = func(_ context.Context, _ string, args []string, in []byte) ([]byte, error) {
		call := strings.Join(args, " ")
		if strings.Contains(call, "-O forward") {
			if !strings.Contains(call, "-R") {
				t.Fatal(call)
			}
			forwardCalls = append(forwardCalls, call)
			port++
			return []byte(strconv.Itoa(port)), nil
		}
		if len(in) > 0 {
			var rpc RPCRequest
			if json.Unmarshal(in, &rpc) != nil || rpc.Operation != "probe-route" {
				t.Fatal(string(in))
			}
			return []byte(`{"status":"ready"}`), nil
		}
		return nil, nil
	}
	b := Broker{Store: local.Store, Client: local.Client, Tunnels: manager}
	if _, e := b.Handle(context.Background(), p, remoteReq); e == nil {
		t.Fatal("lost outcome ignored")
	}
	out, e := b.Handle(context.Background(), p, remoteReq)
	if e != nil || out.Binding == nil || cliCalls != 1 || len(forwardCalls) != 2 {
		t.Fatal(out, e, cliCalls, forwardCalls)
	}
	if len(manager.leases) != 1 {
		t.Fatal(manager.leases)
	}
	for _, lease := range manager.leases {
		if len(lease.bindings) != 1 {
			t.Fatal(lease.bindings)
		}
	}
	state, e := remote.Store.Read()
	if e != nil || state.Requests[remoteReq.ID].Phase != "complete" {
		t.Fatal(state.Requests, e)
	}
}
func TestBrokerLocalJoinRemoteRoomCreatesLocalForwards(t *testing.T) {
	h, r, root := brokerFixture(t)
	room := brokerRoom(t, h, r)
	if e := os.Remove(filepath.Join(h.Store.dir, "rooms", room.ID+".json")); e != nil {
		t.Fatal(e)
	}
	room.Host.MachineID = "peer"
	room.Host.HerdrSocket = "/tmp/remote-herdr.sock"
	key, _ := SessionKey(room.Host.HerdrSocket)
	p := Peer{Machine: Machine{ID: "peer", Target: "host", Session: "agents", Enabled: true}, Config: SessionConfig{MachineID: "peer", ControllerID: "local", HerdrSocket: room.Host.HerdrSocket, SessionKey: key}, Binary: "/opt/parley"}
	if e := h.Store.Update(func(s *State) error { s.Peers["peer"] = p; return nil }); e != nil {
		t.Fatal(e)
	}
	h.Client.managed.Store("peer", p)
	calls := 0
	h.Client.Run = func(_ context.Context, binary string, args []string, in []byte) ([]byte, error) {
		if binary == "parley" {
			calls++
			binding, _ := h.Store.Binding(HostBindingKey(r.RoomID, r.Target))
			originalFixture(t, root, binding, true)
			return []byte(`{"status":"joined","room_id":"room-a","session_id":"psn_original"}`), nil
		}
		if binary != "ssh" {
			return json.Marshal([]Machine{p.Machine})
		}
		var rpc RPCRequest
		if json.Unmarshal(in, &rpc) != nil {
			t.Fatal(string(in))
		}
		switch rpc.Operation {
		case "rooms":
			return json.Marshal(map[string]Room{room.ID: room})
		case "room":
			return json.Marshal(room)
		case "probe":
			return json.Marshal(ProbeResult{Config: p.Config, Agent: &room.Host, AgentStatus: "idle"})
		}
		return nil, fmt.Errorf("unexpected helper %s %v", rpc.Operation, args)
	}
	forwards := []string{}
	manager := &TunnelManager{Masters: map[string]*Master{"peer:" + key: {ControlPath: "/tmp/owned/master", Target: "host", done: make(chan struct{})}}}
	manager.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-O forward") {
			return nil, nil
		}
		index := slices.Index(args, "-L")
		if index < 0 {
			t.Fatal(joined)
		}
		forwards = append(forwards, joined)
		parts := strings.Split(args[index+1], ":")
		listener, e := net.Listen("tcp", parts[0]+":"+parts[1])
		if e != nil {
			return nil, e
		}
		t.Cleanup(func() { listener.Close() })
		go func() {
			for {
				conn, e := listener.Accept()
				if e != nil {
					return
				}
				go func() {
					defer conn.Close()
					source, e := net.DialTimeout("tcp", parts[2]+":"+parts[3], time.Second)
					if e != nil {
						return
					}
					defer source.Close()
					done := make(chan struct{}, 2)
					go func() { io.Copy(source, conn); done <- struct{}{} }()
					go func() { io.Copy(conn, source); done <- struct{}{} }()
					<-done
				}()
			}
		}()
		return nil, nil
	}
	if e := h.Store.Enqueue(r); e != nil {
		t.Fatal(e)
	}
	b := Broker{Store: h.Store, Client: h.Client, Tunnels: manager}
	out, e := b.Handle(context.Background(), b.localPeer(), r)
	if e != nil || out.Binding == nil || calls != 1 || len(forwards) != 2 {
		t.Fatal(out, e, calls, forwards)
	}
	if out.Binding.Routed == room.Source {
		t.Fatal("source endpoints were not remapped")
	}
	if len(manager.leases) != 1 {
		t.Fatal(manager.leases)
	}
}
func TestHostStartLostStdoutAndOriginalPaneJoin(t *testing.T) {
	h, r, root := brokerFixture(t)
	r.Op = "start"
	r.RoomID = ""
	r.Topic = "debug parser"
	calls := 0
	h.Client.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		calls++
		if args[0] != "start" {
			t.Fatal(args)
		}
		binding := Binding{RoomID: "room-a", ParticipantName: args[4]}
		originalFixture(t, root, binding, true)
		p := paths.New(root)
		if e := runtime.SaveRoomRuntime(p, runtime.RoomRuntime{RoomID: binding.RoomID, Topic: r.Topic, LocalHost: "127.0.0.1", LocalPort: 49001, ArtifactLocalPort: 49002, ArtifactPath: "/rooms/room-a/artifacts"}); e != nil {
			t.Fatal(e)
		}
		listener, e := net.Listen("unix", runtime.ServerSocketPath(p, binding.RoomID))
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { listener.Close() })
		go func() {
			for {
				conn, e := listener.Accept()
				if e != nil {
					return
				}
				conn.Close()
			}
		}()
		return nil, io.EOF
	}
	first, e := h.Start(context.Background(), r)
	if e != nil || first.Binding == nil || first.Binding.SessionID != "psn_original" {
		t.Fatal(first, e)
	}
	second, e := h.Start(context.Background(), r)
	if e != nil || second.Binding == nil || calls != 1 {
		t.Fatal(second, e, calls)
	}
	r.ID = "join-host"
	r.Op = "join"
	r.Topic = ""
	r.RoomID = first.Room.ID
	joined, e := h.Join(context.Background(), r, first.Room.Source)
	if e != nil || joined.Binding == nil || joined.Binding.SessionID != first.Binding.SessionID || calls != 1 {
		t.Fatal(joined, e, calls)
	}
}
func TestBrokerReconcileClosedRoomReleasesOnlyOwnedLease(t *testing.T) {
	h, r, _ := brokerFixture(t)
	room := brokerRoom(t, h, r)
	binding := Binding{RoomID: room.ID, Target: r.Target, ParticipantName: "h_" + HostBindingKey(room.ID, r.Target)[:16], JoinPhase: "intent", JoinRequestID: r.ID}
	if e := h.Store.saveBinding(binding); e != nil {
		t.Fatal(e)
	}
	room.Closed = true
	if e := h.Store.saveRoom(room); e != nil {
		t.Fatal(e)
	}
	reference := r.Target.MachineID + ":" + HostBindingKey(room.ID, r.Target)
	manager := &TunnelManager{leases: map[string]*tunnelLease{"owned": {bindings: map[string]bool{reference: true}}, "other": {bindings: map[string]bool{"other": true}}}}
	b := Broker{Store: h.Store, Client: h.Client, Tunnels: manager}
	if e := b.Reconcile(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(manager.leases) != 1 || manager.leases["other"] == nil {
		t.Fatal(manager.leases)
	}
}
func TestHostJoinKnownNoEffectCrashAllowsNewRequestOnly(t *testing.T) {
	h, r, root := brokerFixture(t)
	normalized, _ := normalize(r, (&Broker{Client: h.Client}).localPeer())
	if e := h.journal(normalized); e != nil {
		t.Fatal(e)
	}
	if e := h.recordFailure(normalized, failure("invalid_descriptor", nil)); e != nil {
		t.Fatal(e)
	}
	binding := Binding{RoomID: r.RoomID, Target: r.Target, ParticipantName: "h_" + HostBindingKey(r.RoomID, r.Target)[:16], JoinPhase: "intent", JoinRequestID: r.ID}
	if e := h.Store.saveBinding(binding); e != nil {
		t.Fatal(e)
	}
	calls := 0
	h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) {
		calls++
		originalFixture(t, root, binding, true)
		return []byte(`{"status":"joined","room_id":"room-a","session_id":"psn_original"}`), nil
	}
	endpoints := Endpoints{Descriptor: "parley://127.0.0.1:49001/room-a", ArtifactURL: "http://127.0.0.1:49002/rooms/room-a/artifacts"}
	out, e := h.Join(context.Background(), r, endpoints)
	if e != nil || out.Code != "invalid_descriptor" || calls != 0 {
		t.Fatal(out, e, calls)
	}
	r.ID = "new"
	out, e = h.Join(context.Background(), r, endpoints)
	if e != nil || out.Binding == nil || calls != 1 {
		t.Fatal(out, e, calls)
	}
}
