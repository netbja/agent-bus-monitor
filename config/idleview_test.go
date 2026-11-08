package config

import (
	"testing"
	"time"
)

// busmon idle view §2: display.idle_view_after, off by default; 0 or a
// duration from 1m to 24h. A file value outside is refused, an env value
// outside is skipped (the file's value or off applies).
func TestIdleViewAfter(t *testing.T) {
	k, ok := Lookup("display.idle_view_after")
	if !ok || k.Default != time.Duration(0) || k.Kind != Duration || k.Group() != GroupDisplay ||
		k.Env != "AGENT_BUS_IDLE_VIEW_AFTER" || k.ReadBy != "busmon" {
		t.Fatalf("key %+v", k)
	}
	for raw, want := range map[string]time.Duration{"0": 0, "1m": time.Minute, "10m": 10 * time.Minute, "24h": 24 * time.Hour} {
		if v, why := k.file(raw); why != "" || v != want {
			t.Errorf("file %q: %v %q", raw, v, why)
		}
		if v, res := k.env(raw); res != envUse || v != want {
			t.Errorf("env %q: %v %v", raw, v, res)
		}
	}
	for raw, want := range map[string]time.Duration{"60": time.Minute, "600": 10 * time.Minute, "86400": 24 * time.Hour} { // whole seconds, env only
		if v, res := k.env(raw); res != envUse || v != want {
			t.Errorf("env %q: %v %v", raw, v, res)
		}
	}
	for _, raw := range []string{"30s", "59s", "59", "25h", "86401", "-1m", "soon", ""} {
		if _, why := k.file(raw); why != "want 0 (off) or a duration from 1m to 24h, such as \"10m\"" {
			t.Errorf("file %q: %q", raw, why)
		}
		if _, res := k.env(raw); res != envSkip {
			t.Errorf("env %q not skipped", raw)
		}
	}
	if _, why := k.file(int64(600)); why == "" {
		t.Error("file number accepted")
	}
}
