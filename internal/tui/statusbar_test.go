package tui

import "testing"

func TestStatusBarShowsConnected(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)
	sb.SetConnected(true)
	out := sb.View()
	if !contains(out, "connected") {
		t.Errorf("expected 'connected' in output, got: %q", stripANSI(out))
	}
}

func TestStatusBarShowsDisconnected(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)
	sb.SetConnected(false)
	out := sb.View()
	if !contains(out, "disconnected") {
		t.Errorf("expected 'disconnected' in output, got: %q", stripANSI(out))
	}
}

func TestStatusBarNoHelpText(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)
	out := sb.View()
	if contains(out, "help") {
		t.Errorf("expected no help text in status bar, got: %q", stripANSI(out))
	}
}

func TestStatusBarShowsYoloBadgeWhenActive(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)
	sb.SetYolo(true)
	out := sb.View()
	if !contains(out, "YOLO") {
		t.Errorf("expected 'YOLO' badge in output when yolo=true, got: %q", stripANSI(out))
	}
}

func TestStatusBarHidesYoloBadgeWhenInactive(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)
	sb.SetYolo(false)
	out := sb.View()
	if contains(out, "YOLO") {
		t.Errorf("expected no 'YOLO' badge when yolo=false, got: %q", stripANSI(out))
	}
}
