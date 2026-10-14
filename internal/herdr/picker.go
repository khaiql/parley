package herdr

import (
	"context"
	"fmt"
	"sort"
)

type RoomChoice struct {
	Room    Room   `json:"room"`
	Host    string `json:"host"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Joined  bool   `json:"joined"`
}

type RoomCatalog struct {
	Rooms    []RoomChoice `json:"rooms"`
	Warnings []string     `json:"warnings,omitempty"`
}

func (b *Broker) CheckRoomChoice(ctx context.Context, target AgentTarget, selected Room) error {
	if _, _, err := b.Client.Agent(ctx, target); err != nil {
		return err
	}
	live, err := b.ResolveSelectedRoom(ctx, selected.ID, RoomHostReference(selected, b.Client.LocalMachineID))
	if err != nil {
		return err
	}
	if !SameTerminal(live.Host, selected.Host) || live.Source != selected.Source || live.HostSessionID != selected.HostSessionID {
		return fmt.Errorf("room_identity_mismatch: refresh the room list")
	}
	return nil
}

func (b *Broker) ResolveSelectedRoom(ctx context.Context, id string, host RoomHostRef) (Room, error) {
	local := b.localPeer()
	if host.MachineID == local.Config.MachineID && host.SessionKey == local.Config.SessionKey {
		room, err := b.Store.Room(id)
		if err != nil {
			return room, err
		}
		if RoomHostReference(room, b.Client.LocalMachineID) != host {
			return room, fmt.Errorf("room_identity_mismatch")
		}
		return b.validateRoom(ctx, room)
	}
	state, err := b.Store.Read()
	if err != nil {
		return Room{}, err
	}
	peer, ok := state.Peers[host.MachineID]
	if !ok || peer.Config.SessionKey != host.SessionKey {
		return Room{}, fmt.Errorf("session_unverified")
	}
	machines, err := b.Client.Machines(ctx)
	if err != nil {
		return Room{}, err
	}
	valid := false
	for _, machine := range machines {
		if machine.ID == peer.Machine.ID && machine.Enabled && machine.Target == peer.Machine.Target && machine.Session == peer.Machine.Session {
			valid = true
		}
	}
	if !valid {
		return Room{}, fmt.Errorf("saved_profile_changed")
	}
	binary, err := b.Client.helper(ctx, peer.Machine)
	if err != nil {
		return Room{}, err
	}
	if binary != peer.Binary {
		return Room{}, fmt.Errorf("helper_changed")
	}
	b.Client.managed.Store(peer.Machine.ID, peer)
	var room Room
	if err = b.peerCall(ctx, peer, RPCRequest{Operation: "room", RoomID: id}, &room); err != nil {
		return room, err
	}
	if room.ID != id || RoomHostReference(room, b.Client.LocalMachineID) != host {
		return room, fmt.Errorf("room_identity_mismatch")
	}
	return b.validateRoom(ctx, room)
}

// RoomChoices reads this session and its activated enabled peers; it never claims or joins.
func (b *Broker) RoomChoices(ctx context.Context, target AgentTarget) (RoomCatalog, error) {
	catalog := RoomCatalog{Rooms: []RoomChoice{}}
	_, _, err := b.Client.Agent(ctx, target)
	if err != nil {
		return catalog, err
	}
	local, err := b.Store.Rooms()
	if err != nil {
		return catalog, err
	}
	state, err := b.Store.Read()
	if err != nil {
		return catalog, err
	}
	appendRoom := func(room Room, host string) {
		choice := RoomChoice{Room: room, Host: host, Status: "closed"}
		if !room.Closed {
			live, err := b.validateRoom(ctx, room)
			choice.Status = "open"
			if err != nil {
				choice.Status, choice.Message = "stale", err.Error()
				if EventFailureCode(err) == "helper_transport_failed" {
					choice.Status = "unreachable"
				}
			} else {
				choice.Room = live
			}
		}
		for _, binding := range state.Bindings {
			if binding.RoomID == room.ID && binding.SessionID != "" && binding.JoinPhase == "complete" && SameTerminal(binding.Target, target) && bindingMatchesRoom(binding, room, state, b.Client.LocalMachineID) {
				choice.Joined = true
				break
			}
		}
		catalog.Rooms = append(catalog.Rooms, choice)
	}
	for _, room := range local {
		appendRoom(room, "Local")
	}
	machines, err := b.Client.Machines(ctx)
	if err != nil {
		catalog.Warnings = append(catalog.Warnings, "Peer lookup: "+err.Error())
	}
	for _, machine := range machines {
		if !machine.Enabled {
			continue
		}
		host := machine.Label
		if host == "" {
			host = machine.ID
		}
		peer, ok := state.Peers[machine.ID]
		if !ok || peer.Machine.Target != machine.Target || peer.Machine.Session != machine.Session {
			catalog.Warnings = append(catalog.Warnings, host+": session_unverified (controller activation required)")
			continue
		}
		binary, err := b.Client.helper(ctx, machine)
		if err != nil || binary != peer.Binary {
			if err == nil {
				err = fmt.Errorf("helper_changed (controller refresh required)")
			}
			catalog.Warnings = append(catalog.Warnings, host+": "+err.Error())
			continue
		}
		var records map[string]Room
		if err := b.peerCall(ctx, peer, RPCRequest{Operation: "rooms"}, &records); err != nil {
			catalog.Warnings = append(catalog.Warnings, host+": "+err.Error())
			continue
		}
		// Targeted agent probes retain the activated socket/controller pins without new ownership claims.
		b.Client.managed.Store(machine.ID, peer)
		for id, room := range records {
			if safeID(id) != nil || room.ID != id || room.Host.MachineID != machine.ID || room.Host.HerdrSocket != peer.Config.HerdrSocket {
				catalog.Warnings = append(catalog.Warnings, host+": room_identity_mismatch")
				continue
			}
			if _, _, err := validateEndpoints(room.Source, room.ID); err != nil {
				catalog.Warnings = append(catalog.Warnings, host+": "+err.Error())
				continue
			}
			appendRoom(room, host)
		}
	}
	counts := map[string]int{}
	for _, choice := range catalog.Rooms {
		counts[choice.Room.ID+":"+choice.Room.Host.MachineID+":"+choice.Room.Host.HerdrSocket]++
	}
	for i := range catalog.Rooms {
		if counts[catalog.Rooms[i].Room.ID+":"+catalog.Rooms[i].Room.Host.MachineID+":"+catalog.Rooms[i].Room.Host.HerdrSocket] > 1 {
			catalog.Rooms[i].Status = "ambiguous"
			catalog.Rooms[i].Message = "room_ambiguous"
		}
	}
	sort.Slice(catalog.Rooms, func(i, j int) bool {
		a, z := catalog.Rooms[i], catalog.Rooms[j]
		if a.Room.Topic != z.Room.Topic {
			return a.Room.Topic < z.Room.Topic
		}
		if a.Host != z.Host {
			return a.Host < z.Host
		}
		return a.Room.ID < z.Room.ID
	})
	sort.Strings(catalog.Warnings)
	return catalog, nil
}

func bindingMatchesRoom(binding Binding, room Room, state State, localMachine string) bool {
	ref := RoomHostReference(room, localMachine)
	if binding.RoomHost != nil {
		return *binding.RoomHost == ref
	}
	if binding.Target.HerdrSocket == room.Host.HerdrSocket && binding.Routed == room.Source {
		return true
	}
	cached, ok := state.Rooms[room.ID]
	return ok && RoomHostReference(cached, localMachine) == ref && cached.Source == room.Source
}
