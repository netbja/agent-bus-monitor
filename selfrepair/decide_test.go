package selfrepair

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/relaunch"
)

var t0 = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

// dead is an agent dead for 2 minutes on 5 reads, every precondition met.
func dead(name string) Agent {
	return Agent{Name: name, Life: relaunch.LifeDead, DeadSince: t0.Add(-2 * time.Minute), DeadReads: 5,
		Tab: relaunch.VerdictRelaunch, Session: "58b5f18b-aaaa-bbbb-cccc-000000000001", Resumable: true, ProviderFresh: true}
}

func facts(agents ...Agent) Facts {
	return Facts{Now: t0, Mode: ModeShadow, HerdrUp: true, WorkspaceOpen: true,
		Grace: 60 * time.Second, Every: 30 * time.Minute, PerDay: 3, Agents: agents}
}

func one(t *testing.T, f Facts) Decision {
	t.Helper()
	d := Decide(f)
	if len(d) != len(f.Agents) {
		t.Fatalf("%d decisions for %d agents", len(d), len(f.Agents))
	}
	return d[0]
}

// R1 and R2 (spec 2026-11-09-self-repair-watcher-design.md 4.1, 4.2): each
// precondition alone stops the relaunch, with its reason.
func TestDecideOneAgent(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Facts, *Agent)
		kind   Kind
		reason string
	}{
		{"every precondition met", func(*Facts, *Agent) {}, KindRelaunch, "dead 2m"},
		{"alive", func(_ *Facts, a *Agent) { a.Life = relaunch.LifeAlive }, KindNone, "alive"},
		{"unknown never acts", func(_ *Facts, a *Agent) { a.Life = relaunch.LifeUnknown }, KindUnknown, "no process identity"},
		{"off", func(f *Facts, _ *Agent) { f.Mode = ModeOff }, KindNone, "self-repair off"},
		{"within the grace", func(_ *Facts, a *Agent) { a.DeadSince = t0.Add(-30 * time.Second) }, KindWait, "grace"},
		{"too few reads", func(_ *Facts, a *Agent) { a.DeadReads = 2 }, KindWait, "grace"},
		{"a retired pop", func(_ *Facts, a *Agent) { a.Pop, a.Retired = true, true }, KindSkip, "pop"},
		{"an intent held", func(_ *Facts, a *Agent) { a.Intent = "reload" }, KindSkip, "reload in progress"},
		{"paused", func(f *Facts, _ *Agent) { f.Paused = true }, KindSkip, "paused"},
		{"suspended", func(f *Facts, _ *Agent) { f.Suspended = true }, KindSkip, "suspended"},
		{"a hand-off clear in flight", func(_ *Facts, a *Agent) { a.HandoffClear = true }, KindSkip, "hand-off"},
		{"its relaunch failed its proof", func(_ *Facts, a *Agent) { a.ProofFailed = true }, KindFailed, "no retry for this death"},
		{"a failed proof waits on a declared departure first", func(_ *Facts, a *Agent) { a.ProofFailed, a.Intent = true, "retire" }, KindSkip, "retire"},
		{"tab busy", func(_ *Facts, a *Agent) { a.Tab = relaunch.VerdictBusy }, KindWait, "tab"},
		{"tab unknown", func(_ *Facts, a *Agent) { a.Tab = relaunch.VerdictUnknown }, KindWait, "tab"},
		{"tab still shows the client", func(_ *Facts, a *Agent) { a.Tab = relaunch.VerdictAlive }, KindWait, "tab"},
		{"tab runs something else", func(_ *Facts, a *Agent) { a.Tab = relaunch.VerdictRunning }, KindWait, "tab"},
		{"an optional tab missing", func(_ *Facts, a *Agent) { a.Tab = relaunch.VerdictMissing }, KindReport, "tab missing"},
		{"a required tab missing (R2)", func(_ *Facts, a *Agent) { a.Tab, a.Required = relaunch.VerdictCreate, true }, KindCreate, "tab missing"},
		{"herdr down", func(f *Facts, _ *Agent) { f.HerdrUp = false }, KindHold, "herdr"},
		{"workspace closed", func(f *Facts, _ *Agent) { f.WorkspaceOpen = false }, KindHold, "workspace"},
		{"provider not fresh", func(_ *Facts, a *Agent) { a.ProviderFresh = false }, KindHold, "provider"},
		{"provider held", func(_ *Facts, a *Agent) { a.ProviderHeld = true }, KindHold, "quota"},
		{"on a quota fallback", func(_ *Facts, a *Agent) { a.Fallback = "kimi" }, KindReport, "fallback profile kimi"},
		{"on a quota fallback, its own provider fresh again", func(_ *Facts, a *Agent) { a.Fallback, a.ProviderHeld = "kimi", false }, KindReport, "swap"},
		{"no proved session", func(_ *Facts, a *Agent) { a.Session = "" }, KindReport, "no session"},
		{"a client whose resume is not verified", func(_ *Facts, a *Agent) { a.Resumable = false }, KindReport, "resume"},
		{"a crash loop", func(_ *Facts, a *Agent) { a.CrashLoop = true }, KindBudget, "crash loop"},
		{"the day's budget spent", func(_ *Facts, a *Agent) {
			a.Relaunches = []time.Time{t0.Add(-20 * time.Hour), t0.Add(-10 * time.Hour), t0.Add(-2 * time.Hour)}
		}, KindBudget, "3 relaunches in 24 h"},
		{"a relaunch older than a day does not count", func(_ *Facts, a *Agent) {
			a.Relaunches = []time.Time{t0.Add(-30 * time.Hour), t0.Add(-25 * time.Hour), t0.Add(-2 * time.Hour)}
		}, KindRelaunch, "dead"},
		{"too soon after the last relaunch", func(_ *Facts, a *Agent) { a.Relaunches = []time.Time{t0.Add(-10 * time.Minute)} }, KindWait, "budget"},
		{"just past the every", func(_ *Facts, a *Agent) { a.Relaunches = []time.Time{t0.Add(-30 * time.Minute)} }, KindRelaunch, "dead"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := dead("coder")
			f := facts()
			c.change(&f, &a)
			f.Agents = []Agent{a}
			d := one(t, f)
			if d.Kind != c.kind || !strings.Contains(d.Reason, c.reason) || d.Agent != "coder" {
				t.Fatalf("%s %q; want %s ~%q", d.Kind, d.Reason, c.kind, c.reason)
			}
			if d.Act != (c.kind == KindRelaunch || c.kind == KindCreate) {
				t.Fatalf("act %v for %s", d.Act, d.Kind)
			}
		})
	}
}

// One act per pass: master first, then the oldest death; the others wait.
// No act while one is in flight or within 2 minutes of the last.
func TestDecideOneAtATime(t *testing.T) {
	older := dead("coder")
	older.DeadSince = t0.Add(-10 * time.Minute)
	newer := dead("foureyes")
	master := dead("master")
	master.Master = true
	got := Decide(facts(older, newer, master))
	acts := map[string]bool{}
	for _, d := range got {
		if d.Act {
			acts[d.Agent] = true
		}
	}
	if len(acts) != 1 || !acts["master"] {
		t.Fatalf("acts %v: master first", acts)
	}
	got = Decide(facts(newer, older))
	if !got[1].Act || got[0].Act || got[0].Kind != KindWait || !strings.Contains(got[0].Reason, "after coder") {
		t.Fatalf("oldest first: %+v", got)
	}
	f := facts(older)
	f.InFlight = true
	if d := one(t, f); d.Act || d.Kind != KindWait || !strings.Contains(d.Reason, "in flight") {
		t.Fatalf("in flight: %+v", d)
	}
	f = facts(older)
	f.LastRelaunch = t0.Add(-90 * time.Second)
	if d := one(t, f); d.Act || !strings.Contains(d.Reason, "pause") {
		t.Fatalf("within the project pause: %+v", d)
	}
	f.LastRelaunch = t0.Add(-2 * time.Minute)
	if d := one(t, f); !d.Act {
		t.Fatalf("after the pause: %+v", d)
	}
	// A candidate held back still says what it is: its kind stays, its act does not.
	f = facts(dead("a"), dead("b"))
	got = Decide(f)
	if got[0].Kind != KindRelaunch || got[1].Kind != KindWait {
		t.Fatalf("%+v", got)
	}
}

// Health (spec 5): off and nothing to do are ok; shadow with a would-be
// action, a hold or a tab not settling warn; a crash loop fails. Shadow
// never fails: it acts on nothing, so it spends no budget.
func TestHealth(t *testing.T) {
	cases := []struct {
		name  string
		mode  Mode
		ds    []Decision
		want  health.Status
		words string
	}{
		{"off", ModeOff, []Decision{{Agent: "a", Kind: KindNone, Reason: "self-repair off"}}, health.OK, "off"},
		{"nothing dead", ModeShadow, []Decision{{Agent: "a", Kind: KindNone, Reason: "alive"}}, health.OK, "nothing to repair"},
		{"a grace wait is fine", ModeShadow, []Decision{{Agent: "a", Kind: KindWait, Reason: "dead 20s, grace 60s"}}, health.OK, "nothing to repair"},
		{"shadow would relaunch", ModeShadow, []Decision{{Agent: "coder", Kind: KindRelaunch, Act: true, Reason: "dead 2m"}}, health.Warn, "shadow: would relaunch coder"},
		{"shadow would create", ModeShadow, []Decision{{Agent: "coder", Kind: KindCreate, Act: true, Reason: "tab missing"}}, health.Warn, "would recreate coder's tab"},
		{"held", ModeShadow, []Decision{{Agent: "coder", Kind: KindHold, Reason: "provider anthropic unreachable"}}, health.Warn, "held: coder"},
		{"a tab not settling", ModeShadow, []Decision{{Agent: "coder", Kind: KindWait, Reason: "tab skipped (busy): waited on"}}, health.Warn, "coder"},
		{"a crash loop", ModeOn, []Decision{{Agent: "coder", Kind: KindBudget, Reason: "crash loop"}}, health.Fail, "coder"},
		{"a failed proof", ModeOn, []Decision{{Agent: "coder", Kind: KindFailed, Reason: "its relaunch failed its proof"}}, health.Fail, "coder: its relaunch failed"},
		{"a report only", ModeShadow, []Decision{{Agent: "grok", Kind: KindReport, Reason: "no resume"}}, health.OK, "reported: grok"},
	}
	for _, c := range cases {
		out := Health(c.mode, c.ds)
		if out.Status != c.want || !strings.Contains(out.Detail, c.words) {
			t.Errorf("%s: %+v", c.name, out)
		}
	}
	if out := Health(ModeShadow, []Decision{{Agent: "coder", Kind: KindBudget, Reason: "crash loop"}}); out.Status == health.Fail {
		t.Errorf("shadow failed: %+v", out)
	}
}

// The check is in the catalogue (spec 5), measured every minute, its
// problems in master's hands: sentinel only annotates a fail and tells
// master, it never retries.
func TestHealthDefinition(t *testing.T) {
	d, ok := health.ByID("self-repair")
	if !ok || d.Handler != "master" || d.Interval != time.Minute || len(d.Person) != 0 {
		t.Fatalf("definition %+v %v", d, ok)
	}
	if d.Place(health.Fail) == health.PlaceForYou || !strings.Contains(d.Remediation, "Shadow mode (development mode only) acts on nothing") {
		t.Fatalf("definition %+v", d)
	}
}
