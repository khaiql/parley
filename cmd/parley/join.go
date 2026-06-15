package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/descriptor"
)

func joinCmd() *cobra.Command {
	var name string
	var role string

	cmd := &cobra.Command{
		Use:   "join <descriptor>",
		Short: "Join a Parley room",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return writeJSONError(cmd, "missing_required_flag", "join requires --name")
			}
			if len(args) != 1 {
				return writeJSONError(cmd, "invalid_arguments", "join requires exactly one descriptor")
			}
			if _, err := descriptor.Parse(args[0]); err != nil {
				return writeJSONError(cmd, "invalid_descriptor", fmt.Sprintf("invalid descriptor: %v", err))
			}
			return notImplemented(cmd, "join")
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Participant name")
	cmd.Flags().StringVar(&role, "role", "participant", "Participant role")
	cmd.Flags().String("dir", "", "Participant working directory")
	cmd.Flags().String("repo", "", "Participant repository URL")

	return cmd
}
