package config

import (
	"regexp"
	"strconv"
)

// The updater's window (quiet-update-auto §4.2, updates.window): "HH:MM-HH:MM"
// in local time, 24-hour, two digits each; a window whose end is before its
// start crosses midnight and belongs to the day it starts.

var windowRE = regexp.MustCompile(`^([0-9]{2}):([0-9]{2})-([0-9]{2}):([0-9]{2})$`)

// ParseWindow is the window's start and end in minutes after midnight; ok
// false for any other form, an hour above 23, a minute above 59, or equal
// bounds (an empty window).
func ParseWindow(s string) (start, end int, ok bool) {
	m := windowRE.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	n := make([]int, 4)
	for i := range n {
		n[i], _ = strconv.Atoi(m[i+1])
	}
	if n[0] > 23 || n[1] > 59 || n[2] > 23 || n[3] > 59 {
		return 0, 0, false
	}
	start, end = n[0]*60+n[1], n[2]*60+n[3]
	if start == end {
		return 0, 0, false
	}

	return start, end, true
}
