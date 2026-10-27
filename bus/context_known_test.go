package bus

import (
	"testing"
	"time"
)

// A fill is known when it names a session and was at most ContextMaxAge old
// when published; the threshold comes from AGENT_BUS_CONTEXT_ALERT (default
// 80, 0 = off, anything else invalid keeps the default).
func TestContextFillKnownAndThreshold(t *testing.T) {
	at := time.Now().UnixMilli()
	for _, c := range []struct {
		f    ContextFill
		want bool
	}{
		{ContextFill{Pct: 85, Session: "s", ObservedAt: at, At: at}, true},
		{ContextFill{Pct: 85, Session: "s", ObservedAt: at - ContextMaxAge.Milliseconds(), At: at}, true},
		{ContextFill{Pct: 85, Session: "s", ObservedAt: at - ContextMaxAge.Milliseconds() - 1, At: at}, false},
		{ContextFill{Pct: 85, ObservedAt: at, At: at}, false},
	} {
		if got := c.f.Known(); got != c.want {
			t.Errorf("%+v: Known = %v", c.f, got)
		}
	}
	for env, want := range map[string]float64{"": 80, "0": 0, "90": 90, "abc": 80, "-5": 80, "150": 80} {
		if got := ContextAlert(func(string) string { return env }); got != want {
			t.Errorf("%q: %v, want %v", env, got, want)
		}
	}
}
