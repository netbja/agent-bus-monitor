package notify

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
)

var (
	now0    = time.Date(2026, 11, 3, 14, 0, 0, 0, time.Local)
	phone   = Target{Name: "phone", Kind: "ntfy", Topic: "agentbus-0123456789", Server: DefaultServer}
	home    = Target{Name: "home", Kind: "ntfy", Topic: "agentbus-0123456789", Server: "https://n.example", SelfHosted: true}
	team    = Target{Name: "team", Kind: "discord", Webhook: "https://discord.com/api/webhooks/1/x"}
	defSets = Settings{Events: []string{"decision", "stall", "fail", "blocked", "prompt"}, Detail: "summary",
		Delay: 2 * time.Minute, MaxPerHour: 6, QuietExempt: []string{"fail", "stall"}}
)

func ask(id string, at time.Time) Event {
	return Event{Kind: "decision", Episode: "ask:" + id, At: at, Line: "master asks a question (2) — busmon !", Detail: "Merge PR 281 at /home/u/x/y?"}
}

func fail(check string, at time.Time) Event {
	return Event{Kind: "fail", Episode: check + "|" + fmt.Sprint(at.UnixMilli()), At: at, Line: check + " fails — busmon F5"}
}

// baselined is the record after a first pass with targets over events.
func baselined(events []Event, targets ...Target) map[string]string {
	rec := map[string]string{}
	for _, t := range targets {
		rec["_target|"+t.Name] = "1"
		for _, e := range events {
			rec[e.Key()+"|"+t.Name] = "baseline"
		}
	}
	return rec
}

func targetPlan(p Plan, name string) TargetPlan {
	for _, tp := range p.Targets {
		if tp.Target.Name == name {
			return tp
		}
	}
	return TargetPlan{}
}

func TestFirstPassTakesABaselineAndSendsNothing(t *testing.T) {
	standing := []Event{ask("1-0", now0.Add(-time.Hour)), fail("disk", now0.Add(-48*time.Hour)),
		{Kind: "for-you", Episode: "backup|1", At: now0.Add(-time.Hour), Line: "backup"}} // a kind not sent is baselined too
	p := Decide(Pass{Project: "demo", Now: now0, Events: standing, Targets: []Target{phone}}, defSets)
	tp := targetPlan(p, "phone")
	if tp.Message != nil || !tp.TakeBaseline {
		t.Fatalf("plan %+v", tp)
	}
	want := []string{"decision|ask:1-0|phone", fail("disk", now0.Add(-48*time.Hour)).Key() + "|phone", "for-you|backup|1|phone"}
	if !sameSet(tp.Baseline, want) {
		t.Fatalf("baseline %v, want %v", tp.Baseline, want)
	}
}

func TestANewEpisodeAfterTheBaselineIsSent(t *testing.T) {
	old := ask("1-0", now0.Add(-time.Hour))
	rec := baselined([]Event{old}, phone)
	fresh := ask("2-0", now0.Add(-3*time.Minute))
	p := Decide(Pass{Project: "demo", Now: now0, Events: []Event{old, fresh}, Targets: []Target{phone}, Records: rec}, defSets)
	tp := targetPlan(p, "phone")
	if tp.Message == nil || len(tp.Message.Lines) != 1 || !slices.Equal(tp.Fields, []string{"decision|ask:2-0|phone"}) || !tp.Counted {
		t.Fatalf("plan %+v", tp)
	}
	if tp.Message.Title != "demo · 1 waiting for you" || tp.Message.Priority != Default {
		t.Fatalf("message %+v", tp.Message)
	}
	// Recorded: the next pass sends nothing more.
	rec["decision|ask:2-0|phone"] = "t"
	p = Decide(Pass{Project: "demo", Now: now0.Add(time.Minute), Events: []Event{old, fresh}, Targets: []Target{phone}, Records: rec}, defSets)
	if targetPlan(p, "phone").Message != nil {
		t.Fatal("sent twice")
	}
}

// A field whose fact still holds is never pruned by age: a week-long fail
// is sent once (a fake clock 8 days on sends nothing).
func TestAStandingFactIsNeverSentAgain(t *testing.T) {
	f := fail("disk", now0)
	rec := baselined(nil, phone)
	rec[f.Key()+"|phone"] = "t"
	p := Decide(Pass{Project: "demo", Now: now0.Add(8 * 24 * time.Hour), Events: []Event{f}, Targets: []Target{phone}, Records: rec}, defSets)
	if targetPlan(p, "phone").Message != nil || len(p.Delete) != 0 {
		t.Fatalf("plan %+v", p)
	}
}

func TestAFieldWhoseFactIsGoneIsDeleted(t *testing.T) {
	rec := baselined([]Event{ask("1-0", now0)}, phone)
	p := Decide(Pass{Project: "demo", Now: now0, Targets: []Target{phone}, Records: rec}, defSets)
	if !slices.Equal(p.Delete, []string{"decision|ask:1-0|phone"}) {
		t.Fatalf("delete %v", p.Delete)
	}
}

// A plan task waiting, then not, then waiting again is two stretches: sent
// twice (the caller gives each stretch its own episode; the gone one is
// pruned in between).
func TestAFactThatComesBackIsSentAgain(t *testing.T) {
	task := Event{Kind: "decision", Episode: "plan:t1", At: now0.Add(-5 * time.Minute), Line: "plan task t1 waits"}
	rec := baselined(nil, phone)
	rec[task.Key()+"|phone"] = "t"
	p := Decide(Pass{Project: "demo", Now: now0, Targets: []Target{phone}, Records: rec}, defSets)
	for _, f := range p.Delete {
		delete(rec, f)
	}
	task.At = now0.Add(5 * time.Minute)
	p = Decide(Pass{Project: "demo", Now: now0.Add(10 * time.Minute), Events: []Event{task}, Targets: []Target{phone}, Records: rec}, defSets)
	if targetPlan(p, "phone").Message == nil {
		t.Fatal("the second stretch was not sent")
	}
}

func TestATargetAddedLaterGetsNoHistory(t *testing.T) {
	a := ask("1-0", now0.Add(-time.Hour))
	rec := baselined([]Event{a}, phone)
	p := Decide(Pass{Project: "demo", Now: now0, Events: []Event{a}, Targets: []Target{phone, team}, Records: rec}, defSets)
	if tp := targetPlan(p, "team"); tp.Message != nil || !tp.TakeBaseline || len(tp.Baseline) != 1 {
		t.Fatalf("team %+v", tp)
	}
	if tp := targetPlan(p, "phone"); tp.Message != nil || tp.TakeBaseline {
		t.Fatalf("phone %+v", tp)
	}
}

// A target that leaves notify.targets loses its fields, so coming back
// takes a new baseline.
func TestATargetRemovedLosesItsFields(t *testing.T) {
	a := ask("1-0", now0)
	rec := baselined([]Event{a}, phone, team)
	p := Decide(Pass{Project: "demo", Now: now0, Events: []Event{a}, Targets: []Target{phone}, Records: rec}, defSets)
	if !sameSet(p.Delete, []string{"_target|team", "decision|ask:1-0|team"}) {
		t.Fatalf("delete %v", p.Delete)
	}
}

// With two targets, one that failed has no field: only it is sent again.
func TestOnlyTheTargetWithoutItsFieldIsSent(t *testing.T) {
	a := ask("2-0", now0.Add(-5*time.Minute))
	rec := baselined(nil, phone, team)
	rec[a.Key()+"|phone"] = "t"
	p := Decide(Pass{Project: "demo", Now: now0, Events: []Event{a}, Targets: []Target{phone, team}, Records: rec}, defSets)
	if targetPlan(p, "phone").Message != nil || targetPlan(p, "team").Message == nil {
		t.Fatalf("plan %+v", p)
	}
}

func TestTheDelayHoldsAnEvent(t *testing.T) {
	rec := baselined(nil, phone)
	young := ask("2-0", now0.Add(-time.Minute))
	p := Decide(Pass{Project: "demo", Now: now0, Events: []Event{young}, Targets: []Target{phone}, Records: rec}, defSets)
	if targetPlan(p, "phone").Message != nil {
		t.Fatal("sent before the delay")
	}
	s := defSets
	s.Delay = 0
	if targetPlan(Decide(Pass{Project: "demo", Now: now0, Events: []Event{young}, Targets: []Target{phone}, Records: rec}, s), "phone").Message == nil {
		t.Fatal("delay 0 holds")
	}
}

func TestOnlyTheConfiguredKindsAreSent(t *testing.T) {
	rec := baselined(nil, phone)
	fy := Event{Kind: "for-you", Episode: "backup|1", At: now0.Add(-time.Hour), Line: "backup"}
	p := Decide(Pass{Project: "demo", Now: now0, Events: []Event{fy}, Targets: []Target{phone}, Records: rec}, defSets)
	if targetPlan(p, "phone").Message != nil || len(p.Delete) != 0 {
		t.Fatalf("a for-you warn is off by default: %+v", p)
	}
	s := defSets
	s.Events = append(s.Events, "for-you")
	tp := targetPlan(Decide(Pass{Project: "demo", Now: now0, Events: []Event{fy}, Targets: []Target{phone}, Records: rec}, s), "phone")
	if tp.Message == nil || tp.Message.Priority != Low {
		t.Fatalf("plan %+v", tp)
	}
}

func TestElevenEventsAreOneMessage(t *testing.T) {
	rec := baselined(nil, phone)
	var evs []Event
	for i := 0; i < 11; i++ {
		evs = append(evs, ask(fmt.Sprintf("%d-0", i+2), now0.Add(-5*time.Minute)))
	}
	tp := targetPlan(Decide(Pass{Project: "demo", Now: now0, Events: evs, Targets: []Target{phone}, Records: rec}, defSets), "phone")
	if tp.Message == nil || len(tp.Message.Lines) != 11 || tp.Message.Lines[10] != "and 1 more: open busmon" || len(tp.Fields) != 11 {
		t.Fatalf("plan %+v", tp)
	}
	if tp.Message.Title != "demo · 11 waiting for you" {
		t.Fatalf("title %q", tp.Message.Title)
	}
}

func TestTheHourlyLimitHoldsAllButFailAndStall(t *testing.T) {
	rec := baselined(nil, phone)
	a := ask("2-0", now0.Add(-5*time.Minute))
	f := fail("disk", now0.Add(-5*time.Minute))
	full := func(string) int { return 6 }
	tp := targetPlan(Decide(Pass{Project: "demo", Now: now0, Events: []Event{a, f}, Targets: []Target{phone}, Records: rec, SentInHour: full}, defSets), "phone")
	if tp.Message == nil || !slices.Equal(tp.Fields, []string{f.Key() + "|phone"}) || tp.Counted || tp.Message.Priority != High {
		t.Fatalf("plan %+v", tp)
	}
	five := func(string) int { return 5 }
	tp = targetPlan(Decide(Pass{Project: "demo", Now: now0, Events: []Event{a, f}, Targets: []Target{phone}, Records: rec, SentInHour: five}, defSets), "phone")
	if len(tp.Fields) != 2 || !tp.Counted {
		t.Fatalf("under the limit: %+v", tp)
	}
}

func TestQuietHoursHoldAllButTheExempt(t *testing.T) {
	q, _ := config.ParseQuiet("22:00-07:30")
	s := defSets
	s.Quiet = &q
	night := time.Date(2026, 11, 3, 23, 0, 0, 0, time.Local)
	rec := baselined(nil, phone)
	a := ask("2-0", night.Add(-5*time.Minute))
	f := fail("disk", night.Add(-5*time.Minute))
	tp := targetPlan(Decide(Pass{Project: "demo", Now: night, Events: []Event{a, f}, Targets: []Target{phone}, Records: rec}, s), "phone")
	if !slices.Equal(tp.Fields, []string{f.Key() + "|phone"}) {
		t.Fatalf("night %+v", tp)
	}
	rec[f.Key()+"|phone"] = "t"
	morning := time.Date(2026, 11, 4, 7, 31, 0, 0, time.Local)
	tp = targetPlan(Decide(Pass{Project: "demo", Now: morning, Events: []Event{a, f}, Targets: []Target{phone}, Records: rec}, s), "phone")
	if tp.Message == nil || len(tp.Message.Lines) != 1 || !strings.HasSuffix(tp.Message.Lines[0], "(during quiet hours)") {
		t.Fatalf("morning %+v", tp)
	}
	s.QuietExempt = nil // nothing exempt: a fail waits too
	delete(rec, f.Key()+"|phone")
	if tp := targetPlan(Decide(Pass{Project: "demo", Now: night, Events: []Event{f}, Targets: []Target{phone}, Records: rec}, s), "phone"); tp.Message != nil {
		t.Fatalf("quiet_exempt = []: %+v", tp)
	}
}

func TestAPausedProjectSendsOnlyAFail(t *testing.T) {
	rec := baselined(nil, phone)
	a := ask("2-0", now0.Add(-5*time.Minute))
	st := Event{Kind: "stall", Episode: "1", At: now0.Add(-5 * time.Minute), Line: "team stall"}
	f := fail("disk", now0.Add(-5*time.Minute))
	tp := targetPlan(Decide(Pass{Project: "demo", Now: now0, Events: []Event{a, st, f}, Paused: true, Targets: []Target{phone}, Records: rec}, defSets), "phone")
	if !slices.Equal(tp.Fields, []string{f.Key() + "|phone"}) {
		t.Fatalf("paused %+v", tp)
	}
}

// summary carries no agent text; full carries it scrubbed, and only to a
// private target (the person's answer 5): ntfy.sh and Discord stay summary.
func TestDetailPerTarget(t *testing.T) {
	a := ask("2-0", now0.Add(-5*time.Minute))
	a.Detail = "pw-SECRET at https://x.example/a in /home/u/backups"
	rec := baselined(nil, phone, home, team)
	desk := Target{Name: "here", Kind: "desktop"}
	rec["_target|here"] = "1"
	in := Pass{Project: "demo", Now: now0, Events: []Event{a}, Targets: []Target{phone, home, team, desk}, Records: rec}
	for _, tp := range Decide(in, defSets).Targets {
		if strings.Contains(strings.Join(tp.Message.Lines, " "), "pw-SECRET") {
			t.Fatalf("summary sent agent text to %s", tp.Target.Name)
		}
	}
	s := defSets
	s.Detail = "full"
	for _, tp := range Decide(in, s).Targets {
		body := strings.Join(tp.Message.Lines, " ")
		private := tp.Target.Name == "home" || tp.Target.Name == "here"
		if private != strings.Contains(body, "pw-SECRET at [url] in [path]") {
			t.Errorf("%s: %q", tp.Target.Name, body)
		}
		if strings.Contains(body, "https://") || strings.Contains(body, "/home/") {
			t.Errorf("%s leaks: %q", tp.Target.Name, body)
		}
	}
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
