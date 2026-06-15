package main

import "github.com/spf13/cobra"

func inviteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invite",
		Short: "Print room invitation details",
		Args:  noArgsJSON,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented(cmd, "invite")
		},
	}
	cmd.Flags().String("room", "", "Room ID")
	return cmd
}
