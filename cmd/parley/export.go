package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/persistence"
	"github.com/khaiql/parley/internal/web"
)

var exportOutput string

var exportCmd = &cobra.Command{
	Use:   "export [roomID]",
	Short: "Export a chat session as a shareable HTML file",
	Args:  cobra.ExactArgs(1),
	RunE:  runExport,
}

func init() {
	exportCmd.Flags().StringVarP(&exportOutput, "output", "o", "", "Output file path (default: <roomID>.html)")

	rootCmd.AddCommand(exportCmd)
}

func runExport(_ *cobra.Command, args []string) error {
	roomID := args[0]
	store := persistence.NewJSONStore(defaultParleyDir())
	dir := store.RoomDir(roomID)

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("export: room %q not found at %s", roomID, dir)
	}

	output := exportOutput
	if output == "" {
		output = roomID + ".html"
	}

	f, err := os.Create(output)
	if err != nil {
		return fmt.Errorf("export: create output file: %w", err)
	}
	defer f.Close()

	if err := web.Export(dir, f); err != nil {
		os.Remove(output)
		return fmt.Errorf("export: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Exported to %s\n", output)
	return nil
}
