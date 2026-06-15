package main

import "github.com/spf13/cobra"

func inviteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invite",
		Short: "Print room invitation details",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented(cmd, "invite")
		},
	}
	return cmd
}
