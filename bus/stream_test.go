package bus

import "testing"

func TestStreamKeyNaming(t *testing.T) {
	if got := StreamKey("busmon", "status"); got != "busmon:status" {
		t.Fatalf("StreamKey = %q, want busmon:status", got)
	}
	if got := PilotKey("busmon"); got != "busmon:pilot" {
		t.Fatalf("PilotKey = %q, want busmon:pilot", got)
	}
	if got := GateKey("busmon", "dev"); got != "busmon:gate:dev" {
		t.Fatalf("GateKey = %q, want busmon:gate:dev", got)
	}
}

func TestValidName(t *testing.T) {
	valid := []string{"dev", "busmon", "dev-1", "hermes", "x", "a_b-2"}
	invalid := []string{"", "Dev", "1dev", "a:b", "dev ", "trading/dev",
		"this-name-is-way-too-long-to-be-accepted-okay"}
	for _, s := range valid {
		if !ValidName(s) {
			t.Errorf("ValidName(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if ValidName(s) {
			t.Errorf("ValidName(%q) = true, want false", s)
		}
	}
}
