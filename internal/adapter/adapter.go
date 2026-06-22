package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
)

type Meta struct {
	RoomID           string `json:"room_id"`
	Name             string `json:"name"`
	Role             string `json:"role"`
	Descriptor       string `json:"descriptor"`
	ArtifactEndpoint string `json:"artifact_endpoint,omitempty"`
	Status           string `json:"status"`
	LastReceivedSeq  int64  `json:"last_received_seq"`
	LastSeenSeq      int64  `json:"last_seen_seq"`
}

type Store struct {
	metaPath   string
	eventsPath string
}

func NewStore(metaPath, eventsPath string) *Store {
	return &Store{metaPath: metaPath, eventsPath: eventsPath}
}

func (s *Store) DefaultDownloadsDir() string {
	return filepath.Join(filepath.Dir(s.metaPath), "downloads")
}

func (s *Store) EventsAfterSeq(seq int64, limit int) ([]model.Event, error) {
	return eventlog.New(s.eventsPath).AfterSeq(seq, limit)
}

func (s *Store) ReadEvents() ([]model.Event, error) {
	return eventlog.New(s.eventsPath).ReadAll()
}

func (s *Store) LoadMeta() (Meta, error) {
	unlock, err := s.lock()
	if err != nil {
		return Meta{}, err
	}
	defer unlock()
	return s.loadMeta()
}

func (s *Store) loadMeta() (Meta, error) {
	data, err := os.ReadFile(s.metaPath)
	if errors.Is(err, os.ErrNotExist) {
		return Meta{}, nil
	}
	if err != nil {
		return Meta{}, err
	}
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return Meta{}, err
	}
	return meta, nil
}

func (s *Store) SaveMeta(meta Meta) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	return s.saveMetaMerged(meta)
}

func (s *Store) saveMetaMerged(meta Meta) error {
	current, err := s.loadMeta()
	if err != nil {
		return err
	}
	if current.LastReceivedSeq > meta.LastReceivedSeq {
		meta.LastReceivedSeq = current.LastReceivedSeq
	}
	if current.LastSeenSeq > meta.LastSeenSeq {
		meta.LastSeenSeq = current.LastSeenSeq
	}
	return s.writeMeta(meta)
}

func (s *Store) writeMeta(meta Meta) error {
	dir := filepath.Dir(s.metaPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".participant-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.metaPath)
}

func (s *Store) AppendLocal(ev model.Event) error {
	if ev.Seq <= 0 {
		return fmt.Errorf("event seq must be positive")
	}

	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	meta, err := s.loadMeta()
	if err != nil {
		return err
	}
	log := eventlog.New(s.eventsPath)
	events, err := log.ReadAll()
	if err != nil {
		return err
	}

	var maxSeq int64
	for _, existing := range events {
		if existing.Seq > maxSeq {
			maxSeq = existing.Seq
		}
		if existing.Seq != ev.Seq {
			continue
		}
		if !sameEvent(existing, ev) {
			return fmt.Errorf("event seq %d already exists with different content", ev.Seq)
		}
		return s.advanceLastReceived(meta, ev.Seq)
	}
	events = append(events, ev)
	sort.Slice(events, func(i, j int) bool {
		return events[i].Seq < events[j].Seq
	})
	if ev.Seq > maxSeq {
		maxSeq = ev.Seq
	}

	if err := s.writeEvents(events); err != nil {
		return err
	}
	return s.advanceLastReceived(meta, maxSeq)
}

func (s *Store) Inbox(peek bool) ([]model.Event, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	meta, err := s.loadMeta()
	if err != nil {
		return nil, err
	}
	events, err := eventlog.New(s.eventsPath).AfterSeq(meta.LastSeenSeq, 0)
	if err != nil {
		return nil, err
	}
	events = contiguousEvents(events, meta.LastSeenSeq, 0)
	if peek || len(events) == 0 {
		return events, nil
	}
	for _, ev := range events {
		if ev.Seq > meta.LastSeenSeq {
			meta.LastSeenSeq = ev.Seq
		}
	}
	if err := s.writeMeta(meta); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) WaitReadyBatch(self string) ([]model.Event, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	meta, err := s.loadMeta()
	if err != nil {
		return nil, err
	}
	events, err := eventlog.New(s.eventsPath).AfterSeq(meta.LastSeenSeq, 0)
	if err != nil {
		return nil, err
	}
	batch := make([]model.Event, 0, len(events))
	for _, ev := range events {
		if ev.Seq != meta.LastSeenSeq+1 {
			break
		}
		batch = append(batch, ev)
		meta.LastSeenSeq = ev.Seq
		if ev.Type == model.EventMessage && ev.Actor != self {
			return batch, nil
		}
	}
	return []model.Event{}, nil
}

func (s *Store) MarkSeenThrough(seq int64) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	meta, err := s.loadMeta()
	if err != nil {
		return err
	}
	if seq <= meta.LastSeenSeq {
		return nil
	}
	events, err := eventlog.New(s.eventsPath).AfterSeq(meta.LastSeenSeq, 0)
	if err != nil {
		return err
	}
	for _, ev := range events {
		if ev.Seq > seq || ev.Seq != meta.LastSeenSeq+1 {
			break
		}
		meta.LastSeenSeq = ev.Seq
	}
	return s.writeMeta(meta)
}

func (s *Store) MarkReceivedSeen() error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	meta, err := s.loadMeta()
	if err != nil {
		return err
	}
	if meta.LastReceivedSeq <= meta.LastSeenSeq {
		return nil
	}
	meta.LastSeenSeq = meta.LastReceivedSeq
	return s.writeMeta(meta)
}

func (s *Store) TakeUnseenThrough(seq int64) ([]model.Event, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	meta, err := s.loadMeta()
	if err != nil {
		return nil, err
	}
	events, err := eventlog.New(s.eventsPath).AfterSeq(meta.LastSeenSeq, 0)
	if err != nil {
		return nil, err
	}
	out := make([]model.Event, 0, len(events))
	for _, ev := range events {
		if ev.Seq > seq || ev.Seq != meta.LastSeenSeq+1 {
			break
		}
		out = append(out, ev)
		meta.LastSeenSeq = ev.Seq
	}
	if len(out) == 0 {
		return out, nil
	}
	if err := s.writeMeta(meta); err != nil {
		return nil, err
	}
	return out, nil
}

func contiguousEvents(events []model.Event, afterSeq, throughSeq int64) []model.Event {
	out := make([]model.Event, 0, len(events))
	next := afterSeq + 1
	for _, ev := range events {
		if throughSeq > 0 && ev.Seq > throughSeq {
			break
		}
		if ev.Seq != next {
			break
		}
		out = append(out, ev)
		next++
	}
	return out
}

func (s *Store) advanceLastReceived(meta Meta, seq int64) error {
	if seq <= meta.LastReceivedSeq {
		return nil
	}
	meta.LastReceivedSeq = seq
	return s.writeMeta(meta)
}

func sameEvent(a, b model.Event) bool {
	aData, aErr := json.Marshal(a)
	bData, bErr := json.Marshal(b)
	if aErr != nil || bErr != nil {
		return false
	}
	return bytes.Equal(aData, bData)
}

func (s *Store) writeEvents(events []model.Event) (err error) {
	dir := filepath.Dir(s.eventsPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".events-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	enc := json.NewEncoder(tmp)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.eventsPath)
}

func (s *Store) lock() (func(), error) {
	dir := filepath.Dir(s.metaPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.metaPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
