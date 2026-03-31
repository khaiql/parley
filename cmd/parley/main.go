package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "parley",
	Short: "TUI group chat for coding agents",
}

var hostTopic string
var hostPort int

var hostCmd = &cobra.Command{
	Use:   "host",
	Short: "Host a new group chat session",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Hosting session with topic %q on port %d\n", hostTopic, hostPort)
		return nil
	},
}

var joinPort int
var joinName string
var joinRole string

var joinCmd = &cobra.Command{
	Use:   "join",
	Short: "Join an existing group chat session",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Joining session on port %d as %q (role: %q)\n", joinPort, joinName, joinRole)
		return nil
	},
}

func init() {
	hostCmd.Flags().StringVar(&hostTopic, "topic", "", "Topic for the chat session (required)")
	hostCmd.Flags().IntVar(&hostPort, "port", 0, "Port to listen on (0 = auto-assign)")
	_ = hostCmd.MarkFlagRequired("topic")

	joinCmd.Flags().IntVar(&joinPort, "port", 0, "Port of the session to join (required)")
	joinCmd.Flags().StringVar(&joinName, "name", "", "Your name in the session (required)")
	joinCmd.Flags().StringVar(&joinRole, "role", "", "Your role in the session")
	_ = joinCmd.MarkFlagRequired("port")
	_ = joinCmd.MarkFlagRequired("name")

	rootCmd.AddCommand(hostCmd)
	rootCmd.AddCommand(joinCmd)
}
