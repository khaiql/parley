package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/adapter"
	"github.com/khaiql/parley/internal/descriptor"
	"github.com/khaiql/parley/internal/paths"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
)

func joinCmd() *cobra.Command {
	var name string
	var role string
	var dir string
	var repo string

	cmd := &cobra.Command{
		Use:   "join <descriptor>",
		Short: "Join a Parley room",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return writeJSONError(cmd, "invalid_arguments", "join requires exactly one descriptor")
			}
			desc, err := descriptor.Parse(args[0])
			if err != nil {
				return writeJSONError(cmd, "invalid_descriptor", fmt.Sprintf("invalid descriptor: %v", err))
			}
			if name == "" {
				generated, err := generatedParticipantName()
				if err != nil {
					return writeJSONError(cmd, "runtime_error", fmt.Sprintf("generate participant name: %v", err))
				}
				name = generated
			}
			pid, err := launchParticipantDaemon(participantDaemonConfig{
				Descriptor: desc,
				Name:       name,
				Role:       role,
				Directory:  dir,
				Repo:       repo,
			})
			if err != nil {
				return writeJSONError(cmd, "runtime_error", fmt.Sprintf("start participant daemon: %v", err))
			}
			p := paths.New(paths.DefaultRoot())
			if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: desc.RoomID, Name: name}); err != nil {
				return writeJSONError(cmd, "runtime_error", fmt.Sprintf("save active participation: %v", err))
			}
			sessionID, err := parleyRuntime.NewSessionID()
			if err != nil {
				return writeJSONError(cmd, "runtime_error", fmt.Sprintf("create session id: %v", err))
			}
			if err := parleyRuntime.SaveSession(p, parleyRuntime.Session{ID: sessionID, RoomID: desc.RoomID, Name: name}); err != nil {
				return writeJSONError(cmd, "runtime_error", fmt.Sprintf("save session: %v", err))
			}
			store, err := parleyRuntime.ParticipantStore(p, desc.RoomID, name)
			if err != nil {
				return writeJSONError(cmd, "runtime_error", err.Error())
			}
			meta, err := store.LoadMeta()
			if err != nil {
				return writeJSONError(cmd, "runtime_error", fmt.Sprintf("load participant metadata: %v", err))
			}
			return writeJSON(cmd, struct {
				Status      string       `json:"status"`
				RoomID      string       `json:"room_id"`
				Name        string       `json:"name"`
				SessionID   string       `json:"session_id"`
				CommandArgs string       `json:"command_args"`
				Descriptor  string       `json:"descriptor"`
				PID         int          `json:"pid"`
				Participant adapter.Meta `json:"participant"`
			}{
				Status:      "joined",
				RoomID:      desc.RoomID,
				Name:        name,
				SessionID:   sessionID,
				CommandArgs: "--session " + sessionID,
				Descriptor:  desc.String(),
				PID:         pid,
				Participant: meta,
			})
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Participant name (generated when omitted)")
	cmd.Flags().StringVar(&role, "role", "participant", "Participant role")
	cmd.Flags().StringVar(&dir, "dir", "", "Participant working directory")
	cmd.Flags().StringVar(&repo, "repo", "", "Participant repository URL")

	return cmd
}
