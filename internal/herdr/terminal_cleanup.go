package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
	"github.com/khaiql/parley/internal/paths"
	runtime "github.com/khaiql/parley/internal/runtime"
)

func terminalPresent(ctx context.Context, socket, terminalID string) (bool, error) {
	b, err := socketAPI(ctx, socket, "session.snapshot", map[string]any{})
	if err != nil {
		return false, err
	}
	var response struct {
		Result struct {
			Type     string `json:"type"`
			Snapshot *struct {
				Panes []struct {
					TerminalID string `json:"terminal_id"`
				} `json:"panes"`
			} `json:"snapshot"`
		} `json:"result"`
		Error *apiError `json:"error"`
	}
	if err = json.Unmarshal(b, &response); err != nil {
		return false, err
	}
	if response.Error != nil {
		return false, response.Error
	}
	if response.Result.Type != "session_snapshot" || response.Result.Snapshot == nil || response.Result.Snapshot.Panes == nil {
		return false, fmt.Errorf("invalid Herdr session snapshot")
	}
	for _, pane := range response.Result.Snapshot.Panes {
		if pane.TerminalID == terminalID {
			return true, nil
		}
	}
	return false, nil
}

func terminalLockKey(target AgentTarget) string {
	return "terminal:" + hashBytes([]byte(target.HerdrSocket+"\x00"+target.TerminalID+"\x00"+target.Kind))
}

func (h *HostOperations) localCleanupTarget(target AgentTarget) bool {
	if target.HerdrSocket != h.Client.HerdrSocket || target.MachineID != "self" && target.MachineID != h.Client.LocalMachineID {
		return false
	}
	return target.TerminalID != "" && safeID(target.TerminalID) == nil && target.Kind != "" && safeID(target.Kind) == nil && target.HerdrSocket != ""
}

// ReconcileTerminal only cleans records when Herdr's authoritative snapshot no longer contains the original terminal.
func (h *HostOperations) ReconcileTerminal(ctx context.Context) error {
	state, err := h.Store.Read()
	if err != nil {
		return err
	}
	targets := map[string]AgentTarget{}
	for _, binding := range state.Bindings {
		if binding.Status != "left" && h.localCleanupTarget(binding.Target) {
			targets[terminalLockKey(binding.Target)] = binding.Target
		}
	}
	rooms, err := h.Store.Rooms()
	if err != nil {
		return err
	}
	for _, room := range rooms {
		if !room.Closed && h.localCleanupTarget(room.Host) {
			targets[terminalLockKey(room.Host)] = room.Host
		}
	}
	var failures []error
	for key, target := range targets {
		if err = h.Store.WithBinding(ctx, key, func() error {
			present, err := terminalPresent(ctx, h.Client.HerdrSocket, target.TerminalID)
			if err != nil || present {
				return err
			}
			if present, err := h.notificationRecipientPresent(ctx, target); err != nil || present {
				return err
			}
			latest, err := h.Store.Read()
			if err != nil {
				return err
			}
			var cleanupErrs []error
			for _, binding := range latest.Bindings {
				if binding.Status == "left" || !h.localCleanupTarget(binding.Target) || !SameTerminal(binding.Target, target) {
					continue
				}
				if err = h.leaveBinding(ctx, binding); err != nil {
					cleanupErrs = append(cleanupErrs, err)
				}
			}
			freshRooms, readErr := h.Store.Rooms()
			if readErr != nil {
				cleanupErrs = append(cleanupErrs, readErr)
			} else {
				for _, room := range freshRooms {
					if room.Closed || !h.localCleanupTarget(room.Host) || !SameTerminal(room.Host, target) {
						continue
					}
					if err = h.stopRoom(ctx, room); err != nil {
						cleanupErrs = append(cleanupErrs, err)
					}
				}
			}
			return errors.Join(cleanupErrs...)
		}); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (h *HostOperations) leaveBinding(ctx context.Context, binding Binding) error {
	fail := func(err error) error {
		binding.CleanupStatus, binding.CleanupError = "pending", err.Error()
		if saveErr := h.Store.saveBinding(binding); saveErr != nil {
			return errors.Join(err, saveErr)
		}
		return err
	}
	if binding.JoinPhase != "complete" || safeID(binding.SessionID) != nil {
		return fail(fmt.Errorf("participant session identity unavailable"))
	}
	p, err := h.runtimePaths()
	if err != nil || p.Root != binding.StateDir {
		if err == nil {
			err = fmt.Errorf("participant runtime root mismatch")
		}
		return fail(err)
	}
	sessions, err := runtime.ListSessions(p)
	if err != nil {
		return fail(err)
	}
	var found bool
	for _, session := range sessions {
		if session.ID == binding.SessionID {
			if session.RoomID != binding.RoomID || session.Name != binding.ParticipantName || found {
				return fail(fmt.Errorf("participant session identity mismatch"))
			}
			found = true
		}
	}
	if !found {
		return fail(fmt.Errorf("participant session record missing"))
	}
	participant, err := runtime.ParticipantStore(p, binding.RoomID, binding.ParticipantName)
	if err != nil {
		return fail(err)
	}
	meta, err := participant.LoadMeta()
	if err != nil || meta.RoomID != binding.RoomID || meta.Name != binding.ParticipantName {
		if err == nil {
			err = fmt.Errorf("participant metadata mismatch")
		}
		return fail(err)
	}
	if meta.Status == "left" {
		binding.Status, binding.CleanupStatus, binding.CleanupError = "left", "complete", ""
		return h.Store.saveBinding(binding)
	}
	data, runErr := h.localCLI(ctx, []string{"leave", "--session", binding.SessionID})
	var result struct {
		Status string    `json:"status"`
		Error  *apiError `json:"error"`
	}
	decodeErr := json.Unmarshal(data, &result)
	if runErr == nil && decodeErr == nil && result.Error == nil && result.Status == "left" {
		binding.Status, binding.CleanupStatus, binding.CleanupError = "left", "complete", ""
		return h.Store.saveBinding(binding)
	}
	if runErr == nil && decodeErr == nil && result.Error != nil {
		runErr = result.Error
	} else if runErr == nil {
		runErr = fmt.Errorf("leave outcome was not confirmed")
	}
	if decodeErr != nil {
		runErr = errors.Join(runErr, decodeErr)
	}
	return fail(runErr)
}

func (h *HostOperations) stopRoom(ctx context.Context, room Room) error {
	fail := func(err error) error {
		if saveErr := h.recordRoomCleanup(room, "pending", err.Error(), false); saveErr != nil {
			return errors.Join(err, saveErr)
		}
		return err
	}
	current, err := h.Store.Room(room.ID)
	if err != nil {
		return fail(err)
	}
	if current.HostSessionID != room.HostSessionID || current.Host != room.Host || current.Closed {
		return fail(fmt.Errorf("host room incarnation changed"))
	}
	if safeID(room.ID) != nil || safeID(room.HostSessionID) != nil || validateRequest(Request{ID: "cleanup", Op: "start", Topic: "cleanup", Target: room.Host}) != nil {
		return fail(fmt.Errorf("invalid host room identity"))
	}
	if _, _, err = validateEndpoints(room.Source, room.ID); err != nil {
		return fail(err)
	}
	p, err := h.Store.pinnedPaths()
	if err != nil {
		return fail(err)
	}
	meta, err := runtime.LoadRoomRuntime(p, room.ID)
	if err != nil {
		return fail(err)
	}
	expected := Endpoints{Descriptor: fmt.Sprintf("parley://%s:%d/%s", meta.LocalHost, meta.LocalPort, room.ID), ArtifactURL: fmt.Sprintf("http://%s:%d%s", meta.LocalHost, meta.ArtifactLocalPort, meta.ArtifactPath)}
	if meta.RoomID != room.ID || expected != room.Source {
		return fail(fmt.Errorf("host room runtime or endpoints mismatch"))
	}
	sessions, err := runtime.ListSessions(p)
	if err != nil {
		return fail(err)
	}
	found := false
	for _, session := range sessions {
		if session.ID == room.HostSessionID {
			if session.RoomID != room.ID || found {
				return fail(fmt.Errorf("host session identity mismatch"))
			}
			found = true
		}
	}
	if !found {
		return fail(fmt.Errorf("host session record missing"))
	}
	data, runErr := h.localCLI(ctx, []string{"stop", "--session", room.HostSessionID})
	var result struct {
		Status string    `json:"status"`
		Error  *apiError `json:"error"`
	}
	decodeErr := json.Unmarshal(data, &result)
	if runErr == nil && decodeErr == nil && result.Error != nil {
		runErr = result.Error
	}
	if err = waitRoomStopped(ctx, p, room, runErr); err != nil {
		if decodeErr != nil {
			err = errors.Join(err, decodeErr)
		}
		return fail(err)
	}
	return h.recordRoomCleanup(room, "complete", "", true)
}

func (h *HostOperations) recordRoomCleanup(expected Room, status, message string, closed bool) error {
	return h.Store.locked(func() error {
		current, err := h.Store.readRoom(expected.ID)
		if err != nil {
			return err
		}
		if current.HostSessionID != expected.HostSessionID || current.Host != expected.Host || current.Source != expected.Source {
			return fmt.Errorf("host room incarnation changed")
		}
		if current.Closed && !closed {
			return nil
		}
		current.CleanupStatus, current.CleanupError = status, message
		current.Closed = current.Closed || closed
		dir, err := h.Store.recordDir("rooms")
		if err != nil {
			return err
		}
		return writeJSON(filepath.Join(dir, expected.ID+".json"), current)
	})
}

func waitRoomStopped(ctx context.Context, p paths.Paths, room Room, stopErr error) error {
	log := eventlog.New(runtime.RoomEventsPath(p, room.ID))
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		events, err := log.ReadAll()
		if err != nil {
			return err
		}
		stopped := false
		for _, event := range events {
			if event.Type == model.EventRoomStopped && event.RoomID == room.ID {
				stopped = true
			}
		}
		if stopped && !liveSocket(ctx, runtime.ServerSocketPath(p, room.ID)) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if stopErr != nil {
				return fmt.Errorf("stop request failed and shutdown was not confirmed: %w", stopErr)
			}
			return fmt.Errorf("room shutdown was not confirmed")
		case <-ticker.C:
		}
	}
}

func (b *Broker) ReconcileTerminals(ctx context.Context) error {
	local := Peer{Machine: Machine{ID: b.Client.LocalMachineID}}
	var failures []error
	if err := b.reconcileTerminalPeer(ctx, local); err != nil {
		failures = append(failures, err)
	}
	state, err := b.Store.Read()
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	for _, peer := range state.Peers {
		if !peer.Machine.Enabled {
			continue
		}
		if err = b.reconcileTerminalPeer(ctx, peer); err != nil {
			failures = append(failures, fmt.Errorf("peer %s terminal cleanup: %w", peer.Machine.ID, err))
		}
	}
	return errors.Join(failures...)
}

func (b *Broker) reconcileTerminalPeer(ctx context.Context, peer Peer) error {
	if peer.Machine.ID == b.Client.LocalMachineID {
		return (&HostOperations{Store: b.Store, Client: b.Client}).ReconcileTerminal(ctx)
	}
	_, err := b.Client.Call(ctx, peer, RPCRequest{Operation: "cleanup-terminals", Session: peer.Machine.Session, SessionKey: peer.Config.SessionKey, ControllerID: peer.Config.ControllerID})
	return err
}
