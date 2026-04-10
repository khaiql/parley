package tui

import "testing"

func TestStatusBarHidesConnectedWhenHealthy(t *testing.T) {
	// Connected dot is only shown when disconnected (it's noise when always green).
	sb := NewStatusBar()
	sb.SetWidth(80)
	sb.SetConnected(true)
	out := stripANSI(sb.View())
	if contains(out, "connected") {
		t.Errorf("expected no 'connected' indicator when healthy, got: %q", out)
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

func TestStatusBarScrollIndicatorShownWhenScrolledUp(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)
	sb.SetScrollPosition(0.5, false)
	out := stripANSI(sb.View())
	if !contains(out, "↑") {
		t.Errorf("expected scroll indicator when not at bottom, got: %q", out)
	}
	if !contains(out, "50%") {
		t.Errorf("expected '50%%' in scroll indicator, got: %q", out)
	}
}

func TestStatusBarScrollIndicatorHiddenAtBottom(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)
	sb.SetScrollPosition(1.0, true)
	out := stripANSI(sb.View())
	if contains(out, "↑") {
		t.Errorf("expected no scroll indicator when at bottom, got: %q", out)
	}
}

func TestStatusBarMouseHintShowsSelectWhenEnabled(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(120)
	sb.SetSidebarVisible(true)
	sb.SetMouseEnabled(true)
	out := stripANSI(sb.View())
	if !contains(out, `Ctrl+\ select`) {
		t.Errorf(`expected 'Ctrl+\ select' hint when mouse enabled, got: %q`, out)
	}
}

func TestStatusBarMouseHintShowsScrollWhenDisabled(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(120)
	sb.SetSidebarVisible(true)
	sb.SetMouseEnabled(false)
	out := stripANSI(sb.View())
	if !contains(out, `Ctrl+\ scroll`) {
		t.Errorf(`expected 'Ctrl+\ scroll' hint when mouse disabled, got: %q`, out)
	}
}
