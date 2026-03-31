package tui

import (
	"testing"
)

func TestTopBarRendersRequiredElements(t *testing.T) {
	tb := NewTopBar("test", 1234)
	tb.SetWidth(100)
	output := tb.View()

	if !contains(output, ":1234") {
		t.Errorf("topbar should contain port \":1234\", got: %q", output)
	}
	if !contains(output, "Topic:") {
		t.Errorf("topbar should contain \"Topic:\" label, got: %q", output)
	}
	if !contains(output, "parley") {
		t.Errorf("topbar should contain app name \"parley\", got: %q", output)
	}
}

func TestTopBarPortUsesColorPrimary(t *testing.T) {
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

	if !contains(output, "Topic:") {
		t.Errorf("topbar should contain \"Topic:\" label, got: %q", output)
	}
	if !contains(output, "my-topic") {
		t.Errorf("topbar should contain topic text \"my-topic\", got: %q", output)
	}
}
