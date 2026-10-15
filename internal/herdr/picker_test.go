package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/paths"
	runtime "github.com/khaiql/parley/internal/runtime"
)

func TestRoomChoicesDurableLocalAndIsolation(t *testing.T) {
	h, req, root := brokerFixture(t)
	room := brokerRoom(t, h, req)
	room.Topic = "parser discussion"
	if err := h.Store.saveRoom(room); err != nil {
		t.Fatal(err)
	}
	unjoined := req
	unjoined.RoomID = "unjoined"
	brokerRoom(t, h, unjoined)
	closed := room
	closed.ID = "closed"
	closed.Closed = true
	if err := h.Store.saveRoom(closed); err != nil {
		t.Fatal(err)
	}
	stale := room
	stale.ID = "stale"
	if err := h.Store.saveRoom(stale); err != nil {
		t.Fatal(err)
	}
	sibling := NewStore(filepath.Join(root, "sibling"))
	other := room
	other.ID = "sibling-room"
	if err := sibling.saveRoom(other); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.Update(func(state *State) error {
		state.Rooms[other.ID] = other // Discovery cache must not become a host registry.
		state.Bindings[HostBindingKey(room.ID, req.Target)] = Binding{RoomID: room.ID, Target: req.Target, SessionID: "psn_existing", JoinPhase: "complete", Routed: room.Source}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) { return []byte(`[]`), nil }
	before, _ := h.Store.Read()
	broker := Broker{Store: NewStore(h.Store.dir), Client: h.Client}
	got, err := broker.RoomChoices(context.Background(), req.Target)
	if err != nil || len(got.Rooms) != 4 || len(got.Warnings) != 0 {
		t.Fatalf("catalog=%+v err=%v", got, err)
	}
	statuses := map[string]string{}
	for _, choice := range got.Rooms {
		statuses[choice.Room.ID] = choice.Status
		if choice.Joined != (choice.Room.ID == room.ID) {
			t.Fatal("incorrect joined status", choice)
		}
		if choice.Room.ID == room.ID && (!choice.Joined || choice.Room.Topic != room.Topic || choice.Host != "Local") {
			t.Fatal("lost room/agent metadata", choice)
		}
	}
	if !reflect.DeepEqual(statuses, map[string]string{room.ID: "open", "unjoined": "open", "closed": "closed", "stale": "stale"}) {
		t.Fatal(statuses)
	}
	after, _ := h.Store.Read()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("browsing changed durable state")
	}
}

func TestRoomChoiceRechecksBeforeJoin(t *testing.T) {
	for _, change := range []string{"unchanged", "closed", "target", "missing", "host", "session"} {
		t.Run(change, func(t *testing.T) {
			var replaced atomic.Bool
			h, req, _ := brokerFixtureIdentity(t, func(int64) bool { return replaced.Load() })
			room := brokerRoom(t, h, req)
			h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) { return []byte(`[]`), nil }
			broker := Broker{Store: h.Store, Client: h.Client}
			catalog, err := broker.RoomChoices(context.Background(), req.Target)
			if err != nil || len(catalog.Rooms) != 1 || catalog.Rooms[0].Status != "open" {
				t.Fatal(catalog, err)
			}
			switch change {
			case "closed":
				room.Closed = true
				if err := h.Store.saveRoom(room); err != nil {
					t.Fatal(err)
				}
			case "target":
				replaced.Store(true)
			case "missing":
				if err := os.Remove(filepath.Join(h.Store.dir, "rooms", room.ID+".json")); err != nil {
					t.Fatal(err)
				}
			case "host":
				room.Host.Kind = "different"
				if err := h.Store.saveRoom(room); err != nil {
					t.Fatal(err)
				}
			case "session":
				room.HostSessionID = "psn_replaced"
				if err := h.Store.saveRoom(room); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := h.Store.Read()
			if err := broker.CheckRoomChoice(context.Background(), req.Target, catalog.Rooms[0].Room); (err == nil) != (change == "unchanged") {
				t.Fatal("selection validation", change, err)
			}
			after, _ := h.Store.Read()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("selection validation mutated state")
			}
		})
	}
}

func TestRoomChoicesPeersAndAmbiguity(t *testing.T) {
	for _, mode := range []string{"open", "offline", "ambiguous", "changed-profile", "forged-session", "unactivated"} {
		t.Run(mode, func(t *testing.T) {
			h, req, _ := brokerFixture(t)
			local := brokerRoom(t, h, req)
			key, _ := SessionKey("/remote/herdr.sock")
			peer := Peer{Machine: Machine{ID: "peer", Label: "Work", Target: "host", Session: "agents", Enabled: true}, Config: SessionConfig{MachineID: "peer", ControllerID: "local", HerdrSocket: "/remote/herdr.sock", SessionKey: key}, Binary: "/opt/plugin/bin/parley"}
			if mode != "unactivated" {
				if err := h.Store.Update(func(state *State) error { state.Peers[peer.Machine.ID] = peer; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			remote := local
			remote.ID = "remote-room"
			remote.Host.MachineID = peer.Machine.ID
			remote.Host.HerdrSocket = peer.Config.HerdrSocket
			if mode == "ambiguous" {
				remote.ID = local.ID
				if err := h.Store.Update(func(state *State) error {
					ref := RoomHostReference(local, h.Client.LocalMachineID)
					state.Bindings[HostBindingKey(local.ID, req.Target)] = Binding{RoomID: local.ID, Target: req.Target, SessionID: "psn_existing", JoinPhase: "complete", RoomHost: &ref}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			remote.Source.Descriptor = strings.ReplaceAll(local.Source.Descriptor, local.ID, remote.ID)
			remote.Source.ArtifactURL = strings.ReplaceAll(local.Source.ArtifactURL, local.ID, remote.ID)
			if mode == "forged-session" {
				remote.Host.HerdrSocket = "/sibling/herdr.sock"
			}
			h.Client.Run = func(_ context.Context, binary string, args []string, input []byte) ([]byte, error) {
				if binary != "ssh" {
					if len(args) > 1 && args[0] == "--machine" {
						return []byte(`{"result":{"plugins":[{"plugin_id":"parley","enabled":true,"plugin_root":"/opt/plugin"}]}}`), nil
					}
					machine := peer.Machine
					if mode == "changed-profile" {
						machine.Target = "different-host"
					}
					return json.Marshal([]Machine{machine})
				}
				if mode == "offline" {
					return nil, fmt.Errorf("host unreachable")
				}
				var rpc RPCRequest
				if err := json.Unmarshal(input, &rpc); err != nil {
					t.Fatal(err)
				}
				switch rpc.Operation {
				case "rooms":
					return json.Marshal(map[string]Room{remote.ID: remote})
				case "room":
					return json.Marshal(remote)
				case "probe":
					return json.Marshal(ProbeResult{Version: HelperVersion, Config: peer.Config, Agent: &remote.Host, AgentStatus: "idle"})
				default:
					t.Fatalf("browsing attempted mutation: %s %v", rpc.Operation, args)
					return nil, nil
				}
			}
			broker := Broker{Store: h.Store, Client: h.Client}
			before, _ := h.Store.Read()
			got, err := broker.RoomChoices(context.Background(), req.Target)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "open" || mode == "ambiguous" {
				if len(got.Rooms) != 2 || len(got.Warnings) != 0 {
					t.Fatal(got)
				}
				for _, choice := range got.Rooms {
					want := "open"
					if choice.Status != want {
						t.Fatal(choice)
					}
					if mode == "ambiguous" && choice.Joined != (choice.Host == "Local") {
						t.Fatal("joined badge leaked across hosts", choice)
					}
				}
			} else if len(got.Warnings) == 0 || len(got.Rooms) != 1 {
				t.Fatal("failed lookup presented as complete or leaked foreign records", got)
			}
			after, _ := h.Store.Read()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("browsing changed peer ownership, bindings, or requests")
			}
		})
	}
}

func TestRoomChoicesSlowPeerDoesNotHideHealthyPeer(t *testing.T) {
	h, req, _ := brokerFixture(t)
	key, _ := SessionKey("/remote/herdr.sock")
	healthy := Peer{Machine: Machine{ID: "healthy", Target: "healthy", Session: "agents", Enabled: true}, Config: SessionConfig{MachineID: "healthy", ControllerID: "local", HerdrSocket: "/remote/herdr.sock", SessionKey: key}, Binary: "/opt/plugin/bin/parley"}
	slow := healthy
	slow.Machine.ID, slow.Machine.Target, slow.Config.MachineID = "slow", "slow", "slow"
	room := Room{ID: "healthy-room", HostSessionID: "psn_host", Host: AgentTarget{MachineID: "healthy", HerdrSocket: healthy.Config.HerdrSocket, TerminalID: "remote-term", Kind: "gemini"}, Source: Endpoints{Descriptor: "parley://127.0.0.1:40001/healthy-room", ArtifactURL: "http://127.0.0.1:40002/rooms/healthy-room/artifacts"}}
	if err := h.Store.Update(func(s *State) error { s.Peers["slow"], s.Peers["healthy"] = slow, healthy; return nil }); err != nil {
		t.Fatal(err)
	}
	slowEnabled := true
	h.Client.Run = func(ctx context.Context, binary string, args []string, input []byte) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if binary != "ssh" {
			if len(args) > 1 && args[0] == "--machine" {
				if args[1] == "slow" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				timer := time.NewTimer(20 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-timer.C:
				}
				return []byte(`{"result":{"plugins":[{"plugin_id":"parley","enabled":true,"plugin_root":"/opt/plugin"}]}}`), nil
			}
			if slowEnabled {
				machines := []Machine{slow.Machine, healthy.Machine}
				for i := range 100 {
					machines = append(machines, Machine{ID: fmt.Sprintf("disabled-%d", i), Target: "unused", Session: "agents"})
				}
				return json.Marshal(machines)
			}
			return json.Marshal([]Machine{healthy.Machine})
		}
		var rpc RPCRequest
		if err := json.Unmarshal(input, &rpc); err != nil {
			return nil, err
		}
		switch rpc.Operation {
		case "rooms":
			return json.Marshal(map[string]Room{room.ID: room})
		case "room":
			return json.Marshal(room)
		case "probe":
			return json.Marshal(ProbeResult{Version: HelperVersion, Config: healthy.Config, Agent: &room.Host})
		default:
			return nil, fmt.Errorf("unexpected operation %s", rpc.Operation)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	b := Broker{Store: h.Store, Client: h.Client}
	catalog, err := b.RoomChoices(ctx, req.Target)
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range catalog.Rooms {
		if choice.Room.ID == room.ID && choice.Status == "open" {
			return
		}
	}
	slowEnabled = false
	control, controlErr := b.RoomChoices(context.Background(), req.Target)
	if controlErr != nil || len(control.Rooms) != 1 || control.Rooms[0].Status != "open" {
		t.Fatalf("invalid healthy control: %+v, %v", control, controlErr)
	}
	t.Fatalf("healthy peer hidden by slow peer: %+v", catalog)
}

func TestRoomChoicesSlowLocalRoomDoesNotHideHealthyPeer(t *testing.T) {
	h, req, root := brokerFixture(t)
	local := brokerRoom(t, h, req)
	stalled := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(stalled.Close)
	local.Source.ArtifactURL = stalled.URL + "/rooms/" + local.ID + "/artifacts"
	endpoint, _ := url.Parse(local.Source.ArtifactURL)
	meta, err := runtime.LoadRoomRuntime(paths.New(root), local.ID)
	if err != nil {
		t.Fatal(err)
	}
	meta.ArtifactLocalPort, _ = strconv.Atoi(endpoint.Port())
	if err := runtime.SaveRoomRuntime(paths.New(root), meta); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.saveRoom(local); err != nil {
		t.Fatal(err)
	}
	key, _ := SessionKey("/remote/herdr.sock")
	peer := Peer{Machine: Machine{ID: "healthy", Target: "healthy", Session: "agents", Enabled: true}, Config: SessionConfig{MachineID: "healthy", ControllerID: "local", HerdrSocket: "/remote/herdr.sock", SessionKey: key}, Binary: "/opt/plugin/bin/parley"}
	remote := Room{ID: "healthy-room", HostSessionID: "psn_host", Host: AgentTarget{MachineID: "healthy", HerdrSocket: peer.Config.HerdrSocket, TerminalID: "remote-term", Kind: "gemini"}, Source: Endpoints{Descriptor: "parley://127.0.0.1:40001/healthy-room", ArtifactURL: "http://127.0.0.1:40002/rooms/healthy-room/artifacts"}}
	if err := h.Store.Update(func(s *State) error { s.Peers[peer.Machine.ID] = peer; return nil }); err != nil {
		t.Fatal(err)
	}
	h.Client.Run = func(ctx context.Context, binary string, args []string, input []byte) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if binary != "ssh" {
			if len(args) > 1 && args[0] == "--machine" {
				return []byte(`{"result":{"plugins":[{"plugin_id":"parley","enabled":true,"plugin_root":"/opt/plugin"}]}}`), nil
			}
			return json.Marshal([]Machine{peer.Machine})
		}
		var rpc RPCRequest
		if err := json.Unmarshal(input, &rpc); err != nil {
			return nil, err
		}
		switch rpc.Operation {
		case "rooms":
			return json.Marshal(map[string]Room{remote.ID: remote})
		case "room":
			return json.Marshal(remote)
		case "probe":
			return json.Marshal(ProbeResult{Version: HelperVersion, Config: peer.Config, Agent: &remote.Host})
		default:
			return nil, fmt.Errorf("unexpected operation %s", rpc.Operation)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	catalog, err := (&Broker{Store: h.Store, Client: h.Client}).RoomChoices(ctx, req.Target)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, choice := range catalog.Rooms {
		statuses[choice.Room.ID] = choice.Status
	}
	if statuses[local.ID] != "unreachable" || statuses[remote.ID] != "open" {
		t.Fatalf("local timeout hid healthy peer or reported stale certainty: %+v", catalog)
	}
}

func TestRoomChoicesSlowPeerListingDoesNotHideLocalRoom(t *testing.T) {
	h, req, _ := brokerFixture(t)
	room := brokerRoom(t, h, req)
	h.Client.Run = func(ctx context.Context, _ string, _ []string, _ []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	catalog, err := (&Broker{Store: h.Store, Client: h.Client}).RoomChoices(ctx, req.Target)
	if err != nil || len(catalog.Rooms) != 1 || catalog.Rooms[0].Room.ID != room.ID || catalog.Rooms[0].Status != "open" || len(catalog.Warnings) != 1 {
		t.Fatalf("peer listing hid healthy local room: %+v, %v", catalog, err)
	}
}

func TestSelectedHostRejectsUnprovenLegacyRoute(t *testing.T) {
	h, req, root := brokerFixture(t)
	room := brokerRoom(t, h, req)
	key, _ := SessionKey("/remote/herdr.sock")
	peer := Peer{Machine: Machine{ID: "remote", Target: "host", Session: "agents", Enabled: true}, Config: SessionConfig{MachineID: "remote", ControllerID: "local", HerdrSocket: "/remote/herdr.sock", SessionKey: key}, Binary: "/opt/plugin/bin/parley"}
	room.Host.MachineID, room.Host.HerdrSocket = peer.Machine.ID, peer.Config.HerdrSocket
	ref := RoomHostReference(room, h.Client.LocalMachineID)
	req.RoomHost = &ref
	legacy := Binding{RoomID: req.RoomID, Target: req.Target, SessionID: "psn_original", ParticipantName: "legacy", StateDir: root, Routed: Endpoints{Descriptor: "parley://127.0.0.1:49001/room-a", ArtifactURL: "http://127.0.0.1:49002/rooms/room-a/artifacts"}, JoinPhase: "complete", JoinRequestID: "legacy-request"}
	originalFixture(t, root, legacy, true)
	if err := h.saveBinding(legacy); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.Update(func(s *State) error { s.Peers[peer.Machine.ID] = peer; s.Rooms[room.ID] = room; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.Enqueue(req); err != nil {
		t.Fatal(err)
	}
	h.Client.Run = func(_ context.Context, binary string, args []string, input []byte) ([]byte, error) {
		if binary != "ssh" {
			if len(args) > 1 && args[0] == "--machine" {
				return []byte(`{"result":{"plugins":[{"plugin_id":"parley","enabled":true,"plugin_root":"/opt/plugin"}]}}`), nil
			}
			return json.Marshal([]Machine{peer.Machine})
		}
		var rpc RPCRequest
		if err := json.Unmarshal(input, &rpc); err != nil {
			return nil, err
		}
		switch rpc.Operation {
		case "room":
			return json.Marshal(room)
		case "probe":
			return json.Marshal(ProbeResult{Version: HelperVersion, Config: peer.Config, Agent: &room.Host})
		default:
			return nil, fmt.Errorf("unexpected operation %s", rpc.Operation)
		}
	}
	b := Broker{Store: h.Store, Client: h.Client}
	result, err := b.Handle(context.Background(), b.localPeer(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != "room_mismatch" {
		t.Fatalf("selected host %s accepted unproven legacy route: %+v", ref.MachineID, result)
	}
}

func TestLegacyDirectBindingRequiresSameMachine(t *testing.T) {
	room := Room{ID: "room-a", HostSessionID: "psn_host", Host: AgentTarget{MachineID: "remote", HerdrSocket: "/shared/path/herdr.sock"}, Source: Endpoints{Descriptor: "parley://127.0.0.1:49001/room-a", ArtifactURL: "http://127.0.0.1:49002/rooms/room-a/artifacts"}}
	binding := Binding{Target: AgentTarget{MachineID: "self", HerdrSocket: room.Host.HerdrSocket}, Routed: room.Source}
	if bindingMatchesRoom(binding, room, "local") {
		t.Fatal("identical paths and ports on another machine proved legacy ownership")
	}
	room.Host.MachineID = "local"
	if !bindingMatchesRoom(binding, room, "local") {
		t.Fatal("same-host legacy route was rejected")
	}
}
