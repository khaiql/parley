package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/adapter"
)

func artifactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "artifact",
		Short: "Work with room artifacts",
	}
	cmd.AddCommand(artifactFetchCmd())
	return cmd
}

func artifactFetchCmd() *cobra.Command {
	var roomID string
	var name string
	var sessionID string
	var out string

	cmd := &cobra.Command{
		Use:   "fetch <artifact-id>...",
		Short: "Fetch room artifacts by id",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return writeJSONError(cmd, "invalid_arguments", "artifact fetch requires at least one artifact id")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 && out != "" {
				if info, err := os.Stat(out); err == nil && !info.IsDir() {
					return writeJSONError(cmd, "invalid_output_path", "artifact fetch --out must be a directory when fetching multiple ids")
				}
			}
			return callParticipantControl(cmd, roomID, name, sessionID, adapter.ControlRequest{
				Type:        "artifact_fetch",
				ArtifactIDs: args,
				Out:         out,
			})
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "Output file or directory")
	addParticipationFlags(cmd, &roomID, &name, &sessionID)
	return cmd
}
