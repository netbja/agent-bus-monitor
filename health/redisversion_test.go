package health

import (
	"strings"
	"testing"
	"time"
)

// redis-watch: the broker is compared only with the latest release of its
// own major (the compose pins redis:8-alpine, so a pull stays in that major;
// master's call on reviewer-codex's review). Behind in its major warns; a newer
// major is never a warning, only a line in the detail: changing major is the
// person's decision. Anything not known (no broker version, no recent
// successful check, no release of its major found) is unknown, never ok.
func TestEvaluateRedisVersion(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-3 * time.Hour)
	eight := map[int]string{8: "8.2.2", 7: "7.4.6"}
	nine := map[int]string{9: "9.0.1", 8: "8.2.2", 7: "7.4.6"}
	for _, c := range []struct {
		name, running string
		latest        map[int]string
		foundAt       time.Time
		want          Status
		detail        string
	}{
		{"behind in its major", "8.2.1", eight, fresh, Warn, "Redis 8.2.1 runs; 8.2.2 is the latest 8.x"},
		{"behind by a minor in its major", "8.0.3", eight, fresh, Warn, "8.2.2 is the latest 8.x"},
		{"behind in its major, a newer major out", "8.2.1", nine, fresh, Warn, "Redis 9.0.1 is out"},
		{"equal", "8.2.2", eight, fresh, OK, "Redis 8.2.2 is the latest 8.x"},
		{"equal, a newer major only", "8.2.2", nine, fresh, OK, "Redis 9.0.1 is out; this broker follows the compose pin (8); changing major is your decision"},
		{"an older major, current in it", "7.4.6", eight, fresh, OK, "Redis 8.2.2 is out; this broker follows the compose pin (7)"},
		{"ahead of the release", "8.4.0", eight, fresh, OK, "Redis 8.4.0"},
		{"ten beats nine numerically", "8.10.0", map[int]string{8: "8.9.0"}, fresh, OK, ""},
		{"no release of its major found", "6.2.1", eight, fresh, Unknown, "no recent release of Redis 6"},
		{"no broker version", "", eight, fresh, Unknown, ""},
		{"unreadable broker version", "unstable", eight, fresh, Unknown, ""},
		{"never found", "8.2.1", nil, time.Time{}, Unknown, ""},
		{"found too long ago", "8.2.1", eight, now.Add(-RedisLatestMaxAge - time.Minute), Unknown, ""},
		{"found in the future", "8.2.1", eight, now.Add(time.Hour), Unknown, ""},
		{"an unreadable release", "8.2.1", map[int]string{8: "8.x"}, fresh, Unknown, ""},
	} {
		got := EvaluateRedisVersion(c.running, c.latest, c.foundAt, now)
		if got.Status != c.want || !strings.Contains(got.Detail, c.detail) {
			t.Errorf("%s: %+v, want %s containing %q", c.name, got, c.want, c.detail)
		}
		newer := false
		if have, ok := parseRedisVersion(c.running); ok {
			for major := range c.latest {
				newer = newer || major > have[0]
			}
		}
		if c.want != Unknown && strings.Contains(got.Detail, "is out") != newer {
			t.Errorf("%s: %q names a newer major: %v, want %v", c.name, got.Detail, !newer, newer)
		}
		if c.want != Warn && strings.Contains(got.Detail, "pull") {
			t.Errorf("%s: %q suggests a pull without a warning", c.name, got.Detail)
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
	for _, want := range []string{"person", "never restarts or upgrades", "same major", "pin"} {
		if !strings.Contains(r, want) {
			t.Errorf("remediation %q lacks %q", d.Remediation, want)
		}
	}
}
