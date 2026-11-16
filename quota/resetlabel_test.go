package quota

import (
	"testing"
	"time"
)

// Quota settings step 1 (spec 2026-11-16-quota-settings-design.md §2.3): a
// reset is written with its day whenever it is not today. The field case:
// tomorrow's 13:59, seen at 14:30 today, read '13:59'.
func TestResetLabel(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no zone data:", err)
	}
	now := time.Date(2026, 11, 16, 14, 30, 0, 0, paris) // a Monday
	at := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, paris) }
	for _, c := range []struct {
		t      time.Time
		format string
		want   string
	}{
		{at(17, 13, 59), DateRelative, "tomorrow 13:59"}, // the field case
		{at(16, 18, 5), DateRelative, "today 18:05"},
		{at(18, 13, 59), DateRelative, "Wed 18 Nov 13:59"},
		{at(17, 0, 10), DateRelative, "tomorrow 00:10"}, // past midnight
		{at(16, 23, 59), DateRelative, "today 23:59"},
		{time.Date(2027, 1, 2, 9, 0, 0, 0, paris), DateRelative, "Sat 2 Jan 2027 09:00"}, // another year
		{at(17, 13, 59), DateISO, "2026-11-17 13:59"},
		{at(17, 13, 59), DateClock12, "tomorrow 1:59 PM"},
		{at(16, 9, 5), DateClock12, "today 9:05 AM"},
		{at(18, 13, 59), DateClock12, "Wed 18 Nov 1:59 PM"},
		{at(17, 13, 59), "", "tomorrow 13:59"},          // unset: the default
		{at(17, 13, 59), "bogus", "tomorrow 13:59"},     // unknown: the default
		{at(17, 13, 59).UTC(), DateRelative, "tomorrow 13:59"}, // shown in now's zone
	} {
		if got := ResetLabel(c.t, now, c.format); got != c.want {
			t.Errorf("%v %q: %q, want %q", c.t, c.format, got, c.want)
		}
	}
	// Across a DST change: the night of 2026-10-25 is 25 hours long in Paris.
	sat := time.Date(2026, 10, 24, 22, 0, 0, 0, paris)
	if got := ResetLabel(time.Date(2026, 10, 25, 23, 30, 0, 0, paris), sat, DateRelative); got != "tomorrow 23:30" {
		t.Errorf("across DST: %q", got)
	}
	if got := ResetLabel(time.Date(2026, 10, 26, 0, 30, 0, 0, paris), sat, DateRelative); got != "Mon 26 Oct 00:30" {
		t.Errorf("two days across DST: %q", got)
	}
}
