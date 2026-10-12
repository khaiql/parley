package herdr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Room and Rooms read the durable host registry, never the discovery cache.
func (s *Store) Room(id string) (Room, error) {
	var room Room
	if e := safeID(id); e != nil {
		return room, e
	}
	e := s.locked(func() error { var e error; room, e = s.readRoom(id); return e })
	return room, e
}
func (s *Store) readRoom(id string) (Room, error) {
	var room Room
	dir, e := s.recordDir("rooms")
	if e != nil {
		return room, e
	}
	if e = readJSON(filepath.Join(dir, id+".json"), &room); e != nil {
		return room, e
	}
	if room.ID != id {
		return room, fmt.Errorf("room record id mismatch")
	}
	return room, nil
}
func (s *Store) Rooms() (map[string]Room, error) {
	rooms := map[string]Room{}
	e := s.locked(func() error {
		dir, e := s.recordDir("rooms")
		if e != nil {
			return e
		}
		entries, e := os.ReadDir(dir)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), ".json")
			if e := safeID(id); e != nil {
				return e
			}
			room, e := s.readRoom(id)
			if e != nil {
				return e
			}
			rooms[id] = room
		}
		return nil
	})
	return rooms, e
}
