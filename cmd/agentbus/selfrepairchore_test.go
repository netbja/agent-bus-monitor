package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/relaunch"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/selfrepair"
)

var srT0 = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

const srSession = "58b5f18b-aaaa-bbbb-cccc-000000000001"

// srProcs fakes /proc: a pid in it runs with that start; any other is gone.
func srProcs(running map[int]uint64) func(int) (uint64, error) {
	return func(pid int) (uint64, error) {
		if s, ok := running[pid]; ok {
			return s, nil
		}
		return 0, dispatch.ErrNoProcess
	}
}

// srReads: a template of master, coder and foureyes in project demo (with a
// busmon tab and a plain tab, neither an agent), each registered with a
// proved Claude Code session and a fresh anthropic reading.
func srReads(now time.Time) selfRepairReads {
	snap := func(pid int) bus.AgentSnapshot {
		return bus.AgentSnapshot{State: "idle", PID: pid, PIDStart: 99, Pane: "w1:p" + strconv.Itoa(pid),
			Client: "claude-code", Provider: "anthropic", IdentSession: srSession}
	}
	return selfRepairReads{
		Now:   now,
		Snaps: map[string]bus.AgentSnapshot{"master": snap(101), "coder": snap(102), "foureyes": snap(103)},
		Marks: map[string]string{},
		Budgets: map[string]bus.BudgetSnapshot{
			"anthropic": {Provider: "anthropic", ObservedAt: now.Add(-time.Minute).UnixMilli()},
		},
		Health:  map[string]health.Record{},
		Intents: map[string]bus.SwapIntent{},
		Tabs: []relaunch.Tab{
			{Name: "master", Command: "agent-launch master demo"},
			{Name: "busmon", Command: "busmon --project demo"},
			{Name: "coder", Command: "env X=1 /usr/bin/agent-launch coder demo"},
			{Name: "notes", Command: "vim notes.md"},
			{Name: "foureyes", Command: "agent-launch foureyes demo"},
		},
		Required: map[string]bool{"master": true, "coder": true},
		Pops:     map[string]bool{},
		Own: map[string]rolecfg.Profile{"master": {Client: "claude-code", Provider: "anthropic"},
			"coder": {Client: "claude-code", Provider: "anthropic"}, "foureyes": {Client: "claude-code", Provider: "anthropic"}},
	}
}

// srChore: coder's process (102) is gone, the others run.
func srChore(running map[int]uint64) (*selfRepairChore, *int) {
	herdrCalls := 0
	c := &selfRepairChore{project: "demo", mode: selfrepair.ModeShadow, every: 30 * time.Minute, perDay: 3,
		master:    func() string { return "master" },
		procStart: srProcs(running),
		herdr: func(map[string]bus.AgentSnapshot) selfRepairHerdr {
			herdrCalls++
			return selfRepairHerdr{Up: true, Open: true, Judge: func(t relaunch.Tab, create bool) relaunch.Verdict {
				if create {
					return relaunch.VerdictRelaunch
				}
				return relaunch.VerdictMissing
			}}
		}}
	return c, &herdrCalls
}

func srAgent(t *testing.T, f selfrepair.Facts, name string) selfrepair.Agent {
	t.Helper()
	for _, a := range f.Agents {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("no agent %s in %+v", name, f.Agents)
	return selfrepair.Agent{}
}

// The facts of one pass (self-repair spec 4.1, step 6b): the template's
// agents only, in its order; a death counted over reads from its first one,
// per episode (the dead process); every precondition read from the bus.
func TestSelfRepairFacts(t *testing.T) {
	alive := map[int]uint64{101: 99, 103: 99}
	c, herdrCalls := srChore(alive)
	f := c.facts(srReads(srT0))
	var names []string
	for _, a := range f.Agents {
		names = append(names, a.Name)
	}
	if strings.Join(names, ",") != "master,coder,foureyes" {
		t.Fatalf("agents %v: the template's agent tabs, in order", names)
	}
	m, co := srAgent(t, f, "master"), srAgent(t, f, "coder")
	if !m.Master || co.Master || m.Life != relaunch.LifeAlive || co.Life != relaunch.LifeDead {
		t.Fatalf("master %+v coder %+v", m, co)
	}
	if co.DeadReads != 1 || !co.DeadSince.Equal(srT0) || !co.Required || srAgent(t, f, "foureyes").Required {
		t.Fatalf("first dead read %+v", co)
	}
	if co.Session != srSession || !co.Resumable || !co.ProviderFresh || co.ProviderHeld || co.Tab != relaunch.VerdictRelaunch {
		t.Fatalf("coder %+v", co)
	}
	if !f.HerdrUp || !f.WorkspaceOpen || f.Mode != selfrepair.ModeShadow || f.Every != 30*time.Minute || f.PerDay != 3 || f.Grace != 60*time.Second {
		t.Fatalf("facts %+v", f)
	}
	if *herdrCalls != 1 {
		t.Fatalf("herdr read %d times: once a pass with a death", *herdrCalls)
	}
	// The death's later reads count from its first; its grace is Redis time.
	c.facts(srReads(srT0.Add(20 * time.Second)))
	f = c.facts(srReads(srT0.Add(40 * time.Second)))
	if co := srAgent(t, f, "coder"); co.DeadReads != 3 || !co.DeadSince.Equal(srT0) {
		t.Fatalf("third read %+v", co)
	}
	// Another process of the role dead: another episode, counted afresh.
	r := srReads(srT0.Add(60 * time.Second))
	s := r.Snaps["coder"]
	s.PID = 202
	r.Snaps["coder"] = s
	f = c.facts(r)
	if co := srAgent(t, f, "coder"); co.DeadReads != 1 || !co.DeadSince.Equal(srT0.Add(60*time.Second)) || c.deaths["coder"].episode != "202:99" {
		t.Fatalf("a new episode %+v %+v", co, c.deaths["coder"])
	}
	// Alive again: the death is forgotten.
	c.procStart = srProcs(map[int]uint64{101: 99, 102: 99, 103: 99})
	f = c.facts(srReads(srT0.Add(80 * time.Second)))
	if co := srAgent(t, f, "coder"); co.Life != relaunch.LifeAlive || co.DeadReads != 0 || len(c.deaths) != 0 {
		t.Fatalf("alive %+v %v", co, c.deaths)
	}
	// Nobody dead: herdr is not read.
	before := *herdrCalls
	c.facts(srReads(srT0.Add(100 * time.Second)))
	if *herdrCalls != before {
		t.Fatal("herdr read with nobody dead")
	}
}

// Each precondition's source, one change at a time.
func TestSelfRepairFactsSources(t *testing.T) {
	cases := []struct {
		name   string
		change func(*selfRepairReads)
		check  func(selfrepair.Facts, selfrepair.Agent) bool
	}{
		{"a reload's intent", func(r *selfRepairReads) { r.Intents["coder"] = bus.SwapIntent{Kind: bus.SwapKindReload} },
			func(f selfrepair.Facts, a selfrepair.Agent) bool { return a.Intent == "reload" && !f.InFlight }},
		{"a swap's intent", func(r *selfRepairReads) { r.Intents["coder"] = bus.SwapIntent{ToProfile: "opus"} },
			func(f selfrepair.Facts, a selfrepair.Agent) bool { return a.Intent == "swap" }},
		{"a self-repair in flight elsewhere", func(r *selfRepairReads) { r.Intents["foureyes"] = bus.SwapIntent{Kind: bus.SwapKindSelfRepair} },
			func(f selfrepair.Facts, a selfrepair.Agent) bool { return f.InFlight && a.Intent == "" }},
		{"paused", func(r *selfRepairReads) { r.Pause = &bus.PauseRecord{Until: srT0.Add(time.Hour).UnixMilli()} },
			func(f selfrepair.Facts, _ selfrepair.Agent) bool { return f.Paused && !f.Suspended }},
		{"suspended", func(r *selfRepairReads) { r.Pause = &bus.PauseRecord{Kind: bus.PauseKindSuspend} },
			func(f selfrepair.Facts, _ selfrepair.Agent) bool { return f.Suspended && !f.Paused }},
		{"its provider held", func(r *selfRepairReads) { r.Marks["hold:anthropic"] = "1799999999" },
			func(_ selfrepair.Facts, a selfrepair.Agent) bool { return a.ProviderHeld }},
		{"another provider held", func(r *selfRepairReads) { r.Marks["hold:openai"] = "1799999999" },
			func(_ selfrepair.Facts, a selfrepair.Agent) bool { return !a.ProviderHeld }},
		{"a reading 6 min old", func(r *selfRepairReads) {
			r.Budgets["anthropic"] = bus.BudgetSnapshot{ObservedAt: srT0.Add(-6 * time.Minute).UnixMilli()}
		}, func(_ selfrepair.Facts, a selfrepair.Agent) bool { return !a.ProviderFresh }},
		{"a reading the reader could not read", func(r *selfRepairReads) {
			r.Budgets["anthropic"] = bus.BudgetSnapshot{ObservedAt: srT0.UnixMilli(), Unknown: true}
		}, func(_ selfrepair.Facts, a selfrepair.Agent) bool { return !a.ProviderFresh }},
		{"no reading", func(r *selfRepairReads) { delete(r.Budgets, "anthropic") },
			func(_ selfrepair.Facts, a selfrepair.Agent) bool { return !a.ProviderFresh }},
		{"a local model whose endpoint answers", func(r *selfRepairReads) {
			s := r.Snaps["coder"]
			s.Client, s.Provider = "local-llm", ""
			r.Snaps["coder"] = s
			r.Health["llm-endpoint"] = health.Record{Status: health.OK, ObservedAt: srT0.Add(-time.Minute).UnixMilli()}
		}, func(_ selfrepair.Facts, a selfrepair.Agent) bool {
			return a.ProviderFresh && !a.Resumable && a.Session == ""
		}},
		{"a local model whose endpoint fails", func(r *selfRepairReads) {
			s := r.Snaps["coder"]
			s.Client, s.Provider = "local-llm", ""
			r.Snaps["coder"] = s
			r.Health["llm-endpoint"] = health.Record{Status: health.Warn, ObservedAt: srT0.UnixMilli()}
		}, func(_ selfrepair.Facts, a selfrepair.Agent) bool { return !a.ProviderFresh }},
		{"a hand-off clear in flight", func(r *selfRepairReads) { r.Marks[bus.ClearMark("coder")] = srSession + "|1|1-0|1-0|3" },
			func(_ selfrepair.Facts, a selfrepair.Agent) bool { return a.HandoffClear }},
		{"a pop done", func(r *selfRepairReads) {
			s := r.Snaps["coder"]
			s.Tier, s.State = "pop", "done"
			r.Snaps["coder"] = s
		}, func(_ selfrepair.Facts, a selfrepair.Agent) bool { return a.Pop && a.Retired }},
		{"a pop told done", func(r *selfRepairReads) { r.Pops["coder"] = true; r.Marks[bus.PopDoneMark("coder")] = "7" },
			func(_ selfrepair.Facts, a selfrepair.Agent) bool { return a.Pop && a.Retired }},
		{"no proved session", func(r *selfRepairReads) {
			s := r.Snaps["coder"]
			s.IdentSession = ""
			r.Snaps["coder"] = s
		}, func(_ selfrepair.Facts, a selfrepair.Agent) bool { return a.Session == "" && a.Resumable }},
		{"its budget's past (step 7)", func(r *selfRepairReads) {
			r.Past = map[string]bus.SelfRepairRecord{"coder": {Attempts: []time.Time{srT0.Add(-3 * time.Hour), srT0.Add(-time.Hour)},
				CrashLoop: true, Failed: map[string]bool{"7:7": true}}}
		}, func(f selfrepair.Facts, a selfrepair.Agent) bool {
			return len(a.Relaunches) == 2 && a.CrashLoop && !a.ProofFailed && f.LastRelaunch.Equal(srT0.Add(-time.Hour))
		}},
		{"this death's relaunch failed its proof", func(r *selfRepairReads) {
			r.Past = map[string]bus.SelfRepairRecord{"coder": {Failed: map[string]bool{"102:99": true}}}
		}, func(_ selfrepair.Facts, a selfrepair.Agent) bool { return a.ProofFailed && !a.CrashLoop }},
		{"the project's last relaunch is any role's", func(r *selfRepairReads) {
			r.Past = map[string]bus.SelfRepairRecord{"retired-pop": {Attempts: []time.Time{srT0.Add(-time.Minute)}},
				"coder": {Attempts: []time.Time{srT0.Add(-time.Hour)}}}
		}, func(f selfrepair.Facts, a selfrepair.Agent) bool {
			return f.LastRelaunch.Equal(srT0.Add(-time.Minute)) && len(a.Relaunches) == 1
		}},
		{"no snapshot", func(r *selfRepairReads) { delete(r.Snaps, "coder") },
			func(_ selfrepair.Facts, a selfrepair.Agent) bool {
				return a.Life == relaunch.LifeUnknown && a.DeadReads == 0
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := srChore(map[int]uint64{101: 99, 103: 99})
			r := srReads(srT0)
			tc.change(&r)
			f := c.facts(r)
			if a := srAgent(t, f, "coder"); !tc.check(f, a) {
				t.Fatalf("facts %+v coder %+v", f, a)
			}
		})
	}
	// herdr down or the workspace closed: said, and no tab judged.
	c, _ := srChore(map[int]uint64{101: 99, 103: 99})
	c.herdr = func(map[string]bus.AgentSnapshot) selfRepairHerdr { return selfRepairHerdr{} }
	if f := c.facts(srReads(srT0)); f.HerdrUp || f.WorkspaceOpen || srAgent(t, f, "coder").Tab != relaunch.VerdictUnknown {
		t.Fatalf("herdr down %+v", f)
	}
	c.herdr = func(map[string]bus.AgentSnapshot) selfRepairHerdr { return selfRepairHerdr{Up: true} }
	if f := c.facts(srReads(srT0)); !f.HerdrUp || f.WorkspaceOpen || srAgent(t, f, "coder").Tab != relaunch.VerdictUnknown {
		t.Fatalf("workspace closed %+v", f)
	}
}

// The shadow pass (step 6b) on a disposable broker: a death past its grace
// writes one would-relaunch per episode, fenced on the lease; the check
// says what it would do; nothing else is written.
func TestSelfRepairShadowPassRedis(t *testing.T) {
	b := notifyBus(t) // holds the lease as "tok"
	ctx := context.Background()
	c, _ := srChore(map[int]uint64{101: 99, 103: 99})
	c.b = b
	now := srT0
	c.read = func(context.Context) (selfRepairReads, error) { return srReads(now), nil }
	if out, _ := c.check(ctx); out.Status != health.Unknown {
		t.Fatalf("before a pass: %+v", out)
	}
	for i := 0; i < 4; i++ { // 0 s, 20 s, 40 s, 60 s: three reads, then the grace
		now = srT0.Add(time.Duration(i) * 20 * time.Second)
		if err := c.pass(ctx, "tok"); err != nil {
			t.Fatal(err)
		}
	}
	out, err := c.check(ctx)
	if err != nil || out.Status != health.Warn || !strings.Contains(out.Detail, "shadow: would relaunch coder") {
		t.Fatalf("check %+v %v", out, err)
	}
	if err := c.pass(ctx, "tok"); err != nil { // the same death: not recorded twice
		t.Fatal(err)
	}
	log, _ := b.SwapLog(ctx, "", 0)
	if len(log) != 1 {
		t.Fatalf("ledger %+v", log)
	}
	if e := log[0]; e.Event != bus.SwapWouldRelaunch || e.Role != "coder" || e.Episode != "102:99" || e.OldPID != 102 ||
		e.By != "dispatch" || !strings.Contains(e.Reason, "relaunch in a new pane") {
		t.Fatalf("entry %+v", e)
	}
	if snaps, _ := b.Agents(ctx); len(snaps) != 0 {
		t.Fatalf("the shadow wrote snapshots: %v", snaps)
	}
	// Another death of coder, past its grace: its write is refused without the lease.
	now = now.Add(20 * time.Second)
	c.read = func(context.Context) (selfRepairReads, error) {
		r := srReads(now)
		s := r.Snaps["coder"]
		s.PID = 202
		r.Snaps["coder"] = s
		return r, nil
	}
	for i := 0; i < 4; i++ {
		now = now.Add(20 * time.Second)
		err = c.pass(ctx, "not-the-lease")
	}
	if err == nil || !strings.Contains(err.Error(), bus.ErrNotHolder.Error()) {
		t.Fatalf("a write without the lease: %v", err)
	}
}

// The check by mode: off is ok with no pass at all; on (step 7b) and
// shadow report their last pass, unknown before one and once stale.
func TestSelfRepairCheckModes(t *testing.T) {
	ctx := context.Background()
	if out, err := (&selfRepairChore{mode: selfrepair.ModeOff}).check(ctx); err != nil || out.Status != health.OK || !strings.Contains(out.Detail, "off") {
		t.Errorf("off: %+v %v", out, err)
	}
	for _, m := range []selfrepair.Mode{selfrepair.ModeOn, selfrepair.ModeShadow} {
		c := &selfRepairChore{mode: m, every: 30 * time.Minute, clock: func() time.Time { return srT0 }}
		if out, _ := c.check(ctx); out.Status != health.Unknown || !strings.Contains(out.Detail, "no self-repair pass yet") {
			t.Fatalf("%s before a pass: %+v", m, out)
		}
		c.ran, c.ranAt, c.out = true, srT0.Add(-30*time.Second), health.Outcome{Status: health.OK, Detail: "nothing to repair"}
		if out, _ := c.check(ctx); out.Status != health.OK {
			t.Fatalf("%s fresh: %+v", m, out)
		}
		c.ranAt = srT0.Add(-5 * time.Minute)
		if out, _ := c.check(ctx); out.Status != health.Unknown || !strings.Contains(out.Detail, "no self-repair pass since") {
			t.Fatalf("%s stale: %+v", m, out)
		}
	}
}

// The service reads the mode once at its start: on (the default, step 7b)
// and shadow (development mode only) run the pass; off runs none; shadow
// outside development mode is refused and reads off. Only on has the acts. The check is there in every mode, and the
// settings reach the facts.
func TestServiceSelfRepairByMode(t *testing.T) {
	b := notifyBus(t)
	cases := []struct {
		env  map[string]string
		mode selfrepair.Mode
		pass bool
	}{
		{map[string]string{}, selfrepair.ModeOn, true},
		{map[string]string{"AGENT_BUS_SELF_REPAIR": "off"}, selfrepair.ModeOff, false},
		{map[string]string{"AGENT_BUS_SELF_REPAIR": "shadow"}, selfrepair.ModeOff, false},
		{map[string]string{"AGENT_BUS_SELF_REPAIR": "shadow", "AGENT_BUS_DEV": "1",
			"AGENT_BUS_SELF_REPAIR_EVERY": "45m", "AGENT_BUS_SELF_REPAIR_PER_DAY": "5"}, selfrepair.ModeShadow, true},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		getenv := func(k string) string {
			if k == "XDG_CONFIG_HOME" || k == "HOME" {
				return dir
			}
			return tc.env[k]
		}
		escalate := "boss"
		c, cfg := serviceSelfRepair(b, getenv, &escalate)
		if c.mode != tc.mode || (cfg.Pass != nil) != tc.pass || c.master() != "boss" {
			t.Errorf("%v: mode %s pass %v", tc.env, c.mode, cfg.Pass != nil)
		}
		if check := selfRepairCheck(c); check.Definition.ID != "self-repair" || check.Run == nil {
			t.Errorf("%v: check %+v", tc.env, check.Definition)
		}
		if (c.relaunch != nil) != (tc.mode == selfrepair.ModeOn) || (c.agents != nil) != (tc.mode == selfrepair.ModeOn) {
			t.Errorf("%v: the acts wired in mode %s", tc.env, c.mode)
		}
		if tc.mode == selfrepair.ModeShadow && (c.every != 45*time.Minute || c.perDay != 5 || cfg.Every != dispatch.SelfRepairEvery) {
			t.Errorf("settings: every %s per day %d chore %s", c.every, c.perDay, cfg.Every)
		}
	}
}

// A pass's reads on a disposable broker: the template's roles, roles.toml's
// required, each role's intent and (step 7) its self-repair past.
func TestReadSelfRepairRedis(t *testing.T) {
	b := notifyBus(t) // holds the lease as "tok"
	ctx := context.Background()
	cfg := t.TempDir()
	dir := filepath.Join(cfg, "agentbus", "projects")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tpl := "name = \"" + b.Project() + "\"\n[[tabs]]\nname = \"coder\"\ncommand = \"agent-launch coder " + b.Project() + "\"\n" +
		"[[tabs]]\nname = \"busmon\"\ncommand = \"busmon --project " + b.Project() + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, b.Project()+".toml"), []byte(tpl), 0o644); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	t.Setenv("ROLES_TOML", filepath.Join(wd, "..", "..", "roles.toml"))
	if _, err := b.Status(ctx, "coder", "idle", "", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if err := b.BeginSelfRepair(ctx, "tok", "coder", "n1", bus.SwapEvent{By: "dispatch", Episode: "5:5"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.MarkCrashLoop(ctx, "tok", "coder"); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return cfg
		}
		return ""
	}
	r, err := readSelfRepair(ctx, b, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tabs) != 2 || !r.Required["coder"] || r.Pops["coder"] || r.Intents["coder"].Kind != bus.SwapKindSelfRepair {
		t.Fatalf("reads %+v", r)
	}
	if p := r.Past["coder"]; !p.CrashLoop || len(p.Attempts) != 1 {
		t.Fatalf("past %+v", r.Past)
	}
	if _, ok := r.Snaps["coder"]; !ok || r.Now.IsZero() {
		t.Fatalf("snaps %v now %v", r.Snaps, r.Now)
	}
}
