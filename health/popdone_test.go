package health

import (
	"strings"
	"testing"
	"time"
)

func TestEvaluatePopDone(t *testing.T) {
	now := time.UnixMilli(10_000_000_000)
	after := 15 * time.Minute
	pop := func(idle time.Duration, marked bool) PopDoneAgent {
		return PopDoneAgent{Name: "security", Since: now.Add(-idle), Told: marked}
	}
	for _, c := range []struct {
		name   string
		in     PopDoneInput
		status Status
		detail string
	}{
		{"off", PopDoneInput{Off: true}, Unknown, "off"},
		{"no pop", PopDoneInput{Now: now, After: after}, OK, ""},
		{"told, within the delay", PopDoneInput{Now: now, After: after, Pops: []PopDoneAgent{pop(40*time.Minute, true)}}, OK, ""},
		{"not told", PopDoneInput{Now: now, After: after, Pops: []PopDoneAgent{pop(2*time.Hour, false)}}, OK, ""},
		{"told, still there", PopDoneInput{Now: now, After: after, Pops: []PopDoneAgent{pop(50*time.Minute, true)}}, Warn, "security done 50m ago, not ended"},
	} {
		out := EvaluatePopDone(c.in)
		if out.Status != c.status || !strings.Contains(out.Detail, c.detail) {
			t.Errorf("%s: %+v, want %s with %q", c.name, out, c.status, c.detail)
		}
	}
	d, ok := ByID("pop-done")
	if !ok || d.Handler != "master" || len(d.Person) != 0 {
		t.Fatalf("pop-done definition = %+v", d)
	}
	if d.Place(Warn) != PlaceInHand {
		t.Fatal("pop-done is in hand, never for you")
	}
}
