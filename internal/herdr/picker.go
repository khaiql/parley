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
	catalog, err := b.RoomChoices(ctx, target)
	if err != nil {
		return err
	}
	if len(catalog.Warnings) != 0 {
		return fmt.Errorf("room_resolution_incomplete: refresh or use manual room ID")
	}
	for _, choice := range catalog.Rooms {
		if choice.Room.ID != selected.ID {
			continue
		}
		if choice.Status != "open" {
			return fmt.Errorf("room_%s: refresh the room list", choice.Status)
		}
		if HostBindingKey(selected.ID, choice.Room.Host) != HostBindingKey(selected.ID, selected.Host) || choice.Room.Source != selected.Source || choice.Room.HostSessionID != selected.HostSessionID {
			return fmt.Errorf("room_identity_mismatch: refresh the room list")
		}
		return nil
	}
	return fmt.Errorf("room_missing: refresh the room list")
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
			if isCompletedJoinFor(binding, room.ID, target) {
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
		counts[choice.Room.ID]++
	}
	for i := range catalog.Rooms {
		if counts[catalog.Rooms[i].Room.ID] > 1 {
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

func isCompletedJoinFor(binding Binding, roomID string, target AgentTarget) bool {
	return binding.RoomID == roomID &&
		binding.SessionID != "" &&
		binding.JoinPhase == "complete" &&
		HostBindingKey(roomID, binding.Target) == HostBindingKey(roomID, target)
}
