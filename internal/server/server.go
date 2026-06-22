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
	"sync/atomic"
	"time"

	"github.com/khaiql/parley/internal/artifact"
	"github.com/khaiql/parley/internal/model"
	"github.com/khaiql/parley/internal/protocol"
)

const (
	scanBufSize     = 1024 * 1024 // 1 MB
	joinHistorySize = 50
	writeTimeout    = 2 * time.Second
)

var mentionRE = regexp.MustCompile(`@([A-Za-z0-9_][A-Za-z0-9_-]*)`)
var nameRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*$`)

type Config struct {
	RoomID            string
	Topic             string
	Log               EventLog
	ArtifactStore     ArtifactRepository
	ArtifactLocalPort int
	ArtifactPath      string
	ArtifactLimits    artifact.Limits
}

type Server struct {
	listener  net.Listener
	cfg       Config
	log       EventLog
	artifacts ArtifactRepository

	mu           sync.Mutex
	participants map[string]model.Participant
	conns        map[string]*clientConn
	activeConns  map[net.Conn]struct{}
	closing      atomic.Bool

	artifactTxMu sync.Mutex

	serveStarted   chan struct{}
	closed         chan struct{}
	wg             sync.WaitGroup
	publishMu      sync.Mutex
	publishCond    *sync.Cond
	publishQueue   []publication
	publishClosing bool
	publisherDone  chan struct{}

	closeOnce sync.Once
	closeErr  error
	startOnce sync.Once
}

type publication struct {
	event      model.Event
	recipients []*clientConn
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

	srv := &Server{
		listener:      ln,
		cfg:           cfg,
		log:           cfg.Log,
		artifacts:     cfg.ArtifactStore,
		participants:  make(map[string]model.Participant),
		conns:         make(map[string]*clientConn),
		activeConns:   make(map[net.Conn]struct{}),
		serveStarted:  make(chan struct{}),
		closed:        make(chan struct{}),
		publisherDone: make(chan struct{}),
	}
	if srv.artifacts == nil {
		srv.artifacts = artifact.NewStore("")
	}
	srv.publishCond = sync.NewCond(&srv.publishMu)
	go srv.runPublisher()
	return srv, nil
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
		if !s.trackAcceptedConn(conn) {
			return
		}
		go func() {
			defer s.wg.Done()
			s.handleConn(conn)
		}()
	}
}

func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.beginClose()
		s.closeErr = s.listener.Close()

		select {
		case <-s.serveStarted:
			<-s.closed
		default:
		}

		s.closeAllConnections()
		s.wg.Wait()
		s.publishMu.Lock()
		s.publishClosing = true
		s.publishCond.Signal()
		s.publishMu.Unlock()
		<-s.publisherDone
		if errors.Is(s.closeErr, net.ErrClosed) {
			s.closeErr = nil
		}
	})
	return s.closeErr
}

func (s *Server) handleConn(conn net.Conn) {
	defer func() {
		s.removeActiveConn(conn)
		_ = conn.Close()
	}()

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, scanBufSize), scanBufSize)

	var (
		cc     *clientConn
		name   string
		joined bool
	)

	defer func() {
		if joined && cc != nil {
			_ = s.cleanupConnectionLeave(name, cc)
			return
		}
		if cc != nil {
			s.removeConnection(name, cc)
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
				if writeErr := writeCurrentResponse(conn, cc, joined, errorResponse("bad_request", "join payload is required")); writeErr != nil {
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
				name = resp.Event.Actor
			}
			if !resp.OK {
				_ = writeResponse(conn, resp)
				return
			}

			cc = s.connectionForName(name)
			if cc == nil {
				_ = writeResponse(conn, errorResponse("internal_error", "join connection was not registered"))
				return
			}
			if err := cc.writeLocked(resp); err != nil {
				if rollbackErr := s.rollbackJoin(name, cc); rollbackErr != nil {
					// The join is durable and compensation failed, so keep the
					// registered online state consistent with the event log.
					cc = nil
					return
				}
				cc = nil
				return
			}
			joined = true

		case protocol.RequestSend:
			if req.Send == nil {
				if writeErr := writeCurrentResponse(conn, cc, joined, errorResponse("bad_request", "send payload is required")); writeErr != nil {
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

		case protocol.RequestHistory:
			if req.History == nil {
				if writeErr := writeCurrentResponse(conn, cc, joined, errorResponse("bad_request", "history payload is required")); writeErr != nil {
					return
				}
				continue
			}
			if !joined {
				if writeErr := writeResponse(conn, errorResponse("not_joined", "join before reading history")); writeErr != nil {
					return
				}
				continue
			}

			resp := s.handleHistory(*req.History)
			if err := cc.write(resp); err != nil {
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
			if resp.OK {
				joined = false
			}
			if err := cc.write(resp); err != nil {
				return
			}
			if !resp.OK {
				continue
			}
			s.removeConnection(name, cc)
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

	if err := sc.Err(); err != nil {
		_ = writeCurrentResponse(conn, cc, joined, errorResponse("bad_request", "invalid request"))
	}
}

func (s *Server) handleJoin(conn net.Conn, req protocol.JoinRequest) protocol.Response {
	name := strings.TrimSpace(req.Name)
	if req.RoomID != s.cfg.RoomID {
		return errorResponse("room_mismatch", "room id does not match this server")
	}
	if name == "" {
		return errorResponse("bad_request", "name is required")
	}
	if !nameRE.MatchString(name) {
		return errorResponse("bad_request", "name must match mention syntax")
	}
	if req.Role == "" {
		req.Role = "participant"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isClosing() {
		return errorResponse("server_closing", "server is closing")
	}
	if s.hasOnlineParticipantLocked(name) {
		return errorResponse("name_taken", "name is already online")
	}

	events, _, err := s.historyLocked(protocol.HistoryRequest{Limit: joinHistorySize}, 0)
	if err != nil {
		return errorResponse("history_failed", err.Error())
	}

	ev, err := s.log.Append(model.Event{
		Type:   model.EventParticipantJoined,
		RoomID: s.cfg.RoomID,
		Actor:  name,
		Payload: model.ParticipantPayload{
			Role:      req.Role,
			Directory: req.Directory,
			Repo:      req.Repo,
		},
	})
	if err != nil {
		return errorResponse("log_append_failed", err.Error())
	}

	cc := &clientConn{name: name, conn: conn}
	cc.writeMu.Lock()
	s.participants[name] = model.Participant{
		Name:      name,
		Role:      req.Role,
		Directory: req.Directory,
		Repo:      req.Repo,
		Online:    true,
	}
	s.conns[name] = cc
	s.enqueuePublicationLocked(name, ev)

	return protocol.Response{
		OK:           true,
		Room:         s.roomMetadata(),
		Participants: s.participantSnapshotLocked(),
		Events:       events,
		Event:        &ev,
		LatestSeq:    ev.Seq,
	}
}

func (s *Server) handleSend(name string, req protocol.SendRequest) protocol.Response {
	s.mu.Lock()
	defer s.mu.Unlock()

	participant, ok := s.participants[name]
	if !ok || !participant.Online {
		return errorResponse("not_joined", "join before sending messages")
	}

	s.artifactTxMu.Lock()
	defer s.artifactTxMu.Unlock()

	artifactMetas, err := s.artifacts.Commit(name, req.ArtifactIDs)
	if err != nil {
		return artifactErrorResponse(err)
	}
	committedArtifactIDs := make([]string, 0, len(artifactMetas))
	messageArtifacts := make([]model.ArtifactMetadata, 0, len(artifactMetas))
	for _, meta := range artifactMetas {
		committedArtifactIDs = append(committedArtifactIDs, meta.ID)
		messageArtifacts = append(messageArtifacts, model.ArtifactMetadata{
			ID:     meta.ID,
			Name:   meta.Name,
			Size:   meta.Size,
			SHA256: meta.SHA256,
		})
	}

	ev, err := s.log.Append(model.Event{
		Type:   model.EventMessage,
		RoomID: s.cfg.RoomID,
		Actor:  name,
		Payload: model.MessagePayload{
			Text:      req.Text,
			Mentions:  parseMentions(req.Text, s.participants),
			Artifacts: messageArtifacts,
		},
	})
	if err != nil {
		_ = s.artifacts.CleanupCommitted(committedArtifactIDs)
		return errorResponse("log_append_failed", err.Error())
	}
	s.enqueuePublicationLocked(name, ev)

	return protocol.Response{
		OK:        true,
		Event:     &ev,
		LatestSeq: ev.Seq,
	}
}

func (s *Server) handleHistory(req protocol.HistoryRequest) protocol.Response {
	s.mu.Lock()
	defer s.mu.Unlock()

	events, latestSeq, err := s.historyLocked(req, 0)
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

	ev, err := s.log.Append(model.Event{
		Type:    model.EventParticipantLeft,
		RoomID:  s.cfg.RoomID,
		Actor:   name,
		Payload: model.ParticipantPayload{Role: participant.Role, Directory: participant.Directory, Repo: participant.Repo},
	})
	if err != nil {
		return errorResponse("log_append_failed", err.Error())
	}

	participant.Online = false
	s.participants[name] = participant
	s.enqueuePublicationLocked(name, ev)

	return protocol.Response{
		OK:        true,
		Event:     &ev,
		LatestSeq: ev.Seq,
	}
}

func (s *Server) hasOnlineParticipantLocked(name string) bool {
	for _, participant := range s.participants {
		if participant.Online && strings.EqualFold(participant.Name, name) {
			return true
		}
	}
	return false
}

func (s *Server) beginClose() {
	s.closing.Store(true)
}

func (s *Server) isClosing() bool {
	return s.closing.Load()
}

func (s *Server) trackAcceptedConn(conn net.Conn) bool {
	if s.isClosing() {
		_ = conn.Close()
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isClosing() {
		_ = conn.Close()
		return false
	}
	s.activeConns[conn] = struct{}{}
	s.wg.Add(1)
	return true
}

func (s *Server) removeActiveConn(conn net.Conn) {
	s.mu.Lock()
	delete(s.activeConns, conn)
	s.mu.Unlock()
}

func (s *Server) connectionForName(name string) *clientConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns[name]
}

func (s *Server) rollbackJoin(name string, cc *clientConn) error {
	s.mu.Lock()
	participant, participantOK := s.participants[name]
	current, connOK := s.conns[name]
	if !participantOK {
		if connOK && current == cc {
			delete(s.conns, name)
		}
		s.mu.Unlock()
		cc.close.Do(func() {
			_ = cc.conn.Close()
		})
		return nil
	}
	ev, err := s.log.Append(model.Event{
		Type:    model.EventParticipantLeft,
		RoomID:  s.cfg.RoomID,
		Actor:   name,
		Payload: model.ParticipantPayload{Role: participant.Role, Directory: participant.Directory, Repo: participant.Repo},
	})
	if err != nil {
		// Leave the participant/connection registered so memory still reflects
		// the durable participant.joined event when compensation cannot be logged.
		s.mu.Unlock()
		return err
	}

	participant.Online = false
	s.participants[name] = participant
	if connOK && current == cc {
		delete(s.conns, name)
	}
	s.enqueuePublicationLocked(name, ev)
	s.mu.Unlock()

	cc.close.Do(func() {
		_ = cc.conn.Close()
	})
	return nil
}

func (s *Server) removeConnection(name string, cc *clientConn) {
	_ = s.artifacts.CleanupStaged(name)
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
	for _, cc := range s.conns {
		conns = append(conns, cc)
	}
	activeConns := make([]net.Conn, 0, len(s.activeConns))
	for conn := range s.activeConns {
		activeConns = append(activeConns, conn)
	}
	s.activeConns = make(map[net.Conn]struct{})
	s.mu.Unlock()

	for _, cc := range conns {
		cc.close.Do(func() {
			_ = cc.conn.Close()
		})
	}
	for _, conn := range activeConns {
		_ = conn.Close()
	}
}

func (s *Server) enqueuePublicationLocked(name string, ev model.Event) {
	pub := s.newPublicationLocked(name, ev)
	s.publishMu.Lock()
	s.publishQueue = append(s.publishQueue, pub)
	s.publishCond.Signal()
	s.publishMu.Unlock()
}

func (s *Server) newPublicationLocked(name string, ev model.Event) publication {
	recipients := make([]*clientConn, 0, len(s.conns))
	for connName, cc := range s.conns {
		if connName == name {
			continue
		}
		participant, ok := s.participants[connName]
		if !ok || !participant.Online {
			continue
		}
		recipients = append(recipients, cc)
	}
	return publication{event: ev, recipients: recipients}
}

func (s *Server) runPublisher() {
	defer close(s.publisherDone)
	for {
		s.publishMu.Lock()
		for len(s.publishQueue) == 0 && !s.publishClosing {
			s.publishCond.Wait()
		}
		if len(s.publishQueue) == 0 && s.publishClosing {
			s.publishMu.Unlock()
			return
		}
		pub := s.publishQueue[0]
		copy(s.publishQueue, s.publishQueue[1:])
		s.publishQueue[len(s.publishQueue)-1] = publication{}
		s.publishQueue = s.publishQueue[:len(s.publishQueue)-1]
		s.publishMu.Unlock()

		s.publishDirect(pub)
	}
}

func (s *Server) publishDirect(pub publication) {
	resp := protocol.Response{OK: true, Event: &pub.event, LatestSeq: pub.event.Seq}
	data, err := protocol.EncodeLine(resp)
	if err != nil {
		return
	}

	var failed []*clientConn
	for _, cc := range pub.recipients {
		if err := cc.writeData(data); err != nil {
			failed = append(failed, cc)
		}
	}
	for _, cc := range failed {
		s.dropConnection(cc)
	}
}

func (s *Server) dropConnection(cc *clientConn) {
	_ = s.cleanupConnectionLeave(cc.name, cc)
}

func (s *Server) cleanupConnectionLeave(name string, cc *clientConn) error {
	_ = s.artifacts.CleanupStaged(name)
	s.mu.Lock()

	participant, ok := s.participants[name]
	current, connOK := s.conns[name]
	if connOK && current != cc {
		s.mu.Unlock()
		cc.close.Do(func() {
			_ = cc.conn.Close()
		})
		return nil
	}
	if !ok || !participant.Online {
		if connOK {
			delete(s.conns, name)
		}
		s.mu.Unlock()
		cc.close.Do(func() {
			_ = cc.conn.Close()
		})
		return nil
	}

	ev, err := s.log.Append(model.Event{
		Type:    model.EventParticipantLeft,
		RoomID:  s.cfg.RoomID,
		Actor:   name,
		Payload: model.ParticipantPayload{Role: participant.Role, Directory: participant.Directory, Repo: participant.Repo},
	})
	if err != nil {
		// Keep the current connection/name registered so memory does not claim
		// a durable leave that the event log failed to record.
		s.mu.Unlock()
		return err
	}

	participant.Online = false
	s.participants[name] = participant
	if connOK {
		delete(s.conns, name)
	}
	s.enqueuePublicationLocked(name, ev)
	s.mu.Unlock()

	cc.close.Do(func() {
		_ = cc.conn.Close()
	})
	return nil
}

func (s *Server) historyLocked(req protocol.HistoryRequest, excludeSeq int64) ([]model.Event, int64, error) {
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
		if excludeSeq > 0 && ev.Seq == excludeSeq {
			continue
		}
		if !req.All && ev.Seq <= req.AfterSeq {
			continue
		}
		events = append(events, ev)
	}
	if req.Limit > 0 && len(events) > req.Limit {
		events = events[len(events)-req.Limit:]
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
		RoomID:            s.cfg.RoomID,
		Topic:             s.cfg.Topic,
		ArtifactLocalPort: s.cfg.ArtifactLocalPort,
		ArtifactPath:      s.cfg.ArtifactPath,
		ArtifactLimits:    s.cfg.ArtifactLimits,
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

func (cc *clientConn) writeLocked(resp protocol.Response) error {
	defer cc.writeMu.Unlock()
	data, err := protocol.EncodeLine(resp)
	if err != nil {
		return err
	}
	return writeData(cc.conn, data)
}

func (cc *clientConn) writeData(data []byte) error {
	cc.writeMu.Lock()
	defer cc.writeMu.Unlock()
	return writeData(cc.conn, data)
}

func writeResponse(conn net.Conn, resp protocol.Response) error {
	data, err := protocol.EncodeLine(resp)
	if err != nil {
		return err
	}
	return writeData(conn, data)
}

func writeData(conn net.Conn, data []byte) error {
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	_, err := conn.Write(data)
	_ = conn.SetWriteDeadline(time.Time{})
	return err
}

func writeCurrentResponse(conn net.Conn, cc *clientConn, joined bool, resp protocol.Response) error {
	if joined && cc != nil {
		return cc.write(resp)
	}
	return writeResponse(conn, resp)
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
