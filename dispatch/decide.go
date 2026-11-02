// Package dispatch is the event-driven dispatcher: it wakes an agent through its
// terminal pane when the bus holds work for it and nothing else will.
package dispatch

import (
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Inspection is what herdr says about an agent's pane at one instant.
type Inspection struct {
	State      string // idle|working|blocked|done|unknown
	Client     string
	Focused    bool
	PIDs       []int
	Starts     map[int]uint64
	Ready      bool   // the client's ready signature matched on a settled screen
	Busy       bool   // the client's busy signature matched
	ScreenHash string // opaque hash of a stable screen; the text itself is never retained
	Gone       bool
	Err        error
}

// Action is what the dispatcher should do for one agent now.
type Action int

const (
	ActNone Action = iota
	ActPrompt
	ActDefer
	ActEscalate
)

func (a Action) String() string {
	switch a {
	case ActNone:
		return "none"
	case ActPrompt:
		return "prompt"
	case ActDefer:
		return "defer"
	case ActEscalate:
		return "escalate"
	}
	return "action(" + strconv.Itoa(int(a)) + ")"
}

// Decision is the outcome of Decide. Rule is the row of the spec §3.2 table
// that decided. ResetTries asks the caller to clear tries and escalated (there
// was progress or the agent's identity changed). Clear (rule 1 only) asks the
// caller to write a cleared wake record that keeps At — never to delete it, which
// would erase the retry window of rule 5. Cause (rules 7–8) is the pane state
// behind a wait, recorded with an escalation so that it can be re-armed once
// that state ends (v0.15 §14).
type Decision struct {
	Action     Action
	Rule       int
	Reason     string
	ResetTries bool
	Clear      bool
	Cause      string
}

// Policy holds the tunables of the decision.
type Policy struct {
	Retry         time.Duration // one prompt per agent per window (rule 5)
	EscalateAfter time.Duration // T: oldest obligation age before escalating
	MaxTries      int           // prompts per obligation without progress (rule 4)
	Supported     map[string]bool
}

// Input is everything Decide looks at for one agent. Wake == nil means no
// record yet. SinceWake (server now − Wake.At) is meaningful only when
// Wake != nil && Wake.At != 0.
type Input struct {
	Obligation bus.Obligation
	Armed      bool // a receiver lease is present
	Snap       *bus.AgentSnapshot
	In         Inspection
	Wake       *bus.WakeRecord
	OldestAge  time.Duration // age of the oldest obligation entry, from its id
	SinceWake  time.Duration
}

// workingFactor: an agent herdr reports as working never escalates before
// workingFactor×T — a long turn is not a stall.
const workingFactor = 4

// Decide applies the spec §3.2 rules top to bottom. It is pure: no Redis, no
// herdr, no clock.
func Decide(p Policy, x Input) Decision {
	o := x.Obligation
	if !o.Wakes() {
		reason := "no obligation"
		if o.Stale > 0 {
			reason = "only stale entries"
		}
		return Decision{Action: ActNone, Rule: 1, Reason: reason, Clear: true}
	}
	if x.Armed {
		return Decision{Action: ActNone, Rule: 2, Reason: "receiver lease present"}
	}

	var w bus.WakeRecord
	if x.Wake != nil {
		w = *x.Wake
	}
	progressed := Progressed(x.Wake, o)
	changed := IdentityChanged(x.Wake, x.Snap)
	// A new message after an escalation is a new obligation (chain-reliability):
	// the escalation was for the high id it recorded, once; past it the wake
	// re-opens with fresh tries.
	reopened := w.Escalated == "true" && w.HighID != "" && idAfter(o.HighID, w.HighID)
	reset := progressed || changed || reopened
	suffix := ""
	if reset {
		// A new stall: evaluate rules 3–5 as if nothing had been tried.
		w.Tries, w.Escalated, w.At = 0, "", 0
		switch {
		case progressed:
			suffix = "; tries reset (progress)"
		case changed:
			suffix = "; tries reset (identity change)"
		default:
			suffix = "; tries reset (new message since the escalation)"
		}
	}
	d := func(a Action, rule int, reason string) Decision {
		return Decision{Action: a, Rule: rule, Reason: reason + suffix, ResetTries: reset}
	}

	// Rule 3, plus the crash case: an escalation was committed (pending) but
	// may not have been sent — resend it (at-least-once).
	switch w.Escalated {
	case "pending":
		return d(ActEscalate, 3, "escalation pending, resending")
	case "true":
		// An escalation caused by the pane's state is re-armed once that state
		// has ended (the loop inspects such a record first): tries clear, At is
		// kept so rule 5 still spaces the prompts. Anything else waits for
		// progress, as before.
		cause := EscCause(w)
		if cause == "" || !paneReady(p, x) {
			return d(ActNone, 3, "already escalated")
		}
		w.Tries, w.Escalated = 0, ""
		reset = true
		suffix += "; escalation re-armed (" + cause + " ended)"
	}
	if w.Tries >= p.MaxTries {
		return d(ActEscalate, 4, "tries exhausted without progress")
	}
	if w.At != 0 && x.SinceWake < p.Retry {
		return d(ActNone, 5, "within retry window")
	}

	// From here the outcome is defer, escalating once the oldest entry is
	// older than the threshold (escalated is "" here: rule 3 caught the rest).
	wait := func(rule int, reason string, after time.Duration) Decision {
		var r Decision
		if x.OldestAge > after {
			r = d(ActEscalate, rule, reason+", escalating")
		} else {
			r = d(ActDefer, rule, reason)
		}
		if rule >= 7 {
			r.Cause = reason
		}
		return r
	}
	T := p.EscalateAfter

	// Rule 6 (no usable snapshot) before rule 7, but an identity mismatch is
	// only judged against a successful inspection: a gone pane is rule 7.
	if x.Snap == nil {
		return wait(6, "no snapshot", T)
	}
	if x.Snap.PID == 0 {
		return wait(6, "unverified snapshot", T)
	}
	in := x.In
	switch {
	case in.Err != nil:
		return wait(7, "inspection error", T)
	case in.Gone:
		return wait(7, "pane gone", T)
	case !p.Supported[in.Client]:
		return wait(7, "client unsupported", T)
	}
	if !identityOK(x.Snap, in) {
		return wait(6, "identity mismatch", T)
	}

	switch {
	case in.State == "working":
		return wait(8, "agent working", workingFactor*T)
	case in.State != "idle" && in.State != "done":
		return wait(8, "agent "+stateWord(in.State), T)
	case in.Busy:
		return wait(8, "client busy", T)
	case !in.Ready:
		return wait(8, "input not ready", T)
	case in.Focused:
		return wait(8, "pane focused", T)
	}
	return d(ActPrompt, 9, "ready")
}

// paneCauses are the reasons of rules 7–8, the pane states an escalation can
// be re-armed from. A record written before v0.15 has no esc_cause, only its
// reason: EscCause reads the cause from there.
var paneCauses = []string{"inspection error", "pane gone", "client unsupported",
	"agent ", "client busy", "input not ready", "pane focused"}

// EscCause is the pane state behind a record's escalation, or "".
func EscCause(w bus.WakeRecord) string {
	if w.EscCause != "" {
		return w.EscCause
	}
	for _, c := range paneCauses {
		if strings.HasPrefix(w.Reason, c) {
			reason, _, _ := strings.Cut(w.Reason, ",")
			return reason
		}
	}
	return ""
}

// paneReady: the inspection shows a verified pane that would take a prompt now
// (the conditions of rule 9).
func paneReady(p Policy, x Input) bool {
	in := x.In
	if x.Snap == nil || x.Snap.PID == 0 || in.Err != nil || in.Gone || !p.Supported[in.Client] {
		return false
	}
	if !identityOK(x.Snap, in) {
		return false
	}
	return (in.State == "idle" || in.State == "done") && !in.Busy && in.Ready && !in.Focused
}

func stateWord(s string) string {

	if s == "" {
		return "unknown"
	}
	return strings.ToLower(s)
}

// identityOK: the registered pid is among the pane's foreground processes and
// its start time matches, so a recycled pane or a restarted agent never passes.
func identityOK(s *bus.AgentSnapshot, in Inspection) bool {
	found := false
	for _, pid := range in.PIDs {
		if pid == s.PID {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	start, ok := in.Starts[s.PID]
	return ok && start == s.PIDStart
}

// Progressed reports whether the agent made progress since the record's last
// reservation: its group's last-delivered id advanced, or its pending count
// decreased (a growing PEL is not progress). A record without marks has no
// baseline and never shows progress.
func Progressed(w *bus.WakeRecord, o bus.Obligation) bool {
	if w == nil {
		return false
	}
	if w.LastDelivered != "" && idAfter(o.LastDelivered, w.LastDelivered) {
		return true
	}
	return o.PendingTotal < w.PendingTotal
}

// IdentityChanged reports whether the agent behind the snapshot is a different
// process (pid or pid start time) from the one the record last woke. With no
// recorded pid, or no snapshot, there is nothing to compare.
func IdentityChanged(w *bus.WakeRecord, snap *bus.AgentSnapshot) bool {
	if w == nil || w.PID == 0 || snap == nil {
		return false
	}
	return snap.PID != w.PID || snap.PIDStart != w.PIDStart
}

// idAfter reports whether stream id a is strictly after b (ms, then seq).
// Unparseable ids are never after anything.
func idAfter(a, b string) bool {
	am, as, ok1 := parseID(a)
	bm, bs, ok2 := parseID(b)
	if !ok1 || !ok2 {
		return false
	}
	if am != bm {
		return am > bm
	}
	return as > bs
}

func parseID(id string) (ms, seq uint64, ok bool) {
	head, tail, found := strings.Cut(id, "-")
	ms, err := strconv.ParseUint(head, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	if !found {
		return ms, 0, true
	}
	seq, err = strconv.ParseUint(tail, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return ms, seq, true
}
