package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/plan"
)

// The stall guard's readers (spec docs/superpowers/specs/2026-11-01-stall-guard-design.md,
// Data paths): each check reads what it judges from and hands it to the pure
// evaluator in health/stall.go. A read that fails is a fixed phrase, read as
// unknown, never ok and never a stall.

// stallSettings are the three thresholds, read once per pass (0 = off).
type stallSettings struct{ stall, clear, quiet time.Duration }

func readStallSettings(getenv func(string) string) stallSettings {
	r := config.Machine(getenv)
	return stallSettings{r.Duration("dispatch.stall_after"), r.Duration("dispatch.clear_stall_after"), r.Duration("dispatch.quiet_after")}
}

// stallReader holds what the checks keep between passes: the verdict ledger,
// read forward (spec: one full read at start, then only new entries).
type stallReader struct {
	b      *bus.Bus
	getenv func(string) string
	clear  dispatch.ClearConfig
	master *string // the dispatcher's escalation target, set once its flags are read
	now    func() time.Time

	mu       sync.Mutex
	verdicts []bus.Verdict
	lastID   string
}

func newStallReader(b *bus.Bus, getenv func(string) string, clear dispatch.ClearConfig, master *string) *stallReader {
	return &stallReader{b: b, getenv: getenv, clear: clear, master: master, now: time.Now}
}

func (s *stallReader) escalateTo() string {
	if s.master == nil || *s.master == "" {
		return "master"
	}
	return *s.master
}

func sortStrings(lists ...[]string) {
	for _, l := range lists {
		sort.Strings(l)
	}
}

// heldProviders are the providers the quota pass holds ({p}:quota hold:<p>).
func (s *stallReader) heldProviders(ctx context.Context) (map[string]bool, error) {
	marks, err := s.b.QuotaMarks(ctx)
	if err != nil {
		return nil, err
	}
	held := map[string]bool{}
	for k := range marks {
		if p, ok := strings.CutPrefix(k, "hold:"); ok {
			held[p] = true
		}
	}
	return held, nil
}

func dormantAt(snap bus.AgentSnapshot, now time.Time) bool {
	return now.Sub(time.UnixMilli(snap.TS)) > bus.DormantAfter
}

// clearStall reads the valid hand-offs and the clear pass's last decisions.
func (s *stallReader) clearStall(ctx context.Context) (health.Outcome, error) {
	in := health.ClearStallInput{Now: s.now(), After: readStallSettings(s.getenv).clear, AutoClear: s.clear.On}
	if in.After <= 0 {
		return health.EvaluateClearStall(in), nil
	}
	recs, err := s.b.Handoffs(ctx)
	if err != nil {
		in.ReadErr = "hand-offs unavailable"
		return health.EvaluateClearStall(in), nil
	}
	snaps, err := s.b.Agents(ctx)
	if err != nil {
		in.ReadErr = "agents unavailable"
		return health.EvaluateClearStall(in), nil
	}
	for a, rec := range recs {
		snap, ok := snaps[a]
		void, _ := rec.VoidFor(snap, ok)
		in.Handoffs = append(in.Handoffs, health.ClearHandoff{Agent: a, At: time.UnixMilli(rec.At), Void: void,
			Decision: s.clear.Decisions.Last(a)})
	}
	return health.EvaluateClearStall(in), nil
}

// quietAgent reads the retained cmd history, each agent's cursor and its
// writes on status, report and cmd.
func (s *stallReader) quietAgent(ctx context.Context) (health.Outcome, error) {
	now := s.now()
	in := health.QuietAgentInput{Now: now, After: readStallSettings(s.getenv).quiet, Master: s.escalateTo()}
	if in.After <= 0 {
		return health.EvaluateQuietAgent(in), nil
	}
	fail := func(phrase string) (health.Outcome, error) {
		in.ReadErr = phrase
		return health.EvaluateQuietAgent(in), nil
	}
	snaps, err := s.b.Agents(ctx)
	if err != nil {
		return fail("agents unavailable")
	}
	held, err := s.heldProviders(ctx)
	if err != nil {
		return fail("quota holds unavailable")
	}
	obs, err := s.b.Observed(ctx)
	if err != nil {
		return fail("observations unavailable")
	}
	cmds, _, err := s.b.Recent(ctx, []string{"cmd"}, 2000)
	if err != nil {
		return fail("cmd history unavailable")
	}
	reports, _, err := s.b.Recent(ctx, []string{"report"}, 2000)
	if err != nil {
		return fail("report history unavailable")
	}
	if len(cmds) > 0 {
		in.OldestRetained = cmds[0].ID
	}
	lastWrite := map[string]string{}
	wrote := func(agent, id string) {
		if cur, ok := lastWrite[agent]; !ok || health.StreamIDLess(cur, id) {
			lastWrite[agent] = id
		}
	}
	for _, e := range cmds {
		wrote(e.From, e.ID)
		in.Cmds = append(in.Cmds, health.QuietCmd{ID: e.ID, From: e.From, Target: e.Target, Type: e.Type,
			NoWake: e.NoWake, Human: e.Human, Expired: e.ExpiresAt > 0 && e.ExpiresAt <= now.UnixMilli()})
	}
	for _, e := range reports {
		wrote(e.Agent, e.ID)
	}
	for name, snap := range snaps {
		wrote(name, fmt.Sprintf("%d-0", snap.TS)) // the status entry's millisecond
		o, err := s.b.Obligation(ctx, name, bus.DefaultHorizon)
		if err != nil { // a cursor not read is never "not read yet" (foureyes)
			return fail("delivery cursors unavailable")
		}
		a := health.QuietAgent{Name: name, State: snap.State, Held: held[snap.Provider], Dormant: dormantAt(snap, now),
			LastDelivered: o.LastDelivered, Pending: o.PendingIDs, LastWrite: lastWrite[name]}
		if ob, ok := obs[name]; ok && ob.Fresh(now.UnixMilli()) && ob.Busy {
			a.Pane = health.PaneBusy
		}
		in.Agents = append(in.Agents, a)
	}
	return health.EvaluateQuietAgent(in), nil
}

// readVerdicts brings the ledger copy up to date, reading only new entries.
func (s *stallReader) readVerdicts(ctx context.Context) (map[string][]bus.Verdict, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		vs, err := s.b.VerdictsAfter(ctx, s.lastID, 500)
		if err != nil {
			return nil, err
		}
		s.verdicts = append(s.verdicts, vs...)
		if len(vs) > 0 {
			s.lastID = vs[len(vs)-1].ID
		}
		if len(vs) < 500 {
			return plan.BySubject(s.verdicts, nil), nil
		}
	}
}

// teamStall reads everything team-stall judges from.
func (s *stallReader) teamStall(ctx context.Context) (health.Outcome, error) {
	now := s.now()
	set := readStallSettings(s.getenv)
	in := health.TeamStallInput{Now: now, After: set.stall, QuietOff: set.quiet <= 0}
	if in.After <= 0 {
		return health.EvaluateTeamStall(in), nil
	}
	fail := func(phrase string) (health.Outcome, error) {
		in.ReadErr = phrase
		return health.EvaluateTeamStall(in), nil
	}
	snaps, err := s.b.Agents(ctx)
	if err != nil {
		return fail("agents unavailable")
	}
	obs, err := s.b.Observed(ctx)
	if err != nil {
		return fail("observations unavailable")
	}
	held, err := s.heldProviders(ctx)
	if err != nil {
		return fail("quota holds unavailable")
	}
	in.NoIdentities = true
	team := map[string]bus.AgentSnapshot{}
	for name, snap := range snaps {
		if snap.PID > 0 {
			in.NoIdentities = false
		}
		a := health.StallAgent{Name: name, State: snap.State, DeclaredAt: time.UnixMilli(snap.TS), Dormant: dormantAt(snap, now)}
		if ob, ok := obs[name]; ok && ob.Fresh(now.UnixMilli()) {
			a.Pane = health.PaneIdle
			if ob.Busy {
				a.Pane = health.PaneBusy
			}
		}
		in.Agents = append(in.Agents, a)
		if !a.Dormant {
			team[name] = snap
		}
	}
	if phrase, err := s.readOpenWork(ctx, team, &in.Open); err != nil {
		return fail(phrase)
	}
	recs, err := s.b.Health(ctx)
	if err != nil {
		return fail("health records unavailable")
	}
	if rec, ok := recs["quiet-agent"]; ok {
		in.Quiet, in.QuietDetail = health.EffectiveStatus(rec, now.UnixMilli(), time.Minute), rec.Detail
	} else {
		in.Quiet = health.Unknown
	}
	act, ok, err := plan.Load(ctx, s.b)
	if err != nil {
		return fail("plan unavailable")
	}
	if ok {
		verdicts, err := s.readVerdicts(ctx)
		if err != nil {
			return fail("verdicts unavailable")
		}
		pin, errs := plan.ReadWithVerdicts(ctx, s.b, act.File, verdicts, true)
		if len(errs) > 0 {
			return fail("plan inputs unavailable")
		}
		in.PlanLoaded = true
		for _, r := range plan.Derive(act.File, pin) {
			switch {
			case r.State == plan.Waiting:
				in.PersonReady = append(in.PersonReady, r.Task.Title)
			case r.State == plan.Upcoming && r.Task.Owner != plan.Person && len(r.WaitingOn) == 0:
				t := health.ReadyTask{ID: r.Task.ID, Title: r.Task.Title, Owner: r.Task.Owner}
				if snap, ok := snaps[r.Task.Owner]; ok && held[snap.Provider] {
					t.OwnerHeld, t.Hold = true, snap.Provider+" held"
				}
				in.AgentReady = append(in.AgentReady, t)
			}
		}
	}
	return health.EvaluateTeamStall(in), nil
}

// readOpenWork fills open with what keeps the team out of a stall; on a
// failed read it returns the phrase to report.
func (s *stallReader) readOpenWork(ctx context.Context, team map[string]bus.AgentSnapshot, open *health.TeamOpenWork) (string, error) {
	board, err := s.b.Board(ctx)
	if err != nil {
		return "board unavailable", err
	}
	for _, e := range board {
		switch e.State {
		case "requested":
			open.Requests++
		case "accepted", "working":
			open.Accepted++ // an accepted task with a sleeping owner is open work (the person's answer)
		}
	}
	asks, err := s.b.Asks(ctx)
	if err != nil {
		return "asks unavailable", err
	}
	for _, a := range asks {
		if a.Open() > 0 {
			open.Asks++
		}
	}
	pause, err := s.b.Pause(ctx)
	if err != nil {
		return "pause unavailable", err
	}
	open.Paused = pause != nil
	swaps, err := s.b.SwapIntents(ctx)
	if err != nil {
		return "swap intents unavailable", err
	}
	for role := range swaps {
		open.Swapping = append(open.Swapping, role)
	}
	recs, err := s.b.Handoffs(ctx)
	if err != nil {
		return "hand-offs unavailable", err
	}
	for name, snap := range team {
		gates, err := s.b.OpenChallenges(ctx, name)
		if err != nil {
			return "gates unavailable", err
		}
		open.Gates += len(gates)
		o, err := s.b.Obligation(ctx, name, bus.DefaultHorizon)
		if err != nil {
			return "delivery cursors unavailable", err
		}
		if o.Wakes() {
			open.Obligations = append(open.Obligations, name)
		}
		if rec, ok := recs[name]; ok {
			if void, _ := rec.VoidFor(snap, true); !void {
				open.Handoffs = append(open.Handoffs, name)
			}
		}
		ms, err := missionsOf(ctx, s.b, name)
		if err != nil {
			return "missions unavailable", err
		}
		if len(ms) > 0 {
			open.Missions = append(open.Missions, name)
		}
	}
	sortStrings(open.Swapping, open.Obligations, open.Handoffs, open.Missions)
	return "", nil
}

// stallNotify is the health loop's hook after each stored team-stall record
// (HealthConfig.StallNotify): while the fail lasts, the person is told once
// per episode, at once when master cannot act (dispatch.CannotAct, the
// escalation rule), else once the fail has lasted twice stall_after. The
// mark "stall" holds the episode (the record's since, Redis time), so
// {p}:quota keeps one field; it is deleted once the fail ends.
func (s *stallReader) stallNotify(ctx context.Context, token string, _ health.Record) error {
	recs, err := s.b.Health(ctx)
	if err != nil {
		return err
	}
	marks, err := s.b.QuotaMarks(ctx)
	if err != nil {
		return err
	}
	rec := recs["team-stall"]
	_, told := marks[stallMark]
	switch {
	case rec.Status == health.OK || rec.Status == health.Warn:
		if told { // the stall is over: the next one is a new episode
			return s.b.FencedQuotaDel(ctx, token, stallMark)
		}
		return nil
	case rec.Status != health.Fail || rec.Since == 0:
		return nil // unknown (a failed read) keeps the episode and its mark (reviewer-codex)
	case told:
		return nil // told this episode: a fail after an unknown has a new since, not a new stall
	}
	episode := strconv.FormatInt(rec.Since, 10)
	target := s.escalateTo()
	snaps, err := s.b.Agents(ctx)
	if err != nil {
		return err
	}
	obs, err := s.b.Observed(ctx)
	if err != nil {
		return err
	}
	held := map[string]bool{}
	for k := range marks {
		if p, ok := strings.CutPrefix(k, "hold:"); ok {
			held[p] = true
		}
	}
	why := target + " is not registered"
	if snap, ok := snaps[target]; ok {
		o, seen := obs[target]
		why = dispatch.CannotAct(target, held[snap.Provider], seen && o.State == "blocked")
	}
	var msg string
	switch since := s.now().Sub(time.UnixMilli(rec.Since)); {
	case why != "":
		msg = fmt.Sprintf("Stall guard: the team is idle with work ready (%s), and %s cannot act (%s). Dispatch from busmon, or wait.", rec.Detail, target, why)
	case since >= 2*readStallSettings(s.getenv).stall:
		msg = fmt.Sprintf("Stall guard: the team is still idle with work ready (%s); %s was told and nothing moved. Look at %s's pane, or dispatch from busmon.", rec.Detail, target, target)
	default:
		return nil
	}
	_, err = s.b.FencedNotifyOnceForPerson(ctx, token, stallMark, episode, "dispatch", msg)
	return err
}

// stallMark is the {p}:quota field of the stall guard's notice for the person.
const stallMark = "stall"

// stallChecks are the three health checks of the stall guard.
func stallChecks(s *stallReader) []dispatch.HealthCheck {
	return []dispatch.HealthCheck{
		healthOutcomeTask("quiet-agent", s.quietAgent), // before team-stall, which reads its record
		healthOutcomeTask("clear-stall", s.clearStall),
		healthOutcomeTask("team-stall", s.teamStall),
	}
}
