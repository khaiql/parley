package command

import "fmt"

// InfoCommand displays current room information.
var InfoCommand = &Command{
	Name:        "info",
	Usage:       "/info",
	Description: "Display current room information",
	Execute: func(ctx Context, _ string) Result {
		room := ctx.Room
		participants := room.GetParticipants()
		port := room.GetPort()

		info := fmt.Sprintf("Room: %s\nTopic: %s\nPort: %d\nParticipants: %d\nMessages: %d\n",
			room.GetID(),
			room.GetTopic(),
			port,
			len(participants),
			room.GetMessageCount(),
		)

		if len(participants) > 0 {
			info += "\nParticipants:\n"
			for _, p := range participants {
				status := "online"
				if !p.Online {
					status = "offline"
				}
				line := fmt.Sprintf("  • %s (%s) [%s]", p.Name, p.Role, status)
				if p.Directory != "" {
					line += fmt.Sprintf(" — %s", p.Directory)
				}
				info += line + "\n"

				// Show resume hint for offline agents.
				if !p.Online && p.AgentType != "" {
					agentCmd := p.AgentType
					info += fmt.Sprintf("      resume: parley join --port %d --name %s --resume -- %s\n", port, p.Name, agentCmd)
				}
			}
		}

		// Ready-to-copy join command.
		info += fmt.Sprintf("\nJoin command:\n  parley join --port %d -- claude\n", port)

		return Result{Modal: &ModalContent{Title: "Room Info", Body: info}}
	},
}
