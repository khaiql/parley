package main

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/client"
	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/persistence"
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
	store := persistence.NewJSONStore(defaultParleyDir())

	serverState, err := createServerState(store)
	if err != nil {
		return err
	}

	srv, err := startServer(serverState)
	if err != nil {
		return err
	}

	port := srv.Port()
	roomID := serverState.GetID()
	fmt.Fprintf(os.Stderr, "Parley server listening on port %d\n", port)
	fmt.Fprintf(os.Stderr, "Room ID: %s\n", roomID)

	stopAutoSave := startPersistence(store, srv)
	defer stopAutoSave()

	c, err := client.New(srv.Addr())
	if err != nil {
		srv.Close()
		return fmt.Errorf("host: connect client: %w", err)
	}
	defer c.Close()

	sendFn := func(text string, mentions []string) {
		_ = c.Send(protocol.Content{Type: "text", Text: text}, mentions)
	}

	tuiState, reg, cmdCtx := setupTUIState(sendFn, c, port, store, srv)

	app := tui.NewApp(hostTopic, port, tui.InputModeHuman, hostName(), sendFn)
	app.SetCommandRegistry(reg, cmdCtx)
	app.SetRoomState(tuiState)

	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())

	bridgeEvents(p, tuiState)
	startNetworkLoop(c, tuiState, hostName())

	_, err = p.Run()

	tuiState.Close()
	shutdownHost(srv)
	return err
}

// createServerState builds the authoritative room.State for the server.
// For resume, it loads from the persistence store.
func createServerState(store *persistence.JSONStore) (*room.State, error) {
	serverState := room.New(nil, command.Context{})
	if hostYolo {
		serverState.SetAutoApprove(true)
	}

	if hostResume != "" {
		snapshot, err := store.Load(hostResume)
		if err != nil {
			return nil, fmt.Errorf("host: load room %q: %w", hostResume, err)
		}
		serverState.Restore(snapshot.RoomID, snapshot.Topic, snapshot.Participants, snapshot.Messages, snapshot.AutoApprove)
		if hostTopic == "" {
			hostTopic = snapshot.Topic
		}
		if hostYolo {
			serverState.SetAutoApprove(true)
		}
	} else {
		if hostTopic == "" {
			return nil, fmt.Errorf("host: --topic is required when not using --resume")
		}
		serverState.Restore(serverState.GetID(), hostTopic, nil, nil, hostYolo)
	}

	return serverState, nil
}

// startServer creates and starts the TCP server with the given state.
func startServer(serverState *room.State) (*server.TCPServer, error) {
	addr := fmt.Sprintf(":%d", hostPort)
	srv, err := server.New(addr, serverState)
	if err != nil {
		return nil, fmt.Errorf("host: create server: %w", err)
	}
	go srv.Serve()
	return srv, nil
}

// setupTUIState creates the client-side room.State for the TUI, along with
// the command registry and context. The TUI state is fed by HandleServerMessage
// from the network loop — separate from the server's authoritative state.
func setupTUIState(
	sendFn func(string, []string),
	c *client.TCPClient,
	port int,
	store *persistence.JSONStore,
	srv *server.TCPServer,
) (*room.State, *command.Registry, command.Context) {
	tuiState := room.New(nil, command.Context{})
	tuiState.SetSendFn(sendFn)

	reg := command.NewRegistry()
	reg.Register(command.InfoCommand)
	reg.Register(command.SaveCommand)
	reg.Register(command.SendCommandCommand)

	cmdCtx := command.Context{
		Room: &RoomAdapter{state: tuiState, port: port},
		SaveFn: func() error {
			return store.Save(srv.Snapshot())
		},
		SendFn: func(to, text string) {
			_ = c.Send(protocol.Content{Type: "text", Text: fmt.Sprintf("@%s %s", to, text)}, []string{to})
		},
	}
	tuiState.SetCommands(reg, cmdCtx)

	return tuiState, reg, cmdCtx
}

// bridgeEvents subscribes to room events and forwards them to the TUI.
func bridgeEvents(p *tea.Program, tuiState *room.State) {
	events := tuiState.Subscribe()
	go func() {
		for e := range events {
			p.Send(e)
		}
	}()
}

// startNetworkLoop joins the room and feeds incoming server messages to the
// TUI's room.State.
func startNetworkLoop(c *client.TCPClient, tuiState *room.State, name string) {
	dir, _ := os.Getwd()
	repo := detectRepo()
	go func() {
		_ = c.Join(protocol.JoinParams{
			Name:      name,
			Role:      "human",
			Directory: dir,
			Repo:      repo,
		})
		for msg := range c.Incoming() {
			tuiState.HandleServerMessage(msg)
		}
	}()
}

// shutdownHost closes the server and pauses to let agents save session IDs.
func shutdownHost(srv *server.TCPServer) {
	srv.Close()
	time.Sleep(500 * time.Millisecond)
}

// startPersistence saves room state immediately, on a 30s interval, and on
// exit. Reads from srv.Snapshot() which is concurrency-safe.
func startPersistence(store *persistence.JSONStore, srv *server.TCPServer) func() {
	saveSnapshot := func() error {
		return store.Save(srv.Snapshot())
	}

	if err := saveSnapshot(); err != nil {
		fmt.Fprintf(os.Stderr, "Initial save failed: %v\n", err)
	}

	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := saveSnapshot(); err != nil {
					fmt.Fprintf(os.Stderr, "Auto-save failed: %v\n", err)
				}
			case <-stop:
				return
			}
		}
	}()

	return func() {
		close(stop)
		if err := saveSnapshot(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save room: %v\n", err)
		}
	}
}

// hostName returns the human participant name from $USER or "host".
func hostName() string {
	name := os.Getenv("USER")
	if name == "" {
		name = "host"
	}
	return name
}
