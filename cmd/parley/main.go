package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"math/rand/v2"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/client"
	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/driver"
	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/server"
	"github.com/khaiql/parley/internal/tui"
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
var hostResume string
var hostYolo bool

var hostCmd = &cobra.Command{
	Use:   "host",
	Short: "Host a new group chat session",
	RunE:  runHost,
}

var joinPort int
var joinName string
var joinRole string
var joinResume bool

var joinCmd = &cobra.Command{
	Use:   "join",
	Short: "Join an existing group chat session",
	Args:  cobra.ArbitraryArgs,
	RunE:  runJoin,
}

func init() {
	hostCmd.Flags().StringVar(&hostTopic, "topic", "", "Topic for the chat session (required unless --resume is set)")
	hostCmd.Flags().IntVar(&hostPort, "port", 0, "Port to listen on (0 = auto-assign)")
	hostCmd.Flags().StringVar(&hostResume, "resume", "", "Room ID to resume (loads saved room from ~/.parley/rooms/<id>)")
	hostCmd.Flags().BoolVar(&hostYolo, "yolo", false, "Enable auto-approve mode for all joining agents")

	joinCmd.Flags().IntVar(&joinPort, "port", 0, "Port of the session to join (required)")
	joinCmd.Flags().StringVar(&joinName, "name", "", "Your name in the session (random if not set)")
	joinCmd.Flags().StringVar(&joinRole, "role", "agent", "Your role in the session")
	joinCmd.Flags().BoolVar(&joinResume, "resume", false, "Resume prior agent session (looks up session ID from saved agents.json)")
	joinCmd.Flags().SetInterspersed(false)
	_ = joinCmd.MarkFlagRequired("port")

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
func runHost(_ *cobra.Command, _ []string) error {
	addr := fmt.Sprintf(":%d", hostPort)

	var srv *server.Server
	var err error

	if hostResume != "" {
		// Resume an existing room from disk.
		dir := server.RoomDir(hostResume)
		room, loadErr := server.LoadRoom(dir)
		if loadErr != nil {
			return fmt.Errorf("host: load room %q: %w", hostResume, loadErr)
		}
		srv, err = server.NewWithRoom(addr, room)
		if err != nil {
			return fmt.Errorf("host: create server: %w", err)
		}
		// Use loaded topic if --topic was not explicitly set.
		if hostTopic == "" {
			hostTopic = room.Topic
		}
	} else {
		if hostTopic == "" {
			return fmt.Errorf("host: --topic is required when not using --resume")
		}
		srv, err = server.New(addr, hostTopic)
		if err != nil {
			return fmt.Errorf("host: create server: %w", err)
		}
	}

	if hostYolo {
		srv.Room().AutoApprove = true
	}

	go srv.Serve()

	port := srv.Port()
	roomID := srv.Room().ID
	fmt.Fprintf(os.Stderr, "Parley server listening on port %d\n", port)
	fmt.Fprintf(os.Stderr, "Room ID: %s\n", roomID)

	roomDir := server.RoomDir(roomID)

	// Save immediately so the room folder exists from the start.
	if err := server.SaveRoom(roomDir, srv.Room()); err != nil {
		fmt.Fprintf(os.Stderr, "Initial save failed: %v\n", err)
	}

	// Save on exit.
	defer func() {
		if err := server.SaveRoom(roomDir, srv.Room()); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save room: %v\n", err)
		}
	}()

	// Auto-save every 30 seconds so data isn't lost on crash.
	autoSaveStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := server.SaveRoom(roomDir, srv.Room()); err != nil {
					fmt.Fprintf(os.Stderr, "Auto-save failed: %v\n", err)
				}
			case <-autoSaveStop:
				return
			}
		}
	}()
	defer close(autoSaveStop)

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

	sendFn := func(text string, mentions []string) {
		_ = c.Send(protocol.Content{Type: "text", Text: text}, mentions)
	}

	app := tui.NewApp(hostTopic, port, tui.InputModeHuman, name, sendFn)

	// Set up slash command registry.
	reg := command.NewRegistry()
	reg.Register(command.InfoCommand)
	reg.Register(command.SaveCommand)
	reg.Register(command.SendCommandCommand)

	cmdCtx := command.Context{
		Room: &RoomAdapter{room: srv.Room(), port: port},
		SaveFn: func() error {
			return server.SaveRoom(roomDir, srv.Room())
		},
		SendFn: func(to, text string) {
			// Send the command text as a message mentioning the target agent.
			_ = c.Send(protocol.Content{Type: "text", Text: fmt.Sprintf("@%s %s", to, text)}, []string{to})
		},
	}
	app.SetCommandRegistry(reg, cmdCtx)

	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Bridge network → TUI. Join is done here (not before p.Run) so that
	// the room.state response arrives after the TUI event loop is running.
	go func() {
		// Join the room — this triggers room.state from the server.
		_ = c.Join(protocol.JoinParams{
			Name:      name,
			Role:      "human",
			Directory: dir,
			Repo:      repo,
		})
		for msg := range c.Incoming() {
			p.Send(tui.ServerMsg{Raw: msg})
		}
	}()

	_, err = p.Run()

	// Save room state BEFORE closing the server. This ensures the final
	// messages are persisted while participants are still in the room.
	if saveErr := server.SaveRoom(roomDir, srv.Room()); saveErr != nil {
		fmt.Fprintf(os.Stderr, "Failed to save room on exit: %v\n", saveErr)
	}

	// Host is leaving — shut down the server so all agent connections drop.
	// Agent processes will save their session IDs to agents.json after this.
	srv.Close()

	// Brief pause to let agent processes save their session IDs.
	time.Sleep(500 * time.Millisecond)

	return err
}

// runJoin implements the join command: connects to an existing server as an
// agent participant and runs the TUI with a Claude driver.
// randomName picks a random agent name from a curated list.
func randomName() string {
	names := []string{
		"atlas", "nova", "cipher", "echo", "flux",
		"helix", "iris", "juno", "kappa", "lumen",
		"nexus", "onyx", "pixel", "quark", "rune",
		"sage", "titan", "vega", "wren", "zephyr",
	}
	return names[rand.IntN(len(names))]
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
	timeout := time.After(5 * time.Second)
	found := false
	for !found {
		select {
		case msg, ok := <-c.Incoming():
			if !ok {
				return fmt.Errorf("join: connection closed before receiving room state")
			}
			if msg.Method == "room.state" {
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
		Command:         command,
		Args:            extraArgs,
		Name:            joinName,
		Role:            joinRole,
		Directory:       dir,
		Repo:            repo,
		Topic:           topic,
		Participants:    participants,
		InitialMessage:  intro,
		ResumeSessionID: resumeSessionID,
		AutoApprove:     roomState.AutoApprove,
	}
	config.SystemPrompt = driver.BuildSystemPrompt(config)

	ctx := context.Background()
	d, err := driver.NewDriver(command)
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
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Bridge network → TUI + agent driver.
	go func() {
		var pendingMsg string
		var pendingTimer *time.Timer

		flushPending := func() {
			if pendingMsg != "" {
				_ = d.Send(pendingMsg)
				pendingMsg = ""
			}
			pendingTimer = nil
		}

		for msg := range c.Incoming() {
			p.Send(tui.ServerMsg{Raw: msg})

			if msg.Method == "room.message" {
				var params protocol.MessageParams
				if err := json.Unmarshal(msg.Params, &params); err == nil {
					if params.From != joinName {
						// Format message and decide whether to delay.
						formatted := fmt.Sprintf("%s: %s", params.From, contentText(params.Content))
						if isMentioned(params.Mentions, joinName) {
							// @-mentioned: flush any pending messages and send immediately.
							if pendingTimer != nil {
								pendingTimer.Stop()
								pendingTimer = nil
							}
							if pendingMsg != "" {
								_ = d.Send(pendingMsg)
								pendingMsg = ""
							}
							_ = d.Send(formatted)
						} else {
							// Not mentioned: batch with a 2-second debounce timer.
							if pendingMsg != "" {
								pendingMsg += "\n" + formatted
							} else {
								pendingMsg = formatted
							}
							if pendingTimer == nil {
								pendingTimer = time.AfterFunc(2*time.Second, flushPending)
							} else {
								pendingTimer.Reset(2 * time.Second)
							}
						}
					}
				}
			}
		}

		// Flush any remaining pending message when the channel closes.
		if pendingTimer != nil {
			pendingTimer.Stop()
		}
		flushPending()

		// Server disconnected — quit the TUI and stop the agent.
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

// isMentioned reports whether name appears in the mentions list.
func isMentioned(mentions []string, name string) bool {
	for _, m := range mentions {
		if m == name {
			return true
		}
	}
	return false
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

// RoomAdapter wraps *server.Room to implement command.RoomQuerier.
type RoomAdapter struct {
	room *server.Room
	port int
}

func (a *RoomAdapter) GetID() string        { return a.room.ID }
func (a *RoomAdapter) GetTopic() string     { return a.room.Topic }
func (a *RoomAdapter) GetPort() int         { return a.port }
func (a *RoomAdapter) GetMessageCount() int { return a.room.MessageCount() }

func (a *RoomAdapter) GetParticipants() []command.ParticipantInfo {
	conns := a.room.GetParticipants()
	out := make([]command.ParticipantInfo, len(conns))
	for i, cc := range conns {
		out[i] = command.ParticipantInfo{
			Name:      cc.Name,
			Role:      cc.Role,
			Directory: cc.Directory,
			AgentType: cc.AgentType,
			Online:    cc.Online,
		}
	}
	return out
}
