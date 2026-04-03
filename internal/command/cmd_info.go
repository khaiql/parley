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

		info := fmt.Sprintf("Room: %s\nTopic: %s\nPort: %d\nParticipants: %d\nMessages: %d\n",
			room.GetID(),
			room.GetTopic(),
			room.GetPort(),
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

		return Result{LocalMessage: info}
	},
}
