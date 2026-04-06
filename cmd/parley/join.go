package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/client"
	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/driver"
	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/room"
	"github.com/khaiql/parley/internal/server"
	"github.com/khaiql/parley/internal/tui"
)

var (
	joinPort   int
	joinName   string
	joinRole   string
	joinResume bool
)

var joinCmd = &cobra.Command{
	Use:   "join",
	Short: "Join an existing group chat session",
	Args:  cobra.ArbitraryArgs,
	RunE:  runJoin,
}

func init() {
	joinCmd.Flags().IntVar(&joinPort, "port", 0, "Port of the session to join (required)")
	joinCmd.Flags().StringVar(&joinName, "name", "", "Your name in the session (random if not set)")
	joinCmd.Flags().StringVar(&joinRole, "role", "agent", "Your role in the session")
	joinCmd.Flags().BoolVar(&joinResume, "resume", false, "Resume prior agent session (looks up session ID from saved agents.json)")
	joinCmd.Flags().SetInterspersed(false)
	_ = joinCmd.MarkFlagRequired("port")

	rootCmd.AddCommand(joinCmd)
}

func randomName() string {
	names := []string{
		"babbage", "bramble", "cosmo", "dingo", "ember",
		"ferris", "goblin", "hickory", "ibex", "junco",
		"kitsune", "loki", "moss", "noodle", "orca",
		"pascal", "pickle", "quokka", "ruckus", "sprocket",
		"turing", "umbra", "vortex", "wombat", "yeti",
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(names))))
	return names[n.Int64()]
}

func runJoin(cmd *cobra.Command, args []string) error {
	if joinName == "" {
		joinName = randomName()
		fmt.Fprintf(os.Stderr, "No --name provided, using: %s\n", joinName)
	}

	// Extract agent command from args after "--".
	var agentArgs []string
	dashPos := cmd.Flags().ArgsLenAtDash()
	if dashPos >= 0 {
		agentArgs = args[dashPos:]
	}

	agentCmd := protocol.AgentTypeClaude // default agent type
	if len(agentArgs) > 0 {
		base := agentArgs[0]
		switch {
		case strings.Contains(base, "gemini"):
			agentCmd = protocol.AgentTypeGemini
		case strings.Contains(base, "codex"):
			agentCmd = protocol.AgentTypeCodex
		default:
			agentCmd = protocol.AgentTypeClaude
		}
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
	timeout := time.After(5 * time.Second)
	found := false
	for !found {
		select {
		case msg, ok := <-c.Incoming():
			if !ok {
				return fmt.Errorf("join: connection closed before receiving room state")
			}
			if msg.Method == protocol.MethodState {
				if err := json.Unmarshal(msg.Params, &roomState); err == nil {
					found = true
				}
			}
		case <-timeout:
			return fmt.Errorf("join: timeout: server did not send room state within 5 seconds")
		}
	}

	topic := roomState.Topic
	roomID := roomState.RoomID

	cmdName := "claude"
	var extraArgs []string
	if len(agentArgs) > 0 {
		cmdName = agentArgs[0]
		extraArgs = agentArgs[1:]
	}

	// If --resume is set and we know the room ID, look up the prior session ID.
	var resumeSessionID string
	if joinResume && roomID != "" {
		roomDir := server.RoomDir(roomID)
		sid, lookupErr := server.FindAgentSessionID(roomDir, joinName)
		if lookupErr != nil {
			fmt.Fprintf(os.Stderr, "join: warning: could not load prior session ID: %v\n", lookupErr)
		} else if sid != "" {
			resumeSessionID = sid
			fmt.Fprintf(os.Stderr, "join: resuming session %s\n", sid)
		} else {
			fmt.Fprintf(os.Stderr, "join: no prior session found for %q, starting fresh\n", joinName)
		}
	}

	// Build the intro message for the agent.
	intro := fmt.Sprintf("You have joined a parley chat room. Topic: %s. Introduce yourself briefly.", topic)
	history := driver.FormatHistory(roomState.Messages)
	if history != "" {
		intro = history + "\n" + intro
	}

	config := driver.AgentConfig{
		Command:         cmdName,
		Args:            extraArgs,
		Name:            joinName,
		Role:            joinRole,
		Directory:       dir,
		Repo:            repo,
		Topic:           topic,
		Participants:    roomState.Participants,
		InitialMessage:  intro,
		ResumeSessionID: resumeSessionID,
		AutoApprove:     roomState.AutoApprove,
	}
	config.SystemPrompt = driver.BuildSystemPrompt(config)

	ctx := context.Background()
	d, err := driver.NewDriver(cmdName)
	if err != nil {
		return fmt.Errorf("join: %w", err)
	}
	if err := d.Start(ctx, config); err != nil {
		return fmt.Errorf("join: start agent driver: %w", err)
	}
	defer func() { _ = d.Stop() }()

	// For drivers that don't consume InitialMessage in Start() (e.g. Claude),
	// send the intro explicitly.
	if _, isGemini := d.(*driver.GeminiDriver); !isGemini {
		if err := d.Send(intro); err != nil {
			return fmt.Errorf("join: send initial prompt: %w", err)
		}
	}

	app := tui.NewApp(topic, joinPort, tui.InputModeAgent, joinName, nil, roomState.Participants...)
	app.SetAgent(joinName, joinRole)
	app.SetYolo(roomState.AutoApprove)

	// Create room.State for event-sourced state management (join has no commands).
	rs := room.New(nil, command.Context{})
	app.SetRoomState(rs)

	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Subscribe to room events BEFORE replaying state so no events are lost.
	rsEvents := rs.Subscribe()
	go func() {
		for e := range rsEvents {
			p.Send(e)
		}
	}()

	// Replay the room.state we already consumed during the join handshake.
	stateJSON, err := json.Marshal(roomState)
	if err != nil {
		return fmt.Errorf("join: marshal room state for replay: %w", err)
	}
	rs.HandleServerMessage(&protocol.RawMessage{
		Method: protocol.MethodState,
		Params: stateJSON,
	})

	// Subscribe router to room events — routes messages to the agent driver.
	router := room.NewDebounceRouter(joinName, 2*time.Second, func(text string) {
		_ = d.Send(text)
	})
	router.Start(rs.Subscribe())

	// Bridge network → room.State.
	go func() {
		for msg := range c.Incoming() {
			rs.HandleServerMessage(msg)
		}
		p.Send(tui.ServerDisconnectedMsg{})
	}()

	// Bridge agent → network.
	go func() {
		var accumulated strings.Builder
		for event := range d.Events() {
			switch event.Type {
			case driver.EventText:
				accumulated.WriteString(event.Text)
				p.Send(tui.AgentTypingMsg{Text: accumulated.String()})
			case driver.EventThinking:
				_ = c.SendStatus(joinName, "thinking…")
			case driver.EventToolUse:
				status := "using tool…"
				if event.ToolName != "" {
					status = fmt.Sprintf("using: %s…", event.ToolName)
				}
				_ = c.SendStatus(joinName, status)
			case driver.EventDone:
				text := strings.TrimSpace(accumulated.String())
				if driver.IsListeningSignal(text) {
					_ = c.SendStatus(joinName, "listening")
				} else if text != "" {
					mentions := protocol.ParseMentions(text)
					_ = c.Send(protocol.Content{Type: "text", Text: text}, mentions)
					_ = c.SendStatus(joinName, "")
				}
				accumulated.Reset()
				p.Send(tui.AgentTypingMsg{Text: ""})
			default:
				// ignore other event types like Error or ToolResult
			}
		}
	}()

	_, err = p.Run()

	// Save the agent's session ID so it can be resumed next time.
	if roomID != "" {
		if sid := d.SessionID(); sid != "" {
			roomDir := server.RoomDir(roomID)
			if saveErr := server.UpdateAgentSessionID(roomDir, joinName, sid); saveErr != nil {
				fmt.Fprintf(os.Stderr, "join: warning: could not save session ID: %v\n", saveErr)
			}
		}
	}

	return err
}
