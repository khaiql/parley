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

func TestStatusBarShowsHelp(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)
	out := sb.View()
	if !contains(out, "? help") {
		t.Errorf("expected '? help' in output, got: %q", stripANSI(out))
	}
}
