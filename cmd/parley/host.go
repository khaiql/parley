package main

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/client"
	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/room"
	"github.com/khaiql/parley/internal/server"
	"github.com/khaiql/parley/internal/tui"
)

var (
	hostTopic  string
	hostPort   int
	hostResume string
	hostYolo   bool
)

var hostCmd = &cobra.Command{
	Use:   "host",
	Short: "Host a new group chat session",
	RunE:  runHost,
}

func init() {
	hostCmd.Flags().StringVar(&hostTopic, "topic", "", "Topic for the chat session (required unless --resume is set)")
	hostCmd.Flags().IntVar(&hostPort, "port", 0, "Port to listen on (0 = auto-assign)")
	hostCmd.Flags().StringVar(&hostResume, "resume", "", "Room ID to resume (loads saved room from ~/.parley/rooms/<id>)")
	hostCmd.Flags().BoolVar(&hostYolo, "yolo", false, "Enable auto-approve mode for all joining agents")

	rootCmd.AddCommand(hostCmd)
}

// RoomAdapter wraps *room.State to implement command.RoomQuerier,
// adding the transport-level port that room.State doesn't know about.
type RoomAdapter struct {
	state *room.State
	port  int
}

func (a *RoomAdapter) GetID() string                           { return a.state.GetID() }
func (a *RoomAdapter) GetTopic() string                        { return a.state.GetTopic() }
func (a *RoomAdapter) GetPort() int                            { return a.port }
func (a *RoomAdapter) GetParticipants() []protocol.Participant { return a.state.GetParticipants() }
func (a *RoomAdapter) GetMessageCount() int                    { return a.state.GetMessageCount() }

func runHost(_ *cobra.Command, _ []string) error {
	srv, err := createServer()
	if err != nil {
		return err
	}
	go srv.Serve()

	port := srv.Port()
	roomID := srv.Room().ID
	roomDir := server.RoomDir(roomID)
	fmt.Fprintf(os.Stderr, "Parley server listening on port %d\n", port)
	fmt.Fprintf(os.Stderr, "Room ID: %s\n", roomID)

	stopAutoSave := startPersistence(srv, roomDir)
	defer stopAutoSave()

	c, err := client.New(srv.Addr())
	if err != nil {
		srv.Close()
		return fmt.Errorf("host: connect client: %w", err)
	}
	defer c.Close()

	name := hostName()
	dir, _ := os.Getwd()
	repo := detectRepo()

	sendFn := func(text string, mentions []string) {
		_ = c.Send(protocol.Content{Type: "text", Text: text}, mentions)
	}

	roomState, reg, cmdCtx := setupRoomState(sendFn, c, port, roomDir, srv)

	app := tui.NewApp(hostTopic, port, tui.InputModeHuman, name, sendFn)
	app.SetCommandRegistry(reg, cmdCtx)
	app.SetRoomState(roomState)

	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())

	bridgeEvents(p, roomState)
	startNetworkLoop(c, roomState, name, dir, repo)

	_, err = p.Run()

	shutdown(srv, roomDir)
	return err
}

// createServer creates either a new or resumed server based on CLI flags.
func createServer() (server.Server, error) {
	addr := fmt.Sprintf(":%d", hostPort)

	if hostResume != "" {
		dir := server.RoomDir(hostResume)
		rm, err := server.LoadRoom(dir)
		if err != nil {
			return nil, fmt.Errorf("host: load room %q: %w", hostResume, err)
		}
		srv, err := server.NewWithRoom(addr, rm)
		if err != nil {
			return nil, fmt.Errorf("host: create server: %w", err)
		}
		if hostTopic == "" {
			hostTopic = rm.Topic
		}
		if hostYolo {
			srv.Room().AutoApprove = true
		}
		return srv, nil
	}

	if hostTopic == "" {
		return nil, fmt.Errorf("host: --topic is required when not using --resume")
	}
	srv, err := server.New(addr, hostTopic)
	if err != nil {
		return nil, fmt.Errorf("host: create server: %w", err)
	}
	if hostYolo {
		srv.Room().AutoApprove = true
	}
	return srv, nil
}

// startPersistence saves room state immediately, on a 30s interval, and on
// exit. Returns a cleanup function that stops the auto-save ticker and
// performs a final save.
func startPersistence(srv server.Server, roomDir string) func() {
	// Save immediately so the room folder exists from the start.
	if err := server.SaveRoom(roomDir, srv.Room()); err != nil {
		fmt.Fprintf(os.Stderr, "Initial save failed: %v\n", err)
	}

	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := server.SaveRoom(roomDir, srv.Room()); err != nil {
					fmt.Fprintf(os.Stderr, "Auto-save failed: %v\n", err)
				}
			case <-stop:
				return
			}
		}
	}()

	return func() {
		close(stop)
		if err := server.SaveRoom(roomDir, srv.Room()); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save room: %v\n", err)
		}
	}
}

// setupRoomState creates the room.State, command registry, and command context.
func setupRoomState(
	sendFn func(string, []string),
	c *client.TCPClient,
	port int,
	roomDir string,
	srv server.Server,
) (*room.State, *command.Registry, command.Context) {
	roomState := room.New(nil, command.Context{})
	roomState.SetSendFn(sendFn)
	if hostYolo {
		roomState.SetAutoApprove(true)
	}

	reg := command.NewRegistry()
	reg.Register(command.InfoCommand)
	reg.Register(command.SaveCommand)
	reg.Register(command.SendCommandCommand)

	cmdCtx := command.Context{
		Room: &RoomAdapter{state: roomState, port: port},
		SaveFn: func() error {
			return server.SaveRoom(roomDir, srv.Room())
		},
		SendFn: func(to, text string) {
			_ = c.Send(protocol.Content{Type: "text", Text: fmt.Sprintf("@%s %s", to, text)}, []string{to})
		},
	}

	roomState.SetCommands(reg, cmdCtx)
	return roomState, reg, cmdCtx
}

// bridgeEvents subscribes to room events and forwards them to the TUI.
func bridgeEvents(p *tea.Program, roomState *room.State) {
	events := roomState.Subscribe()
	go func() {
		for e := range events {
			p.Send(e)
		}
	}()
}

// startNetworkLoop joins the room and feeds incoming server messages to room.State.
func startNetworkLoop(c *client.TCPClient, roomState *room.State, name, dir, repo string) {
	go func() {
		_ = c.Join(protocol.JoinParams{
			Name:      name,
			Role:      "human",
			Directory: dir,
			Repo:      repo,
		})
		for msg := range c.Incoming() {
			roomState.HandleServerMessage(msg)
		}
	}()
}

// shutdown saves room state and closes the server.
func shutdown(srv server.Server, roomDir string) {
	if err := server.SaveRoom(roomDir, srv.Room()); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save room on exit: %v\n", err)
	}
	srv.Close()
	// Brief pause to let agent processes save their session IDs.
	time.Sleep(500 * time.Millisecond)
}

// hostName returns the human participant name from $USER or "host".
func hostName() string {
	name := os.Getenv("USER")
	if name == "" {
		name = "host"
	}
	return name
}
