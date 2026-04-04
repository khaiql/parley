package command

import (
	"fmt"
	"strings"
)

// Registry holds registered slash commands and dispatches input to them.
type Registry struct {
	commands map[string]*Command
	order    []string // insertion order for help text
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		commands: make(map[string]*Command),
	}
}

// Register adds a command to the registry. If a command with the same name
// already exists it is silently replaced.
func (r *Registry) Register(cmd *Command) {
	if _, exists := r.commands[cmd.Name]; !exists {
		r.order = append(r.order, cmd.Name)
	}
	r.commands[cmd.Name] = cmd
}

// Execute parses rawInput (e.g. "/info" or "/save"), looks up the command,
// and calls its Execute function. Returns an error Result for unknown commands.
func (r *Registry) Execute(ctx Context, rawInput string) Result {
	rawInput = strings.TrimSpace(rawInput)
	if !strings.HasPrefix(rawInput, "/") {
		return Result{Error: fmt.Errorf("not a command")}
	}

	// Split into command name and args.
	without := rawInput[1:] // strip leading "/"
	name, args, _ := strings.Cut(without, " ")
	name = strings.ToLower(name)
	args = strings.TrimSpace(args)

	cmd, ok := r.commands[name]
	if !ok {
		return Result{
			Error: fmt.Errorf("Unknown command: /%s. Available: %s", name, r.availableString()),
		}
	}

	return cmd.Execute(ctx, args)
}

// IsCommand reports whether rawInput looks like a slash command.
func IsCommand(rawInput string) bool {
	trimmed := strings.TrimSpace(rawInput)
	return strings.HasPrefix(trimmed, "/") && len(trimmed) > 1
}

// Available returns the registered command names in insertion order.
func (r *Registry) Available() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Commands returns the registered commands in insertion order.
func (r *Registry) Commands() []*Command {
	out := make([]*Command, len(r.order))
	for i, name := range r.order {
		out[i] = r.commands[name]
	}
	return out
}

// availableString returns a comma-separated list of /commands.
func (r *Registry) availableString() string {
	parts := make([]string, len(r.order))
	for i, name := range r.order {
		parts[i] = "/" + name
	}
	return strings.Join(parts, ", ")
}
