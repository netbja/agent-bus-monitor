package update

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
)

// The updater's loop (quiet-update-auto §4.1, §4.2, step 4d): a ticker of
// five minutes and one test. A pass is a child process, the agentbus
// installed now, so a pass always runs the newest logic and the loop itself
// never needs an update.

// LoopEvery is the loop's tick (§4.1).
const LoopEvery = 5 * time.Minute

// checkEvery is how old the last check may be, outside the window, before a
// check-only pass runs (§4.2 step 4): max_wait is counted on a host that is
// never awake in the window.
const checkEvery = 24 * time.Hour

// Due is the loop's test (§4.2 steps 3 and 4) at now, in now's zone: inside
// the window with no pass finished in this occurrence of it, a pass; outside
// it with the last check more than 24 hours old, a check-only pass. A
// deferred pass is not finished: it changed nothing, and the next tick in the
// same window tries again (§4.3 step 4, foureyes); installed, current,
// skipped, failed and rolled_back are (a failed tag is not retried). A window
// that crosses midnight belongs to the day it starts. A time dated after now
// (a pass or a check) counts as none. An unreadable window never installs: it
// only checks.
func Due(st AutoState, window string, now time.Time) (pass, check bool) {
	if start, ok := occurrence(window, now); ok {
		p := st.LastPass
		return p == nil || p.Result == "deferred" || p.At.Before(start) || p.At.After(now), false
	}
	last := st.LastCheck
	return false, last.IsZero() || last.After(now) || now.Sub(last) > checkEvery
}

// occurrence is the start of the window's occurrence that holds now, ok
// false outside it (or with no readable window). Its bounds are built with
// time.Date in now's location, so a daylight-saving day keeps its wall clock.
func occurrence(window string, now time.Time) (time.Time, bool) {
	start, end, ok := config.ParseWindow(window)
	if !ok {
		return time.Time{}, false
	}
	y, m, d := now.Date()
	for _, day := range []int{d, d - 1} { // today's occurrence, or yesterday's across midnight
		s := time.Date(y, m, day, start/60, start%60, 0, 0, now.Location())
		e := time.Date(y, m, day, end/60, end%60, 0, 0, now.Location())
		if end < start {
			e = e.AddDate(0, 0, 1)
		}
		if !now.Before(s) && now.Before(e) {
			return s, true
		}
	}
	return time.Time{}, false
}

// Loop is the updater's loop, its reads injected: each tick reads
// updates.auto and updates.window afresh, so a change needs no restart.
type Loop struct {
	Now    func() time.Time
	Auto   func() string    // updates.auto
	Window func() string    // updates.window
	State  func() AutoState // auto.json
	// Pass runs agentbus update --auto (--check when check) and returns its
	// exit code; it never kills the pass when ctx ends.
	Pass func(ctx context.Context, check bool) int
	Log  io.Writer
}

// Tick is one test (§4.2): with notify, nothing; else the pass Due says.
func (l Loop) Tick(ctx context.Context) {
	if l.Auto() != "install" {
		return
	}
	pass, check := Due(l.State(), l.Window(), l.Now())
	if !pass && !check {
		return
	}
	name := "update --auto"
	if check {
		name += " --check"
	}
	fmt.Fprintf(l.Log, "updater: %s: starting\n", name)
	fmt.Fprintf(l.Log, "updater: %s: exit %d\n", name, l.Pass(ctx, check))
}

// Run ticks at once, then every LoopEvery, until ctx ends.
func (l Loop) Run(ctx context.Context) {
	t := time.NewTicker(LoopEvery)
	defer t.Stop()
	for {
		l.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
