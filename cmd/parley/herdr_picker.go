package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/khaiql/parley/internal/herdr"
)

func pickRoom(in io.Reader, out io.Writer, lookup func() (herdr.RoomCatalog, error), check func(herdr.Room) error) (string, error) {
	reader := bufio.NewReader(in)
	catalog, err := lookup()
	if err != nil {
		return "", err
	}
	page := 0
	notice := ""
	const pageSize = 4
	for {
		fmt.Fprint(out, "Choose a room — number + Enter joins; Escape closes.\r\n")
		if len(catalog.Rooms) == 0 {
			fmt.Fprint(out, "No rooms found in this session.\r\n")
		}
		for i := page * pageSize; i < len(catalog.Rooms) && i < (page+1)*pageSize; i++ {
			choice := catalog.Rooms[i]
			topic := choice.Room.Topic
			if topic == "" {
				topic = choice.Room.ID
			}
			status := choice.Status
			if choice.Joined {
				status += ", already joined"
			}
			fmt.Fprintf(out, "%d. %.56s\r\n   %.20s | %s\r\n   %s\r\n", i+1, pickerText(topic), pickerText(choice.Host), pickerText(status), pickerText(choice.Room.ID))
		}
		if len(catalog.Warnings) > 0 {
			fmt.Fprint(out, "Lookup incomplete; numbered Join disabled.\r\n")
			for _, warning := range catalog.Warnings[:min(2, len(catalog.Warnings))] {
				fmt.Fprintf(out, "  %s\r\n", pickerText(warning))
			}
			if len(catalog.Warnings) > 2 {
				fmt.Fprintf(out, "  %d more peer warnings; lookup incomplete.\r\n", len(catalog.Warnings)-2)
			}
		}
		fmt.Fprintf(out, "Page %d/%d — N: next, P: previous, R: refresh, M: enter room ID\r\n", page+1, max(1, (len(catalog.Rooms)+pageSize-1)/pageSize))
		if notice != "" {
			fmt.Fprintf(out, "%s\r\n", pickerText(notice))
		}
		fmt.Fprint(out, "> ")
		notice = ""
		value, err := herdr.ReadTerminalForm(reader, out, "start")
		if errors.Is(err, herdr.ErrFormCancelled) {
			return "", err
		}
		if err != nil {
			if !errors.Is(err, herdr.ErrFormInput) {
				return "", err
			}
			notice = err.Error()
			continue
		}
		switch strings.ToLower(value) {
		case "r":
			catalog, err = lookup()
			if err != nil {
				return "", err
			}
			page = 0
			continue
		case "m":
			fmt.Fprint(out, "Room ID (Enter submits, Escape closes):\r\n> ")
			return herdr.ReadTerminalForm(reader, out, "join")
		case "n":
			if (page+1)*pageSize < len(catalog.Rooms) {
				page++
			}
			continue
		case "p":
			if page > 0 {
				page--
			}
			continue
		}
		index, err := strconv.Atoi(value)
		if err != nil || index < page*pageSize+1 || index > min(len(catalog.Rooms), (page+1)*pageSize) {
			notice = "Enter a visible room number or R/M/N/P."
			continue
		}
		choice := catalog.Rooms[index-1]
		if len(catalog.Warnings) > 0 || choice.Status != "open" {
			notice = fmt.Sprintf("Cannot join %s. Refresh with R or enter an ID with M.", choice.Status)
			continue
		}
		if err := check(choice.Room); err != nil {
			notice = err.Error()
			continue
		}
		return choice.Room.ID, nil
	}
}

// Remote metadata must not control the popup terminal or reorder its text.
func pickerText(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, value)
	if text := []rune(value); len(text) > 64 {
		return string(text[:64]) + "..."
	}
	return value
}
