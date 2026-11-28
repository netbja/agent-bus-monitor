package bus

import (
	"fmt"
	"strings"
)

// Presence (cleaning-up-agents §3, spec
// docs/superpowers/specs/2026-11-26-cleaning-up-agents-design.md): what a
// reader shows for an agent besides its declared state. Red means someone
// must act: an optional role that finished is stopped, not lost. A reader's
// projection only: the snapshot is never rewritten (putSnapshot).
const (
	PresenceRunning = "running" // a live terminal, or no holder read: its declared state shows
	PresenceParked  = "parked"  // a park record names it and no live terminal holds it (§4.3)
	PresenceStopped = "stopped" // no terminal, not required, owes nothing
	PresenceLost    = "lost"    // no terminal, and it owes something or is required
)

// Owes is what an agent owes (§3.1), each item read or named in Unknown.
type Owes struct {
	Tasks    []string // "<task> (<state>)" it owns or is targeted by, not done, sorted
	Gates    int      // open 4-eyes gates it takes part in
	Ask      bool     // its own open ask
	Handoff  bool     // a declared hand-off of its process no /clear or relaunch took
	Messages int      // actionable addressed cmd entries, pending or unread (not stale, not wake=no)
	Unpushed []string // branches it holds whose local tip is not on the remote (§3.1 item 5)
	Stash    bool     // a stash whose message names the role (§3.1 item 5)
	Unknown  []string // items not read: each makes a terminal-less agent lost
}

// Something reports whether o names any owed item (Unknown aside).
func (o Owes) Something() bool {
	return len(o.Tasks) > 0 || o.Gates > 0 || o.Ask || o.Handoff || o.Messages > 0 || len(o.Unpushed) > 0 || o.Stash
}

// PresenceIn are Presence's inputs.
type PresenceIn struct {
	Pane        string // the registered terminal, "" when it never ran in one
	TermsRead   bool   // a holder of the project answered and its terminals were read
	Live        bool   // one of them is the agent's, alive
	Parked      bool   // a park record names this registration (§4.3)
	ParkUnknown bool   // the park record could not be read
	Required    bool   // the role is required (self-repair)
	SelfRepair  bool   // dispatch.self_repair is on
	Owes        Owes
}

// Presence judges in (§3.2): the presence and, when lost, its reason, the
// first of §10's list. It fails closed: an unknown input makes a
// terminal-less agent lost, "unknown: <item>".
func Presence(in PresenceIn) (state, reason string) {
	switch {
	case in.Pane == "" || !in.TermsRead || in.Live:
		return PresenceRunning, ""
	case in.Parked:
		return PresenceParked, ""
	case in.ParkUnknown:
		return PresenceLost, "unknown: park record"
	case in.Required && in.SelfRepair:
		return PresenceLost, "required: self-repair relaunches it"
	case in.Required:
		return PresenceLost, "required: agentbus up brings it back"
	}
	o := in.Owes
	switch {
	case len(o.Tasks) > 2:
		return PresenceLost, fmt.Sprintf("owns %s +%d", strings.Join(o.Tasks[:2], ", "), len(o.Tasks)-2)
	case len(o.Tasks) > 0:
		return PresenceLost, "owns " + strings.Join(o.Tasks, ", ")
	case len(o.Unpushed) > 0:
		return PresenceLost, "unpushed " + o.Unpushed[0]
	case o.Stash:
		return PresenceLost, "stash"
	case o.Messages > 0:
		return PresenceLost, fmt.Sprintf("%d message(s) waiting", o.Messages)
	case o.Ask:
		return PresenceLost, "an open ask"
	case o.Gates > 0:
		return PresenceLost, "a gate"
	case o.Handoff:
		return PresenceLost, "a hand-off not taken"
	case len(o.Unknown) > 0:
		return PresenceLost, "unknown: " + o.Unknown[0]
	}
	return PresenceStopped, ""
}

// Shown is the state a reader prints: the declared state while running,
// else the presence word.
func Shown(declared, presence string) string {
	if presence == PresenceRunning {
		return declared
	}
	return presence
}
