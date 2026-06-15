package main

import "github.com/spf13/cobra"

func infoCmd() *cobra.Command {
	return participantCommand("info", "Print local participant metadata")
}

func statusCmd() *cobra.Command {
	return participantCommand("status", "Print room status")
}

func inboxCmd() *cobra.Command {
	return participantCommand("inbox", "Print unseen room events")
}

func historyCmd() *cobra.Command {
	return participantCommand("history", "Print room history")
}

func waitCmd() *cobra.Command {
	cmd := participantCommand("wait", "Wait for unseen room events")
	cmd.Flags().Duration("timeout", 0, "Maximum time to wait")
	return cmd
}

func sendCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "send <message>",
		Short: "Send a message to the room",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented(cmd, "send")
		},
	}
}

func leaveCmd() *cobra.Command {
	return participantCommand("leave", "Leave the room")
}

func stopCmd() *cobra.Command {
	return participantCommand("stop", "Stop the room")
}

func participantCommand(name, short string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented(cmd, name)
		},
	}
}
