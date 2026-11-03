package config

import (
	"strings"
	"time"
)

// Notifications (spec docs/superpowers/specs/2026-11-03-notifications-design.md,
// "Settings"): the host's, read by every project's dispatch service through
// Machine; config.toml only (no env variable), none a secret. The targets'
// URLs and tokens are in notify.toml, never here.

// NotifyEvents are the kinds of event a notification can carry, in the
// spec's order.
var NotifyEvents = []string{"decision", "stall", "fail", "blocked", "prompt", "for-you"}

func notifyEvent(s string) bool {
	for _, e := range NotifyEvents {
		if s == e {
			return true
		}
	}
	return false
}

// fileNameList is a TOML list of distinct names each ok, kept as one
// comma-separated string (as backup.after_env is).
func fileNameList(want string, ok func(string) bool) func(any) (any, string) {
	return func(v any) (any, string) {
		list, isList := v.([]any)
		if !isList {
			return nil, want
		}
		seen := map[string]bool{}
		names := make([]string, 0, len(list))
		for _, x := range list {
			s, isStr := x.(string)
			if !isStr || !ok(s) {
				return nil, want
			}
			if seen[s] {
				return nil, s + " twice"
			}
			seen[s] = true
			names = append(names, s)
		}
		return strings.Join(names, ","), ""
	}
}

// SplitList is a list key's value as its items (none for "").
func SplitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// Quiet is a daily window of quiet hours, in minutes after local midnight;
// it may wrap past midnight (22:00-07:30).
type Quiet struct{ From, To int }

// ParseQuiet reads HH:MM-HH:MM; ok false for "", a malformed value or an
// empty window.
func ParseQuiet(s string) (Quiet, bool) {
	from, to, cut := strings.Cut(s, "-")
	if !cut {
		return Quiet{}, false
	}
	f, okF := clockMinutes(from)
	t, okT := clockMinutes(to)
	if !okF || !okT || f == t {
		return Quiet{}, false
	}
	return Quiet{From: f, To: t}, true
}

func clockMinutes(s string) (int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	for _, i := range []int{0, 1, 3, 4} {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	h, m := int(s[0]-'0')*10+int(s[1]-'0'), int(s[3]-'0')*10+int(s[4]-'0')
	if h > 23 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// Holds: t (in its own location) is within the window; From included, To
// excluded.
func (q Quiet) Holds(t time.Time) bool {
	m := t.Hour()*60 + t.Minute()
	if q.From < q.To {
		return m >= q.From && m < q.To
	}
	return m >= q.From || m < q.To
}
