package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
)

type Meta struct {
	RoomID          string `json:"room_id"`
	Name            string `json:"name"`
	Role            string `json:"role"`
	Descriptor      string `json:"descriptor"`
	Status          string `json:"status"`
	LastReceivedSeq int64  `json:"last_received_seq"`
	LastSeenSeq     int64  `json:"last_seen_seq"`
}

type Store struct {
	MetaPath   string
	EventsPath string
}

func NewStore(metaPath, eventsPath string) *Store {
	return &Store{MetaPath: metaPath, EventsPath: eventsPath}
}

func (s *Store) LoadMeta() (Meta, error) {
	data, err := os.ReadFile(s.MetaPath)
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
	dir := filepath.Dir(s.MetaPath)
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
	return os.Rename(tmpPath, s.MetaPath)
}

func (s *Store) AppendLocal(ev model.Event) error {
	if ev.Seq <= 0 {
		return fmt.Errorf("event seq must be positive")
	}
	meta, err := s.LoadMeta()
	if err != nil {
		return err
	}
	log := eventlog.New(s.EventsPath)
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
	if ev.Seq < maxSeq {
		return fmt.Errorf("event seq %d is older than last local seq %d", ev.Seq, maxSeq)
	}

	if err := log.AppendAssigned(ev); err != nil {
		return err
	}
	return s.advanceLastReceived(meta, ev.Seq)
}

func (s *Store) Inbox(peek bool) ([]model.Event, error) {
	meta, err := s.LoadMeta()
	if err != nil {
		return nil, err
	}
	events, err := eventlog.New(s.EventsPath).AfterSeq(meta.LastSeenSeq, 0)
	if err != nil {
		return nil, err
	}
	if peek || len(events) == 0 {
		return events, nil
	}
	for _, ev := range events {
		if ev.Seq > meta.LastSeenSeq {
			meta.LastSeenSeq = ev.Seq
		}
	}
	if err := s.SaveMeta(meta); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) WaitReadyBatch(self string) ([]model.Event, error) {
	meta, err := s.LoadMeta()
	if err != nil {
		return nil, err
	}
	events, err := eventlog.New(s.EventsPath).AfterSeq(meta.LastSeenSeq, 0)
	if err != nil {
		return nil, err
	}
	batch := make([]model.Event, 0, len(events))
	for _, ev := range events {
		batch = append(batch, ev)
		if ev.Type == model.EventMessage && ev.Actor != self {
			return batch, nil
		}
	}
	return []model.Event{}, nil
}

func (s *Store) advanceLastReceived(meta Meta, seq int64) error {
	if seq <= meta.LastReceivedSeq {
		return nil
	}
	meta.LastReceivedSeq = seq
	return s.SaveMeta(meta)
}

func sameEvent(a, b model.Event) bool {
	aData, aErr := json.Marshal(a)
	bData, bErr := json.Marshal(b)
	if aErr != nil || bErr != nil {
		return false
	}
	return bytes.Equal(aData, bData)
}
