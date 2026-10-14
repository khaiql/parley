package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrFormCancelled = errors.New("cancelled")

// ReadForm treats terminal editing as input, never as an executable command.
func ReadForm(r io.Reader, op string) (string, error) { return ReadTerminalForm(r, io.Discard, op) }

// ReadTerminalForm adds display feedback without depending on terminal echo mode.
func ReadTerminalForm(r io.Reader, out io.Writer, op string) (string, error) {
	if op != "start" && op != "join" {
		return "", fmt.Errorf("unsupported action")
	}
	input := []byte{}
	reader := bufio.NewReader(r)
	for {
		b, err := reader.ReadByte()
		if errors.Is(err, io.EOF) {
			return "", ErrFormCancelled
		}
		if err != nil {
			return "", err
		}
		switch b {
		case 27, 3, 4:
			return "", ErrFormCancelled
		case '\r', '\n':
			fmt.Fprint(out, "\r\n")
			value := strings.TrimSpace(string(input))
			if value == "" || !utf8.Valid(input) {
				return "", fmt.Errorf("nonempty UTF-8 input required")
			}
			if op == "join" {
				if err := safeID(value); err != nil {
					return "", err
				}
			}
			return value, nil
		case 8, 127:
			if len(input) != 0 {
				_, n := utf8.DecodeLastRune(input)
				input = input[:len(input)-n]
				fmt.Fprint(out, "\b \b")
			}
		default:
			if b < 32 {
				return "", fmt.Errorf("control character rejected")
			}
			input = append(input, b)
			_, _ = out.Write([]byte{b})
			if len(input) > 512 {
				return "", fmt.Errorf("input exceeds 512 bytes")
			}
		}
	}
}

func DecodeTarget(input, socket string) (AgentTarget, error) {
	var target AgentTarget
	if len(input) > requestLimit || !utf8.ValidString(input) {
		return target, fmt.Errorf("invalid target")
	}
	d := json.NewDecoder(strings.NewReader(input))
	d.DisallowUnknownFields()
	if err := d.Decode(&target); err != nil {
		return target, err
	}
	if d.Decode(new(any)) != io.EOF {
		return target, fmt.Errorf("exactly one target required")
	}
	if target.HerdrSocket != socket {
		return target, fmt.Errorf("session_mismatch")
	}
	err := validateRequest(Request{ID: "form", Op: "join", RoomID: "identity", Target: target})
	return target, err
}

func (c *Client) OpenForm(ctx context.Context, op string, target AgentTarget) error {
	if op != "start" && op != "join" {
		return fmt.Errorf("unsupported action")
	}
	b, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if _, err = DecodeTarget(string(b), c.HerdrSocket); err != nil {
		return err
	}
	_, err = socketAPI(ctx, c.HerdrSocket, "plugin.pane.open", map[string]any{
		"plugin_id": PluginID, "entrypoint": "form", "placement": "popup", "focus": true,
		"env": map[string]string{"PARLEY_ACTION": op, "PARLEY_TARGET_JSON": string(b)},
	})
	return err
}

// SubmitForm owns direct delivery; cross-host mutation belongs only to the coordinator.
func (h HostSession) SubmitForm(ctx context.Context, c *Client, req Request) (Response, error) {
	if err := validateRequest(req); err != nil {
		return Response{}, err
	}
	owner, err := h.Owner(ctx)
	if err != nil {
		return Response{}, err
	}
	origin := Peer{Machine: Machine{ID: owner.MachineID}, Config: owner}
	normalized, err := normalize(req, origin)
	if err != nil {
		return Response{}, err
	}
	c.LocalMachineID = owner.MachineID
	c.HerdrSocket = owner.HerdrSocket
	if err = h.PinTrustedRuntime(c); err != nil {
		return Response{}, err
	}
	operations := HostOperations{Store: h.Store, Client: c}
	if req.Op == "start" {
		return operations.Start(ctx, normalized)
	}
	room, roomErr := h.Store.Room(req.RoomID)
	direct := roomErr == nil && room.Host.HerdrSocket == owner.HerdrSocket && (room.Host.MachineID == "self" || room.Host.MachineID == owner.MachineID)
	if roomErr != nil && !errors.Is(roomErr, os.ErrNotExist) {
		return Response{}, roomErr
	}
	if !direct && owner.ControllerID == "" {
		return failure("setup_required", fmt.Errorf("run parley herdr setup on the local controller and enable its saved SSH profile")), nil
	}
	// Persist self so later activation can normalize this host to its saved profile.
	req.Target.MachineID = "self"
	if err = h.Store.Enqueue(req); err != nil {
		return Response{}, err
	}
	if direct {
		return h.directForm(ctx, operations, owner, normalized, room)
	}
	if err = c.SignalRequest(ctx, req.Target, req.ID); err != nil {
		return Response{Status: "pending", Code: "signal_failed", Message: "Request " + req.ID + " saved: " + err.Error()}, nil
	}
	if owner.Role == "controller" {
		path, err := h.CoordinatorSocket()
		if err != nil {
			return Response{}, err
		}
		// Socket submission wakes the local coordinator; the queue remains authoritative.
		_, _ = CallLocal(ctx, path, normalized)
	}
	return h.WaitOutcome(ctx, req.ID)
}
func (h HostSession) directForm(ctx context.Context, operations HostOperations, owner SessionConfig, req Request, room Room) (Response, error) {
	if owner.ControllerID == "" {
		owner.ControllerID = "direct_" + owner.SessionKey
	}
	outcome, err := h.Store.ClaimRequest(req, owner.ControllerID)
	if err != nil {
		return Response{}, err
	}
	if outcome.Phase == "complete" {
		return hostResponse(outcome.Response, owner.MachineID), nil
	}
	room, err = operations.liveRoom(ctx, room)
	if err != nil {
		return Response{}, err
	}
	response, err := operations.Join(ctx, req, room.Source)
	if err != nil {
		return Response{}, err
	}
	outcome.Response = hostResponse(response, "self")
	outcome.Phase = "complete"
	if response.Code == "operation_uncertain" || response.Code == "participant_disconnected" {
		outcome.Phase = "uncertain"
	}
	if err = h.Store.Complete(req.ID, outcome); err != nil {
		return Response{}, err
	}
	return response, nil
}
func (h HostSession) WaitOutcome(ctx context.Context, id string) (Response, error) {
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for {
		outcome, err := h.Store.readOutcome(id)
		if err == nil && (outcome.Phase == "complete" || outcome.Phase == "uncertain") {
			return outcome.Response, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Response{}, err
		}
		select {
		case <-ctx.Done():
			return Response{Status: "pending", Message: "Request " + id + " retained. Inspect parley herdr status before retrying."}, nil
		case <-timer.C:
		}
	}
}
