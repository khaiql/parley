package main

import "github.com/spf13/cobra"

func startCmd() *cobra.Command {
	var name string
	var topic string

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
			return notImplemented(cmd, "start")
		},
	}
	cmd.Flags().StringVar(&topic, "topic", "", "Room topic")
	cmd.Flags().StringVar(&name, "name", "", "Host participant name")
	cmd.Flags().String("role", "host", "Host participant role")
	return cmd
}
