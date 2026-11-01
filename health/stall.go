package health

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The stall guard's judgement (spec docs/superpowers/specs/2026-11-01-stall-guard-design.md):
// three pure evaluators over what the dispatcher reads. Anything not read,
// stale or unknown is Unknown, never ok and never a stall.

// PaneSeen is what a fresh observation of an agent's pane says.
type PaneSeen int

const (
	PaneUnseen PaneSeen = iota // no fresh observation: cannot judge
	PaneIdle                   // seen, not running a turn
	PaneBusy                   // seen running a turn
)

// StallAgent is one registered agent as team-stall reads it.
type StallAgent struct {
	Name       string
	State      string    // declared: working, idle, blocked, waiting, done
	DeclaredAt time.Time // the status entry's time (Redis)
	Dormant    bool      // silent past bus.DormantAfter: not part of the team
	Pane       PaneSeen
}

// TeamOpenWork is what keeps a team out of a stall: work someone already
// owns or waits on. Each kind is counted or named.
type TeamOpenWork struct {
	Requests, Accepted, Asks, Gates int
	Paused                          bool
	Swapping, Obligations, Handoffs []string
	Missions                        []string
}

// Any reports whether anything is open.
func (w TeamOpenWork) Any() bool { return len(w.items()) > 0 }

func (w TeamOpenWork) items() []string {
	var out []string
	count := func(n int, one, many string) {
		if n == 1 {
			out = append(out, "1 "+one)
		} else if n > 1 {
			out = append(out, fmt.Sprintf("%d %s", n, many))
		}
	}
	named := func(who []string, what string) {
		if len(who) > 0 {
			out = append(out, fmt.Sprintf("%s (%s)", what, strings.Join(who, ", ")))
		}
	}
	count(w.Requests, "request", "requests")
	count(w.Accepted, "accepted task", "accepted tasks")
	count(w.Asks, "ask", "asks")
	count(w.Gates, "gate", "gates")
	if w.Paused {
		out = append(out, "a pause")
	}
	named(w.Swapping, "a swap or reload")
	if len(w.Obligations) > 0 {
		out = append(out, "messages waiting for "+strings.Join(w.Obligations, ", "))
	}
	named(w.Handoffs, "a hand-off")
	named(w.Missions, "a mission attempt")
	return out
}

// ReadyTask is a plan task an agent could start now: Upcoming, owned by an
// agent, nothing it waits on.
type ReadyTask struct {
	ID, Title, Owner string
	OwnerHeld        bool   // its owner's provider is held
	Hold             string // what holds it, for the detail ("anthropic until 15:40")
}

// TeamStallInput is everything team-stall judges from.
type TeamStallInput struct {
	Now          time.Time
	After        time.Duration // stall_after; 0 = off
	ReadErr      string        // a read failed: its fixed phrase
	Agents       []StallAgent
	NoIdentities bool // no agent has a verified identity on this host (macOS)
	Open         TeamOpenWork
	Quiet        Status // the quiet-agent record's effective status
	QuietDetail  string
	QuietOff     bool // quiet_after = 0: its record says nothing, and team-stall judges without it
	PlanLoaded   bool
	AgentReady   []ReadyTask
	PersonReady  []string // titles of the person's tasks Waiting
}

// EvaluateTeamStall: every agent idle for After, nothing open and work ready
// is a fail (reason "stalled", which wakes sentinel); only the person's work
// ready is a warn for the person ("waiting-person").
func EvaluateTeamStall(in TeamStallInput) Outcome {
	unknown := func(d string) Outcome { return Outcome{Status: Unknown, Detail: truncateDetail(d)} }
	ok := func(d string) Outcome { return Outcome{Status: OK, Detail: truncateDetail(d)} }
	switch {
	case in.After <= 0:
		return unknown("off (stall_after = 0)")
	case in.ReadErr != "":
		return unknown(in.ReadErr)
	}
	var team []StallAgent
	for _, a := range in.Agents {
		if !a.Dormant {
			team = append(team, a)
		}
	}
	if len(team) == 0 {
		return unknown("no registered agent")
	}
	for _, a := range team { // any agent at work, or with its own circuit, settles it
		switch a.State {
		case "working", "blocked", "waiting":
			return ok(a.Name + " " + a.State)
		}
	}
	idleFor := time.Duration(-1) // the team's idle time: since its most recent declaration
	for _, a := range team {
		d := in.Now.Sub(a.DeclaredAt)
		if d < in.After {
			return ok(fmt.Sprintf("%s idle only %s", a.Name, minutes(d)))
		}
		if a.Pane == PaneBusy {
			return ok(a.Name + " busy (not declared)")
		}
		if idleFor < 0 || d < idleFor {
			idleFor = d
		}
	}
	for _, a := range team {
		if a.Pane == PaneUnseen {
			if in.NoIdentities {
				return unknown("cannot see panes on this host: no verified identities")
			}
			return unknown(fmt.Sprintf("cannot see %s's pane (no fresh observation)", a.Name))
		}
	}
	if in.Open.Any() {
		return ok("work is open: " + strings.Join(in.Open.items(), ", "))
	}
	switch {
	case in.QuietOff, in.Quiet == OK:
	case in.Quiet == Warn:
		return ok("quiet agent: " + in.QuietDetail + "; see quiet-agent")
	default:
		return unknown("quiet-agent not measured")
	}
	if !in.PlanLoaded {
		return unknown("no plan loaded: cannot tell whether work remains")
	}
	var held []string
	for _, t := range in.AgentReady {
		if t.OwnerHeld {
			held = append(held, fmt.Sprintf("%s (%s, %s)", t.ID, t.Owner, t.Hold))
			continue
		}
		return Outcome{Status: Fail, Reason: "stalled", Detail: truncateDetail(
			fmt.Sprintf("team idle %s, nothing open; next ready: %s (%s)", minutes(idleFor), t.ID, t.Owner))}
	}
	if len(in.PersonReady) > 0 {
		return Outcome{Status: Warn, Reason: "waiting-person", Detail: truncateDetail(
			fmt.Sprintf("team idle %s; waiting for you: %s", minutes(idleFor), in.PersonReady[0]))}
	}
	if len(held) > 0 {
		return ok("ready work waits on a quota hold: " + strings.Join(held, ", "))
	}
	return ok("team idle, plan has nothing ready")
}

// ClearHandoff is one declared hand-off as clear-stall reads it.
type ClearHandoff struct {
	Agent    string
	At       time.Time // the hand-off's time (Redis)
	Void     bool      // Handoff.VoidFor: no longer a hand-off
	Decision string    // the clear pass's last reason; "" = none yet
}

// ClearStallInput is everything clear-stall judges from.
type ClearStallInput struct {
	Now       time.Time
	After     time.Duration // clear_stall_after; 0 = off
	ReadErr   string
	AutoClear bool
	Handoffs  []ClearHandoff
}

// FocusedReason is DecideClear's reason when the person's focus holds the
// pane: only the person can release it.
const FocusedReason = "the pane is focused"

// NotIdleReason is DecideClear's reason when the agent has not declared idle
// since its hand-off: the one the agent fixes itself.
const NotIdleReason = "not idle since its hand-off"

// StallWake is one waking directive the stall guard owes (chain-reliability):
// to Target, once per Episode under its {p}:quota Mark.
type StallWake struct {
	Mark, Episode, Target, Text string
}

// ClearStallWakes: each hand-off clear-stall warns about wakes whoever can
// act, once per hand-off and target: the agent itself, with the exact
// command, when it only has to declare idle; master for any other reason.
func ClearStallWakes(in ClearStallInput, master string) []StallWake {
	if in.After <= 0 || in.ReadErr != "" {
		return nil
	}
	var out []StallWake
	for _, h := range in.Handoffs {
		age := in.Now.Sub(h.At)
		if h.Void || age < in.After {
			continue
		}
		w := StallWake{Mark: "stall-clear:" + h.Agent, Target: master}
		switch {
		case !in.AutoClear:
			w.Text = fmt.Sprintf("Stall guard: %s handed off %s ago and auto-clear is off: clear its pane by hand, or ask the person to.", h.Agent, minutes(age))
		case h.Decision == NotIdleReason:
			w.Target = h.Agent
			w.Text = fmt.Sprintf("Stall guard: your hand-off waits %s for its /clear because you are not idle since it. Run: agentbus status %s idle \"hand-off declared\" and nothing after it.", minutes(age), h.Agent)
		default:
			d := h.Decision
			if d == "" {
				d = "no decision yet"
			}
			w.Text = fmt.Sprintf("Stall guard: %s's hand-off waits %s for its /clear: %s. Unblock it, or tell the person.", h.Agent, minutes(age), d)
		}
		w.Episode = strconv.FormatInt(h.At.UnixMilli(), 10) + "|" + w.Target
		out = append(out, w)
	}
	return out
}

// EvaluateClearStall: a valid hand-off older than After that was not cleared
// warns, naming the clear pass's own reason. Reason "pane-focused" when the
// person's focus is what holds it.
func EvaluateClearStall(in ClearStallInput) Outcome {
	switch {
	case in.After <= 0:
		return Outcome{Status: Unknown, Detail: "off (clear_stall_after = 0)"}
	case in.ReadErr != "":
		return Outcome{Status: Unknown, Detail: truncateDetail(in.ReadErr)}
	}
	var stalled, pending []string
	focused := false
	for _, h := range in.Handoffs {
		if h.Void {
			continue
		}
		age := in.Now.Sub(h.At)
		if age < in.After {
			pending = append(pending, fmt.Sprintf("%s handed off %s ago; clear pending", h.Agent, minutes(age)))
			continue
		}
		why := "auto-clear is off: clear the pane by hand"
		if in.AutoClear {
			d := h.Decision
			if d == "" {
				d = "no decision yet"
			}
			focused = focused || d == FocusedReason
			why = "not cleared: " + d
		}
		stalled = append(stalled, fmt.Sprintf("%s handed off %s ago; %s", h.Agent, minutes(age), why))
	}
	if len(stalled) > 0 {
		out := Outcome{Status: Warn, Detail: truncateDetail(strings.Join(stalled, "; "))}
		if focused {
			out.Reason = "pane-focused"
		}
		return out
	}
	return Outcome{Status: OK, Detail: truncateDetail(strings.Join(pending, "; "))}
}

// QuietCmd is one retained cmd entry as quiet-agent reads it.
type QuietCmd struct {
	ID, From, Target, Type string
	NoWake, Human          bool
	Expired                bool // a tracked request past its ExpiresAt: maybe delivered payload-less
	RequestUnknown         bool // its tracked request could not be read
}

// QuietAgent is one agent as quiet-agent reads it.
type QuietAgent struct {
	Name, State   string
	Held          bool
	Dormant       bool     // silent past bus.DormantAfter: not judged
	Pane          PaneSeen // PaneBusy: running a turn, never quiet
	LastDelivered string   // its consumer group's cursor
	Pending       []string // its group's pending ids
	LastWrite     string   // its newest id on status, report or cmd (from it)
}

// QuietAgentInput is everything quiet-agent judges from.
type QuietAgentInput struct {
	Now     time.Time
	After   time.Duration // quiet_after; 0 = off
	ReadErr string
	Master  string // the escalation target: its directives give work
	Cmds    []QuietCmd
	// OldestRetained is the cmd stream's oldest id: a directive older than it
	// is gone, so an agent silent since before it cannot be judged.
	OldestRetained string
	Agents         []QuietAgent
}

// QuietDirectiveFor is the newest entry to agent that gives it work: a
// directive or an escalation that wakes, from master, the person or the
// dispatcher, whose payload could be delivered. Replies and verdicts never
// give work, and an expired request is skipped while choosing, so it never
// hides an older one still unanswered (reviewer-codex).
func QuietDirectiveFor(agent, master string, cmds []QuietCmd) (QuietCmd, bool) {
	var best QuietCmd
	found := false
	for _, c := range cmds {
		if c.Target != agent || c.NoWake || c.Expired || (c.Type != "directive" && c.Type != "escalation") {
			continue
		}
		if c.From != master && c.From != "dispatch" && !c.Human {
			continue
		}
		if !found || streamIDLess(best.ID, c.ID) {
			best, found = c, true
		}
	}
	return best, found
}

// EvaluateQuietAgent: an agent given work that was delivered to it, with its
// payload, and that has written nothing since, for After, warns.
func EvaluateQuietAgent(in QuietAgentInput) Outcome {
	switch {
	case in.After <= 0:
		return Outcome{Status: Unknown, Detail: "off (quiet_after = 0)"}
	case in.ReadErr != "":
		return Outcome{Status: Unknown, Detail: truncateDetail(in.ReadErr)}
	}
	// Collected per agent: one that cannot be judged never hides another's
	// warning (foureyes); unknown only when nobody is quiet.
	hits, unjudged := quietHits(in)
	var quiet []string
	for _, h := range hits {
		state := ""
		if h.State != "idle" && h.State != "" {
			state = " (" + h.State + ")"
		}
		quiet = append(quiet, fmt.Sprintf("%s%s: %s from %s %s ago delivered, nothing written since", h.Name, state, h.D.Type, h.D.From, minutes(h.Age)))
	}
	sort.Strings(quiet) // a stable detail, so its fingerprint moves only when the facts do
	sort.Strings(unjudged)
	notJudged := ""
	if len(unjudged) > 0 {
		notJudged = "not judged: " + strings.Join(unjudged, ", ")
	}
	switch {
	case len(quiet) > 0:
		d := strings.Join(quiet, "; ")
		if notJudged != "" {
			d += "; " + notJudged
		}
		return Outcome{Status: Warn, Detail: truncateDetail(d)}
	case notJudged != "":
		return Outcome{Status: Unknown, Detail: truncateDetail(notJudged)}
	}
	return Outcome{Status: OK}
}

// quietHit is one quiet agent: the directive it went quiet on, and how long.
type quietHit struct {
	QuietAgent
	D   QuietCmd
	Age time.Duration
}

// quietHits is EvaluateQuietAgent's judgement per agent: the quiet ones, and
// those that cannot be judged.
func quietHits(in QuietAgentInput) ([]quietHit, []string) {
	var hits []quietHit
	var unjudged []string
	for _, a := range in.Agents {
		if a.Dormant || a.State == "working" || a.Held || a.Pane == PaneBusy {
			continue
		}
		d, ok := QuietDirectiveFor(a.Name, in.Master, in.Cmds)
		if !ok {
			// Nothing retained gives it work; but the history may have been
			// trimmed past it: silent since before the oldest retained entry,
			// with something delivered once, it cannot be judged (reviewer-codex).
			if in.OldestRetained != "" && a.LastDelivered != "" && streamMillisBefore(a.LastWrite, in.OldestRetained) {
				unjudged = append(unjudged, a.Name+" (silent since before the retained cmd history)")
			}
			continue
		}
		if d.RequestUnknown {
			unjudged = append(unjudged, a.Name+" (request record unreadable)")
			continue
		}
		if streamIDLess(a.LastDelivered, d.ID) || contains(a.Pending, d.ID) {
			continue // not read yet, or still pending: not quiet
		}
		if !streamMillisBefore(a.LastWrite, d.ID) { // wrote since, same millisecond included
			continue
		}
		age := in.Now.Sub(time.UnixMilli(streamMillis(d.ID)))
		if age < in.After {
			continue
		}
		hits = append(hits, quietHit{QuietAgent: a, D: d, Age: age})
	}
	return hits, unjudged
}

// QuietAgentWakes: each agent quiet-agent warns about wakes master once per
// directive it went quiet on (chain-reliability): a warning alone woke nobody.
func QuietAgentWakes(in QuietAgentInput) []StallWake {
	if in.After <= 0 || in.ReadErr != "" {
		return nil
	}
	hits, _ := quietHits(in)
	out := make([]StallWake, 0, len(hits))
	for _, h := range hits {
		out = append(out, StallWake{Mark: "stall-quiet:" + h.Name, Episode: h.D.ID, Target: in.Master,
			Text: fmt.Sprintf("Stall guard: %s read %s %s from %s %s ago and has written nothing since. Look at its pane, or send it again.",
				h.Name, h.D.Type, h.D.ID, h.D.From, minutes(h.Age))})
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// streamMillis is a stream id's millisecond part; -1 when malformed.
func streamMillis(id string) int64 {
	ms, _, ok := splitStreamID(id)
	if !ok {
		return -1
	}
	return ms
}

func splitStreamID(id string) (ms, seq int64, ok bool) {
	a, b, found := strings.Cut(id, "-")
	if !found {
		return 0, 0, false
	}
	ms, err1 := strconv.ParseInt(a, 10, 64)
	seq, err2 := strconv.ParseInt(b, 10, 64)
	return ms, seq, err1 == nil && err2 == nil
}

// StreamIDLess orders stream ids; a malformed id sorts first.
func StreamIDLess(a, b string) bool { return streamIDLess(a, b) }

// streamIDLess orders stream ids; a malformed id sorts first.
func streamIDLess(a, b string) bool {
	am, as, aok := splitStreamID(a)
	bm, bs, bok := splitStreamID(b)
	switch {
	case !aok:
		return bok
	case !bok:
		return false
	case am != bm:
		return am < bm
	}
	return as < bs
}

// streamMillisBefore: a was written in an earlier millisecond than b (an
// unknown a counts as before: nothing written).
func streamMillisBefore(a, b string) bool { return streamMillis(a) < streamMillis(b) }

// minutes renders a duration as whole minutes ("18m"), hours past two.
func minutes(d time.Duration) string {
	if d >= 2*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
