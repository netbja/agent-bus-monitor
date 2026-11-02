package dispatch

import (
	"fmt"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// PopFacts is everything §2 of the pop-auto-retire spec reads about one pop,
// gathered by ReadPopFacts for the dispatch pass and for retire --forget
// alike, so the act re-judges exactly what the pass judged.
type PopFacts struct {
	Now         time.Time
	RetireAfter time.Duration // dispatch.pop_retire_after (condition 9)
	// Unknown names each read that failed: any one makes the pop not done.
	Unknown    []string
	Snap       *bus.AgentSnapshot
	OwnedTasks []string // board tasks it owns in a state other than done
	Gates      []string // gates holding it or opened by it (Bus.ReviewsOf)
	Missions   []string // mission attempts in progress on it
	OpenAsk    bool
	Obligation bus.Obligation
	Armed      bool
	WakeState  string // its wake record's state; "prompting" = a wake is being typed
	// Intent is {p}:swap:<role> as stored ("" = none). OwnIntent is the
	// exact JSON retire --forget wrote, "" outside it: only a stored intent
	// byte-equal to it passes condition 8.
	Intent, OwnIntent string
	Handoff           bool       // a valid hand-off waits for its /clear
	Pane              Inspection // a live inspection of its pane
	Paused, Held      bool
	// Episode, when not 0, is the episode retire was told (--episode):
	// another one is not done.
	Episode int64
}

// PopEpisode is the episode a done judgement is for: the time of the
// declaration condition 9 reads (a turn flip is never one).
func PopEpisode(s *bus.AgentSnapshot) int64 { return s.TS }

// PopDone is §2's judgement: done only when every condition holds, else the
// first failing one as the reason.
func PopDone(f PopFacts) (bool, string) {
	no := func(format string, a ...any) (bool, string) { return false, fmt.Sprintf(format, a...) }
	s := f.Snap
	switch {
	case len(f.Unknown) > 0:
		return no("unknown: %s", strings.Join(f.Unknown, ", "))
	case s == nil:
		return no("no snapshot")
	case s.Tier != "pop":
		return no("not a pop (tier %q)", s.Tier)
	case len(f.OwnedTasks) > 0:
		return no("it owns a board task: %s", strings.Join(f.OwnedTasks, ", "))
	case len(f.Gates) > 0:
		return no("a review gate concerns it: %s", strings.Join(f.Gates, ", "))
	case len(f.Missions) > 0:
		return no("a mission attempt is in progress: %s", strings.Join(f.Missions, ", "))
	case f.OpenAsk:
		return no("it has an open ask")
	case f.Obligation.Wakes():
		return no("messages wait for it (%d)", f.Obligation.Size())
	case f.Armed:
		return no("it is armed")
	case f.WakeState == "prompting":
		return no("a wake is being typed into its pane")
	case f.OwnIntent != "" && f.Intent == "":
		return no("its retire intent lapsed")
	case f.Intent != "" && f.Intent != f.OwnIntent:
		return no("a swap or reload holds it")
	case f.Handoff:
		return no("a hand-off waits for its /clear")
	case s.Auto != "":
		return no("a turn since its declaration")
	case s.State != "idle" && s.State != "done":
		return no("declared %s", s.State)
	}
	if age := f.Now.Sub(time.UnixMilli(s.TS)); age < f.RetireAfter {
		return no("idle for %s, under %s", age.Round(time.Minute), f.RetireAfter)
	}
	if f.Episode != 0 && f.Episode != PopEpisode(s) {
		return no("another episode (declared again since)")
	}
	in := f.Pane
	switch {
	case in.Err != nil || in.Gone:
		return no("its pane could not be read")
	case !identityOK(s, in):
		return no("its pane does not run its verified process")
	case in.Focused:
		return no("the pane is focused")
	case in.State == "blocked":
		return no("its pane is on a prompt")
	case in.Busy || !in.Ready || (in.State != "idle" && in.State != "done"):
		return no("its pane is not ready")
	case f.Paused:
		return no("the project is paused")
	case f.Held:
		return no("its provider is held")
	}
	return true, ""
}
