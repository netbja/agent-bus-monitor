package main

import (
	"context"
	"errors"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// busReader is everything the 1 s tick reads. *bus.Bus satisfies it; tests use
// a counting fake to pin the read inventory (spec §9).
type busReader interface {
	PilotDriver(ctx context.Context) (string, error)
	ArmedAgents(ctx context.Context) (map[string]string, error)
	CmdLag(ctx context.Context) (map[string]int64, error)
	Agents(ctx context.Context) (map[string]bus.AgentSnapshot, error)
	Usage(ctx context.Context) (map[string]bus.UsageSnapshot, error)
	Budgets(ctx context.Context) (map[string]bus.BudgetSnapshot, error)
	Board(ctx context.Context) (map[string]bus.BoardEntry, error)
	Recent(ctx context.Context, kinds []string, n int) ([]bus.Event, map[string]string, error)
	DispatchAlive(ctx context.Context) (bool, error)
	ServiceStates(ctx context.Context) (*bus.ServicesInstall, map[string]bus.ServiceRecord, error)
	ConductorAlive(ctx context.Context) (bool, error)
	ChangeNames(ctx context.Context) ([]string, error)
	Obligation(ctx context.Context, agent string, horizon time.Duration) (bus.Obligation, error)
	Wake(ctx context.Context, agent string) (bus.WakeRecord, bool, error)
	OpenChallenges(ctx context.Context, agent string) (map[string]string, error)
}

// setHealth records the monitor's OWN link to the broker. A transition stamps
// `since`, so the header can say how long it has been down — and an
// unreachable broker never gets rendered as agents falling silent.
func (st *shared) setHealth(err error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if err != nil {
		if st.health.ok {
			st.health.since = time.Now()
		}
		st.health.ok, st.health.err = false, err.Error()
		return
	}
	if !st.health.ok {
		st.health.since = time.Now()
	}
	st.health.ok, st.health.err = true, ""
}

// pollOnce is one tick of the poller: it reads the broker and folds the
// answers into st. Every snapshot survives its own failed read — an unanswered
// question must not be rendered as an answer.
func pollOnce(ctx context.Context, b busReader, st *shared, tick int) {
	// PilotDriver is the canary for the monitor's own link: a plain GET, run
	// every tick, whose failure means busmon is blind — not that the agents
	// have gone quiet. The whole tick shares one deadline (pollBudget): the
	// deadline only governs I/O on an open connection because bus.Connect sets
	// ContextTimeoutEnabled on the client.
	pollCtx, cancel := context.WithTimeout(ctx, pollBudget)
	defer cancel()
	driver, perr := b.PilotDriver(pollCtx)
	st.setHealth(perr)
	if perr != nil {
		// Keep every previous snapshot: the remaining reads would each sit on
		// their own timeout and blank the screen. Queue values are marked as
		// unconfirmed (N?), not erased.
		st.mu.Lock()
		for _, a := range st.agents {
			a.queueFailed = true
		}
		st.mu.Unlock()
		return
	}
	armed, armedErr := b.ArmedAgents(pollCtx)
	lag, lagErr := b.CmdLag(pollCtx)
	snaps, snapsErr := b.Agents(pollCtx)
	usageSnaps, usageErr := b.Usage(pollCtx)
	budgetSnaps, budgetErr := b.Budgets(pollCtx)
	boardSnap, boardErr := b.Board(pollCtx)

	// The board is already in hand, so the requests view costs nothing extra;
	// only the cmd-stream scan behind the untracked threads is throttled.
	var reqs []request
	var haveReqs bool
	var threads []openThread
	var haveThreads bool
	if boardErr == nil {
		reqs, haveReqs = boardRequests(boardSnap), true
	}
	if tick%requestPoll == 1 {
		if cmds, _, err := b.Recent(pollCtx, []string{"cmd"}, openThreadScan); err == nil {
			st.mu.Lock()
			known := st.requests
			st.mu.Unlock()
			if haveReqs {
				known = reqs
			}
			threads, haveThreads = recentThreads(openThreads(cmds, known), time.Now().Add(-bus.DefaultHorizon)), true
		}
	}

	st.mu.Lock()
	names := make([]string, 0, len(st.agents))
	for n := range st.agents {
		names = append(names, n)
	}
	st.mu.Unlock()
	type wakeInfo struct {
		ob     bus.Obligation
		obOK   bool
		rec    bus.WakeRecord
		hasRec bool
		recOK  bool
	}
	// The dispatcher facts cost a few reads per agent and change at the
	// dispatcher's own 5 s tick, so they are read every wakePoll ticks.
	wakes := make(map[string]wakeInfo, len(names))
	dispAlive, dispErr := false, errNotPolled
	missionsState, missionsErr := "", errNotPolled
	var svcInst *bus.ServicesInstall
	var svcRecs map[string]bus.ServiceRecord
	svcErr := errNotPolled
	if wakePollDue(tick) {
		dispAlive, dispErr = b.DispatchAlive(pollCtx)
		missionsState, missionsErr = missionsFact(pollCtx, b)
		svcInst, svcRecs, svcErr = b.ServiceStates(pollCtx)
		for _, n := range names {
			var wi wakeInfo
			if ob, err := b.Obligation(pollCtx, n, bus.DefaultHorizon); err == nil {
				wi.ob, wi.obOK = ob, true
			}
			if rec, ok, err := b.Wake(pollCtx, n); err == nil {
				wi.rec, wi.hasRec, wi.recOK = rec, ok, true
			}
			wakes[n] = wi
		}
	}
	gates := make(map[string]int, len(names))
	for _, n := range names {
		if m, err := b.OpenChallenges(pollCtx, n); err == nil {
			gates[n] = len(m)
		}
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	st.pilot = driver
	if svcErr == nil {
		st.svcInst, st.svcRecs = svcInst, svcRecs
	}
	now := time.Now()
	if missionsErr == nil {
		r, has := st.svcRecs["missions"]
		st.missions = serviceFact(r, has, missionsState, now)
	}
	if dispErr == nil {
		legacy := "down"
		if dispAlive {
			legacy = "up"
		}
		r, has := st.svcRecs["dispatch"]
		st.dispatch = serviceFact(r, has, legacy, now)
	}
	if budgetErr == nil {
		st.budgets = budgetSnaps
	}
	if boardErr == nil {
		st.board = boardSnap
	}
	if haveReqs {
		st.requests = reqs
	}
	if haveThreads {
		st.threads = threads
	}
	// The agents hash is the authoritative roster: it holds every agent that
	// has ever published a status, whatever the --limit window held.
	for n, s := range agentsOrNil(snaps, snapsErr) {
		a := st.agents[n]
		if a == nil {
			a = &agentState{}
			st.agents[n] = a
		}
		// TS 0: launched and bound, no status declared yet (not 1970).
		if ts := time.UnixMilli(s.TS); s.TS != 0 && ts.After(a.stateAt) {
			a.state, a.message, a.stateAt = s.State, s.Message, ts
			if ts.After(a.lastSeen) {
				a.lastSeen = ts
			}
		}
	}
	// Agents known only via a live armed lease get a row with no state: a
	// lease says a subscriber listens, not what the agent does. Lag-only
	// groups are NOT synthesized — consumer groups outlive their agents.
	for n := range armed {
		if st.agents[n] == nil {
			st.agents[n] = &agentState{}
		}
	}
	for n, a := range st.agents {
		if armedErr == nil {
			_, a.armed = armed[n]
		}
		if lagErr == nil {
			a.lag = lag[n]
		}
		if snapsErr == nil {
			a.pane = snaps[n].Pane
			a.pid, a.pidStart = snaps[n].PID, snaps[n].PIDStart
		}
		if usageErr == nil {
			a.ctxRaw = usageSnaps[n].Ctx
		}
		if c, ok := gates[n]; ok {
			a.gated = c
		}
		if wi, ok := wakes[n]; ok {
			if wi.obOK {
				a.waiting, a.stale, a.gap = wi.ob.Size(), wi.ob.Stale, wi.ob.GapPossible
				a.queueKnown, a.queueFailed, a.queueAt = true, false, time.Now()
			} else {
				a.queueFailed = true
			}
			if wi.recOK {
				a.wake = nil
				if wi.hasRec {
					rec := wi.rec
					a.wake = &rec
				}
			}
		}
	}
}

// errNotPolled marks a dispatcher read skipped on this tick: like a failed
// read, it leaves the last value on screen.
var errNotPolled = errors.New("not polled this tick")

// wakePollDue reports whether tick (1-based) reads the dispatcher facts: the
// first tick, then every wakePoll ticks.
func wakePollDue(tick int) bool { return tick%wakePoll == 1 }

// agentsOrNil drops a snapshot that came from a failed read, so a broker that
// could not answer never looks like a broker that answered "nobody".
func agentsOrNil(snaps map[string]bus.AgentSnapshot, err error) map[string]bus.AgentSnapshot {
	if err != nil {
		return nil
	}
	return snaps
}

// missionsFact is the header's missions state: "up" while a conductor holds
// its lease, "down" when none runs but a change is loaded (work is stalled),
// "" when the project uses no missions at all (nothing to show).
func missionsFact(ctx context.Context, b busReader) (string, error) {
	alive, err := b.ConductorAlive(ctx)
	if err != nil {
		return "", err
	}
	if alive {
		return "up", nil
	}
	names, err := b.ChangeNames(ctx)
	if err != nil {
		return "", err
	}
	if len(names) > 0 {
		return "down", nil
	}
	return "", nil
}
