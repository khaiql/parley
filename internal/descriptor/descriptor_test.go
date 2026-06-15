package descriptor

import "testing"

func TestParseDescriptor(t *testing.T) {
	d, err := Parse("parley://127.0.0.1:49231/01jabc")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.Host != "127.0.0.1" || d.Port != 49231 || d.RoomID != "01jabc" {
		t.Fatalf("descriptor = %#v", d)
	}
	if got := d.String(); got != "parley://127.0.0.1:49231/01jabc" {
		t.Fatalf("String = %q", got)
	}
}

func TestParseDescriptorIPv6(t *testing.T) {
	d, err := Parse("parley://[::1]:49231/01jabc")
	if err != nil {
		t.Fatalf("Parse IPv6: %v", err)
	}
	if d.Host != "::1" || d.Port != 49231 {
		t.Fatalf("descriptor = %#v", d)
	}
}

func TestParseDescriptorRejectsQuery(t *testing.T) {
	if _, err := Parse("parley://127.0.0.1:49231/01jabc?token=x"); err == nil {
		t.Fatal("expected query string to be rejected")
	}
}
