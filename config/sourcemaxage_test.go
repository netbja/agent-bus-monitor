package config

import (
	"testing"
	"time"
)

// model-detection §3.2: models.source_max_age, 7 days by default; a value
// in days or a Go duration, above 0; a bad one is skipped (env) or refused
// (file), never a broken check.
func TestModelsSourceMaxAge(t *testing.T) {
	k, ok := Lookup("models.source_max_age")
	if !ok || k.Default != 7*24*time.Hour || k.Kind != Duration || k.Group() != GroupHealth {
		t.Fatalf("key %+v", k)
	}
	for raw, want := range map[string]time.Duration{"14d": 14 * 24 * time.Hour, "36h": 36 * time.Hour, "1d": 24 * time.Hour} {
		if v, res := k.env(raw); res != envUse || v != want {
			t.Errorf("env %q: %v %v", raw, v, res)
		}
		if v, why := k.file(raw); why != "" || v != want {
			t.Errorf("file %q: %v %q", raw, v, why)
		}
	}
	for _, raw := range []string{"0", "0d", "-1d", "soon", "400d"} {
		if _, res := k.env(raw); res != envSkip {
			t.Errorf("env %q accepted", raw)
		}
		if _, why := k.file(raw); why == "" {
			t.Errorf("file %q accepted", raw)
		}
	}
}
