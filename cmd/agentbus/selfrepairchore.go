package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/relaunch"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/selfrepair"
)

// The self-repair watcher's pass (spec
// docs/superpowers/specs/2026-11-09-self-repair-watcher-design.md, step 6b):
// the dispatch service gathers the facts of the template's agents, decides
// (selfrepair.Decide), and in on mode acts (selfrepairact.go, step 7b): the
// relaunch, its proof, the crash loop, the reports to master. Shadow records
// the would-be act in the ledger only; off never runs the pass.

const (
	// selfRepairGrace: dead this long, by Redis TIME, before R1 acts (4.1).
	selfRepairGrace = 60 * time.Second
	// providerFreshFor: a provider whose quota reader (or a local model whose
	// endpoint check) succeeded within this long can carry a relaunch (4.1.4).
	providerFreshFor = 5 * time.Minute
	// selfRepairStale: the shadow's check reads unknown once its pass has not
	// run for this long (the chore runs every dispatch.SelfRepairEvery).
	selfRepairStale = 2 * time.Minute
)

// selfRepairReads are one pass's reads from the bus and the project's files.
type selfRepairReads struct {
	Now      time.Time // Redis TIME
	Snaps    map[string]bus.AgentSnapshot
	Pause    *bus.PauseRecord                // nil: none
	Marks    map[string]string               // {p}:quota: holds, clears, pops told done
	Budgets  map[string]bus.BudgetSnapshot   // provider readings
	Health   map[string]health.Record        // the stored checks (llm-endpoint)
	Intents  map[string]bus.SwapIntent       // the template roles' held intents
	Tabs     []relaunch.Tab                  // the project's template
	Required map[string]bool                 // roles.toml's required
	Pops     map[string]bool                 // roles.toml's pop tier
	Past     map[string]bus.SelfRepairRecord // each role's attempts, failed proofs, crash loop (step 7)
	Own      map[string]rolecfg.Profile      // each role's own profile: what a relaunch must prove
}

// selfRepairHerdr is herdr as the pass sees it: the server answers, the
// project's workspace is open, and a tab's verdict (create: a required role).
type selfRepairHerdr struct {
	Up, Open bool
	Judge    func(t relaunch.Tab, create bool) relaunch.Verdict
}

// repairDeath is one role's death being watched: the dead process (its
// episode, "pid:start"), the first read that found it dead, the reads since.
type repairDeath struct {
	episode string
	since   time.Time
	reads   int
}

// selfRepairChore holds the pass's readers (tests replace them) and what it
// keeps between passes: the deaths being counted, the last decisions.
type selfRepairChore struct {
	b         *bus.Bus
	project   string
	mode      selfrepair.Mode
	every     time.Duration // dispatch.self_repair_every
	perDay    int           // dispatch.self_repair_per_day
	master    func() string // the escalation target: relaunched first (Q6)
	procStart func(int) (uint64, error)
	read      func(ctx context.Context) (selfRepairReads, error)
	// herdr is read once a pass, and only when an agent of the template
	// reads dead: a project with nobody dead costs herdr nothing.
	herdr func(snaps map[string]bus.AgentSnapshot) selfRepairHerdr
	clock func() time.Time // the check's staleness (local: never persisted)

	// The acts of on mode (step 7b), each replaced by tests: relaunch runs
	// the service entry of a relaunch with its fence; agents reads the
	// snapshots the proof needs; notify rings the desktop (nil: none).
	relaunch   func(ctx context.Context, role, session string, fence func(string) error) error
	agents     func(ctx context.Context) (map[string]bus.AgentSnapshot, error)
	notify     func(ctx context.Context, title, body string) error
	proofTries int                 // reads of the proof, proofEvery apart (60: 60 s)
	sleep      func(time.Duration) // nil: time.Sleep

	mu     sync.Mutex
	deaths map[string]repairDeath
	out    health.Outcome
	ran    bool
	ranAt  time.Time
}

// facts turns one pass's reads into selfrepair.Facts, counting each death
// over the passes. The template's agent tabs only, in its order.
func (c *selfRepairChore) facts(r selfRepairReads) selfrepair.Facts {
	if c.deaths == nil {
		c.deaths = map[string]repairDeath{}
	}
	f := selfrepair.Facts{Now: r.Now, Mode: c.mode, Grace: selfRepairGrace, Every: c.every, PerDay: c.perDay}
	if r.Pause != nil {
		f.Suspended = r.Pause.Suspended()
		f.Paused = !f.Suspended
	}
	for _, p := range r.Past { // any role's: the project's pause after a relaunch (4.5)
		for _, at := range p.Attempts {
			if at.After(f.LastRelaunch) {
				f.LastRelaunch = at
			}
		}
	}
	for _, in := range r.Intents {
		if in.Kind == bus.SwapKindSelfRepair {
			f.InFlight = true // R1 never runs next to another self-repair
		}
	}
	master := ""
	if c.master != nil {
		master = c.master()
	}
	seen := map[string]bool{}
	var tabs []relaunch.Tab
	for _, t := range r.Tabs {
		role, ok := relaunch.LaunchRole(t.Command, c.project)
		if !ok || seen[role] {
			continue
		}
		seen[role] = true
		tabs = append(tabs, t)
		snap, has := r.Snaps[role]
		a := selfrepair.Agent{Name: role, Master: role == master, Required: r.Required[role],
			Life: relaunch.SnapLife(snap, has, c.procStart)}
		if a.Life == relaunch.LifeDead {
			ep := strconv.Itoa(snap.PID) + ":" + strconv.FormatUint(snap.PIDStart, 10)
			d := c.deaths[role]
			if d.episode != ep {
				d = repairDeath{episode: ep, since: r.Now}
			}
			d.reads++
			c.deaths[role] = d
			a.DeadSince, a.DeadReads = d.since, d.reads
		} else {
			delete(c.deaths, role)
		}
		if in, held := r.Intents[role]; held {
			a.Intent = bus.SwapKindWord(in.Kind)
		}
		a.Pop = r.Pops[role] || snap.Tier == "pop"
		a.Retired = a.Pop && (snap.State == "done" || r.Marks[bus.PopDoneMark(role)] != "")
		if v, ok := r.Marks[bus.ClearMark(role)]; ok {
			_, a.HandoffClear = bus.ParseClearMark(v)
		}
		a.Session, a.Resumable = relaunch.ProvedSession(snap), rolecfg.ResumeVerified(snap.Client)
		a.ProviderFresh, a.ProviderHeld = providerFacts(r, snap)
		past := r.Past[role]
		a.Relaunches, a.CrashLoop = past.Attempts, past.CrashLoop
		if d, dead := c.deaths[role]; dead && a.Life == relaunch.LifeDead {
			a.ProofFailed = past.Failed[d.episode]
		}
		f.Agents = append(f.Agents, a)
	}
	for role := range c.deaths { // a role gone from the template
		if !seen[role] {
			delete(c.deaths, role)
		}
	}
	var h selfRepairHerdr
	read := false
	for i, a := range f.Agents {
		if a.Life != relaunch.LifeDead {
			continue
		}
		if !read && c.herdr != nil {
			h, read = c.herdr(r.Snaps), true
			f.HerdrUp, f.WorkspaceOpen = h.Up, h.Up && h.Open
		}
		f.Agents[i].Tab = relaunch.VerdictUnknown
		if f.WorkspaceOpen && h.Judge != nil {
			f.Agents[i].Tab = h.Judge(tabs[i], a.Required)
		}
	}
	return f
}

// providerFacts: whether snap's provider can carry a relaunch (a quota
// reading within providerFreshFor; a local model: its endpoint check ok
// within that long) and whether the coordinator holds it.
func providerFacts(r selfRepairReads, snap bus.AgentSnapshot) (fresh, held bool) {
	if snap.Client == "local-llm" {
		rec, ok := r.Health["llm-endpoint"]
		return ok && rec.Status == health.OK && r.Now.Sub(time.UnixMilli(rec.ObservedAt)) <= providerFreshFor, false
	}
	provider := bus.ProviderFor(snap.Client, snap.Provider)
	if provider == "" {
		return false, false
	}
	row, ok := r.Budgets[provider]
	at := row.ObservedAt
	if at == 0 {
		at = row.CollectedAt
	}
	fresh = ok && !row.Unknown && at > 0 && r.Now.Sub(time.UnixMilli(at)) <= providerFreshFor
	return fresh, r.Marks["hold:"+provider] != ""
}

// pass is one shadow pass: read, decide, record the would-be act once per
// death, keep the outcome for the check. token is the dispatch lease's.
func (c *selfRepairChore) pass(ctx context.Context, token string) error {
	r, err := c.read(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	f := c.facts(r)
	deaths := make(map[string]repairDeath, len(c.deaths))
	for k, v := range c.deaths {
		deaths[k] = v
	}
	c.mu.Unlock()
	ds := selfrepair.Decide(f)
	var first error
	if c.mode == selfrepair.ModeOn {
		first = c.act(ctx, token, ds, r, deaths, f.Now)
	}
	if c.mode == selfrepair.ModeShadow {
		for _, d := range ds {
			if !d.Act {
				continue
			}
			ep := deaths[d.Agent].episode
			logged, err := c.b.WouldRelaunchLogged(ctx, d.Agent, ep)
			if err == nil && !logged {
				snap := r.Snaps[d.Agent]
				err = c.b.LogWouldRelaunch(ctx, token, bus.SwapEvent{Role: d.Agent, By: "dispatch", Reason: string(d.Kind) + ": " + d.Reason,
					OldPID: snap.PID, OldPane: snap.Pane, Episode: ep})
			}
			if err != nil && first == nil {
				first = fmt.Errorf("would-relaunch %s: %w", d.Agent, err)
			}
		}
	}
	out := selfrepair.Health(c.mode, ds)
	c.mu.Lock()
	c.out, c.ran, c.ranAt = out, true, c.now()
	c.mu.Unlock()
	return first
}

func (c *selfRepairChore) now() time.Time {
	if c.clock != nil {
		return c.clock()
	}
	return time.Now()
}

// check is the health check self-repair: off says so; on and shadow report
// their last pass, unknown when none ran lately.
func (c *selfRepairChore) check(context.Context) (health.Outcome, error) {
	switch c.mode {
	case selfrepair.ModeOff:
		return health.Outcome{Status: health.OK, Detail: "self-repair off"}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case !c.ran:
		return health.Outcome{Status: health.Unknown, Detail: "no self-repair pass yet"}, nil
	case c.now().Sub(c.ranAt) > selfRepairStale:
		return health.Outcome{Status: health.Unknown, Detail: "no self-repair pass since " + c.ranAt.UTC().Format("15:04")}, nil
	}
	return c.out, nil
}

// serviceSelfRepair is the dispatch service's watcher, its mode read once at
// the service's start (dispatch.self_repair, a refusal read as off). Only
// shadow runs a pass in step 6; the check is there in every mode.
func serviceSelfRepair(b *bus.Bus, getenv func(string) string, master *string) (*selfRepairChore, dispatch.SelfRepairConfig) {
	r := config.Machine(getenv)
	c := &selfRepairChore{b: b, project: b.Project(), mode: selfrepair.Mode(config.SelfRepairMode(r)),
		every: r.Duration("dispatch.self_repair_every"), perDay: r.Int("dispatch.self_repair_per_day"),
		master: func() string {
			if master != nil && *master != "" {
				return *master
			}
			return "master"
		},
		procStart: dispatch.ProcStart}
	if c.mode == selfrepair.ModeOff {
		return c, dispatch.SelfRepairConfig{}
	}
	if c.mode == selfrepair.ModeOn {
		c.agents = b.Agents
		c.notify = desktopNotify(exec.LookPath, runQuiet)
		c.relaunch = func(ctx context.Context, role, session string, fence func(string) error) error {
			return serviceRelaunch(ctx, b, getenv, c.procStart, role, session, fence)
		}
	}
	c.read = func(ctx context.Context) (selfRepairReads, error) { return readSelfRepair(ctx, b, getenv) }
	c.herdr = func(snaps map[string]bus.AgentSnapshot) selfRepairHerdr {
		return serviceRepairHerdr(getenv, b.Project(), snaps, c.procStart)
	}
	return c, dispatch.SelfRepairConfig{Every: dispatch.SelfRepairEvery, Pass: c.pass}
}

// readSelfRepair reads a pass's facts. A failed read fails the pass (logged
// by the chore), never read as empty: no facts, no decision.
func readSelfRepair(ctx context.Context, b *bus.Bus, getenv func(string) string) (selfRepairReads, error) {
	var r selfRepairReads
	var err error
	if r.Now, err = b.ServerTime(ctx); err != nil {
		return r, err
	}
	if r.Snaps, err = b.Agents(ctx); err != nil {
		return r, err
	}
	if r.Pause, err = b.Pause(ctx); err != nil {
		return r, err
	}
	if r.Marks, err = b.QuotaMarks(ctx); err != nil {
		return r, err
	}
	if r.Budgets, err = b.Budgets(ctx); err != nil {
		return r, err
	}
	if r.Health, err = b.Health(ctx); err != nil {
		return r, err
	}
	tpl, err := relaunch.ReadTemplate(getenv, b.Project())
	if err != nil {
		return r, err
	}
	r.Tabs = tpl.Tabs
	path, err := manifestPath(os.Environ())
	if err != nil {
		return r, err
	}
	m, err := rolecfg.Load(path)
	if err != nil {
		return r, err
	}
	if r.Past, err = b.SelfRepairPast(ctx); err != nil {
		return r, err
	}
	r.Required, r.Pops, r.Intents, r.Own = map[string]bool{}, map[string]bool{}, map[string]bus.SwapIntent{}, map[string]rolecfg.Profile{}
	for _, t := range r.Tabs {
		role, ok := relaunch.LaunchRole(t.Command, b.Project())
		if !ok {
			continue
		}
		r.Required[role], r.Pops[role] = m.RoleRequired(role), m.RoleTier(role) == "pop"
		if own, err := m.Resolve(role); err == nil {
			r.Own[role] = own
		}
		in, held, err := b.SwapIntentOf(ctx, role)
		if err != nil {
			return r, err
		}
		if held {
			r.Intents[role] = in
		}
	}
	return r, nil
}

// serviceRepairHerdr reads herdr the way the service entry of a relaunch
// does (relaunch.ServiceHerdr: the session the services' env file names).
// Any failure reads as herdr down or the workspace closed: a hold, never an
// act.
func serviceRepairHerdr(getenv func(string) string, project string, snaps map[string]bus.AgentSnapshot, procStart func(int) (uint64, error)) selfRepairHerdr {
	h, err := relaunch.ServiceHerdr(getenv)
	if err != nil {
		return selfRepairHerdr{}
	}
	ws, err := relaunch.FindWorkspace(h, project, "")
	if err != nil {
		return selfRepairHerdr{}
	}
	if ws == "" {
		return selfRepairHerdr{Up: true}
	}
	panes, err := relaunch.WorkspacePanes(h, ws)
	if err != nil {
		return selfRepairHerdr{Up: true}
	}
	return selfRepairHerdr{Up: true, Open: true, Judge: func(t relaunch.Tab, create bool) relaunch.Verdict {
		return relaunch.JudgeTab(h, panes, t, project, snaps, true, procStart, create).V
	}}
}

// selfRepairCheck is the health check self-repair, in every mode.
func selfRepairCheck(c *selfRepairChore) dispatch.HealthCheck {
	return healthOutcomeTask("self-repair", c.check)
}
