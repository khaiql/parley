package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/client"
	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/dispatcher"
	"github.com/khaiql/parley/internal/driver"
	"github.com/khaiql/parley/internal/persistence"
	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/room"
	"github.com/khaiql/parley/internal/tui"
)

var (
	joinPort      int
	joinHost      string
	joinName      string
	joinRole      string
	joinResume    bool
	joinAgentType string
)

var joinCmd = &cobra.Command{
	Use:   "join",
	Short: "Join an existing group chat session",
	Args:  cobra.ArbitraryArgs,
	RunE:  runJoin,
}

func initJoinFlags() {
	joinCmd.Flags().IntVar(&joinPort, "port", 0, "Port of the session to join (required)")
	joinCmd.Flags().StringVarP(&joinHost, "host", "H", "localhost", "Hostname or IP of the session to join")
	joinCmd.Flags().StringVar(&joinName, "name", "", "Your name in the session (random if not set)")
	joinCmd.Flags().StringVar(&joinRole, "role", "agent", "Your role in the session")
	joinCmd.Flags().BoolVar(&joinResume, "resume", false, "Resume prior agent session (looks up session ID from saved agents.json)")
	joinCmd.Flags().StringVarP(&joinAgentType, "agent-type", "t", protocol.AgentTypeClaude, fmt.Sprintf("Agent type (%s)", strings.Join(protocol.SupportedAgentTypes(), ", ")))
	joinCmd.Flags().SetInterspersed(false)
	_ = joinCmd.MarkFlagRequired("port")
}

func init() {
	initJoinFlags()
	rootCmd.AddCommand(joinCmd)
}

func randomName() string {
	adjectives := []string{
		"swift", "quiet", "bold", "bright", "fuzzy",
		"clever", "gentle", "keen", "lucky", "nimble",
		"plucky", "rusty", "snowy", "spry", "steady",
		"tidy", "vivid", "warm", "witty", "zesty",
	}
	nouns := []string{
		"babbage", "bramble", "cosmo", "dingo", "ember",
		"ferris", "goblin", "hickory", "ibex", "junco",
		"kitsune", "loki", "moss", "noodle", "orca",
		"pascal", "pickle", "quokka", "ruckus", "sprocket",
		"turing", "umbra", "vortex", "wombat", "yeti",
	}
	ai, _ := rand.Int(rand.Reader, big.NewInt(int64(len(adjectives))))
	ni, _ := rand.Int(rand.Reader, big.NewInt(int64(len(nouns))))
	return adjectives[ai.Int64()] + "-" + nouns[ni.Int64()]
}

func runJoin(cmd *cobra.Command, args []string) error {
	if joinName == "" {
		joinName = randomName()
		fmt.Fprintf(os.Stderr, "No --name provided, using: %s\n", joinName)
	}

	agentType := protocol.NormalizeAgentType(joinAgentType)
	extraArgs := parseExtraArgs(cmd, args)

	c, err := client.New(fmt.Sprintf("%s:%d", joinHost, joinPort))
	if err != nil {
		return fmt.Errorf("join: connect: %w", err)
	}
	defer c.Close()

	roomState, err := joinRoom(c, agentType)
	if err != nil {
		return err
	}

	store := persistence.NewJSONStore(defaultParleyDir())
	resumeSessionID := lookupResumeSession(store, roomState.RoomID)

	rs := room.New(nil, command.Context{})
	app := tui.NewApp(roomState.Topic, joinPort, tui.InputModeAgent, joinName, nil, roomState.Participants...)
	app.SetAgent(joinName, joinRole)
	app.SetHost(joinHost)
	app.SetYolo(roomState.AutoApprove)
	app.SetRoomState(rs)
	app.SetInitializing(true, agentType)

	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())

	bridgeEvents(p, rs)
	replayRoomState(rs, roomState)
	startJoinNetworkLoop(c, rs, p)

	// Agent starts in background; TUI shows loading screen immediately.
	var (
		mu        sync.Mutex
		theDriver driver.AgentDriver
		theDisp   *dispatcher.Debounce
		agentErr  error
	)
	go func() {
		d, err := startAgent(agentType, extraArgs, roomState, resumeSessionID)
		if err != nil {
			mu.Lock()
			agentErr = err
			mu.Unlock()
			p.Send(tui.AgentStartFailedMsg{Err: err})
			return
		}
		disp := startDispatcher(rs, d)
		mu.Lock()
		theDriver = d
		theDisp = disp
		mu.Unlock()
		p.Send(tui.AgentReadyMsg{})
		startAgentBridge(c, d, p)
	}()

	_, err = p.Run()

	rs.Close()

	mu.Lock()
	d := theDriver
	disp := theDisp
	startErr := agentErr
	mu.Unlock()

	if disp != nil {
		disp.Close()
	}
	if d != nil {
		_ = d.Stop()
		saveAgentSession(store, roomState.RoomID, d)
	}
	if startErr != nil {
		return fmt.Errorf("join: start agent driver: %w", startErr)
	}
	return err
}

// parseExtraArgs extracts extra arguments after "--" to pass through to the agent executable.
func parseExtraArgs(cmd *cobra.Command, args []string) []string {
	dashPos := cmd.Flags().ArgsLenAtDash()
	if dashPos >= 0 {
		return args[dashPos:]
	}
	return nil
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
func startAgent(agentType string, extraArgs []string, roomState protocol.RoomStateParams, resumeSessionID string) (driver.AgentDriver, error) {
	cmdName := protocol.DefaultCommand(agentType)
	allArgs := append(protocol.DefaultArgs(agentType), extraArgs...)

	dir, _ := os.Getwd()
	repo := detectRepo()

	intro := fmt.Sprintf("You have joined a parley chat room. Topic: %s. Introduce yourself briefly.", roomState.Topic)
	history := driver.FormatHistory(roomState.Messages)
	if history != "" {
		intro = history + "\n" + intro
	}

	config := driver.AgentConfig{
		Command:         cmdName,
		Args:            allArgs,
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
	d, err := driver.NewDriver(agentType)
	if err != nil {
		return nil, fmt.Errorf("join: %w", err)
	}
	if err := d.Start(ctx, config); err != nil {
		return nil, fmt.Errorf("join: start agent driver: %w", err)
	}

	// For drivers that consume InitialMessage in Start() (e.g. Gemini, Rovodev),
	// skip the explicit send.
	if !driver.ConsumesInitialMessage(agentType) {
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

// startDispatcher subscribes a Debounce dispatcher to room events for
// delivering messages to the agent driver. Returns the dispatcher so the
// caller can Close() it on shutdown.
func startDispatcher(rs *room.State, d driver.AgentDriver) *dispatcher.Debounce {
	disp := dispatcher.NewDebounce(joinName, 2*time.Second, func(text string) {
		_ = d.Send(text)
	})
	disp.Start(rs.Subscribe())
	return disp
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
				_ = c.SendStatus(joinName, protocol.StatusThinking)
			case driver.EventToolUse:
				_ = c.SendStatus(joinName, protocol.StatusUsingTool(event.ToolName))
			case driver.EventDone:
				text := strings.TrimSpace(accumulated.String())
				if protocol.IsPassSignal(text) {
					_ = c.SendStatus(joinName, protocol.StatusListening)
				} else if text != "" {
					mentions := protocol.ParseMentions(text)
					_ = c.Send(protocol.Content{Type: "text", Text: text}, mentions)
					_ = c.SendStatus(joinName, protocol.StatusIdle)
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
