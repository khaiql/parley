package adapter

import (
	"encoding/json"
	"errors"
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
	if err := os.MkdirAll(filepath.Dir(s.MetaPath), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.MetaPath, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(s.MetaPath, 0o600)
}

func (s *Store) AppendLocal(ev model.Event) error {
	if err := eventlog.New(s.EventsPath).AppendAssigned(ev); err != nil {
		return err
	}
	meta, err := s.LoadMeta()
	if err != nil {
		return err
	}
	if ev.Seq > meta.LastReceivedSeq {
		meta.LastReceivedSeq = ev.Seq
		return s.SaveMeta(meta)
	}
	return nil
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
