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
	"github.com/khaiql/parley/internal/persistence"
	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/room"
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

	agentCmd, agentArgs := parseAgentArgs(cmd, args)

	c, err := client.New(fmt.Sprintf("localhost:%d", joinPort))
	if err != nil {
		return fmt.Errorf("join: connect: %w", err)
	}
	defer c.Close()

	roomState, err := joinRoom(c, agentCmd)
	if err != nil {
		return err
	}

	store := persistence.NewJSONStore(defaultParleyDir())
	resumeSessionID := lookupResumeSession(store, roomState.RoomID)

	d, err := startAgent(agentArgs, roomState, resumeSessionID)
	if err != nil {
		return err
	}
	defer func() { _ = d.Stop() }()

	rs := room.New(nil, command.Context{})
	app := tui.NewApp(roomState.Topic, joinPort, tui.InputModeAgent, joinName, nil, roomState.Participants...)
	app.SetAgent(joinName, joinRole)
	app.SetYolo(roomState.AutoApprove)
	app.SetRoomState(rs)

	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())

	bridgeEvents(p, rs)
	replayRoomState(rs, roomState)
	startRouter(rs, d)
	startJoinNetworkLoop(c, rs, p)
	startAgentBridge(c, d, p)

	_, err = p.Run()

	saveAgentSession(store, roomState.RoomID, d)
	return err
}

// parseAgentArgs extracts the agent command and arguments from CLI args after "--".
func parseAgentArgs(cmd *cobra.Command, args []string) (string, []string) {
	var agentArgs []string
	dashPos := cmd.Flags().ArgsLenAtDash()
	if dashPos >= 0 {
		agentArgs = args[dashPos:]
	}

	agentCmd := protocol.AgentTypeClaude
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
	return agentCmd, agentArgs
}

// joinRoom sends a join request and waits for the room.state response.
func joinRoom(c *client.TCPClient, agentType string) (protocol.RoomStateParams, error) {
	dir, _ := os.Getwd()
	repo := detectRepo()

	if err := c.Join(protocol.JoinParams{
		Name:      joinName,
		Role:      joinRole,
		Directory: dir,
		Repo:      repo,
		AgentType: agentType,
	}); err != nil {
		return protocol.RoomStateParams{}, fmt.Errorf("join: room join: %w", err)
	}

	var roomState protocol.RoomStateParams
	timeout := time.After(5 * time.Second)
	for {
		select {
		case msg, ok := <-c.Incoming():
			if !ok {
				return protocol.RoomStateParams{}, fmt.Errorf("join: connection closed before receiving room state")
			}
			if msg.Method == protocol.MethodState {
				if err := json.Unmarshal(msg.Params, &roomState); err == nil {
					return roomState, nil
				}
			}
		case <-timeout:
			return protocol.RoomStateParams{}, fmt.Errorf("join: timeout: server did not send room state within 5 seconds")
		}
	}
}

// lookupResumeSession finds a prior agent session ID if --resume is set.
func lookupResumeSession(store *persistence.JSONStore, roomID string) string {
	if !joinResume || roomID == "" {
		return ""
	}
	sid, err := store.FindAgentSession(roomID, joinName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "join: warning: could not load prior session ID: %v\n", err)
		return ""
	}
	if sid != "" {
		fmt.Fprintf(os.Stderr, "join: resuming session %s\n", sid)
	} else {
		fmt.Fprintf(os.Stderr, "join: no prior session found for %q, starting fresh\n", joinName)
	}
	return sid
}

// startAgent creates and starts the agent driver subprocess.
func startAgent(agentArgs []string, roomState protocol.RoomStateParams, resumeSessionID string) (driver.AgentDriver, error) {
	cmdName := "claude"
	var extraArgs []string
	if len(agentArgs) > 0 {
		cmdName = agentArgs[0]
		extraArgs = agentArgs[1:]
	}

	dir, _ := os.Getwd()
	repo := detectRepo()

	intro := fmt.Sprintf("You have joined a parley chat room. Topic: %s. Introduce yourself briefly.", roomState.Topic)
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
		Topic:           roomState.Topic,
		Participants:    roomState.Participants,
		InitialMessage:  intro,
		ResumeSessionID: resumeSessionID,
		AutoApprove:     roomState.AutoApprove,
	}
	config.SystemPrompt = driver.BuildSystemPrompt(config)

	ctx := context.Background()
	d, err := driver.NewDriver(cmdName)
	if err != nil {
		return nil, fmt.Errorf("join: %w", err)
	}
	if err := d.Start(ctx, config); err != nil {
		return nil, fmt.Errorf("join: start agent driver: %w", err)
	}

	// For drivers that don't consume InitialMessage in Start() (e.g. Claude),
	// send the intro explicitly.
	if _, isGemini := d.(*driver.GeminiDriver); !isGemini {
		if err := d.Send(intro); err != nil {
			_ = d.Stop()
			return nil, fmt.Errorf("join: send initial prompt: %w", err)
		}
	}

	return d, nil
}

// replayRoomState replays the room.state we consumed during the join handshake
// so the TUI gets the initial HistoryLoaded event.
func replayRoomState(rs *room.State, roomState protocol.RoomStateParams) {
	stateJSON, err := json.Marshal(roomState)
	if err != nil {
		return
	}
	rs.HandleServerMessage(&protocol.RawMessage{
		Method: protocol.MethodState,
		Params: stateJSON,
	})
}

// startRouter subscribes a DebounceRouter to room events for routing messages
// to the agent driver.
func startRouter(rs *room.State, d driver.AgentDriver) {
	router := room.NewDebounceRouter(joinName, 2*time.Second, func(text string) {
		_ = d.Send(text)
	})
	router.Start(rs.Subscribe())
}

// startJoinNetworkLoop feeds incoming server messages to the TUI's room.State.
func startJoinNetworkLoop(c *client.TCPClient, rs *room.State, p *tea.Program) {
	go func() {
		for msg := range c.Incoming() {
			rs.HandleServerMessage(msg)
		}
		p.Send(tui.ServerDisconnectedMsg{})
	}()
}

// startAgentBridge bridges agent driver events to the network and TUI.
func startAgentBridge(c *client.TCPClient, d driver.AgentDriver, p *tea.Program) {
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
}

// saveAgentSession persists the agent's session ID for future resume.
func saveAgentSession(store *persistence.JSONStore, roomID string, d driver.AgentDriver) {
	if roomID == "" {
		return
	}
	sid := d.SessionID()
	if sid == "" {
		return
	}
	if err := store.SaveAgentSession(roomID, joinName, sid); err != nil {
		fmt.Fprintf(os.Stderr, "join: warning: could not save session ID: %v\n", err)
	}
}
