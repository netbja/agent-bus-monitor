package quota

import (
	"math"
	"time"
)

// The reset date formats (display.date_format, quota settings spec §2.3).
const (
	DateRelative = "relative" // today 13:59, tomorrow 13:59, Wed 18 Nov 13:59 (the default)
	DateISO      = "iso"      // 2026-11-18 13:59
	DateClock12  = "clock12"  // today 1:59 PM, tomorrow 1:59 PM, Wed 18 Nov 1:59 PM
)

// DateFormats are the formats, in the order Settings offers them.
var DateFormats = []string{DateRelative, DateISO, DateClock12}

// ResetLabel is a reset (or any time to come) as the person reads it, in
// now's zone: with its day whenever it is not today (the field case: a
// seven-day window's reset tomorrow at 13:59, read '13:59' at 14:30 today).
// format is display.date_format; "" or an unknown one is relative. English
// day and month names; the year only when it is not now's.
func ResetLabel(t, now time.Time, format string) string {
	t = t.In(now.Location())
	if format == DateISO {
		return t.Format("2006-01-02 15:04")
	}
	clock := "15:04"
	if format == DateClock12 {
		clock = "3:04 PM"
	}
	switch calendarDays(now, t) {
	case 0:
		return "today " + t.Format(clock)
	case 1:
		return "tomorrow " + t.Format(clock)
	}
	if t.Year() != now.Year() {
		return t.Format("Mon 2 Jan 2006 " + clock)
	}
	return t.Format("Mon 2 Jan " + clock)
}

// calendarDays is how many calendar days t's date is after from's, in
// from's zone; a day of 23 or 25 hours (DST) counts as one.
func calendarDays(from, t time.Time) int {
	day := func(x time.Time) time.Time {
		y, m, d := x.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, from.Location())
	}
	return int(math.Round(day(t).Sub(day(from)).Hours() / 24))
}

// ResetStamp is a reset written into a stored message (a directive, a
// notice), read later and maybe after midnight: always the absolute date and
// time in format's style, never today or tomorrow (master's decision,
// 2026-11-16). Live views use ResetLabel.
func ResetStamp(t, now time.Time, format string) string {
	t = t.In(now.Location())
	if format == DateISO {
		return t.Format("2006-01-02 15:04")
	}
	clock := "15:04"
	if format == DateClock12 {
		clock = "3:04 PM"
	}
	if t.Year() != now.Year() {
		return t.Format("Mon 2 Jan 2006 " + clock)
	}
	return t.Format("Mon 2 Jan " + clock)
}
