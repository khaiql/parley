package herdr

import (
	"strings"
	"testing"
)

func TestReadFormCancelAndInput(t *testing.T) {
	for _, input := range []string{"\x1b", "", "\n", strings.Repeat("x", 513) + "\n", "bad\x00\n", "\xff\n"} {
		if _, err := ReadForm(strings.NewReader(input), "start"); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	for _, input := range []string{"room-1\n", "room-X\b1\r", "room-X\x7f1\n"} {
		got, err := ReadForm(strings.NewReader(input), "join")
		if err != nil || got != "room-1" {
			t.Fatalf("%q %v", got, err)
		}
	}
	got, err := ReadForm(strings.NewReader("café\x7f!\n"), "start")
	if err != nil || got != "caf!" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := ReadForm(strings.NewReader("bad/id\n"), "join"); err == nil {
		t.Fatal("unsafe id accepted")
	}
}

func TestPopupTarget(t *testing.T) {
	socket := "/tmp/original.sock"
	target := `{"machine_id":"self","herdr_socket":"/tmp/original.sock","workspace_id":"w1","pane_id":"w1:p2","terminal_id":"term_original","kind":"codex","native_session":"original"}`
	got, err := DecodeTarget(target, socket)
	if err != nil || got.TerminalID != "term_original" || got.PaneID != "w1:p2" {
		t.Fatalf("%+v %v", got, err)
	}
	for _, input := range []string{strings.Replace(target, "original.sock", "other.sock", 1), strings.Replace(target, `"machine_id"`, `"root":"/tmp/evil","machine_id"`, 1), target + target} {
		if _, err := DecodeTarget(input, socket); err == nil {
			t.Fatal("untrusted target accepted")
		}
	}
}
