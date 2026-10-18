package health

import (
	"strings"
	"testing"
	"time"
)

func TestEffectiveStatusUnknownForMissingAndStaleRecords(t *testing.T) {
	now := int64(2_000_000)
	interval := time.Minute
	cases := []struct {
		name string
		rec  Record
		want Status
	}{
		{"missing", Record{}, Unknown},
		{"fresh", Record{ID: "runaway", Status: Warn, ObservedAt: now - 1}, Warn},
		{"stale", Record{ID: "runaway", Status: OK, ObservedAt: now - interval.Milliseconds()*2}, Unknown},
		{"bad status", Record{ID: "runaway", Status: "surprise", ObservedAt: now - 1}, Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveStatus(tc.rec, now, interval); got != tc.want {
				t.Fatalf("EffectiveStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCatalogueHasUniqueIDsAndBoundedIntervals(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Definitions() {
		if d.ID == "" || seen[d.ID] || d.Interval <= 0 || d.Deadline <= 0 || d.Remediation == "" {
			t.Fatalf("invalid catalogue definition: %+v", d)
		}
		seen[d.ID] = true
	}
	for _, excluded := range []string{"secret-scanner", "canary", "project-checks", "key-inventory"} {
		if _, ok := ByID(excluded); ok {
			t.Errorf("out-of-scope check %q is registered", excluded)
		}
	}
}

func TestSafeTextRejectsOversizedText(t *testing.T) {
	if SafeText(strings.Repeat("x", 601)) {
		t.Fatal("SafeText accepted oversized text")
	}
	if !SafeText("service is ready; restart budget low") {
		t.Fatal("SafeText rejected a safe diagnostic")
	}
}
