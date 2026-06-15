package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/paths"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
)

func inviteCmd() *cobra.Command {
	var roomID string

	cmd := &cobra.Command{
		Use:   "invite",
		Short: "Print room invitation details",
		Args:  noArgsJSON,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := paths.New(paths.DefaultRoot())
			resolvedRoomID, err := resolveRoomID(p, roomID)
			if err != nil {
				return writeJSONError(cmd, "no_active_room", err.Error())
			}
			invite, err := parleyRuntime.Invite(p, resolvedRoomID)
			if err != nil {
				code := "runtime_error"
				if errors.Is(err, os.ErrNotExist) {
					code = "room_runtime_not_found"
				}
				return writeJSONError(cmd, code, fmt.Sprintf("load room runtime: %v", err))
			}
			return writeJSON(cmd, invite)
		},
	}
	cmd.Flags().StringVar(&roomID, "room", "", "Room ID")
	return cmd
}
