package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/khaiql/parley/internal/herdr"
)

func TestPickRoomSelectionRefreshAndValidation(t *testing.T) {
	for _, scenario := range []string{"choose", "refresh", "replaced", "closed", "incomplete", "manual", "cancel", "page"} {
		t.Run(scenario, func(t *testing.T) {
			catalog := herdr.RoomCatalog{Rooms: []herdr.RoomChoice{{Room: herdr.Room{ID: "room-a", Topic: "Parser fix"}, Host: "Local", Status: "open", Joined: true}}}
			input, want, lookups, checks := "1\n", "room-a", 1, 1
			switch scenario {
			case "refresh":
				input, lookups = "r\n1\n", 2
			case "replaced":
				input, want = "1\n\x1b", ""
			case "closed":
				catalog.Rooms[0].Status = "closed"
				input, want, checks = "1\n\x1b", "", 0
			case "incomplete":
				catalog.Warnings = []string{"Work: unreachable"}
				input, want, checks = "1\n", "room-a", 1
			case "manual":
				input, want, checks = "m\nmanual-room\n", "manual-room", 0
			case "cancel":
				input, want, checks = "\x1b", "", 0
			case "page":
				for i := 2; i <= 6; i++ {
					catalog.Rooms = append(catalog.Rooms, herdr.RoomChoice{Room: herdr.Room{ID: fmt.Sprintf("room-%d", i)}, Host: "Local", Status: "open"})
				}
				input, want = "n\n6\n", "room-6"
			}
			var output bytes.Buffer
			lookupCount, checkCount := 0, 0
			got, err := pickRoom(strings.NewReader(input), &output, func() (herdr.RoomCatalog, error) {
				lookupCount++
				return catalog, nil
			}, func(room herdr.Room) error {
				checkCount++
				if scenario == "replaced" {
					return errors.New("room_missing: refresh the list")
				}
				if room.ID != want {
					t.Fatal("wrong room selected", room)
				}
				return nil
			})
			if got != want || (want != "" && err != nil) || (want == "" && !errors.Is(err, herdr.ErrFormCancelled)) {
				t.Fatalf("room=%q err=%v output=%q", got, err, output.String())
			}
			if lookupCount != lookups || checkCount != checks {
				t.Fatalf("lookups=%d checks=%d", lookupCount, checkCount)
			}
			for _, text := range []string{"Choose a room", "Parser fix", "Local", "M: enter room ID"} {
				if !strings.Contains(output.String(), text) {
					t.Fatalf("missing %q in %q", text, output.String())
				}
			}
			if scenario == "replaced" && !strings.Contains(output.String(), "room_missing") {
				t.Fatal("hidden validation error")
			}
			if scenario == "incomplete" && !strings.Contains(output.String(), "incomplete") {
				t.Fatal("hidden incomplete lookup")
			}
		})
	}
}

func TestPickRoomSanitizesUntrustedMetadata(t *testing.T) {
	catalog := herdr.RoomCatalog{Rooms: []herdr.RoomChoice{{Room: herdr.Room{ID: "room-a", Topic: "Parser\x1b[2J\nfix\u202e"}, Host: "Work\r\n\x1b[H", Status: "stale", Message: "bad\x1b[2J"}}, Warnings: []string{"offline\x1b[H"}}
	var output bytes.Buffer
	_, err := pickRoom(strings.NewReader("\x1b"), &output, func() (herdr.RoomCatalog, error) { return catalog, nil }, func(herdr.Room) error { t.Fatal("unexpected validation"); return nil })
	if !errors.Is(err, herdr.ErrFormCancelled) || strings.ContainsAny(output.String(), "\x1b\u202e") {
		t.Fatalf("unsafe terminal output %q, err=%v", output.String(), err)
	}
	if !strings.Contains(output.String(), "Parser") || !strings.Contains(output.String(), "stale") {
		t.Fatal("metadata omitted", output.String())
	}
}

func TestPickRoomKeepsWarningAndErrorVisible(t *testing.T) {
	catalog := herdr.RoomCatalog{Warnings: []string{"first", "second", "third", strings.Repeat("long", 100)}}
	var output bytes.Buffer
	_, err := pickRoom(strings.NewReader("bogus\n\x1b"), &output, func() (herdr.RoomCatalog, error) { return catalog, nil }, func(herdr.Room) error { return nil })
	if !errors.Is(err, herdr.ErrFormCancelled) || !strings.Contains(output.String(), "2 more peer warnings") || !strings.Contains(output.String(), "No rooms found") {
		t.Fatal(output.String(), err)
	}
	lastMenu := strings.LastIndex(output.String(), "Choose a room")
	if !strings.Contains(output.String()[lastMenu:], "Enter a visible room number") {
		t.Fatal("selection error scrolled out of current menu", output.String())
	}
	if len([]rune(pickerText(strings.Repeat("x", 512)))) > 67 {
		t.Fatal("unbounded metadata wraps outside popup")
	}
}

type failedPickerReader struct{ failed bool }

func (r *failedPickerReader) Read(p []byte) (int, error) {
	if !r.failed {
		r.failed = true
		return 0, io.ErrClosedPipe
	}
	return copy(p, "\x1b"), nil
}

func TestPickRoomReturnsTerminalReadFailure(t *testing.T) {
	_, err := pickRoom(&failedPickerReader{}, &bytes.Buffer{}, func() (herdr.RoomCatalog, error) { return herdr.RoomCatalog{}, nil }, func(herdr.Room) error { return nil })
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("terminal read error retried", err)
	}
}
