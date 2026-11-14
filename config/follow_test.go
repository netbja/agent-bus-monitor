package config

import "testing"

// display.follow (palette spec §8.3): Activity's auto-scroll, a setting now,
// on by default (answer a), busmon's, in the display group.
func TestDisplayFollowKey(t *testing.T) {
	k, ok := Lookup("display.follow")
	if !ok {
		t.Fatal("display.follow not registered")
	}
	if k.Kind != Bool || k.Default != true || k.ReadBy != "busmon" || k.Group() != GroupDisplay {
		t.Fatalf("%+v group %q", k, k.Group())
	}
	if v, why := k.file(false); why != "" || v != false {
		t.Fatalf("file false: %v %q", v, why)
	}
}
