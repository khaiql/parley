package main

import "github.com/spf13/cobra"

func herdrCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "herdr", Short: "Herdr integration helpers"}
	cmd.AddCommand(herdrRPCCmd())
	return cmd
}
