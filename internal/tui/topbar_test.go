package tui

import (
	"strings"
	"testing"
)

func TestTopBarRendersRequiredElements(t *testing.T) {
	tb := NewTopBar("test", 1234)
	tb.SetWidth(100)
	output := tb.View()

	if !contains(output, ":1234") {
		t.Errorf("topbar should contain port \":1234\", got: %q", output)
	}
	if !contains(output, "test") {
		t.Errorf("topbar should contain topic text, got: %q", output)
	}
	if !contains(output, "parley") {
		t.Errorf("topbar should contain app name \"parley\", got: %q", output)
	}
}

func TestTopBarPortIsPresent(t *testing.T) {
	tb := NewTopBar("hello", 8080)
	tb.SetWidth(80)
	output := tb.View()

	// The port should be present
	if !contains(output, ":8080") {
		t.Errorf("topbar should contain port \":8080\", got: %q", output)
	}
}

func TestTopBarNoPortOrTopic(t *testing.T) {
	tb := NewTopBar("", 0)
	tb.SetWidth(80)
	output := tb.View()

	if !contains(output, "parley") {
		t.Errorf("topbar should always contain app name \"parley\", got: %q", output)
	}
}

func TestTopBarTopicLabel(t *testing.T) {
	tb := NewTopBar("my-topic", 9999)
	tb.SetWidth(100)
	output := tb.View()

	if !contains(output, "my-topic") {
		t.Errorf("topbar should contain topic text \"my-topic\", got: %q", output)
	}
}

func TestTopBar_DefaultHostIsLocalhost(t *testing.T) {
	tb := NewTopBar("my topic", 8080)
	tb.SetWidth(80)
	view := tb.View()
	if !strings.Contains(view, "localhost:8080") {
		t.Errorf("expected topbar to contain %q, got:\n%s", "localhost:8080", view)
	}
}

func TestTopBar_SetHostChangesDisplay(t *testing.T) {
	tb := NewTopBar("my topic", 9000)
	tb.SetHost("192.168.1.50")
	tb.SetWidth(80)
	view := tb.View()
	if !strings.Contains(view, "192.168.1.50:9000") {
		t.Errorf("expected topbar to contain %q, got:\n%s", "192.168.1.50:9000", view)
	}
	if strings.Contains(view, "localhost") {
		t.Errorf("expected topbar NOT to contain %q after SetHost, got:\n%s", "localhost", view)
	}
}
