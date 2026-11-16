package health

import (
	"strings"
	"testing"
	"time"
)

// vt-osc-title: an idle agent whose screen the dispatcher cannot read
// (observed unknown) cannot be woken; past ScreenUnknownAfter it is a warn
// For you naming it; a busy one, a fresh unknown, one whose client has no
// signature, or a stale observation is not.
func TestEvaluateScreenUnknown(t *testing.T) {
	now := int64(10_000_000_000)
	min := func(n int64) int64 { return now - n*60_000 }
	ev := []ScreenEvidence{
		{Agent: "coder", Declared: "idle", Observed: "unknown", Since: min(14), Fresh: true, Judged: true},
		{Agent: "master", Declared: "idle", Observed: "unknown", Since: min(3), Fresh: true, Judged: true},   // not long enough
		{Agent: "docs", Declared: "working", Observed: "unknown", Since: min(30), Fresh: true, Judged: true}, // working: not woken anyway
		{Agent: "kimi", Declared: "idle", Observed: "unknown", Since: min(30), Fresh: true, Judged: false},   // no signature
		{Agent: "sentinel", Declared: "idle", Observed: "unknown", Since: min(30), Fresh: false, Judged: true},
		{Agent: "foureyes", Declared: "idle", Observed: "idle", Since: min(30), Fresh: true, Judged: true},
	}
	out := EvaluateScreenUnknown(ev, now)
	if out.Status != Warn || !strings.Contains(out.Detail, "coder 14 min") || strings.Contains(out.Detail, "master") ||
		strings.Contains(out.Detail, "kimi") || strings.Contains(out.Detail, "docs") || strings.Contains(out.Detail, "sentinel") {
		t.Fatalf("%+v", out)
	}
	if out := EvaluateScreenUnknown(ev[1:], now); out.Status != OK {
		t.Fatalf("nobody past the bound: %+v", out)
	}
	if out := EvaluateScreenUnknown(nil, now); out.Status != Unknown {
		t.Fatalf("nothing observed: %+v", out)
	}
	d, ok := ByID("screen-unknown")
	if !ok || d.PlaceOf(Record{Status: Warn}, Warn) != PlaceForYou || ScreenUnknownAfter != 10*time.Minute {
		t.Fatalf("catalogue: %+v %v", d, ok)
	}
}
