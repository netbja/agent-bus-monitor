package health

import (
	"strings"
	"testing"
	"time"
)

// redis-watch: the broker is behind when a newer Redis release was found by a
// recent check; anything not known (no broker version, no recent successful
// check, an unreadable version) is unknown, never ok.
func TestEvaluateRedisVersion(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-3 * time.Hour)
	for _, c := range []struct {
		name, running, latest string
		foundAt               time.Time
		want                  Status
		detail                string
	}{
		{"behind by a patch", "8.2.1", "8.2.2", fresh, Warn, "Redis 8.2.1 runs; 8.2.2 is released"},
		{"behind by a minor", "8.0.3", "8.2.0", fresh, Warn, "8.2.0 is released"},
		{"behind by a major", "7.4.5", "8.2.2", fresh, Warn, "8.2.2 is released"},
		{"current", "8.2.2", "8.2.2", fresh, OK, "Redis 8.2.2 is the latest release"},
		{"ahead (a newer build than the release)", "8.4.0", "8.2.2", fresh, OK, ""},
		{"ten beats nine numerically", "8.10.0", "8.9.0", fresh, OK, ""},
		{"no broker version", "", "8.2.2", fresh, Unknown, ""},
		{"unreadable broker version", "unstable", "8.2.2", fresh, Unknown, ""},
		{"never found", "8.2.1", "", time.Time{}, Unknown, ""},
		{"found too long ago", "8.2.1", "8.2.2", now.Add(-RedisLatestMaxAge - time.Minute), Unknown, ""},
		{"found in the future", "8.2.1", "8.2.2", now.Add(time.Hour), Unknown, ""},
	} {
		got := EvaluateRedisVersion(c.running, c.latest, c.foundAt, now)
		if got.Status != c.want || !strings.Contains(got.Detail, c.detail) {
			t.Errorf("%s: %+v, want %s containing %q", c.name, got, c.want, c.detail)
		}
		if !SafeText(got.Detail) {
			t.Errorf("%s: unsafe detail %q", c.name, got.Detail)
		}
	}
}

// The remediation is static: the person updates the broker; sentinel never
// restarts or upgrades it.
func TestRedisVersionRemediation(t *testing.T) {
	d, ok := ByID("redis-version")
	if !ok {
		t.Fatal("redis-version is not in the catalogue")
	}
	if d.Interval <= 0 || d.Deadline <= 0 {
		t.Fatalf("interval %v, deadline %v", d.Interval, d.Deadline)
	}
	r := strings.ToLower(d.Remediation)
	for _, want := range []string{"person", "never restarts or upgrades"} {
		if !strings.Contains(r, want) {
			t.Errorf("remediation %q lacks %q", d.Remediation, want)
		}
	}
}
