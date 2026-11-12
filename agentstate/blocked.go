package agentstate

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Blocked: herdr exit step 2b, spec 2026-11-10-herdr-exit-design.md 4.3 and
// 4.3a. Pure judgements, wired to nothing yet: herdr still decides blocked
// until step 9 puts the dispatcher on them.

// BlockedOn reports a permission or question menu of the client's at the
// bottom of the screen, and what the menu names (the signature's named group
// "what"; "" when it names nothing). Like InDialog, only the last
// dialogLines non-blank lines count (an answered menu higher up is not one),
// and a screen whose current input box reads ready is never blocked. That
// test is the box's own, without Match's screen-wide Modal veto: agy's Modal
// matches its approval menu, so an answered menu still near the bottom would
// veto the ready box below it and read blocked (reviewer-codex).
func (s Signature) BlockedOn(screen string) (bool, string) {
	if len(s.Blocked) == 0 {
		return false, ""
	}
	if s.boxReady(strings.ReplaceAll(screen, "\r", "")) {
		return false, ""
	}
	window := bottomLines(screen)
	for _, r := range s.Blocked {
		m := r.FindStringSubmatch(window)
		if m == nil {
			continue
		}
		if i := r.SubexpIndex("what"); i > 0 {
			return true, strings.TrimSpace(m[i])
		}
		return true, ""
	}
	return false, ""
}

// bottomLines is the last dialogLines non-blank lines of the stripped screen.
func bottomLines(screen string) string {
	plain := Strip(strings.ReplaceAll(screen, "\r", ""))
	var lines []string
	for _, l := range strings.Split(plain, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > dialogLines {
		lines = lines[len(lines)-dialogLines:]
	}
	return strings.Join(lines, "\n")
}

// screenLabelMax caps the agent's own words in a report (4.3a).
const screenLabelMax = 120

// clean makes the agent's output one line of data: escapes stripped,
// control and format characters (bidi overrides included) dropped, line
// breaks and tabs as spaces, runs of space collapsed, at most max runes (the
// last one an ellipsis when cut).
func clean(s string, max int) string {
	s = Strip(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteRune(' ')
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if rs := []rune(out); len(rs) > max {
		out = string(rs[:max-1]) + "…"
	}
	return out
}

// ScreenLabel is what a menu names, as a report may carry it: one line,
// cleaned, capped at 120 characters, quoted and labelled screen:. Empty when
// nothing is left.
func ScreenLabel(what string) string {
	c := clean(what, screenLabelMax)
	if c == "" {
		return ""
	}
	return "screen: " + strconv.Quote(c)
}

// Observation is one look at an agent for the blocked judgement. PID 0 is an
// agent with no process (gone, or no longer observed). Hook: a permission
// hook of the client says blocked and its matching end has not come
// (HookSince: when it fired). Settled: the screen read is quiet (4.3 input
// 4); only a settled read counts for the Blocked signature. Screen and What:
// BlockedOn's answer for that read.
type Observation struct {
	PID       int
	At        time.Time
	Hook      bool
	HookSince time.Time
	Settled   bool
	Screen    bool
	What      string
}

// EventKind names a blocked report.
type EventKind string

const (
	Blocked   EventKind = "blocked"
	Unblocked EventKind = "unblocked"
)

// BlockedEvent is one report to master (4.3a): an episode's enter or leave.
type BlockedEvent struct {
	Kind          EventKind
	Agent, Client string
	Since         time.Time
	What          string
	After         time.Duration
}

// Line is the report's text: `blocked: <agent> (<client>) since HH:MM:SS —
// screen: "<what>"` (the screen part only when the menu names something),
// or `unblocked: <agent> after 3m12s`.
func (e BlockedEvent) Line() string {
	if e.Kind == Unblocked {
		return fmt.Sprintf("unblocked: %s after %s", e.Agent, e.After.Round(time.Second))
	}
	line := fmt.Sprintf("blocked: %s (%s) since %s", e.Agent, e.Client, e.Since.Format("15:04:05"))
	if l := ScreenLabel(e.What); l != "" {
		line += " — " + l
	}
	return line
}

// Episode is one agent's blocked episode, carried from one observation to the
// next: the prompt-notify decision's episode (2026-10-27), keyed by the
// process, so a new pid is a new episode.
type Episode struct {
	Agent, Client string
	PID           int
	Open          bool
	Since         time.Time
	What          string

	hits       int       // settled reads in a row matching the Blocked signature
	firstHit   time.Time // the first of them
	lastWhat   string
	screenHeld bool // the last settled read matched
}

// Step judges one observation and returns the episode as it now stands, with
// the report it makes, if any; one enter and one leave per episode.
//
// Enter: the hook, at once (since: when it fired), or the Blocked signature on
// two settled reads in a row (since: the first of them); an unsettled read
// neither counts nor resets. Leave: no source still says blocked — the hook
// has ended and the last settled read does not match. A hook-only menu (no
// screen signature yet) therefore leaves on the hook's end, and a screen
// menu whose hook ended first leaves once the screen clears. A new pid, or
// none, ends the episode (the leave is its own event; the new process's
// episode starts at its next observation).
func (e Episode) Step(o Observation) (Episode, *BlockedEvent) {
	if o.PID != e.PID {
		fresh := Episode{Agent: e.Agent, Client: e.Client, PID: o.PID}
		if e.PID != 0 { // the process changed or went: this look ends its episode
			var ev *BlockedEvent
			if e.Open {
				ev = e.leave(o.At)
			}
			return fresh, ev
		}
		e = fresh // the first look at a process
	}
	if o.PID == 0 {
		return e, nil
	}
	if o.Settled {
		if o.Screen {
			if e.hits == 0 {
				e.firstHit = o.At
			}
			e.hits++
			e.lastWhat = o.What
		} else {
			e.hits = 0
		}
		e.screenHeld = o.Screen
	}
	if !e.Open {
		switch {
		case o.Hook:
			e.Since = o.HookSince
			if e.Since.IsZero() {
				e.Since = o.At
			}
		case e.hits >= 2:
			e.Since = e.firstHit
		default:
			return e, nil
		}
		e.Open = true
		e.What = o.What
		if e.What == "" {
			e.What = e.lastWhat
		}
		return e, &BlockedEvent{Kind: Blocked, Agent: e.Agent, Client: e.Client, Since: e.Since, What: e.What}
	}
	if o.Hook || e.screenHeld {
		return e, nil
	}
	ev := e.leave(o.At)
	e.Open, e.Since, e.What = false, time.Time{}, ""
	return e, ev
}

func (e Episode) leave(at time.Time) *BlockedEvent {
	return &BlockedEvent{Kind: Unblocked, Agent: e.Agent, Client: e.Client, Since: e.Since, What: e.What, After: at.Sub(e.Since)}
}
