package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/sle/parley/internal/client"
	"github.com/sle/parley/internal/driver"
	"github.com/sle/parley/internal/protocol"
	"github.com/sle/parley/internal/server"
	"github.com/sle/parley/internal/tui"
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
	RunE:  runHost,
}

var joinPort int
var joinName string
var joinRole string

var joinCmd = &cobra.Command{
	Use:   "join",
	Short: "Join an existing group chat session",
	Args:  cobra.ArbitraryArgs,
	RunE:  runJoin,
}

func init() {
	hostCmd.Flags().StringVar(&hostTopic, "topic", "", "Topic for the chat session (required)")
	hostCmd.Flags().IntVar(&hostPort, "port", 0, "Port to listen on (0 = auto-assign)")
	_ = hostCmd.MarkFlagRequired("topic")

	joinCmd.Flags().IntVar(&joinPort, "port", 0, "Port of the session to join (required)")
	joinCmd.Flags().StringVar(&joinName, "name", "", "Your name in the session (required)")
	joinCmd.Flags().StringVar(&joinRole, "role", "agent", "Your role in the session")
	joinCmd.Flags().SetInterspersed(false)
	_ = joinCmd.MarkFlagRequired("port")
	_ = joinCmd.MarkFlagRequired("name")

	rootCmd.AddCommand(hostCmd)
	rootCmd.AddCommand(joinCmd)
}

// detectRepo runs git remote get-url origin and returns the trimmed output,
// or an empty string if the command fails.
func detectRepo() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// runHost implements the host command: starts a server, joins as human, and
// runs the TUI.
func runHost(cmd *cobra.Command, args []string) error {
	addr := fmt.Sprintf(":%d", hostPort)
	srv, err := server.New(addr, hostTopic)
	if err != nil {
		return fmt.Errorf("host: create server: %w", err)
	}

	go srv.Serve()

	port := srv.Port()
	fmt.Fprintf(os.Stderr, "Parley server listening on port %d\n", port)

	defer func() {
		roomID := fmt.Sprintf("%d", srv.Port())
		dir := server.RoomDir(roomID)
		if err := server.SaveRoom(dir, srv.Room()); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save room: %v\n", err)
		}
	}()

	c, err := client.New(srv.Addr())
	if err != nil {
		srv.Close()
		return fmt.Errorf("host: connect client: %w", err)
	}
	defer c.Close()

	name := os.Getenv("USER")
	if name == "" {
		name = "host"
	}
	dir, _ := os.Getwd()
	repo := detectRepo()

	if err := c.Join(protocol.JoinParams{
		Name:      name,
		Role:      "human",
		Directory: dir,
		Repo:      repo,
	}); err != nil {
		return fmt.Errorf("host: join room: %w", err)
	}

	sendFn := func(text string, mentions []string) {
		_ = c.Send(protocol.Content{Type: "text", Text: text}, mentions)
	}

	app := tui.NewApp(hostTopic, port, tui.InputModeHuman, name, sendFn)
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Bridge network → TUI.
	go func() {
		for msg := range c.Incoming() {
			p.Send(tui.ServerMsg{Raw: msg})
		}
	}()

	_, err = p.Run()
	return err
}

// runJoin implements the join command: connects to an existing server as an
// agent participant and runs the TUI with a Claude driver.
func runJoin(cmd *cobra.Command, args []string) error {
	// Extract agent command from args after "--".
	var agentArgs []string
	dashPos := cmd.Flags().ArgsLenAtDash()
	if dashPos >= 0 {
		agentArgs = args[dashPos:]
	}

	agentCmd := ""
	if len(agentArgs) > 0 {
		agentCmd = strings.Join(agentArgs, " ")
	}

	addr := fmt.Sprintf("localhost:%d", joinPort)
	c, err := client.New(addr)
	if err != nil {
		return fmt.Errorf("join: connect: %w", err)
	}
	defer c.Close()

	dir, _ := os.Getwd()
	repo := detectRepo()

	if err := c.Join(protocol.JoinParams{
		Name:      joinName,
		Role:      joinRole,
		Directory: dir,
		Repo:      repo,
		AgentType: agentCmd,
	}); err != nil {
		return fmt.Errorf("join: room join: %w", err)
	}

	// Wait for room.state to get topic and participants.
	var roomState protocol.RoomStateParams
	for msg := range c.Incoming() {
		if msg.Method == "room.state" {
			if err := json.Unmarshal(msg.Params, &roomState); err == nil {
				break
			}
		}
	}

	topic := roomState.Topic

	// Build participant info list.
	participants := make([]driver.ParticipantInfo, 0, len(roomState.Participants))
	for _, p := range roomState.Participants {
		participants = append(participants, driver.ParticipantInfo{
			Name:      p.Name,
			Role:      p.Role,
			Directory: p.Directory,
		})
	}

	command := "claude"
	var extraArgs []string
	if len(agentArgs) > 0 {
		command = agentArgs[0]
		extraArgs = agentArgs[1:]
	}

	config := driver.AgentConfig{
		Command:      command,
		Args:         extraArgs,
		Name:         joinName,
		Role:         joinRole,
		Directory:    dir,
		Repo:         repo,
		Topic:        topic,
		Participants: participants,
	}
	config.SystemPrompt = driver.BuildSystemPrompt(config)

	ctx := context.Background()
	d := &driver.ClaudeDriver{}
	if err := d.Start(ctx, config); err != nil {
		return fmt.Errorf("join: start agent driver: %w", err)
	}
	defer d.Stop()

	app := tui.NewApp(topic, joinPort, tui.InputModeAgent, joinName, nil)
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Bridge network → TUI + agent driver.
	go func() {
		for msg := range c.Incoming() {
			p.Send(tui.ServerMsg{Raw: msg})

			if msg.Method == "room.message" {
				var params protocol.MessageParams
				if err := json.Unmarshal(msg.Params, &params); err == nil {
					if params.From != joinName {
						// Format message and send to agent driver.
						text := fmt.Sprintf("%s: %s", params.From, contentText(params.Content))
						_ = d.Send(text)
					}
				}
			}
		}
	}()

	// Bridge agent → network.
	go func() {
		var accumulated strings.Builder
		for event := range d.Events() {
			switch event.Type {
			case driver.EventText:
				accumulated.WriteString(event.Text)
				p.Send(tui.AgentTypingMsg{Text: accumulated.String()})
			case driver.EventDone:
				text := strings.TrimSpace(accumulated.String())
				if text != "" {
					mentions := parseMentions(text)
					_ = c.Send(protocol.Content{Type: "text", Text: text}, mentions)
				}
				accumulated.Reset()
				p.Send(tui.AgentTypingMsg{Text: ""})
			}
		}
	}()

	_, err = p.Run()
	return err
}

// contentText extracts the text from a slice of Content items.
func contentText(content []protocol.Content) string {
	var parts []string
	for _, c := range content {
		if c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "")
}

// parseMentions extracts @mention tokens from a message string.
func parseMentions(text string) []string {
	var mentions []string
	for _, word := range strings.Fields(text) {
		if strings.HasPrefix(word, "@") && len(word) > 1 {
			mentions = append(mentions, word[1:])
		}
	}
	return mentions
}
