package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/descriptor"
)

type roomDaemonConfig struct {
	RoomID string
	Topic  string
	Name   string
	Role   string
}

type participantDaemonConfig struct {
	Descriptor descriptor.Descriptor
	Name       string
	Role       string
	Directory  string
	Repo       string
}

var (
	launchRoomDaemon        = startRoomDaemonProcess
	launchParticipantDaemon = startParticipantDaemonProcess
)

func roomDaemonCmd() *cobra.Command {
	var cfg roomDaemonConfig

	cmd := &cobra.Command{
		Use:    "__room-daemon",
		Hidden: true,
		Args:   noArgsJSON,
		RunE: func(_ *cobra.Command, _ []string) error {
			if cfg.RoomID == "" || cfg.Topic == "" || cfg.Name == "" {
				return fmt.Errorf("room daemon requires --room, --topic, and --name")
			}
			if cfg.Role == "" {
				cfg.Role = "host"
			}
			return runRoomDaemon(cfg)
		},
	}
	cmd.Flags().StringVar(&cfg.RoomID, "room", "", "Room ID")
	cmd.Flags().StringVar(&cfg.Topic, "topic", "", "Room topic")
	cmd.Flags().StringVar(&cfg.Name, "name", "", "Host participant name")
	cmd.Flags().StringVar(&cfg.Role, "role", "host", "Host participant role")
	return cmd
}

func participantDaemonCmd() *cobra.Command {
	var rawDescriptor string
	var cfg participantDaemonConfig

	cmd := &cobra.Command{
		Use:    "__participant-daemon",
		Hidden: true,
		Args:   noArgsJSON,
		RunE: func(_ *cobra.Command, _ []string) error {
			desc, err := descriptor.Parse(rawDescriptor)
			if err != nil {
				return fmt.Errorf("invalid descriptor: %w", err)
			}
			cfg.Descriptor = desc
			if cfg.Name == "" {
				return fmt.Errorf("participant daemon requires --name")
			}
			if cfg.Role == "" {
				cfg.Role = "participant"
			}
			return runParticipantAdapter(cfg)
		},
	}
	cmd.Flags().StringVar(&rawDescriptor, "descriptor", "", "Room descriptor")
	cmd.Flags().StringVar(&cfg.Name, "name", "", "Participant name")
	cmd.Flags().StringVar(&cfg.Role, "role", "participant", "Participant role")
	cmd.Flags().StringVar(&cfg.Directory, "dir", "", "Participant working directory")
	cmd.Flags().StringVar(&cfg.Repo, "repo", "", "Participant repository URL")
	return cmd
}
