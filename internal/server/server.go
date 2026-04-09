package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/room"
)

const scanBufSize = 1024 * 1024 // 1 MB

// TCPServer accepts TCP connections and routes messages through room.State.
type TCPServer struct {
	listener net.Listener
	state    *room.State
	conns    *ConnectionManager
	mu       sync.Mutex
	wg       sync.WaitGroup
	done     chan struct{} // closed when Serve's accept loop exits
}

// New creates a new Server listening on addr using the given room.State.
func New(addr string, state *room.State) (*TCPServer, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &TCPServer{
		listener: ln,
		state:    state,
		conns:    NewConnectionManager(),
		done:     make(chan struct{}),
	}, nil
}

// Addr returns the server's listening address as host:port.
func (s *TCPServer) Addr() string {
	return s.listener.Addr().String()
}

// Port returns the server's listening port.
func (s *TCPServer) Port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

// Serve runs the accept loop. It blocks until the listener is closed.
func (s *TCPServer) Serve() {
	defer close(s.done)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(conn)
		}()
	}
}

// Snapshot returns a consistent room state snapshot, safe for concurrent use.
// It acquires the server mutex to ensure no handleConn goroutine is mutating
// state mid-read.
func (s *TCPServer) Snapshot() protocol.RoomSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return protocol.RoomSnapshot{
		RoomID:       s.state.GetID(),
		Topic:        s.state.GetTopic(),
		AutoApprove:  s.state.AutoApprove(),
		Participants: s.state.GetParticipants(),
		Messages:     s.state.Messages(),
	}
}

// Close shuts down the server listener and waits for all active connection
// handlers to finish.
func (s *TCPServer) Close() error {
	err := s.listener.Close()
	<-s.done    // wait for accept loop to exit — no more wg.Add() calls after this
	s.wg.Wait() // safe: all Add() calls have completed
	return err
}

// handleConn manages the lifecycle of a single client connection.
func (s *TCPServer) handleConn(conn net.Conn) {
	defer conn.Close()

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, scanBufSize), scanBufSize)

	var cc *ClientConn
	var name, source, role string

	for sc.Scan() {
		line := sc.Bytes()

		raw, err := protocol.DecodeLine(line)
		if err != nil {
			continue
		}

		switch raw.Method {
		case protocol.MethodJoin:
			var params protocol.JoinParams
			if err := json.Unmarshal(raw.Params, &params); err != nil {
				continue
			}

			joinSource := "human"
			if params.AgentType != "" {
				joinSource = "agent"
			}

			s.mu.Lock()
			stateParams, joinErr := s.state.Join(params.Name, params.Role, params.Directory, params.Repo, params.AgentType, joinSource)
			s.mu.Unlock()

			if joinErr != nil {
				resp := protocol.Response{
					JSONRPC: "2.0",
					ID:      0,
					Error:   &protocol.RPCError{Code: -1, Message: joinErr.Error()},
				}
				if data, err := protocol.EncodeLine(resp); err == nil {
					_, _ = conn.Write(data)
				}
				return
			}

			// Capture the effective role and assigned colour from the state
			// (may differ on reconnection).
			effectiveRole := params.Role
			var assignedColor string
			for _, p := range stateParams.Participants {
				if p.Name == params.Name {
					effectiveRole = p.Role
					assignedColor = p.Color
					break
				}
			}

			name = params.Name
			source = joinSource
			role = effectiveRole

			cc = &ClientConn{
				Name: params.Name,
				Send: make(chan []byte, 64),
				Done: make(chan struct{}),
			}

			// Register connection for broadcasting.
			s.conns.Add(params.Name, cc)

			// Send room.state back to the joining client.
			notif := protocol.NewNotification(protocol.MethodState, stateParams)
			if data, err := protocol.EncodeLine(notif); err == nil {
				_, _ = conn.Write(data)
			}

			// Notify other participants.
			jp := protocol.JoinedParams{
				Name:      params.Name,
				Role:      effectiveRole,
				Color:     assignedColor,
				Directory: params.Directory,
				Repo:      params.Repo,
				AgentType: params.AgentType,
				JoinedAt:  time.Now().UTC(),
			}
			joinedNotif := protocol.NewNotification(protocol.MethodJoined, jp)
			if data, err := protocol.EncodeLine(joinedNotif); err == nil {
				s.conns.BroadcastExcept(params.Name, data)
			}

			// Broadcast system message.
			s.mu.Lock()
			sysMsg := s.state.AddSystemMessage(fmt.Sprintf("%s joined", params.Name))
			s.mu.Unlock()
			sysNotif := protocol.NewNotification(protocol.MethodMessage, sysMsg)
			if data, err := protocol.EncodeLine(sysNotif); err == nil {
				s.conns.Broadcast(data)
			}

			// Start writer goroutine for this connection.
			go func(c net.Conn, client *ClientConn) {
				for {
					select {
					case data := <-client.Send:
						_, _ = c.Write(data)
					case <-client.Done:
						return
					}
				}
			}(conn, cc)

		case protocol.MethodSend:
			if cc == nil {
				continue
			}
			var params protocol.SendParams
			if err := json.Unmarshal(raw.Params, &params); err != nil {
				continue
			}
			// Use first content item; if none, skip.
			if len(params.Content) == 0 {
				continue
			}

			s.mu.Lock()
			msg := s.state.AddMessage(name, source, role, params.Content[0])
			s.mu.Unlock()

			msgNotif := protocol.NewNotification(protocol.MethodMessage, msg)
			if data, err := protocol.EncodeLine(msgNotif); err == nil {
				s.conns.Broadcast(data)
			}

		case protocol.MethodStatus:
			if cc == nil {
				continue
			}
			var params protocol.StatusParams
			if err := json.Unmarshal(raw.Params, &params); err != nil {
				continue
			}
			// Override name with the authenticated connection name for safety.
			params.Name = name

			s.mu.Lock()
			s.state.UpdateStatus(params.Name, params.Status)
			s.mu.Unlock()

			statusNotif := protocol.NewNotification(protocol.MethodStatus, params)
			if data, err := protocol.EncodeLine(statusNotif); err == nil {
				s.conns.BroadcastExcept(name, data)
			}
		}
	}

	// Client disconnected.
	if cc != nil {
		s.mu.Lock()
		s.state.Leave(name)
		sysMsg := s.state.AddSystemMessage(fmt.Sprintf("%s left", name))
		s.mu.Unlock()

		s.conns.Remove(name)

		leftNotif := protocol.NewNotification(protocol.MethodLeft, protocol.LeftParams{Name: name})
		if data, err := protocol.EncodeLine(leftNotif); err == nil {
			s.conns.Broadcast(data)
		}

		sysNotif := protocol.NewNotification(protocol.MethodMessage, sysMsg)
		if data, err := protocol.EncodeLine(sysNotif); err == nil {
			s.conns.Broadcast(data)
		}
	}
}
