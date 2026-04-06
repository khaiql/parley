package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/khaiql/parley/internal/protocol"
)

const scanBufSize = 1024 * 1024 // 1 MB

// TcpServer accepts TCP connections and routes messages through a single Room.
type TcpServer struct {
	listener net.Listener
	room     *Room
}

// New creates a new Server listening on addr with the given room topic.
func New(addr string, topic string) (*TcpServer, error) {
	return NewWithRoom(addr, NewRoom(topic))
}

// NewWithRoom creates a new Server listening on addr using an existing Room.
// Use this to resume a previously saved room (e.g. loaded via LoadRoom).
func NewWithRoom(addr string, room *Room) (*TcpServer, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &TcpServer{
		listener: ln,
		room:     room,
	}, nil
}

// Addr returns the server's listening address as host:port.
func (s *TcpServer) Addr() string {
	return s.listener.Addr().String()
}

// Port returns the server's listening port.
func (s *TcpServer) Port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

// Room returns the server's room.
func (s *TcpServer) Room() *Room {
	return s.room
}

// Serve runs the accept loop. It blocks until the listener is closed.
func (s *TcpServer) Serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

// Close shuts down the server listener.
func (s *TcpServer) Close() error {
	return s.listener.Close()
}

// handleConn manages the lifecycle of a single client connection.
func (s *TcpServer) handleConn(conn net.Conn) {
	defer conn.Close()

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, scanBufSize), scanBufSize)

	var cc *ClientConn

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

			source := "human"
			if params.AgentType != "" {
				source = "agent"
			}

			cc = &ClientConn{
				Name:      params.Name,
				Role:      params.Role,
				Directory: params.Directory,
				Repo:      params.Repo,
				AgentType: params.AgentType,
				Source:    source,
			}

			state, joinErr := s.room.Join(cc)
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

			// Send room.state back to the joining client.
			notif := protocol.NewNotification(protocol.MethodState, state)
			if data, err := protocol.EncodeLine(notif); err == nil {
				_, _ = conn.Write(data)
			}

			// Notify other participants. Use the effective role from the
			// room state (may differ from params.Role on reconnection).
			effectiveRole := params.Role
			for _, p := range state.Participants {
				if p.Name == params.Name {
					effectiveRole = p.Role
					break
				}
			}
			jp := protocol.JoinedParams{
				Name:      params.Name,
				Role:      effectiveRole,
				Directory: params.Directory,
				Repo:      params.Repo,
				AgentType: params.AgentType,
				JoinedAt:  time.Now().UTC(),
			}
			s.room.BroadcastJoined(jp)
			s.room.BroadcastSystem(fmt.Sprintf("%s joined", params.Name))

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
			s.room.Broadcast(cc.Name, cc.Source, cc.Role, params.Content[0], params.Mentions)

		case protocol.MethodStatus:
			if cc == nil {
				continue
			}
			var params protocol.StatusParams
			if err := json.Unmarshal(raw.Params, &params); err != nil {
				continue
			}
			// Override name with the authenticated connection name for safety.
			params.Name = cc.Name
			s.room.BroadcastStatus(params)
		}
	}

	// Client disconnected.
	if cc != nil {
		name := cc.Name
		s.room.Leave(name)
		s.room.BroadcastLeft(protocol.LeftParams{Name: name})
		s.room.BroadcastSystem(fmt.Sprintf("%s left", name))
	}
}
