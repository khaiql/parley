package server

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
	"github.com/khaiql/parley/internal/protocol"
)

const scanBufSize = 1024 * 1024 // 1 MB

var mentionRE = regexp.MustCompile(`@([A-Za-z0-9_][A-Za-z0-9_-]*)`)

type Config struct {
	RoomID string
	Topic  string
	Log    *eventlog.Log
}

type Server struct {
	listener net.Listener
	cfg      Config
	log      *eventlog.Log

	mu           sync.Mutex
	participants map[string]model.Participant
	conns        map[string]*clientConn

	serveStarted chan struct{}
	closed       chan struct{}
	wg           sync.WaitGroup

	closeOnce sync.Once
	closeErr  error
	startOnce sync.Once
}

type clientConn struct {
	name string
	conn net.Conn

	writeMu sync.Mutex
	close   sync.Once
}

func New(addr string, cfg Config) (*Server, error) {
	if cfg.Log == nil {
		return nil, errors.New("server: config Log is nil")
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	return &Server{
		listener:     ln,
		cfg:          cfg,
		log:          cfg.Log,
		participants: make(map[string]model.Participant),
		conns:        make(map[string]*clientConn),
		serveStarted: make(chan struct{}),
		closed:       make(chan struct{}),
	}, nil
}

func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

func (s *Server) Port() int {
	addr, ok := s.listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0
	}
	return addr.Port
}

func (s *Server) Serve() {
	started := false
	s.startOnce.Do(func() {
		close(s.serveStarted)
		started = true
	})
	if !started {
		return
	}

	defer close(s.closed)
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

func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.listener.Close()
		s.closeAllConnections()

		select {
		case <-s.serveStarted:
			<-s.closed
		default:
		}

		s.wg.Wait()
		if errors.Is(s.closeErr, net.ErrClosed) {
			s.closeErr = nil
		}
	})
	return s.closeErr
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, scanBufSize), scanBufSize)

	var (
		cc     *clientConn
		name   string
		joined bool
	)

	defer func() {
		if cc != nil {
			s.removeConnection(name, cc)
		}
		if joined {
			resp := s.handleLeave(name)
			if resp.OK && resp.Event != nil {
				s.broadcastEventExcept(name, *resp.Event)
			}
		}
	}()

	for sc.Scan() {
		var req protocol.Request
		if err := protocol.DecodeLine(sc.Bytes(), &req); err != nil {
			if writeErr := writeResponse(conn, errorResponse("bad_request", "invalid request")); writeErr != nil {
				return
			}
			continue
		}

		switch req.Type {
		case protocol.RequestJoin:
			if req.Join == nil {
				if writeErr := writeResponse(conn, errorResponse("bad_request", "join payload is required")); writeErr != nil {
					return
				}
				continue
			}
			if joined {
				if writeErr := cc.write(errorResponse("already_joined", "connection has already joined")); writeErr != nil {
					return
				}
				continue
			}

			resp := s.handleJoin(conn, *req.Join)
			if resp.OK {
				name = req.Join.Name
			}
			if err := writeResponse(conn, resp); err != nil {
				if resp.OK {
					_ = s.handleLeave(name)
				}
				return
			}
			if !resp.OK {
				return
			}

			cc = s.registerConnection(name, conn)
			joined = true
			if resp.Event != nil {
				s.broadcastEventExcept(name, *resp.Event)
			}

		case protocol.RequestSend:
			if req.Send == nil {
				if writeErr := s.writeToJoined(cc, errorResponse("bad_request", "send payload is required")); writeErr != nil {
					return
				}
				continue
			}
			if !joined {
				if writeErr := writeResponse(conn, errorResponse("not_joined", "join before sending messages")); writeErr != nil {
					return
				}
				continue
			}

			resp := s.handleSend(name, *req.Send)
			if err := cc.write(resp); err != nil {
				return
			}
			if resp.OK && resp.Event != nil {
				s.broadcastEventExcept(name, *resp.Event)
			}

		case protocol.RequestHistory:
			if req.History == nil {
				if writeErr := s.writeToJoined(cc, errorResponse("bad_request", "history payload is required")); writeErr != nil {
					return
				}
				continue
			}

			resp := s.handleHistory(*req.History)
			if joined {
				if err := cc.write(resp); err != nil {
					return
				}
			} else if err := writeResponse(conn, resp); err != nil {
				return
			}

		case protocol.RequestLeave:
			if !joined {
				if writeErr := writeResponse(conn, errorResponse("not_joined", "join before leaving")); writeErr != nil {
					return
				}
				return
			}

			resp := s.handleLeave(name)
			joined = false
			if err := cc.write(resp); err != nil {
				return
			}
			s.removeConnection(name, cc)
			if resp.OK && resp.Event != nil {
				s.broadcastEventExcept(name, *resp.Event)
			}
			return

		default:
			target := conn
			if joined {
				if err := cc.write(errorResponse("bad_request", fmt.Sprintf("unknown request type %q", req.Type))); err != nil {
					return
				}
				continue
			}
			if err := writeResponse(target, errorResponse("bad_request", fmt.Sprintf("unknown request type %q", req.Type))); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleJoin(conn net.Conn, req protocol.JoinRequest) protocol.Response {
	_ = conn
	if req.RoomID != s.cfg.RoomID {
		return errorResponse("room_mismatch", "room id does not match this server")
	}
	if strings.TrimSpace(req.Name) == "" {
		return errorResponse("bad_request", "name is required")
	}
	if req.Role == "" {
		req.Role = "participant"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if participant, ok := s.participants[req.Name]; ok && participant.Online {
		return errorResponse("name_taken", "name is already online")
	}

	ev, err := s.log.Append(model.Event{
		Type:   model.EventParticipantJoined,
		RoomID: s.cfg.RoomID,
		Actor:  req.Name,
		Payload: model.ParticipantPayload{
			Role:      req.Role,
			Directory: req.Directory,
			Repo:      req.Repo,
		},
	})
	if err != nil {
		return errorResponse("log_append_failed", err.Error())
	}

	s.participants[req.Name] = model.Participant{
		Name:      req.Name,
		Role:      req.Role,
		Directory: req.Directory,
		Repo:      req.Repo,
		Online:    true,
	}

	events, latestSeq, err := s.historyLocked(protocol.HistoryRequest{All: true})
	if err != nil {
		return errorResponse("history_failed", err.Error())
	}

	return protocol.Response{
		OK:           true,
		Room:         s.roomMetadata(),
		Participants: s.participantSnapshotLocked(),
		Events:       events,
		Event:        &ev,
		LatestSeq:    latestSeq,
	}
}

func (s *Server) handleSend(name string, req protocol.SendRequest) protocol.Response {
	s.mu.Lock()
	defer s.mu.Unlock()

	participant, ok := s.participants[name]
	if !ok || !participant.Online {
		return errorResponse("not_joined", "join before sending messages")
	}

	ev, err := s.log.Append(model.Event{
		Type:   model.EventMessage,
		RoomID: s.cfg.RoomID,
		Actor:  name,
		Payload: model.MessagePayload{
			Text:     req.Text,
			Mentions: parseMentions(req.Text, s.participants),
		},
	})
	if err != nil {
		return errorResponse("log_append_failed", err.Error())
	}

	return protocol.Response{
		OK:        true,
		Event:     &ev,
		LatestSeq: ev.Seq,
	}
}

func (s *Server) handleHistory(req protocol.HistoryRequest) protocol.Response {
	s.mu.Lock()
	defer s.mu.Unlock()

	events, latestSeq, err := s.historyLocked(req)
	if err != nil {
		return errorResponse("history_failed", err.Error())
	}

	return protocol.Response{
		OK:        true,
		Events:    events,
		LatestSeq: latestSeq,
	}
}

func (s *Server) handleLeave(name string) protocol.Response {
	s.mu.Lock()
	defer s.mu.Unlock()

	participant, ok := s.participants[name]
	if !ok || !participant.Online {
		return protocol.Response{OK: true}
	}
	participant.Online = false
	s.participants[name] = participant

	ev, err := s.log.Append(model.Event{
		Type:    model.EventParticipantLeft,
		RoomID:  s.cfg.RoomID,
		Actor:   name,
		Payload: model.ParticipantPayload{Role: participant.Role, Directory: participant.Directory, Repo: participant.Repo},
	})
	if err != nil {
		return errorResponse("log_append_failed", err.Error())
	}

	return protocol.Response{
		OK:        true,
		Event:     &ev,
		LatestSeq: ev.Seq,
	}
}

func (s *Server) registerConnection(name string, conn net.Conn) *clientConn {
	cc := &clientConn{name: name, conn: conn}

	s.mu.Lock()
	s.conns[name] = cc
	s.mu.Unlock()

	return cc
}

func (s *Server) removeConnection(name string, cc *clientConn) {
	s.mu.Lock()
	if current, ok := s.conns[name]; ok && current == cc {
		delete(s.conns, name)
	}
	s.mu.Unlock()
	cc.close.Do(func() {
		_ = cc.conn.Close()
	})
}

func (s *Server) closeAllConnections() {
	s.mu.Lock()
	conns := make([]*clientConn, 0, len(s.conns))
	for name, cc := range s.conns {
		delete(s.conns, name)
		conns = append(conns, cc)
	}
	s.mu.Unlock()

	for _, cc := range conns {
		cc.close.Do(func() {
			_ = cc.conn.Close()
		})
	}
}

func (s *Server) broadcastEventExcept(name string, ev model.Event) {
	resp := protocol.Response{OK: true, Event: &ev, LatestSeq: ev.Seq}
	data, err := protocol.EncodeLine(resp)
	if err != nil {
		return
	}

	s.mu.Lock()
	conns := make([]*clientConn, 0, len(s.conns))
	for connName, cc := range s.conns {
		if connName != name {
			conns = append(conns, cc)
		}
	}
	s.mu.Unlock()

	for _, cc := range conns {
		_ = cc.writeData(data)
	}
}

func (s *Server) writeToJoined(cc *clientConn, resp protocol.Response) error {
	if cc == nil {
		return nil
	}
	return cc.write(resp)
}

func (s *Server) historyLocked(req protocol.HistoryRequest) ([]model.Event, int64, error) {
	all, err := s.log.ReadAll()
	if err != nil {
		return nil, 0, err
	}

	var latestSeq int64
	if len(all) > 0 {
		latestSeq = all[len(all)-1].Seq
	}

	events := make([]model.Event, 0, len(all))
	for _, ev := range all {
		if !ev.Type.IsTranscript() {
			continue
		}
		if !req.All && ev.Seq <= req.AfterSeq {
			continue
		}
		events = append(events, ev)
		if req.Limit > 0 && len(events) >= req.Limit {
			break
		}
	}
	return events, latestSeq, nil
}

func (s *Server) participantSnapshotLocked() []model.Participant {
	participants := make([]model.Participant, 0, len(s.participants))
	for _, participant := range s.participants {
		participants = append(participants, participant)
	}
	sort.Slice(participants, func(i, j int) bool {
		return participants[i].Name < participants[j].Name
	})
	return participants
}

func (s *Server) roomMetadata() *model.RoomMetadata {
	meta := &model.RoomMetadata{
		RoomID: s.cfg.RoomID,
		Topic:  s.cfg.Topic,
	}
	if tcpAddr, ok := s.listener.Addr().(*net.TCPAddr); ok {
		meta.LocalHost = tcpAddr.IP.String()
		meta.LocalPort = tcpAddr.Port
	}
	return meta
}

func (cc *clientConn) write(resp protocol.Response) error {
	data, err := protocol.EncodeLine(resp)
	if err != nil {
		return err
	}
	return cc.writeData(data)
}

func (cc *clientConn) writeData(data []byte) error {
	cc.writeMu.Lock()
	defer cc.writeMu.Unlock()
	_, err := cc.conn.Write(data)
	return err
}

func writeResponse(conn net.Conn, resp protocol.Response) error {
	data, err := protocol.EncodeLine(resp)
	if err != nil {
		return err
	}
	_, err = conn.Write(data)
	return err
}

func errorResponse(code, message string) protocol.Response {
	return protocol.Response{
		OK:    false,
		Error: &protocol.Error{Code: code, Message: message},
	}
}

func parseMentions(text string, participants map[string]model.Participant) []string {
	names := make(map[string]string, len(participants))
	for name, participant := range participants {
		if participant.Online {
			names[strings.ToLower(name)] = name
		}
	}

	var mentions []string
	seen := make(map[string]struct{})
	for _, match := range mentionRE.FindAllStringSubmatch(text, -1) {
		if len(match) != 2 {
			continue
		}
		name, ok := names[strings.ToLower(match[1])]
		if !ok {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		mentions = append(mentions, name)
	}
	return mentions
}
