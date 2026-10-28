package dispatch

import "github.com/netbja/agent-bus-monitor/bus"

// Auto-clear (lifecycle spec, part B): after an agent declared its hand-off
// (agentbus handoff), the dispatcher may type the literal /clear into its
// pane, the one exception to "it types only the wake line", under the rule
// below; then the ordinary wake brings the agent back to its hand-off.

// clearClients are the clients whose /clear semantics are known: it ends the
// conversation and keeps the process, argv and environment, starting a new
// session (which the SessionStart hook proves, part D).
var clearClients = map[string]bool{"claude-code": true}

// ClearFacts is everything DecideClear judges, read by the caller.
type ClearFacts struct {
	On         bool              // AGENT_BUS_AUTO_CLEAR
	Record     bus.Handoff       // the agent's declared hand-off
	Snap       bus.AgentSnapshot // its current snapshot
	Paused     bool              // {p}:pause
	Swapping   bool              // a swap or reload intent
	Held       bool              // its provider is held: a cleared agent would wake into a held quota
	OpenAsk    bool
	Gates      []string // open challenges on either side
	Armed      bool
	WakeState  string // its wake record's state
	Marked     bool   // already cleared once for this record's session
	Inspection Inspection
}

// ClearDecision: Clear, or why not; WaitGates when only gates hold it.
type ClearDecision struct {
	Clear     bool
	Reason    string
	WaitGates []string
}

// DecideClear is part B's rule, cheapest first. IdentSession alone never
// authorises a clear: the record must stand for the snapshot's verified pid
// and start, and the pane must show that same process, ready.
func DecideClear(f ClearFacts) ClearDecision {
	no := func(why string) ClearDecision { return ClearDecision{Reason: why} }
	switch {
	case !f.On:
		return no("auto-clear is off")
	case !clearClients[f.Snap.Client]:
		return no("client " + f.Snap.Client + " has no known /clear")
	}
	if void, why := f.Record.VoidFor(f.Snap, true); void {
		return no("hand-off void: " + why)
	}
	switch {
	case f.Paused:
		return no("paused")
	case f.Swapping:
		return no("a swap or reload is in progress")
	case f.Held:
		return no("its provider is held")
	case f.OpenAsk:
		return no("it has an open ask")
	case len(f.Gates) > 0:
		return ClearDecision{Reason: "a review gate is open", WaitGates: f.Gates}
	case f.Snap.State != "idle" || f.Snap.TS <= f.Record.At:
		return no("not idle since its hand-off")
	case f.Armed:
		return no("armed")
	case f.WakeState == "prompting":
		return no("a wake is being typed")
	case f.Marked:
		return no("already cleared for this session")
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
	return ClearDecision{Clear: true}
}
