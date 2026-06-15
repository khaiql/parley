package main

import "github.com/spf13/cobra"

func infoCmd() *cobra.Command {
	return participantCommand("info", "Print local participant metadata")
}

func statusCmd() *cobra.Command {
	return participantCommand("status", "Print room status")
}

func inboxCmd() *cobra.Command {
	cmd := participantCommand("inbox", "Print unseen room events")
	cmd.Flags().Bool("peek", false, "Do not advance the seen cursor")
	return cmd
}

func historyCmd() *cobra.Command {
	cmd := participantCommand("history", "Print room history")
	cmd.Flags().Int("limit", 0, "Maximum number of history events")
	cmd.Flags().Bool("all", false, "Return all retained history")
	return cmd
}

func waitCmd() *cobra.Command {
	cmd := participantCommand("wait", "Wait for unseen room events")
	cmd.Flags().Duration("timeout", 0, "Maximum time to wait")
	return cmd
}

func sendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send <message>",
		Short: "Send a message to the room",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return writeJSONError(cmd, "invalid_arguments", "send requires exactly one message argument")
			}
			return notImplemented(cmd, "send")
		},
	}
	addParticipationFlags(cmd)
	return cmd
}

func leaveCmd() *cobra.Command {
	return participantCommand("leave", "Leave the room")
}

func stopCmd() *cobra.Command {
	return participantCommand("stop", "Stop the room")
}

func participantCommand(name, short string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   name,
		Short: short,
		Args:  noArgsJSON,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented(cmd, name)
		},
	}
	addParticipationFlags(cmd)
	return cmd
}

func addParticipationFlags(cmd *cobra.Command) {
	cmd.Flags().String("room", "", "Room ID")
	cmd.Flags().String("name", "", "Participant name")
}
