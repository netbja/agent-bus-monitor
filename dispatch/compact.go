package dispatch

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// Auto-compact (the person's answer A): an agent whose context fill stayed at
// the urgent threshold (dispatch.context_urgent) for CompactAfter with no
// hand-off standing gets the fixed line /compact typed into its pane, once
// per context episode, under the auto-clear's pane guards: the pane idle and
// ready, nobody at it, its verified process. Master is told without waking.
// The client keeps its conversation as a summary; a declared hand-off stays
// the auto-clear's.

// compactClients are the clients whose /compact is known: Claude Code
// summarises the conversation and keeps its session and process.
var compactClients = map[string]bool{"claude-code": true}

// compactAfter is how long the fill must stay at the urgent threshold.
const compactAfter = 10 * time.Minute

// CompactFacts is everything DecideCompact judges, read by the caller.
type CompactFacts struct {
	On       bool              // dispatch.auto_compact, read live
	Snap     bus.AgentSnapshot // the agent's snapshot
	Fill     bus.ContextFill   // its last published fill (At is Redis time)
	Urgent   float64           // the effective urgent threshold (0 = off)
	After    time.Duration     // how long at Urgent before /compact
	Since    int64             // Redis ms the fill was first seen at Urgent in this session; 0 = not yet
	Standing bool              // a declared hand-off stands
	Marked   bool              // /compact already reserved in this episode
	Swapping bool              // a swap or reload intent
	Held     bool              // its provider is held: /compact calls the model
	// WakeState is its wake record's state ("prompting" while a wake is typed).
	WakeState  string
	Inspection Inspection
}

// CompactDecision: Compact, or why not.
type CompactDecision struct {
	Compact bool
	Reason  string
}

// DecideCompact is the auto-compact's rule, cheapest first; the pane checks
// are DecideClear's.
func DecideCompact(f CompactFacts) CompactDecision {
	no := func(why string) CompactDecision { return CompactDecision{Reason: why} }
	if why := f.busReason(); why != "" {
		return no(why)
	}
	in := f.Inspection
	switch {
	case in.Err != nil:
		return no("inspection failed: " + in.Err.Error())
	case !identityOK(&f.Snap, in):
		return no("its verified process is not the pane's")
	case in.Focused:
		return no("the pane is focused")
	case in.State == "blocked":
		return no("the pane is blocked on a prompt")
	case in.Busy || !in.Ready:
		return no("the pane is not ready")
	}
	return CompactDecision{Compact: true}
}

// busReason is the part of the rule read from the bus alone, before any
// pane is read: why not, or "".
func (f CompactFacts) busReason() string {
	switch {
	case !f.On:
		return "auto-compact is off"
	case !compactClients[f.Snap.Client]:
		return "client " + f.Snap.Client + " has no known /compact"
	case f.Urgent <= 0:
		return "no urgent threshold"
	case f.Fill.Pct < f.Urgent:
		return "below the urgent fill"
	case f.Since <= 0:
		return "not yet seen at the urgent fill"
	case f.Fill.At-f.Since < f.After.Milliseconds():
		return "at the urgent fill for less than " + f.After.String()
	case f.Standing:
		return "its hand-off stands: the auto-clear's job"
	case f.Marked:
		return "already compacted in this episode"
	case f.Swapping:
		return "a swap or reload is in progress"
	case f.Held:
		return "its provider is held"
	case f.WakeState == "prompting":
		return "a wake is being typed"
	}
	return ""
}

// compactSince parses CompactSinceMark's "<session>|<ms>" for session; 0
// when the mark is absent or holds another session.
func compactSince(marks map[string]string, agent, session string) int64 {
	s, ms, ok := strings.Cut(marks[bus.CompactSinceMark(agent)], "|")
	if !ok || s != session {
		return 0
	}
	n, _ := strconv.ParseInt(ms, 10, 64)
	return n
}

// compact is one alerted agent's auto-compact step, after the escalation:
// it keeps the urgent start time, then judges, reserves, re-inspects and
// types /compact. marks are the {p}:quota marks read after the alert.
func (e *escalation) compact(ctx context.Context, a string, f bus.ContextFill, marks map[string]string) error {
	l := e.l
	c := l.cfg.Context
	if c.Compact == nil || e.urgent <= 0 {
		return nil
	}
	since := compactSince(marks, a, f.Session)
	switch {
	case f.Pct < e.urgent:
		if _, ok := marks[bus.CompactSinceMark(a)]; ok { // the fill fell back: the hold starts again
			return l.b.FencedQuotaDel(ctx, l.token, bus.CompactSinceMark(a))
		}
		return nil
	case since == 0: // first seen at the urgent fill in this session: the hold starts now
		return l.b.FencedQuotaSet(ctx, l.token, bus.CompactSinceMark(a), f.Session+"|"+strconv.FormatInt(f.At, 10))
	}
	if !c.Compact() {
		l.deferCompact(a, "auto-compact is off")
		return nil
	}
	stands, err := e.standing(ctx, a)
	if err != nil {
		return err
	}
	snap, ok := e.snaps[a]
	if !ok || snap.Pane == "" {
		return nil
	}
	cf := CompactFacts{On: true, Snap: snap, Fill: f, Urgent: e.urgent, After: compactAfter, Since: since,
		Standing: stands, Marked: marks[bus.CompactMark(a)] == f.Session, Held: l.heldBy(a, &snap)}
	if why := cf.busReason(); why != "" { // the cheap rules, without reading the pane
		l.deferCompact(a, why)
		return nil
	}
	if cf.Swapping, err = l.b.Swapping(ctx, a); err != nil {
		return err
	}
	if w, ok, err := l.b.Wake(ctx, a); err != nil {
		return err
	} else if ok {
		cf.WakeState = w.State
	}
	cf.Inspection = l.rt.Inspect(ctx, snap.Pane)
	if d := DecideCompact(cf); !d.Compact {
		l.deferCompact(a, d.Reason)
		return nil
	}
	// Reserve first: a /compact is typed at most once per episode.
	if err := l.b.FencedQuotaSet(ctx, l.token, bus.CompactMark(a), f.Session); err != nil {
		return err
	}
	// Re-inspect right before typing: ready, unfocused, the same process.
	cf.Inspection = l.rt.Inspect(ctx, snap.Pane)
	if d := DecideCompact(cf); !d.Compact {
		l.log(map[string]any{"agent": a, "action": "compact-aborted", "reason": d.Reason})
		return l.b.FencedQuotaDel(ctx, l.token, bus.CompactMark(a)) // nothing typed: the next pass judges again
	}
	if err := l.rt.Compact(ctx, snap.Pane); err != nil {
		if errors.Is(err, termhost.ErrNotLanded) { // pasted, no CR: the person must know
			if nerr := l.notLanded(ctx, a, "compact"); nerr != nil {
				return nerr
			}
		}
		return err // the mark stays: no second /compact in this episode
	}
	l.deferCompact(a, "compacted")
	l.log(map[string]any{"agent": a, "action": "compact", "pct": f.Pct, "session": f.Session})
	to := l.cfg.EscalateTo
	if to == a || !bus.ValidName(to) {
		return nil
	}
	mins := (f.At - since) / 60000
	note := fmt.Sprintf("auto-compact: %s at %.0f%% for %d min with no hand-off declared; /compact typed into its idle pane. It keeps working from a summary of its context: check its current step before sending it more.", a, f.Pct, mins)
	_, err = l.b.FencedCmdOnce(ctx, l.token, bus.CompactNoticeMark(a), f.Session, dispatchFrom, to, bus.CmdDirective, note, true)
	if errors.Is(err, bus.ErrSwapInProgress) {
		return nil
	}
	return err
}

// deferCompact logs why an agent was not compacted, once per change of
// reason, never once per pass.
func (l *loop) deferCompact(agent, reason string) {
	if l.compactWhy == nil {
		l.compactWhy = map[string]string{}
	}
	if l.compactWhy[agent] == reason {
		return
	}
	l.compactWhy[agent] = reason
	if reason != "compacted" {
		l.log(map[string]any{"agent": agent, "action": "compact-deferred", "reason": reason})
	}
}
