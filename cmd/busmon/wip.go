package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
)

// The work-in-progress limit as busmon shows it (wip-limit spec
// docs/superpowers/specs/2026-11-21-wip-limit-design.md §3.5, step 3): an
// agent's in-flight requests are its tracked requests in state accepted
// (review, blocked and requested are not, §3.1); board.wip_limit is the
// project's limit (0: none). busmon only shows it: the bus enforces it at a
// first accept (step 1), and nothing here writes.

// wipFacts are an agent's in-flight requests and the limit, stamped on each
// poll's agents (stampWip).
type wipFacts struct {
	max      int           // board.wip_limit for the project (0: no limit)
	inFlight []request     // accepted, this agent the target, oldest accept first
	noReply  time.Duration // board.no_reply_after, for the agent page's mark
}

// atLimit: a limit is set and the agent holds at least that many.
func (w wipFacts) atLimit() bool { return w.max > 0 && len(w.inFlight) >= w.max }

// projectWipLimit is board.wip_limit for project: config.toml's
// [project.<project>.board], then [board], then the default (§3.3: no env
// variable).
func projectWipLimit(getenv func(string) string, project string) int {
	f := config.Load(config.Path(getenv), "config.toml")
	if n, ok := config.ProjectValue(f, nil, project, "board.wip_limit").Value.(int); ok {
		return n
	}
	return 0
}

// stampWip sets each agent's wipFacts from the board's requests.
func stampWip(agents map[string]*agentState, reqs []request, limit int, noReply time.Duration) {
	held := map[string][]request{}
	for _, r := range reqs {
		if r.Tracked && r.State == "accepted" && r.Target != "" {
			held[r.Target] = append(held[r.Target], r)
		}
	}
	for name, a := range agents {
		in := held[name]
		sort.SliceStable(in, func(i, j int) bool { return in[i].Accepted.Before(in[j].Accepted) })
		a.wip = wipFacts{max: limit, inFlight: in, noReply: noReply}
	}
}

// wipFlag is the agents row's wip n/m (§3.5): none without a limit or with
// nothing in flight; at the limit, in the attention role.
func wipFlag(a *agentState) (agentFlag, bool) {
	w := a.wip
	if w.max <= 0 || len(w.inFlight) == 0 {
		return agentFlag{}, false
	}
	f := agentFlag{Word: fmt.Sprintf("wip %d/%d", len(w.inFlight), w.max), Role: RoleMuted, Prio: 5}
	if w.atLimit() {
		f.Role, f.Prio = RoleAttention, 2
	}
	return f, true
}

// waitsTag is a queued request's tag when its target is at its limit
// (§3.5): "waits: coder at wip limit"; "" otherwise.
func waitsTag(r request, agents map[string]*agentState) string {
	if !r.Tracked || r.State != "requested" {
		return ""
	}
	if a := agents[r.Target]; a != nil && a.wip.atLimit() {
		return "waits: " + Preview(r.Target) + " at wip limit"
	}
	return ""
}

// inFlightRows are the agent page's In flight rows (§3.5): the count against
// the limit, then each accepted request with its age, one past
// board.no_reply_after marked as the Board marks it.
func inFlightRows(a *agentState, now time.Time, row func(k string, v ...Seg)) {
	w := a.wip
	if w.max <= 0 && len(w.inFlight) == 0 {
		return
	}
	n := len(w.inFlight)
	switch {
	case w.max <= 0:
		row("In flight", S(RoleText, fmt.Sprintf("%d accepted (no wip limit)", n)))
	case w.atLimit():
		row("In flight", S(RoleAttention, fmt.Sprintf("%d of %d: at the wip limit, a new accept is refused until one is done, in review or blocked", n, w.max)))
	default:
		row("In flight", S(RoleText, fmt.Sprintf("%d of %d (board.wip_limit)", n, w.max)))
	}
	for _, r := range w.inFlight {
		v := []Seg{S(RoleText, Preview(r.Task)), S(RoleMuted, " "+GlyphNone+" accepted "+humanAge(now.Sub(r.Accepted))+" ago")}
		if alert, _ := boardAlert(r, now, boardOpts{noReply: w.noReply}); alert != "" {
			v = append(v, S(RoleAttention, " "+GlyphWarn+" "+alert))
		}
		row("", v...)
	}
}
