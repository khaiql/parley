package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/adapter"
	"github.com/khaiql/parley/internal/artifact"
	"github.com/khaiql/parley/internal/descriptor"
	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/jsonout"
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
	var sessionID string
	var files []string

	cmd := &cobra.Command{
		Use:   "send [message]",
		Short: "Send a message to the room",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return writeJSONError(cmd, "invalid_arguments", "send accepts at most one message argument")
			}
			text := ""
			if len(args) == 1 {
				text = args[0]
			}
			if text == "" && len(files) == 0 {
				return writeJSONError(cmd, "invalid_arguments", "send requires a message or at least one --file")
			}
			fileResults, err := validateSendFiles(files)
			if err != nil {
				code := "invalid_artifacts"
				message := err.Error()
				var artifactErr artifact.Error
				if errors.As(err, &artifactErr) {
					code = artifactErr.Code
					message = artifactErr.Message
				}
				return writeArtifactSendError(cmd, code, message, fileResults)
			}
			return callParticipantControl(cmd, roomID, name, sessionID, adapter.ControlRequest{Type: "send", Text: text, Files: files})
		},
	}
	cmd.Flags().StringArrayVar(&files, "file", nil, "File artifact to send")
	addParticipationFlags(cmd, &roomID, &name, &sessionID)
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
	var sessionID string

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the room",
		Args:  noArgsJSON,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := paths.New(paths.DefaultRoot())
			resolvedRoomID, err := resolveRoomID(p, roomID, name, sessionID)
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
			if !resp.OK {
				return writeJSONError(cmd, "server_error", resp.Error)
			}
			return writeJSON(cmd, resp)
		},
	}
	addParticipationFlags(cmd, &roomID, &name, &sessionID)
	return cmd
}

func newParticipantCommand(name, short string) *cobra.Command {
	var roomID string
	var participantName string
	var sessionID string

	cmd := &cobra.Command{
		Use:   name,
		Short: short,
		Args:  noArgsJSON,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return notImplemented(cmd, name)
		},
	}
	addParticipationFlags(cmd, &roomID, &participantName, &sessionID)
	return cmd
}

func addParticipationFlags(cmd *cobra.Command, roomID, name, sessionID *string) {
	cmd.Flags().StringVar(roomID, "room", "", "Room ID")
	cmd.Flags().StringVar(name, "name", "", "Participant name")
	cmd.Flags().StringVar(sessionID, "session", "", "Session ID returned by start or join")
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
	if events == nil {
		events = []model.Event{}
	}
	status := "empty"
	if len(events) > 0 {
		status = "unread"
	}
	return writeJSON(cmd, struct {
		Status string        `json:"status"`
		Events []model.Event `json:"events"`
	}{Status: status, Events: events})
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
		Status string        `json:"status"`
		Events []model.Event `json:"events"`
	}{Status: "history", Events: events})
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
	sessionID, _ := cmd.Flags().GetString("session")
	return callParticipantControl(cmd, roomID, name, sessionID, req)
}

func callParticipantControl(cmd *cobra.Command, roomID, name, sessionID string, req adapter.ControlRequest) error {
	p := paths.New(paths.DefaultRoot())
	part, err := resolveParticipation(p, roomID, name, sessionID)
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
	if !resp.OK {
		if req.Type == "send" && len(req.Files) > 0 {
			return writeArtifactSendError(cmd, "artifact_upload_failed", resp.Error, resp.Files)
		}
		return writeJSONError(cmd, "adapter_error", resp.Error)
	}
	if req.Type == "wait" && resp.Status == "" {
		resp.Status = "ready"
	}
	return writeJSON(cmd, resp)
}

func writeArtifactSendError(cmd *cobra.Command, code, message string, files []adapter.ArtifactFileResult) error {
	if strings.TrimSpace(message) == "" {
		message = "one or more artifacts failed to upload; no message was sent"
	}
	resp := struct {
		Status string `json:"status"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Files            []adapter.ArtifactFileResult `json:"files,omitempty"`
		MessageCommitted bool                         `json:"message_committed"`
	}{
		Status:           "error",
		Files:            files,
		MessageCommitted: false,
	}
	resp.Error.Code = code
	resp.Error.Message = message
	out, err := jsonout.Marshal(resp)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(cmd.OutOrStderr(), string(out)); err != nil {
		return err
	}
	return cliError{code: code, message: message}
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func resolveParticipationFromFlags(cmd *cobra.Command) (participation, error) {
	roomID, _ := cmd.Flags().GetString("room")
	name, _ := cmd.Flags().GetString("name")
	sessionID, _ := cmd.Flags().GetString("session")
	p := paths.New(paths.DefaultRoot())
	part, err := resolveParticipation(p, roomID, name, sessionID)
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

func resolveParticipation(p paths.Paths, roomID, name, sessionID string) (participation, error) {
	if sessionID != "" && (roomID != "" || name != "") {
		return participation{}, participationError{
			code:    "ambiguous_participation",
			message: "pass --session, pass both --room and --name, or omit all to use the active participation",
		}
	}
	if (roomID == "") != (name == "") {
		return participation{}, participationError{
			code:    "ambiguous_participation",
			message: "pass --session, pass both --room and --name, or omit all to use the active participation",
		}
	}
	if sessionID != "" {
		session, err := parleyRuntime.LoadSession(p, sessionID)
		if err != nil {
			return participation{}, participationError{code: "no_active_participation", message: fmt.Sprintf("session is not available: %v", err)}
		}
		roomID = session.RoomID
		name = session.Name
	}
	usedActive := sessionID == "" && roomID == "" && name == ""
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
	if usedActive {
		names, err := localParticipantNames(p, roomID)
		if err != nil {
			return participation{}, participationError{code: "runtime_error", message: err.Error()}
		}
		if len(names) > 1 {
			return participation{}, participationError{
				code:    "ambiguous_participation",
				message: fmt.Sprintf("multiple local participants in room %s (%s); pass --room and --name", roomID, strings.Join(names, ", ")),
			}
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

func localParticipantNames(p paths.Paths, roomID string) ([]string, error) {
	roomDir, err := p.RoomDir(roomID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(roomDir, "participants"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".events.json") {
			continue
		}
		names = append(names, strings.TrimSuffix(name, ".json"))
	}
	sort.Strings(names)
	return names, nil
}

func resolveRoomID(p paths.Paths, roomID, name, sessionID string) (string, error) {
	if sessionID != "" {
		if roomID != "" || name != "" {
			return "", fmt.Errorf("pass --session, pass --room, or omit both to use the active room")
		}
		session, err := parleyRuntime.LoadSession(p, sessionID)
		if err != nil {
			return "", fmt.Errorf("session is not available: %v", err)
		}
		return session.RoomID, nil
	}
	if name != "" {
		return "", fmt.Errorf("participant name requires --session or --room")
	}
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
		Status            string                    `json:"status"`
		RoomID            string                    `json:"room_id"`
		Descriptor        string                    `json:"descriptor,omitempty"`
		LocalHost         string                    `json:"local_host,omitempty"`
		LocalPort         int                       `json:"local_port,omitempty"`
		ArtifactLocalPort int                       `json:"artifact_local_port,omitempty"`
		ArtifactPath      string                    `json:"artifact_path,omitempty"`
		ArtifactLimits    interface{}               `json:"artifact_limits,omitempty"`
		ActiveParticipant string                    `json:"active_participant,omitempty"`
		Participant       adapter.Meta              `json:"participant"`
		State             participantStatusEnvelope `json:"state"`
	}{
		Status:            "info",
		RoomID:            part.room,
		Descriptor:        desc,
		LocalHost:         room.LocalHost,
		LocalPort:         room.LocalPort,
		ArtifactLocalPort: room.ArtifactLocalPort,
		ArtifactPath:      room.ArtifactPath,
		ArtifactLimits:    room.ArtifactLimits,
		ActiveParticipant: part.name,
		Participant:       meta,
		State:             statusEnvelope(part, meta),
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
	status := meta.Status
	if status == "" {
		status = "unknown"
	}
	return struct {
		Status      string                    `json:"status"`
		RoomID      string                    `json:"room_id"`
		Name        string                    `json:"name"`
		State       participantStatusEnvelope `json:"state"`
		Participant adapter.Meta              `json:"participant"`
	}{
		Status:      status,
		RoomID:      part.room,
		Name:        part.name,
		State:       statusEnvelope(part, meta),
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

func validateSendFiles(files []string) ([]adapter.ArtifactFileResult, error) {
	if len(files) > artifact.MaxFilesPerMessage {
		err := artifact.Error{Code: "too_many_artifacts", Message: fmt.Sprintf("at most %d artifacts are allowed", artifact.MaxFilesPerMessage)}
		return artifactErrorResults(files, err), err
	}
	var total int64
	var validationErrs []error
	results := make([]adapter.ArtifactFileResult, 0, len(files))
	for _, path := range files {
		info, err := artifact.ValidateLocalFile(path)
		if err != nil {
			validationErrs = append(validationErrs, err)
			results = append(results, artifactFileErrorResult(path, err))
			continue
		}
		total += info.Size
		if total > artifact.MaxTotalBytesPerMessage {
			err := artifact.Error{Code: "artifact_batch_too_large", Message: fmt.Sprintf("artifact batch exceeds %d bytes", artifact.MaxTotalBytesPerMessage)}
			return artifactErrorResults(files, err), err
		}
	}
	if len(validationErrs) == 1 {
		return results, validationErrs[0]
	}
	if len(validationErrs) > 1 {
		messages := make([]string, 0, len(validationErrs))
		for _, err := range validationErrs {
			messages = append(messages, err.Error())
		}
		return results, artifact.Error{Code: "invalid_artifacts", Message: strings.Join(messages, "; ")}
	}
	return nil, nil
}

func artifactErrorResults(files []string, err error) []adapter.ArtifactFileResult {
	results := make([]adapter.ArtifactFileResult, 0, len(files))
	for _, path := range files {
		results = append(results, artifactFileErrorResult(path, err))
	}
	return results
}

func artifactFileErrorResult(path string, err error) adapter.ArtifactFileResult {
	return adapter.ArtifactFileResult{
		Path:   path,
		Status: "error",
		Error:  controlErrorFromError(err, "invalid_artifacts"),
	}
}
