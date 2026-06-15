package main

import "github.com/spf13/cobra"

func startCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a Parley room",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented(cmd, "start")
		},
	}
	cmd.Flags().String("topic", "", "Room topic")
	return cmd
}
