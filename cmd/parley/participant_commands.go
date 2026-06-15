package main

import (
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/adapter"
	"github.com/khaiql/parley/internal/descriptor"
	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
	"github.com/khaiql/parley/internal/paths"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
)

func infoCmd() *cobra.Command {
	cmd := newParticipantCommand("info", "Print local participant metadata")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runInfo(cmd, args)
	}
	return cmd
}

func statusCmd() *cobra.Command {
	cmd := newParticipantCommand("status", "Print room status")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runStatus(cmd, args)
	}
	return cmd
}

func inboxCmd() *cobra.Command {
	cmd := newParticipantCommand("inbox", "Print unseen room events")
	cmd.Flags().Bool("peek", false, "Do not advance the seen cursor")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runInbox(cmd, args)
	}
	return cmd
}

func historyCmd() *cobra.Command {
	cmd := newParticipantCommand("history", "Print room history")
	cmd.Flags().Int("limit", 0, "Maximum number of history events")
	cmd.Flags().Bool("all", false, "Return all retained history")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runHistory(cmd, args)
	}
	return cmd
}

func waitCmd() *cobra.Command {
	cmd := newParticipantCommand("wait", "Wait for unseen room events")
	cmd.Flags().Duration("timeout", 0, "Maximum time to wait")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runAdapterControl(cmd, args, adapter.ControlRequest{Type: "wait"}, true)
	}
	return cmd
}

func sendCmd() *cobra.Command {
	var roomID string
	var name string

	cmd := &cobra.Command{
		Use:   "send <message>",
		Short: "Send a message to the room",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return writeJSONError(cmd, "invalid_arguments", "send requires exactly one message argument")
			}
			return callParticipantControl(cmd, roomID, name, adapter.ControlRequest{Type: "send", Text: args[0]})
		},
	}
	addParticipationFlags(cmd, &roomID, &name)
	return cmd
}

func leaveCmd() *cobra.Command {
	cmd := newParticipantCommand("leave", "Leave the room")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runAdapterControl(cmd, args, adapter.ControlRequest{Type: "leave"}, false)
	}
	return cmd
}

func stopCmd() *cobra.Command {
	var roomID string
	var name string

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the room",
		Args:  noArgsJSON,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := paths.New(paths.DefaultRoot())
			resolvedRoomID, err := resolveRoomID(p, roomID)
			if err != nil {
				return writeJSONError(cmd, "no_active_room", err.Error())
			}
			socketPath := parleyRuntime.ServerSocketPath(p, resolvedRoomID)
			if _, err := os.Stat(socketPath); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return writeJSONError(cmd, "server_not_running", "server control socket is not available")
				}
				return writeJSONError(cmd, "runtime_error", err.Error())
			}
			resp, err := adapter.CallControl(socketPath, adapter.ControlRequest{Type: "stop"})
			if err != nil {
				return writeJSONError(cmd, "server_not_running", fmt.Sprintf("server control socket is not reachable: %v", err))
			}
			return writeJSON(cmd, resp)
		},
	}
	addParticipationFlags(cmd, &roomID, &name)
	return cmd
}

func newParticipantCommand(name, short string) *cobra.Command {
	var roomID string
	var participantName string

	cmd := &cobra.Command{
		Use:   name,
		Short: short,
		Args:  noArgsJSON,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented(cmd, name)
		},
	}
	addParticipationFlags(cmd, &roomID, &participantName)
	return cmd
}

func addParticipationFlags(cmd *cobra.Command, roomID, name *string) {
	cmd.Flags().StringVar(roomID, "room", "", "Room ID")
	cmd.Flags().StringVar(name, "name", "", "Participant name")
}

type participation struct {
	paths paths.Paths
	room  string
	name  string
}

type participationError struct {
	code    string
	message string
}

func (e participationError) Error() string {
	return e.message
}

func runInfo(cmd *cobra.Command, args []string) error {
	if err := noArgsJSON(cmd, args); err != nil {
		return err
	}
	part, err := resolveParticipationFromFlags(cmd)
	if err != nil {
		return err
	}
	room, err := parleyRuntime.LoadRoomRuntime(part.paths, part.room)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return writeJSONError(cmd, "runtime_error", fmt.Sprintf("load room runtime: %v", err))
	}
	meta, err := loadParticipantMeta(part)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return writeJSONError(cmd, "runtime_error", fmt.Sprintf("load participant metadata: %v", err))
	}
	return writeJSON(cmd, infoResponse(part, room, meta))
}

func runStatus(cmd *cobra.Command, args []string) error {
	if err := noArgsJSON(cmd, args); err != nil {
		return err
	}
	part, err := resolveParticipationFromFlags(cmd)
	if err != nil {
		return err
	}
	meta, err := loadParticipantMeta(part)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return writeJSONError(cmd, "runtime_error", fmt.Sprintf("load participant metadata: %v", err))
	}
	return writeJSON(cmd, statusResponse(part, meta))
}

func runInbox(cmd *cobra.Command, args []string) error {
	if err := noArgsJSON(cmd, args); err != nil {
		return err
	}
	part, err := resolveParticipationFromFlags(cmd)
	if err != nil {
		return err
	}
	peek, err := cmd.Flags().GetBool("peek")
	if err != nil {
		return writeJSONError(cmd, "invalid_arguments", err.Error())
	}
	store, err := parleyRuntime.ParticipantStore(part.paths, part.room, part.name)
	if err != nil {
		return writeJSONError(cmd, "invalid_arguments", err.Error())
	}
	events, err := store.Inbox(peek)
	if err != nil {
		return writeJSONError(cmd, "runtime_error", err.Error())
	}
	return writeJSON(cmd, adapter.ControlResponse{OK: true, Status: "ok", Events: events})
}

func runHistory(cmd *cobra.Command, args []string) error {
	if err := noArgsJSON(cmd, args); err != nil {
		return err
	}
	part, err := resolveParticipationFromFlags(cmd)
	if err != nil {
		return err
	}
	limit, err := cmd.Flags().GetInt("limit")
	if err != nil {
		return writeJSONError(cmd, "invalid_arguments", err.Error())
	}
	all, err := cmd.Flags().GetBool("all")
	if err != nil {
		return writeJSONError(cmd, "invalid_arguments", err.Error())
	}
	if limit < 0 {
		return writeJSONError(cmd, "invalid_arguments", "history --limit must be non-negative")
	}

	events, err := readHistoryEvents(part, all, limit)
	if err != nil {
		return writeJSONError(cmd, "runtime_error", err.Error())
	}
	return writeJSON(cmd, struct {
		OK     bool          `json:"ok"`
		Status string        `json:"status"`
		Events []model.Event `json:"events"`
	}{OK: true, Status: "ok", Events: events})
}

func runAdapterControl(cmd *cobra.Command, args []string, req adapter.ControlRequest, includeTimeout bool) error {
	if err := noArgsJSON(cmd, args); err != nil {
		return err
	}
	if includeTimeout {
		timeout, err := cmd.Flags().GetDuration("timeout")
		if err != nil {
			return writeJSONError(cmd, "invalid_arguments", err.Error())
		}
		if timeout < 0 {
			return writeJSONError(cmd, "invalid_arguments", "wait --timeout must be non-negative")
		}
		if timeout == 0 {
			return writeJSONError(cmd, "missing_required_flag", "wait requires --timeout")
		}
		req.Timeout = timeout.String()
	}
	roomID, _ := cmd.Flags().GetString("room")
	name, _ := cmd.Flags().GetString("name")
	return callParticipantControl(cmd, roomID, name, req)
}

func callParticipantControl(cmd *cobra.Command, roomID, name string, req adapter.ControlRequest) error {
	p := paths.New(paths.DefaultRoot())
	part, err := resolveParticipation(p, roomID, name)
	if err != nil {
		return writeJSONError(cmd, participationErrorCode(err), err.Error())
	}
	socketPath := parleyRuntime.ParticipantSocketPath(part.paths, part.room, part.name)
	if _, err := os.Stat(socketPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return writeJSONError(cmd, "adapter_not_running", "participant adapter socket is not available")
		}
		return writeJSONError(cmd, "runtime_error", err.Error())
	}
	resp, err := adapter.CallControl(socketPath, req)
	if err != nil {
		if req.Type == "wait" && isTimeout(err) {
			return writeJSON(cmd, adapter.ControlResponse{OK: true, Status: "timeout"})
		}
		return writeJSONError(cmd, "adapter_not_running", fmt.Sprintf("participant adapter socket is not reachable: %v", err))
	}
	return writeJSON(cmd, resp)
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func resolveParticipationFromFlags(cmd *cobra.Command) (participation, error) {
	roomID, _ := cmd.Flags().GetString("room")
	name, _ := cmd.Flags().GetString("name")
	p := paths.New(paths.DefaultRoot())
	part, err := resolveParticipation(p, roomID, name)
	if err != nil {
		return participation{}, writeJSONError(cmd, participationErrorCode(err), err.Error())
	}
	return part, nil
}

func participationErrorCode(err error) string {
	var partErr participationError
	if errors.As(err, &partErr) {
		return partErr.code
	}
	return "no_active_participation"
}

func resolveParticipation(p paths.Paths, roomID, name string) (participation, error) {
	if (roomID == "") != (name == "") {
		return participation{}, participationError{
			code:    "ambiguous_participation",
			message: "pass both --room and --name, or omit both to use the active participation",
		}
	}
	if roomID == "" || name == "" {
		active, err := parleyRuntime.LoadActive(p)
		if err != nil {
			return participation{}, fmt.Errorf("active participation is not available; pass --room and --name")
		}
		if roomID == "" {
			roomID = active.RoomID
		}
		if name == "" {
			name = active.Name
		}
	}
	if roomID == "" || name == "" {
		return participation{}, fmt.Errorf("room and participant name are required")
	}
	if _, err := parleyRuntime.ParticipantStore(p, roomID, name); err != nil {
		return participation{}, err
	}
	return participation{paths: p, room: roomID, name: name}, nil
}

func resolveRoomID(p paths.Paths, roomID string) (string, error) {
	if roomID != "" {
		if err := paths.ValidateRoomID(roomID); err != nil {
			return "", err
		}
		return roomID, nil
	}
	active, err := parleyRuntime.LoadActive(p)
	if err != nil {
		return "", fmt.Errorf("active room is not available; pass --room")
	}
	return active.RoomID, nil
}

func loadParticipantMeta(part participation) (adapter.Meta, error) {
	store, err := parleyRuntime.ParticipantStore(part.paths, part.room, part.name)
	if err != nil {
		return adapter.Meta{}, err
	}
	return store.LoadMeta()
}

func readHistoryEvents(part participation, all bool, limit int) ([]model.Event, error) {
	events, err := eventlog.New(parleyRuntime.RoomEventsPath(part.paths, part.room)).ReadAll()
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		events, err = eventlog.New(parleyRuntime.ParticipantEventsPath(part.paths, part.room, part.name)).ReadAll()
		if err != nil {
			return nil, err
		}
	}

	transcript := make([]model.Event, 0, len(events))
	for _, ev := range events {
		if ev.Type.IsTranscript() {
			transcript = append(transcript, ev)
		}
	}
	if all {
		return transcript, nil
	}
	if limit == 0 {
		limit = 50
	}
	if len(transcript) <= limit {
		return transcript, nil
	}
	return transcript[len(transcript)-limit:], nil
}

func infoResponse(part participation, room parleyRuntime.RoomRuntime, meta adapter.Meta) interface{} {
	desc := ""
	if room.LocalHost != "" && room.LocalPort != 0 && room.RoomID != "" {
		desc = descriptor.Descriptor{Host: room.LocalHost, Port: room.LocalPort, RoomID: room.RoomID}.String()
	}
	return struct {
		OK                bool                      `json:"ok"`
		RoomID            string                    `json:"room_id"`
		Descriptor        string                    `json:"descriptor,omitempty"`
		LocalHost         string                    `json:"local_host,omitempty"`
		LocalPort         int                       `json:"local_port,omitempty"`
		ActiveParticipant string                    `json:"active_participant,omitempty"`
		Participant       adapter.Meta              `json:"participant"`
		Status            participantStatusEnvelope `json:"status"`
	}{
		OK:                true,
		RoomID:            part.room,
		Descriptor:        desc,
		LocalHost:         room.LocalHost,
		LocalPort:         room.LocalPort,
		ActiveParticipant: part.name,
		Participant:       meta,
		Status:            statusEnvelope(part, meta),
	}
}

type participantStatusEnvelope struct {
	ParticipantStatus string `json:"participant_status,omitempty"`
	AdapterRunning    bool   `json:"adapter_running"`
	ServerRunning     bool   `json:"server_running"`
	LastReceivedSeq   int64  `json:"last_received_seq,omitempty"`
	LastSeenSeq       int64  `json:"last_seen_seq,omitempty"`
}

func statusResponse(part participation, meta adapter.Meta) interface{} {
	return struct {
		OK          bool                      `json:"ok"`
		RoomID      string                    `json:"room_id"`
		Name        string                    `json:"name"`
		Status      participantStatusEnvelope `json:"status"`
		Participant adapter.Meta              `json:"participant"`
	}{
		OK:          true,
		RoomID:      part.room,
		Name:        part.name,
		Status:      statusEnvelope(part, meta),
		Participant: meta,
	}
}

func statusEnvelope(part participation, meta adapter.Meta) participantStatusEnvelope {
	return participantStatusEnvelope{
		ParticipantStatus: meta.Status,
		AdapterRunning:    socketExists(parleyRuntime.ParticipantSocketPath(part.paths, part.room, part.name)),
		ServerRunning:     socketExists(parleyRuntime.ServerSocketPath(part.paths, part.room)),
		LastReceivedSeq:   meta.LastReceivedSeq,
		LastSeenSeq:       meta.LastSeenSeq,
	}
}

func socketExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}
