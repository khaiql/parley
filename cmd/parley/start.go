package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/paths"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
)

func startCmd() *cobra.Command {
	var name string
	var topic string
	var role string

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a Parley room",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := noArgsJSON(cmd, args); err != nil {
				return err
			}
			if topic == "" {
				return writeJSONError(cmd, "missing_required_flag", "start requires --topic")
			}
			if name == "" {
				return writeJSONError(cmd, "missing_required_flag", "start requires --name")
			}
			roomID, err := newRoomID()
			if err != nil {
				return writeJSONError(cmd, "runtime_error", fmt.Sprintf("create room id: %v", err))
			}
			pid, err := launchRoomDaemon(roomDaemonConfig{
				RoomID: roomID,
				Topic:  topic,
				Name:   name,
				Role:   role,
			})
			if err != nil {
				return writeJSONError(cmd, "runtime_error", fmt.Sprintf("start room daemon: %v", err))
			}

			p := paths.New(paths.DefaultRoot())
			if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: roomID, Name: name}); err != nil {
				return writeJSONError(cmd, "runtime_error", fmt.Sprintf("save active participation: %v", err))
			}
			sessionID, err := parleyRuntime.NewSessionID()
			if err != nil {
				return writeJSONError(cmd, "runtime_error", fmt.Sprintf("create session id: %v", err))
			}
			if err := parleyRuntime.SaveSession(p, parleyRuntime.Session{ID: sessionID, RoomID: roomID, Name: name}); err != nil {
				return writeJSONError(cmd, "runtime_error", fmt.Sprintf("save session: %v", err))
			}
			room, err := parleyRuntime.LoadRoomRuntime(p, roomID)
			if err != nil {
				return writeJSONError(cmd, "runtime_error", fmt.Sprintf("load room runtime: %v", err))
			}
			invite, err := parleyRuntime.Invite(p, roomID)
			if err != nil {
				return writeJSONError(cmd, "runtime_error", fmt.Sprintf("build invite: %v", err))
			}
			return writeJSON(cmd, struct {
				OK                  bool   `json:"ok"`
				Status              string `json:"status"`
				RoomID              string `json:"room_id"`
				Topic               string `json:"topic"`
				SessionID           string `json:"session_id"`
				CommandArgs         string `json:"command_args"`
				Descriptor          string `json:"descriptor"`
				LocalHost           string `json:"local_host"`
				LocalPort           int    `json:"local_port"`
				ServerPID           int    `json:"server_pid"`
				JoinCommandTemplate string `json:"join_command_template"`
				AgentInstruction    string `json:"agent_instruction"`
			}{
				OK:                  true,
				Status:              "started",
				RoomID:              roomID,
				Topic:               topic,
				SessionID:           sessionID,
				CommandArgs:         "--session " + sessionID,
				Descriptor:          invite.Descriptor,
				LocalHost:           room.LocalHost,
				LocalPort:           room.LocalPort,
				ServerPID:           pid,
				JoinCommandTemplate: invite.JoinCommandTemplate,
				AgentInstruction:    invite.AgentInstruction,
			})
		},
	}
	cmd.Flags().StringVar(&topic, "topic", "", "Room topic")
	cmd.Flags().StringVar(&name, "name", "", "Host participant name")
	cmd.Flags().StringVar(&role, "role", "host", "Host participant role")
	return cmd
}
