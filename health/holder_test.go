package health

import (
	"strings"
	"testing"
	"time"
)

// holder (herdr exit 4.4a): fail when the current build's unit is installed
// and no holder of the project answers; ok when none is installed.
func TestEvaluateHolder(t *testing.T) {
	cur := HolderBuild{Key: "k2", Raw: "v2", Current: true, Unit: true, Active: true, Answers: true, Terms: 3}
	for name, c := range map[string]struct {
		e      HolderEvidence
		status Status
		want   string
	}{
		"unknown":         {HolderEvidence{}, Unknown, "unavailable"},
		"none installed":  {HolderEvidence{Known: true}, OK, "no holder service"},
		"only an old one": {HolderEvidence{Known: true, Builds: []HolderBuild{{Key: "k1", Raw: "v1", Unit: true, Answers: true}}}, OK, "no holder service"},
		"answers":         {HolderEvidence{Known: true, Builds: []HolderBuild{cur}}, OK, "v2"},
		"down": {HolderEvidence{Known: true, Builds: []HolderBuild{{Key: "k2", Raw: "v2", Current: true, Unit: true}}},
			Fail, "no holder answers"},
		"an old one answers, the current down": {HolderEvidence{Known: true, Builds: []HolderBuild{
			{Key: "k2", Raw: "v2", Current: true, Unit: true}, {Key: "k1", Raw: "v1", Unit: true, Active: true, Answers: true, Terms: 1}}},
			Fail, "current build v2"},
	} {
		got := EvaluateHolder(c.e)
		if got.Status != c.status || !strings.Contains(got.Detail, c.want) {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

// holder-draining: warn while an older build still holds terminals; an older
// one that answers with none is about to exit. The drain's age is the
// current build's (it began when that holder came), the old build's own
// when the current one does not answer; past staleAfter it fails, naming
// the agents still in the old build and what ends the drain
// (agent-resource-limits §3.2).
func TestEvaluateHolderDraining(t *testing.T) {
	cur := HolderBuild{Key: "k2", Raw: "v2", Current: true, Unit: true, Active: true, Answers: true, Since: 2 * time.Hour}
	old := HolderBuild{Key: "k1", Raw: "v1", Unit: true, Active: true, Answers: true, Terms: 2, Since: 26 * time.Hour}
	e := func(bs ...HolderBuild) HolderEvidence { return HolderEvidence{Known: true, Builds: bs} }
	held := map[string][]string{"k1": {"coder", "foureyes"}, "k2": {"master"}}
	got := EvaluateHolderDraining(e(cur, old), 6*time.Hour, held, "demo")
	if got.Status != Warn || !strings.Contains(got.Detail, "build v1: 2 terminal(s), draining for 2h") {
		t.Fatalf("draining: %+v", got)
	}
	cur.Since = 21 * time.Hour
	got = EvaluateHolderDraining(e(cur, old), 6*time.Hour, held, "demo")
	// The commands are the real ones (foureyes, reviewer-codex): holder drain
	// relaunches onto the current build; reload one agent. Never a holder
	// restart, which restarts the current build and kills its agents.
	for _, want := range []string{"build v1: 2 terminal(s), draining for 21h", "past limits.holder_drain_stale_after (6h)",
		"build v1 holds coder, foureyes", "agentbus holder drain demo relaunches them onto the current build",
		"agentbus --project demo reload <agent> for one"} {
		if got.Status != Fail || !strings.Contains(got.Detail, want) {
			t.Fatalf("stale: %+v lacks %q", got, want)
		}
	}
	for _, never := range []string{"relaunch coder", "service restart", "kill-agents"} {
		if strings.Contains(got.Detail, never) {
			t.Fatalf("stale names %q: %+v", never, got)
		}
	}
	if strings.Contains(got.Detail, "master") {
		t.Fatalf("the current build's agent named: %+v", got)
	}
	// Exactly at the limit: stale.
	cur.Since = 6 * time.Hour
	if got := EvaluateHolderDraining(e(cur, old), 6*time.Hour, held, "demo"); got.Status != Fail {
		t.Fatalf("at the limit: %+v", got)
	}
	cur.Since = 6*time.Hour - time.Minute
	if got := EvaluateHolderDraining(e(cur, old), 6*time.Hour, held, "demo"); got.Status != Warn {
		t.Fatalf("below the limit: %+v", got)
	}
	cur.Since = 21 * time.Hour
	// The agents unknown (no snapshot read): still a fail, with the person's command.
	if got := EvaluateHolderDraining(e(cur, old), 6*time.Hour, nil, "demo"); got.Status != Fail || !strings.Contains(got.Detail, "agents unknown") {
		t.Fatalf("no agents: %+v", got)
	}
	// The current build does not answer: the old build's own age.
	cur.Answers = false
	if got := EvaluateHolderDraining(e(cur, old), 48*time.Hour, held, "demo"); got.Status != Warn || !strings.Contains(got.Detail, "draining for 26h") {
		t.Fatalf("no current: %+v", got)
	}
	cur.Answers = true
	old.Terms = 0
	if got := EvaluateHolderDraining(e(cur, old), 6*time.Hour, held, "demo"); got.Status != OK {
		t.Fatalf("an empty old holder: %+v", got)
	}
	if got := EvaluateHolderDraining(HolderEvidence{}, 6*time.Hour, held, "demo"); got.Status != Unknown {
		t.Fatalf("unknown: %+v", got)
	}
}

// holder-leftovers: warn when an older build that no longer runs still has
// its unit or its binary copy (a gc that failed); never the current build,
// never one that runs or answers.
func TestEvaluateHolderLeftovers(t *testing.T) {
	cur := HolderBuild{Key: "k3", Raw: "v3", Current: true, Unit: true, Binary: true, Active: true, Answers: true}
	for name, c := range map[string]struct {
		b      HolderBuild
		status Status
	}{
		"unit left":     {HolderBuild{Key: "k1", Raw: "v1", Unit: true}, Warn},
		"binary left":   {HolderBuild{Key: "k1", Raw: "v1", Binary: true}, Warn},
		"still running": {HolderBuild{Key: "k1", Raw: "v1", Unit: true, Binary: true, Active: true}, OK},
		"still answers": {HolderBuild{Key: "k1", Raw: "v1", Unit: true, Binary: true, Answers: true}, OK},
	} {
		got := EvaluateHolderLeftovers(HolderEvidence{Known: true, Builds: []HolderBuild{cur, c.b}})
		if got.Status != c.status || (c.status == Warn && !strings.Contains(got.Detail, "v1")) {
			t.Errorf("%s: %+v", name, got)
		}
	}
	cur.Active, cur.Answers = false, false // the current build down is holder's business, not a leftover
	if got := EvaluateHolderLeftovers(HolderEvidence{Known: true, Builds: []HolderBuild{cur}}); got.Status != OK {
		t.Fatalf("the current build counted: %+v", got)
	}
}

// holder_restarted (spec 4.4a): a holder that answers came back after the
// last proof of an agent whose process is gone (WithHolderAgents keeps only
// those): the agent died with the old process. Each age on its own clock
// (the socket's on this host, the proof's on Redis's): no clock offset.
func TestEvaluateHolderRestarted(t *testing.T) {
	cur := HolderBuild{Key: "k1", Raw: "v1", Current: true, Unit: true, Answers: true, Terms: 1, Since: 2 * time.Minute}
	old := HolderBuild{Key: "k0", Raw: "v0", Unit: true, Answers: true, Terms: 1, Since: time.Hour}
	gone := HolderBuild{Key: "k7", Raw: "v7", Unit: true} // installed, does not answer
	e := HolderEvidence{Known: true, Builds: []HolderBuild{cur, old, gone}, AgentsKnown: true, Agents: []HolderAgent{
		{Name: "coder", Key: "k1", Term: "t3", ProvedAgo: 10 * time.Minute},     // proved before the restart: lost
		{Name: "foureyes", Key: "k1", Term: "t4", ProvedAgo: 20 * time.Minute},  // lost too
		{Name: "sentinel", Key: "k1", Term: "t5", ProvedAgo: time.Minute},       // proved after the start: not this restart
		{Name: "architect", Key: "k0", Term: "t1", ProvedAgo: 30 * time.Minute}, // its build has run for an hour
		{Name: "docs", Key: "k7", Term: "t1", ProvedAgo: 2 * time.Hour},         // a build that does not answer: the fail's business
	}}
	got := EvaluateHolder(e)
	if got.Status != Warn || !strings.Contains(got.Detail, "restarted") || !strings.Contains(got.Detail, "2 agent(s) lost: coder, foureyes") || !strings.Contains(got.Detail, "v1") ||
		strings.Contains(got.Detail, "docs") || strings.Contains(got.Detail, "architect") || strings.Contains(got.Detail, "sentinel") {
		t.Fatalf("%+v", got)
	}
	// The agents unread: the base judgement alone.
	e.AgentsKnown = false
	if got := EvaluateHolder(e); got.Status != OK {
		t.Fatalf("agents unread: %+v", got)
	}
	// No lost agent: OK.
	e.AgentsKnown, e.Agents = true, e.Agents[2:]
	if got := EvaluateHolder(e); got.Status != OK {
		t.Fatalf("nothing lost: %+v", got)
	}
	// A holder that does not answer stays a fail.
	down := HolderEvidence{Known: true, AgentsKnown: true, Builds: []HolderBuild{{Key: "k1", Raw: "v1", Current: true, Unit: true}},
		Agents: []HolderAgent{{Name: "coder", Key: "k1", Term: "t3", ProvedAgo: time.Hour}}}
	if got := EvaluateHolder(down); got.Status != Fail {
		t.Fatalf("down: %+v", got)
	}
	// An old build that restarted (crashed and came back) counts too.
	old.Since = time.Minute
	e2 := HolderEvidence{Known: true, AgentsKnown: true, Builds: []HolderBuild{cur, old},
		Agents: []HolderAgent{{Name: "architect", Key: "k0", Term: "t1", ProvedAgo: 2 * time.Hour}}}
	if got := EvaluateHolder(e2); got.Status != Warn || !strings.Contains(got.Detail, "v0") || !strings.Contains(got.Detail, "architect") {
		t.Fatalf("old build: %+v", got)
	}
}

// The agents holder_restarted weighs: proved in a holder terminal, their
// process gone, not done (reviewer-codex: a terminal that ended by itself
// was not lost; foureyes: a reused terminal id does not hide a dead one).
func TestWithHolderAgents(t *testing.T) {
	now := time.UnixMilli(10_000_000)
	pane := "v0.29.0.0123456789/t1"
	at := now.Add(-5 * time.Minute).UnixMilli()
	e := WithHolderAgents(HolderEvidence{Known: true}, []HolderProof{
		{Name: "coder", Pane: pane, State: "idle", PID: 7, IdentTS: at},                    // dead: weighed
		{Name: "foureyes", Pane: pane, State: "working", PID: 8, IdentTS: at, Alive: true}, // runs (in the reused t1 or elsewhere): not lost
		{Name: "docs", Pane: pane, State: "done", PID: 9, IdentTS: at},                     // ended by itself
		{Name: "sentinel", Pane: pane, State: "idle", IdentTS: at},                         // never proved
		{Name: "master", Pane: "w1:p2", State: "idle", PID: 10, IdentTS: at},               // a herdr pane
		{Name: "architect", Pane: pane, State: "idle", PID: 11},                            // no proof time
	}, now)
	if !e.AgentsKnown || len(e.Agents) != 1 || e.Agents[0] != (HolderAgent{Name: "coder", Key: "v0.29.0.0123456789", Term: "t1", ProvedAgo: 5 * time.Minute}) {
		t.Fatalf("%+v", e)
	}
}

func TestHolderPane(t *testing.T) {
	for pane, want := range map[string][2]string{
		"v0.29.0.0123456789/t3": {"v0.29.0.0123456789", "t3"},
		"w1:p3":                 {"", ""},
		"":                      {"", ""},
		"a/b/c":                 {"", ""},
		"NOTAKEY/t3":            {"", ""},
		"v0.29.0.0123456789/":   {"", ""},
	} {
		key, term, ok := HolderPane(pane)
		if [2]string{key, term} != want || ok != (want[0] != "") {
			t.Errorf("%q: %q %q %v", pane, key, term, ok)
		}
	}
}
