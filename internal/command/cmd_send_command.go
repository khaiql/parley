package command

import (
	"fmt"
	"strings"
)

// SendCommandCommand sends a command to a specific agent.
var SendCommandCommand = &Command{
	Name:        "send_command",
	Usage:       "/send_command @agent /command",
	Description: "Send a command to a specific agent",
	Execute: func(ctx Context, args string) Result {
		args = strings.TrimSpace(args)
		if args == "" {
			return Result{Error: fmt.Errorf("usage: /send_command @agent /command")}
		}

		// Parse @agent name.
		if !strings.HasPrefix(args, "@") {
			return Result{Error: fmt.Errorf("usage: /send_command @agent /command — agent name must start with @")}
		}

		parts := strings.SplitN(args, " ", 2)
		agentName := strings.TrimPrefix(parts[0], "@")
		if agentName == "" {
			return Result{Error: fmt.Errorf("usage: /send_command @agent /command — agent name is empty")}
		}

		// Validate agent exists.
		found := false
		for _, p := range ctx.Room.GetParticipants() {
			if p.Name == agentName {
				found = true
				break
			}
		}
		if !found {
			return Result{Error: fmt.Errorf("agent %q is not connected", agentName)}
		}

		subCmd := ""
		if len(parts) > 1 {
			subCmd = strings.TrimSpace(parts[1])
		}
		if subCmd == "" {
			return Result{Error: fmt.Errorf("usage: /send_command @agent /command — no command specified")}
		}

		if ctx.SendFn == nil {
			return Result{Error: fmt.Errorf("send not available")}
		}

		ctx.SendFn(agentName, subCmd)
		return Result{LocalMessage: fmt.Sprintf("Sent to @%s: %s", agentName, subCmd)}
	},
}
