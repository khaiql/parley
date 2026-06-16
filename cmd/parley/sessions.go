package main

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/adapter"
	"github.com/khaiql/parley/internal/descriptor"
	"github.com/khaiql/parley/internal/paths"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
)

func sessionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "List local Parley sessions",
		Args:  noArgsJSON,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := paths.New(paths.DefaultRoot())
			sessions, err := parleyRuntime.ListSessions(p)
			if err != nil {
				return writeJSONError(cmd, "runtime_error", err.Error())
			}
			items := make([]sessionListItem, 0, len(sessions))
			for _, session := range sessions {
				items = append(items, buildSessionListItem(p, session))
			}
			return writeJSON(cmd, struct {
				Status   string            `json:"status"`
				Sessions []sessionListItem `json:"sessions"`
			}{Status: "sessions", Sessions: items})
		},
	}
}

type sessionListItem struct {
	SessionID       string `json:"session_id"`
	RoomID          string `json:"room_id"`
	Name            string `json:"name"`
	Role            string `json:"role,omitempty"`
	Descriptor      string `json:"descriptor,omitempty"`
	Status          string `json:"status,omitempty"`
	AdapterRunning  bool   `json:"adapter_running"`
	ServerRunning   bool   `json:"server_running"`
	LastReceivedSeq int64  `json:"last_received_seq,omitempty"`
	LastSeenSeq     int64  `json:"last_seen_seq,omitempty"`
	CommandArgs     string `json:"command_args"`
}

func buildSessionListItem(p paths.Paths, session parleyRuntime.Session) sessionListItem {
	part := participation{paths: p, room: session.RoomID, name: session.Name}
	meta := loadSessionParticipantMeta(p, session)
	desc := meta.Descriptor
	if desc == "" {
		desc = loadSessionDescriptor(p, session.RoomID)
	}
	status := statusEnvelope(part, meta)
	return sessionListItem{
		SessionID:       session.ID,
		RoomID:          session.RoomID,
		Name:            session.Name,
		Role:            meta.Role,
		Descriptor:      desc,
		Status:          meta.Status,
		AdapterRunning:  status.AdapterRunning,
		ServerRunning:   status.ServerRunning,
		LastReceivedSeq: status.LastReceivedSeq,
		LastSeenSeq:     status.LastSeenSeq,
		CommandArgs:     "--session " + session.ID,
	}
}

func loadSessionParticipantMeta(p paths.Paths, session parleyRuntime.Session) adapter.Meta {
	store, err := parleyRuntime.ParticipantStore(p, session.RoomID, session.Name)
	if err != nil {
		return adapter.Meta{}
	}
	meta, err := store.LoadMeta()
	if err != nil {
		return adapter.Meta{}
	}
	return meta
}

func loadSessionDescriptor(p paths.Paths, roomID string) string {
	room, err := parleyRuntime.LoadRoomRuntime(p, roomID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if room.LocalHost == "" || room.LocalPort == 0 || room.RoomID == "" {
		return ""
	}
	return descriptor.Descriptor{Host: room.LocalHost, Port: room.LocalPort, RoomID: room.RoomID}.String()
}
