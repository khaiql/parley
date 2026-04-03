package command

import "fmt"

// InfoCommand displays current room information.
var InfoCommand = &Command{
	Name:        "info",
	Usage:       "/info",
	Description: "Display current room information",
	Execute: func(ctx Context, args string) Result {
		room := ctx.Room
		participants := room.GetParticipantSnapshot()
		port := room.GetPort()

		info := fmt.Sprintf("Room: %s\nTopic: %s\nPort: %d\nParticipants: %d\nMessages: %d\n",
			room.GetID(),
			room.GetTopic(),
			port,
			len(participants),
			room.GetMessageCount(),
		)

		if len(participants) > 0 {
			info += "\nConnected:\n"
			for _, p := range participants {
				line := fmt.Sprintf("  • %s (%s)", p.Name, p.Role)
				if p.Directory != "" {
					line += fmt.Sprintf(" — %s", p.Directory)
				}
				info += line + "\n"
			}
		}

		// Ready-to-copy join command.
		info += fmt.Sprintf("\nJoin command:\n  parley join --port %d -- claude\n", port)

		// If there are saved agents from prior sessions, show resume commands.
		savedAgents := room.GetSavedAgents()
		if len(savedAgents) > 0 {
			info += "\nResume prior agents:\n"
			for _, sa := range savedAgents {
				agentCmd := sa.AgentType
				if agentCmd == "" {
					agentCmd = "claude"
				}
				info += fmt.Sprintf("  parley join --port %d --name %s --resume -- %s\n", port, sa.Name, agentCmd)
			}
		}

		return Result{LocalMessage: info}
	},
}
