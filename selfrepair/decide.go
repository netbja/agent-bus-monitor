// Package selfrepair is the self-repair watcher's judgement (spec
// docs/superpowers/specs/2026-11-09-self-repair-watcher-design.md, delivery
// step 6): facts in, one decision per agent out, as a pure function. The
// dispatch service gathers the facts and applies the decision; in step 6 it
// applies nothing but the shadow's record (would relaunch), and nothing
// relaunches before step 7.
package selfrepair

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/relaunch"
)

// Mode is dispatch.self_repair as config.SelfRepairMode reads it.
type Mode string

const (
	ModeOff    Mode = "off"
	ModeOn     Mode = "on"
	ModeShadow Mode = "shadow"
)

// MinDeadReads is how many reads must agree an agent is dead (4.1).
const MinDeadReads = 3

// ProjectPause is the wait between two relaunches of a project (4.5).
const ProjectPause = 2 * time.Minute

// Agent is what is known of one agent of the template, read for this pass.
type Agent struct {
	Name     string
	Master   bool
	Pop      bool
	Required bool

	// Life is relaunch.SnapLife's; DeadSince the first dead read of this
	// death (Redis TIME), DeadReads how many reads since agree.
	Life      relaunch.Life
	DeadSince time.Time
	DeadReads int

	// A declared departure, or another relauncher: the kind of an intent
	// holding the role ("" none; the watcher's own is not passed), a pop
	// retired or done, a hand-off clear in flight.
	Intent       string
	Retired      bool
	HandoffClear bool

	// Tab is relaunch.JudgeTab's verdict for the role's tab (create asked
	// only for a required role, so an optional missing tab is VerdictMissing).
	Tab relaunch.Verdict

	// Session is the proved session; Resumable whether the client's resume is
	// verified (rolecfg.ResumeVerified). Never a fresh start (Q4).
	Session   string
	Resumable bool

	// ProviderFresh: its quota reader succeeded in the last 5 min (a local
	// model: its endpoint answers); ProviderHeld: the coordinator holds it.
	ProviderFresh bool
	ProviderHeld  bool

	// Relaunches are its self-repair relaunches, oldest first; CrashLoop: a
	// spent day escalated, until `agentbus self-repair reset`.
	Relaunches []time.Time
	CrashLoop  bool
}

// Facts are one pass's facts for the project.
type Facts struct {
	Now               time.Time // Redis TIME
	Mode              Mode
	Paused, Suspended bool
	HerdrUp           bool // the herdr server answers
	WorkspaceOpen     bool // the project's workspace is open
	InFlight          bool // a relaunch of the project is not proven yet
	LastRelaunch      time.Time
	Grace             time.Duration // dead this long before acting (60 s)
	Every             time.Duration // dispatch.self_repair_every
	PerDay            int           // dispatch.self_repair_per_day
	Agents            []Agent
}

// Kind is what a decision does.
type Kind string

const (
	KindNone     Kind = "none"     // nothing to do
	KindUnknown  Kind = "unknown"  // the process cannot be judged: never acts
	KindWait     Kind = "wait"     // not yet: the grace, the tab, the budget's spacing, another relaunch
	KindSkip     Kind = "skip"     // a declared departure or another relauncher: not the watcher's
	KindHold     Kind = "hold"     // the conditions to relaunch are not there (herdr, workspace, network)
	KindReport   Kind = "report"   // told to master, never acted on
	KindBudget   Kind = "budget"   // a crash loop: escalated, nothing more until reset
	KindRelaunch Kind = "relaunch" // R1: a new pane split from the tab's, resumed
	KindCreate   Kind = "create"   // R2: the required tab recreated, then the relaunch
)

// Decision is one agent's: its kind, why, and whether this pass acts on it
// (at most one per pass).
type Decision struct {
	Agent  string
	Kind   Kind
	Reason string
	Act    bool
}

func short(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%dh", int(d/time.Hour))
}

// decideOne is one agent's decision, before the project's one-at-a-time.
func decideOne(f Facts, a Agent) Decision {
	d := func(k Kind, why string, args ...any) Decision {
		return Decision{Agent: a.Name, Kind: k, Reason: fmt.Sprintf(why, args...)}
	}
	switch a.Life {
	case relaunch.LifeAlive:
		return d(KindNone, "alive")
	case relaunch.LifeUnknown:
		return d(KindUnknown, "no process identity to judge: never relaunched")
	}
	if f.Mode == ModeOff {
		return d(KindNone, "self-repair off")
	}
	deadFor := f.Now.Sub(a.DeadSince)
	if a.DeadSince.IsZero() || a.DeadReads < MinDeadReads || deadFor < f.Grace {
		return d(KindWait, "dead %s on %d reads, grace %s", short(max(deadFor, 0)), a.DeadReads, short(f.Grace))
	}
	switch {
	case a.Pop && a.Retired:
		return d(KindSkip, "a pop retired or done: master brings it back")
	case a.Intent != "":
		return d(KindSkip, "%s in progress", a.Intent)
	case f.Suspended:
		return d(KindSkip, "the project is suspended")
	case f.Paused:
		return d(KindSkip, "the project is paused")
	case a.HandoffClear:
		return d(KindSkip, "a hand-off clear in flight")
	}
	switch a.Tab {
	case relaunch.VerdictRelaunch, relaunch.VerdictCreate:
	case relaunch.VerdictMissing:
		return d(KindReport, "its tab missing (an optional role: told, not recreated)")
	default:
		return d(KindWait, "tab %s: waited on, never forced", a.Tab)
	}
	switch {
	case !f.HerdrUp:
		return d(KindHold, "herdr does not answer")
	case !f.WorkspaceOpen:
		return d(KindHold, "the workspace is not open")
	case a.ProviderHeld:
		return d(KindHold, "its provider's quota is held")
	case !a.ProviderFresh:
		return d(KindHold, "its provider unreachable (no fresh quota read in 5 min)")
	}
	switch {
	case !a.Resumable:
		return d(KindReport, "its client's resume is not verified: never a fresh start")
	case a.Session == "":
		return d(KindReport, "no session to resume (no proved session)")
	}
	if a.CrashLoop {
		return d(KindBudget, "crash loop: escalated, nothing more until agentbus self-repair reset %s", a.Name)
	}
	day, last := 0, time.Time{}
	for _, r := range a.Relaunches {
		if f.Now.Sub(r) < 24*time.Hour {
			day++
		}
		if r.After(last) {
			last = r
		}
	}
	if f.PerDay > 0 && day >= f.PerDay {
		return d(KindBudget, "%d relaunches in 24 h: a crash loop, escalated", day)
	}
	if !last.IsZero() && f.Now.Sub(last) < f.Every {
		return d(KindWait, "budget: one relaunch per %s, next after %s", short(f.Every), last.Add(f.Every).UTC().Format("15:04"))
	}
	if a.Tab == relaunch.VerdictCreate {
		return d(KindCreate, "its tab missing (a required role): recreate it, resumed; dead %s", short(deadFor))
	}
	return d(KindRelaunch, "dead %s: relaunch in a new pane, resumed", short(deadFor))
}

// Decide is the pass: one decision per agent, in the facts' order. Of the
// agents a pass could act on, one only, and none while a relaunch is in
// flight or within ProjectPause of the last: master first (Q6), then the
// oldest death; the others wait their turn.
func Decide(f Facts) []Decision {
	out := make([]Decision, len(f.Agents))
	var cands []int
	for i, a := range f.Agents {
		out[i] = decideOne(f, a)
		if k := out[i].Kind; k == KindRelaunch || k == KindCreate {
			cands = append(cands, i)
		}
	}
	if len(cands) == 0 {
		return out
	}
	sort.SliceStable(cands, func(x, y int) bool {
		a, b := f.Agents[cands[x]], f.Agents[cands[y]]
		if a.Master != b.Master {
			return a.Master
		}
		return a.DeadSince.Before(b.DeadSince)
	})
	block := ""
	switch {
	case f.InFlight:
		block = "another relaunch in flight"
	case !f.LastRelaunch.IsZero() && f.Now.Sub(f.LastRelaunch) < ProjectPause:
		block = fmt.Sprintf("the project's pause after a relaunch (%s)", short(ProjectPause))
	}
	for n, i := range cands {
		switch {
		case block != "":
			out[i] = Decision{Agent: out[i].Agent, Kind: KindWait, Reason: block}
		case n == 0:
			out[i].Act = true
		default:
			out[i] = Decision{Agent: out[i].Agent, Kind: KindWait, Reason: "one relaunch at a time: after " + f.Agents[cands[0]].Name}
		}
	}
	return out
}

// Health is the health check self-repair (spec 5): ok when off or with
// nothing to repair; warn for a shadow's would-be action, a hold or a tab
// not settling; fail for a crash loop, except in shadow, which acts on
// nothing and so spends no budget.
func Health(mode Mode, ds []Decision) health.Outcome {
	if mode == ModeOff {
		return health.Outcome{Status: health.OK, Detail: "self-repair off"}
	}
	var fail, warn, reports []string
	for _, d := range ds {
		switch {
		case d.Kind == KindBudget:
			fail = append(fail, d.Agent+": "+d.Reason)
		case d.Act && mode == ModeShadow && d.Kind == KindCreate:
			warn = append(warn, "shadow: would recreate "+d.Agent+"'s tab and relaunch it ("+d.Reason+")")
		case d.Act && mode == ModeShadow:
			warn = append(warn, "shadow: would relaunch "+d.Agent+" ("+d.Reason+")")
		case d.Kind == KindHold:
			warn = append(warn, "held: "+d.Agent+": "+d.Reason)
		case d.Kind == KindWait && strings.HasPrefix(d.Reason, "tab "):
			warn = append(warn, d.Agent+": "+d.Reason)
		case d.Kind == KindReport:
			reports = append(reports, d.Agent)
		}
	}
	switch {
	case len(fail) > 0 && mode != ModeShadow:
		return health.Outcome{Status: health.Fail, Detail: strings.Join(fail, "; ")}
	case len(fail) > 0:
		warn = append(fail, warn...)
	}
	if len(warn) > 0 {
		return health.Outcome{Status: health.Warn, Detail: strings.Join(warn, "; ")}
	}
	if len(reports) > 0 {
		return health.Outcome{Status: health.OK, Detail: "reported: " + strings.Join(reports, ", ")}
	}
	return health.Outcome{Status: health.OK, Detail: "nothing to repair"}
}
