package update

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// at is a local wall-clock time on 2026-11-28 (a Saturday), in a fixed zone
// so the occurrences do not depend on the host's.
var loopZone = time.FixedZone("test", 3600)

func at(day, h, m int) time.Time { return time.Date(2026, 11, day, h, m, 0, 0, loopZone) }

func passAt(t time.Time) *AutoPass { return &AutoPass{At: t, Result: "skipped"} }

func TestDueInsideTheWindowOncePerOccurrence(t *testing.T) {
	cases := []struct {
		name        string
		st          AutoState
		now         time.Time
		pass, check bool
	}{
		{"no pass yet", AutoState{LastCheck: at(28, 2, 0)}, at(28, 3, 10), true, false},
		{"a pass this occurrence", AutoState{LastPass: passAt(at(28, 3, 5)), LastCheck: at(28, 3, 5)}, at(28, 3, 10), false, false},
		{"a pass at the window's start is this occurrence", AutoState{LastPass: passAt(at(28, 3, 0)), LastCheck: at(28, 3, 0)}, at(28, 4, 59), false, false},
		{"yesterday's pass", AutoState{LastPass: passAt(at(27, 3, 5)), LastCheck: at(27, 3, 5)}, at(28, 3, 10), true, false},
		{"a pass just before the window", AutoState{LastPass: passAt(at(28, 2, 59)), LastCheck: at(28, 2, 59)}, at(28, 3, 0), true, false},
		{"a pass dated in the future counts as none", AutoState{LastPass: passAt(at(29, 3, 5)), LastCheck: at(28, 2, 0)}, at(28, 3, 10), true, false},
		// §4.3 step 4: a deferred pass changes nothing, the next tick tries again (foureyes)
		{"a deferred pass is retried", AutoState{LastPass: &AutoPass{At: at(28, 3, 0), Result: "deferred"}, LastCheck: at(28, 3, 0)}, at(28, 3, 5), true, false},
		{"a current pass is finished", AutoState{LastPass: &AutoPass{At: at(28, 3, 0), Result: "current"}, LastCheck: at(28, 3, 0)}, at(28, 3, 5), false, false},
		{"a failed pass is finished", AutoState{LastPass: &AutoPass{At: at(28, 3, 0), Result: "failed"}, LastCheck: at(28, 3, 0)}, at(28, 3, 5), false, false},
	}
	for _, c := range cases {
		pass, check := Due(c.st, "03:00-05:00", c.now)
		if pass != c.pass || check != c.check {
			t.Errorf("%s: Due = pass %v check %v, want %v %v", c.name, pass, check, c.pass, c.check)
		}
	}
}

func TestDueOutsideTheWindowChecksAfter24Hours(t *testing.T) {
	cases := []struct {
		name        string
		st          AutoState
		now         time.Time
		pass, check bool
	}{
		{"never checked", AutoState{}, at(28, 12, 0), false, true},
		{"checked 23h59 ago", AutoState{LastCheck: at(27, 12, 1)}, at(28, 12, 0), false, false},
		{"checked exactly 24h ago", AutoState{LastCheck: at(27, 12, 0)}, at(28, 12, 0), false, false},
		{"checked 24h01 ago", AutoState{LastCheck: at(27, 11, 59)}, at(28, 12, 0), false, true},
		{"a check dated in the future counts as none", AutoState{LastCheck: at(29, 12, 0)}, at(28, 12, 0), false, true},
		{"the window's end is outside", AutoState{LastCheck: at(28, 4, 0)}, at(28, 5, 0), false, false},
	}
	for _, c := range cases {
		pass, check := Due(c.st, "03:00-05:00", c.now)
		if pass != c.pass || check != c.check {
			t.Errorf("%s: Due = pass %v check %v, want %v %v", c.name, pass, check, c.pass, c.check)
		}
	}
}

func TestDueAcrossMidnightBelongsToTheDayItStarts(t *testing.T) {
	const w = "23:00-01:00"
	// 00:30 on the 28th is the occurrence that started at 23:00 on the 27th.
	if pass, _ := Due(AutoState{LastPass: passAt(at(27, 23, 30)), LastCheck: at(27, 23, 30)}, w, at(28, 0, 30)); pass {
		t.Error("a pass at 23:30 the day before is this occurrence: no second pass at 00:30")
	}
	if pass, _ := Due(AutoState{LastPass: passAt(at(27, 0, 30)), LastCheck: at(27, 0, 30)}, w, at(28, 0, 30)); !pass {
		t.Error("a pass in the occurrence before: a pass is due at 00:30")
	}
	if pass, _ := Due(AutoState{LastCheck: at(28, 22, 0)}, w, at(28, 23, 15)); !pass {
		t.Error("23:15 is inside the window that starts today")
	}
	if pass, check := Due(AutoState{LastCheck: at(28, 1, 0)}, w, at(28, 1, 0)); pass || check {
		t.Errorf("01:00 is the end, outside, checked just now: pass %v check %v", pass, check)
	}
}

func TestDueUnreadableWindowOnlyChecks(t *testing.T) {
	if pass, check := Due(AutoState{}, "bad", at(28, 3, 10)); pass || !check {
		t.Errorf("an unreadable window: pass %v check %v, want no pass, a check", pass, check)
	}
}

// loopRun records the passes the loop started.
type loopRun struct{ passes []bool }

func (r *loopRun) pass(_ context.Context, check bool) int {
	r.passes = append(r.passes, check)
	return 0
}

func TestLoopTickNotifyRunsNothing(t *testing.T) {
	r := &loopRun{}
	l := Loop{Now: func() time.Time { return at(28, 3, 10) }, Auto: func() string { return "notify" },
		Window: func() string { return "03:00-05:00" }, State: func() AutoState { return AutoState{} }, Pass: r.pass, Log: &bytes.Buffer{}}
	l.Tick(context.Background())
	if len(r.passes) != 0 {
		t.Fatalf("updates.auto = notify: passes %v, want none", r.passes)
	}
}

func TestLoopTickRunsThePassDue(t *testing.T) {
	now := at(28, 3, 10)
	auto, window := "install", "03:00-05:00"
	st := AutoState{LastCheck: at(28, 2, 0)}
	r := &loopRun{}
	var log bytes.Buffer
	l := Loop{Now: func() time.Time { return now }, Auto: func() string { return auto },
		Window: func() string { return window }, State: func() AutoState { return st }, Pass: r.pass, Log: &log}
	l.Tick(context.Background())
	if len(r.passes) != 1 || r.passes[0] {
		t.Fatalf("inside the window: passes %v, want one install pass", r.passes)
	}
	// the pass finished: auto.json says so, the next tick runs nothing
	st.LastPass, st.LastCheck = passAt(at(28, 3, 11)), at(28, 3, 11)
	now = at(28, 3, 15)
	l.Tick(context.Background())
	if len(r.passes) != 1 {
		t.Fatalf("one pass per occurrence: passes %v", r.passes)
	}
	// a setting changed between ticks applies at the next one
	window = "03:20-05:00"
	now = at(28, 3, 25)
	l.Tick(context.Background())
	if len(r.passes) != 2 || r.passes[1] {
		t.Fatalf("a new window read at the next tick: passes %v, want a second install pass", r.passes)
	}
	// outside the window, a day after the last check: a check pass
	now = at(29, 12, 0)
	l.Tick(context.Background())
	if len(r.passes) != 3 || !r.passes[2] {
		t.Fatalf("outside the window, 24 h after the last check: passes %v, want a check pass", r.passes)
	}
	if !strings.Contains(log.String(), "update --auto --check") {
		t.Errorf("the log names the check pass: %q", log.String())
	}
}
