package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// The participant never opens a reverse SSH callback just to browse controller rooms.
func (h HostSession) RoomChoices(ctx context.Context, c *Client, target AgentTarget) (RoomCatalog, error) {
	owner, err := h.Owner(ctx)
	if err != nil {
		return RoomCatalog{}, err
	}
	c.LocalMachineID, c.HerdrSocket = owner.MachineID, owner.HerdrSocket
	broker := Broker{Store: h.Store, Client: c}
	if owner.Role != "participant" {
		catalog, err := broker.RoomChoices(ctx, target)
		if err == nil && owner.ControllerID == "" {
			catalog.Warnings = append(catalog.Warnings, "Controller not activated on this host. Verify setup, SSH authentication and helper versions on the intended controller.")
		}
		return catalog, err
	}
	target, _, err = c.Agent(ctx, target)
	if err != nil {
		return RoomCatalog{}, err
	}
	fallback, err := broker.RoomChoices(ctx, target)
	if err != nil {
		return fallback, err
	}
	id, err := NewID()
	if err != nil {
		return RoomCatalog{}, err
	}
	target.MachineID = "self"
	req := Request{ID: id, Op: "catalog", Target: target}
	if err = h.Store.Enqueue(req); err != nil {
		return RoomCatalog{}, err
	}
	if err = c.SignalRequest(ctx, target, id); err == nil {
		var reply Response
		reply, err = h.WaitOutcome(ctx, id)
		if err == nil && reply.Status == "catalog" && reply.Catalog != nil {
			return *reply.Catalog, nil
		}
		if err == nil {
			err = fmt.Errorf("%s: %s (request %s)", reply.Code, reply.Message, id)
		}
	}
	// A controller outage must not hide otherwise usable same-host rooms.
	fallback.Warnings = append(fallback.Warnings, "Controller catalog unavailable: "+err.Error())
	return fallback, nil
}

func (h HostSession) CheckRoomChoice(ctx context.Context, c *Client, target AgentTarget, selected Room) error {
	owner, err := h.Owner(ctx)
	if err != nil {
		return err
	}
	if owner.Role != "participant" || selected.Host.HerdrSocket == owner.HerdrSocket {
		return (&Broker{Store: h.Store, Client: c}).CheckRoomChoice(ctx, target, selected)
	}
	catalog, err := h.RoomChoices(ctx, c, target)
	if err != nil {
		return err
	}
	for _, choice := range catalog.Rooms {
		live := choice.Room
		if choice.Status == "open" && live.ID == selected.ID && RoomHostReference(live, c.LocalMachineID) == RoomHostReference(selected, c.LocalMachineID) && SameTerminal(live.Host, selected.Host) && live.Source == selected.Source {
			return nil
		}
	}
	return fmt.Errorf("room_identity_mismatch: controller could not confirm selected room; refresh the room list")
}

func (b *Broker) serveCatalog(ctx context.Context, peer Peer, req Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	local := peer.Machine.ID == b.Client.LocalMachineID
	var outcome Outcome
	var err error
	if local {
		outcome, err = b.Store.ClaimRequest(req, peer.Config.ControllerID)
	} else {
		err = b.peerCall(ctx, peer, RPCRequest{Operation: "claim-request", RequestID: req.ID, Digest: hostDigest(req)}, &outcome)
	}
	if err != nil {
		return Response{}, err
	}
	if outcome.Phase == "complete" {
		return outcome.Response, nil
	}
	// Leave time to publish healthy rows even when an unrelated peer stalls.
	lookup, stopLookup := context.WithTimeout(ctx, 8*time.Second)
	catalog, lookupErr := b.RoomChoices(lookup, req.Target)
	stopLookup()
	response := Response{Status: "catalog", Catalog: &catalog}
	if lookupErr != nil {
		response = failure("catalog_unavailable", lookupErr)
	} else {
		for i := range catalog.Rooms {
			choice := &catalog.Rooms[i]
			if choice.Room.Host.MachineID == "self" {
				choice.Room.Host.MachineID = b.Client.LocalMachineID
			}
			choice.Room.Host.NativeSession = ""
			if !local && choice.Room.Host.MachineID == b.Client.LocalMachineID {
				choice.Host = "Controller"
			}
			if !local && choice.Room.Host.MachineID != b.Client.LocalMachineID && choice.Room.Host.MachineID != peer.Machine.ID {
				choice.Status = "unsupported"
				choice.Message = "Joining between distinct remote hosts is unsupported"
			}
		}
		data, _ := json.Marshal(response)
		if len(data) > requestLimit-4096 {
			response = failure("catalog_too_large", fmt.Errorf("controller room catalog exceeds helper response limit"))
		}
	}
	outcome.Phase, outcome.Response = "complete", response
	if local {
		err = b.Store.Complete(req.ID, outcome)
	} else {
		var published Outcome
		err = b.peerCall(ctx, peer, RPCRequest{Operation: "outcome", RequestID: req.ID, Outcome: &outcome}, &published)
	}
	return response, err
}
