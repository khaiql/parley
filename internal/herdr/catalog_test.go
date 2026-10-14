package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCatalogRequestIsReadOnly(t *testing.T) {
	h, req, _ := brokerFixture(t)
	room := brokerRoom(t, h, req)
	room.Host.MachineID = "self"
	if err := h.Store.saveRoom(room); err != nil {
		t.Fatal(err)
	}
	req.ID, req.Op, req.RoomID = "browse", "catalog", ""
	h.Client.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		if len(args) == 0 || args[0] != "machine" {
			t.Fatal("browsing launched command", args)
		}
		return []byte(`[]`), nil
	}
	if err := h.Store.Enqueue(req); err != nil {
		t.Fatal(err)
	}
	b := Broker{Store: h.Store, Client: h.Client}
	result, err := b.Handle(context.Background(), b.localPeer(), req)
	if err != nil || result.Status != "catalog" {
		t.Fatal(result, err)
	}
	state, _ := h.Store.Read()
	if len(state.Bindings) != 0 || len(state.Rooms) != 0 || b.Tunnels != nil {
		t.Fatal("catalog created participant or route", state)
	}
}

func TestRemotePickerGetsControllerRooms(t *testing.T) {
	local, req, _ := brokerFixture(t)
	room := brokerRoom(t, local, req)
	room.Topic = "Mac room"
	room.Host.MachineID = "self"
	if err := local.Store.saveRoom(room); err != nil {
		t.Fatal(err)
	}
	remote, target, root := brokerFixture(t)
	owner := SessionConfig{Role: "participant", ControllerID: "local", MachineID: "ubuntu", HerdrSocket: remote.Client.HerdrSocket}
	owner.SessionKey, _ = SessionKey(owner.HerdrSocket)
	host := HostSession{Config: owner, ConfigDir: filepath.Join(root, "config"), Store: remote.Store}
	if err := privateDir(host.ConfigDir); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(host.ConfigDir, "controller.json"), owner); err != nil {
		t.Fatal(err)
	}
	if err := remote.Store.PinRuntimeRoot(root); err != nil {
		t.Fatal(err)
	}
	peer := Peer{Machine: Machine{ID: "ubuntu", Target: "ubuntu", Session: "native", Enabled: true}, Config: owner, Binary: "/opt/plugin/bin/parley"}
	if err := local.Store.Update(func(s *State) error { s.ControllerID = "local"; s.Peers[peer.Machine.ID] = peer; return nil }); err != nil {
		t.Fatal(err)
	}
	local.Client.managed.Store(peer.Machine.ID, peer)
	local.Client.Run = func(ctx context.Context, binary string, args []string, data []byte) ([]byte, error) {
		if binary != "ssh" {
			if len(args) > 1 && args[0] == "--machine" {
				return []byte(`{"result":{"plugins":[{"plugin_id":"parley","enabled":true,"plugin_root":"/opt/plugin"}]}}`), nil
			}
			return json.Marshal([]Machine{peer.Machine})
		}
		rpc, err := DecodeRPC(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		reply, err := host.Dispatch(ctx, rpc)
		if err != nil {
			return nil, err
		}
		return json.Marshal(reply)
	}
	remote.Client.LocalMachineID = "ubuntu"
	remote.Client.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		if len(args) > 0 && args[0] == "machine" {
			return []byte(`[]`), nil
		}
		return nil, fmt.Errorf("unexpected command %v", args)
	}
	b := Broker{Store: local.Store, Client: local.Client}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		for {
			pending, err := remote.Store.Pending()
			if err != nil {
				done <- err
				return
			}
			if len(pending) > 0 {
				if err := b.Drain(ctx, peer); err != nil {
					done <- err
					return
				}
			}
			select {
			case <-ctx.Done():
				done <- nil
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	catalog, err := host.RoomChoices(ctx, remote.Client, target.Target)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Rooms) != 1 || catalog.Rooms[0].Room.ID != room.ID || catalog.Rooms[0].Room.Host.MachineID != "local" || catalog.Rooms[0].Status != "open" {
		t.Fatal("Ubuntu did not get Mac room", catalog)
	}
	selected := catalog.Rooms[0].Room
	if err := host.CheckRoomChoice(ctx, remote.Client, target.Target, selected); err != nil {
		t.Fatal("fresh controller selection rejected", err)
	}
	room.HostSessionID = "psn_changed"
	if err := local.Store.saveRoom(room); err != nil {
		t.Fatal(err)
	}
	if err := host.CheckRoomChoice(ctx, remote.Client, target.Target, selected); err == nil {
		t.Fatal("stale controller room accepted")
	}

	for _, store := range []*Store{local.Store, remote.Store} {
		state, _ := store.Read()
		if len(state.Bindings) != 0 || len(state.Rooms) != 0 {
			t.Fatal("browse mutated participants/cache", state)
		}
	}
	if b.Tunnels != nil {
		t.Fatal("browse created tunnels")
	}
	entries, _ := os.ReadDir(filepath.Join(remote.Store.dir, "handoffs"))
	if len(entries) != 0 {
		t.Fatal("browse prompted agent")
	}
}

func TestRemotePickerOutageKeepsLocalRooms(t *testing.T) {
	h, target, root := brokerFixture(t)
	room := brokerRoom(t, h, target)
	owner := SessionConfig{Role: "participant", ControllerID: "local", MachineID: "ubuntu", HerdrSocket: h.Client.HerdrSocket}
	owner.SessionKey, _ = SessionKey(owner.HerdrSocket)
	host := HostSession{Config: owner, ConfigDir: filepath.Join(root, "config"), Store: h.Store}
	if err := privateDir(host.ConfigDir); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(host.ConfigDir, "controller.json"), owner); err != nil {
		t.Fatal(err)
	}
	h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) { return []byte(`[]`), nil }
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	catalog, err := host.RoomChoices(ctx, h.Client, target.Target)
	if err != nil || len(catalog.Rooms) != 1 || catalog.Rooms[0].Room.ID != room.ID || len(catalog.Warnings) == 0 {
		t.Fatal("outage hidden or local room lost", catalog, err)
	}
	state, _ := h.Store.Read()
	if len(state.Bindings) != 0 {
		t.Fatal("outage created participant")
	}
}

func TestUnactivatedPickerReportsMissingController(t *testing.T) {
	h, target, root := brokerFixture(t)
	host := HostSession{Config: SessionConfig{MachineID: "local", HerdrSocket: h.Client.HerdrSocket}, ConfigDir: filepath.Join(root, "config"), Store: h.Store}
	host.Config.SessionKey, _ = SessionKey(host.Config.HerdrSocket)
	h.Client.Run = func(context.Context, string, []string, []byte) ([]byte, error) { return []byte(`[]`), nil }
	catalog, err := host.RoomChoices(context.Background(), h.Client, target.Target)
	if err != nil || len(catalog.Rooms) != 0 || len(catalog.Warnings) == 0 {
		t.Fatal("unactivated endpoint returned a successful empty catalog", catalog, err)
	}
	pending, _ := h.Store.Pending()
	if len(pending) != 0 {
		t.Fatal("unowned host enqueued controller request")
	}
}

func TestCatalogLookupLeavesTimeToPublish(t *testing.T) {
	h, req, _ := brokerFixture(t)
	room := brokerRoom(t, h, req)
	req.ID, req.Op, req.RoomID = "bounded-browse", "catalog", ""
	h.Client.Run = func(ctx context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		if len(args) == 0 || args[0] != "machine" {
			t.Fatal(args)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 10*time.Second {
			t.Fatal("catalog lookup can outlive popup", deadline)
		}
		return nil, context.DeadlineExceeded
	}
	if err := h.Store.Enqueue(req); err != nil {
		t.Fatal(err)
	}
	b := Broker{Store: h.Store, Client: h.Client}
	result, err := b.Handle(context.Background(), b.localPeer(), req)
	if err != nil || result.Catalog == nil || len(result.Catalog.Rooms) != 1 || result.Catalog.Rooms[0].Room.ID != room.ID || result.Catalog.Rooms[0].Status != "open" || len(result.Catalog.Warnings) == 0 {
		t.Fatal(result, err)
	}
	outcome, err := h.Store.readOutcome(req.ID)
	if err != nil || outcome.Phase != "complete" {
		t.Fatal(outcome, err)
	}
}
