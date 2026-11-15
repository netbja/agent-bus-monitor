package health

import (
	"strings"
	"testing"
	"time"
)

var imNow = time.Date(2026, 11, 15, 0, 36, 0, 0, time.UTC)

// idleMaster is master idle past 15 minutes, its pane seen idle, with one
// approved head not pushed: a warn.
func idleMaster() IdleMasterInput {
	return IdleMasterInput{
		Now: imNow, After: 15 * time.Minute, Master: "master",
		Agent: &StallAgent{Name: "master", State: "idle", DeclaredAt: imNow.Add(-22 * time.Minute), Pane: PaneIdle},
		Heads: []MasterHead{{Subject: "herdr-6c2-2", Head: "fa2d796f0123456789abcdef0123456789abcdef"}},
	}
}

// §7, one case per rule.
func TestEvaluateIdleMaster(t *testing.T) {
	cb := func(due time.Duration) *MasterCheckback {
		return &MasterCheckback{What: "k1/k2 checks", DueMs: imNow.Add(-due).UnixMilli(), SetID: 1, FirstSetID: 1, Count: 1}
	}
	cases := []struct {
		name   string
		mut    func(*IdleMasterInput)
		status Status
		detail string
	}{
		{"an approved head not pushed", func(*IdleMasterInput) {}, Warn, "master idle 22m; approved, not pushed: herdr-6c2-2 (fa2d796f)"},
		{"a request for master", func(in *IdleMasterInput) { in.Heads, in.Requests = nil, []string{"release-v0-30"} }, Warn, "master idle 22m; requested: release-v0-30"},
		{"a check-back due", func(in *IdleMasterInput) { in.Heads, in.Checkback = nil, cb(3*time.Hour) }, Warn, "master idle 22m; check-back due 3h ago: k1/k2 checks"},
		{"a check-back not yet due is not an item", func(in *IdleMasterInput) { in.Heads, in.Checkback = nil, cb(-time.Minute) }, OK, "nothing in master's hand"},
		{"done counts as idle", func(in *IdleMasterInput) { in.Agent.State = "done" }, Warn, "master idle 22m; approved, not pushed: herdr-6c2-2 (fa2d796f)"},
		{"working", func(in *IdleMasterInput) { in.Agent.State = "working" }, OK, "master working"},
		{"blocked", func(in *IdleMasterInput) { in.Agent.State = "blocked" }, OK, "master blocked"},
		{"an open waiting", func(in *IdleMasterInput) { in.Agent.State, in.OpenAsks = "waiting", 1 }, OK, "master waits for you (1 ask open)"},
		{"an answered waiting counts from the answer", func(in *IdleMasterInput) {
			in.Agent.AnsweredWait, in.Agent.DeclaredAt = true, imNow.Add(-6*time.Minute)
		}, OK, "master idle 6m"},
		{"idle under N", func(in *IdleMasterInput) { in.Agent.DeclaredAt = imNow.Add(-6 * time.Minute) }, OK, "master idle 6m"},
		{"a busy pane", func(in *IdleMasterInput) { in.Agent.Pane = PaneBusy }, OK, "master busy (not declared)"},
		{"an unseen pane", func(in *IdleMasterInput) { in.Agent.Pane = PaneUnseen }, Unknown, "cannot see master's pane (no fresh observation)"},
		{"an obligation", func(in *IdleMasterInput) { in.Obligation = true }, OK, "master has messages; the dispatcher wakes it"},
		{"a pause", func(in *IdleMasterInput) { in.Paused = true }, OK, "project paused"},
		{"a hold", func(in *IdleMasterInput) { in.Held = true }, OK, "master held by its provider's limit"},
		{"not registered", func(in *IdleMasterInput) { in.Agent = nil }, OK, "master is not registered"},
		{"nothing in hand", func(in *IdleMasterInput) { in.Heads = nil }, OK, "nothing in master's hand"},
		{"a stale merge queue alone is unknown", func(in *IdleMasterInput) { in.Heads, in.QueueUnknown = nil, "merge queue not read since 00:02" }, Unknown, "merge queue not read since 00:02"},
		{"a stale merge queue is named beside other items", func(in *IdleMasterInput) {
			in.Heads, in.Requests, in.QueueUnknown = nil, []string{"t1"}, "merge queue not read since 00:02"
		}, Warn, "master idle 22m; requested: t1; merge queue not read since 00:02"},
		{"a failed read", func(in *IdleMasterInput) { in.ReadErr = "board unavailable" }, Unknown, "board unavailable"},
		{"off", func(in *IdleMasterInput) { in.After = 0 }, Unknown, "off (master_idle_after = 0)"},
		{"three items, then +N", func(in *IdleMasterInput) {
			in.Requests = []string{"a", "b", "c"}
		}, Warn, "master idle 22m; approved, not pushed: herdr-6c2-2 (fa2d796f); requested: a; requested: b; +1"},
		{"the wake's time unknown", func(in *IdleMasterInput) { in.WokeEpisode = IdleMasterEpisode(*in) }, Warn,
			"master idle 22m; approved, not pushed: herdr-6c2-2 (fa2d796f); wake time unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := idleMaster()
			c.mut(&in)
			got := EvaluateIdleMaster(in)
			if got.Status != c.status || got.Detail != c.detail {
				t.Fatalf("= %s %q, want %s %q", got.Status, got.Detail, c.status, c.detail)
			}
		})
	}
}

// §7, the 2026-11-15 replay: master's waiting answered at 00:21, fa2d796f
// approved and not on origin, N = 15m: it warns at 00:36 and wakes master
// once; the same input at 00:37, the wake marked, wakes nothing.
func TestIdleMasterReplay(t *testing.T) {
	answered := time.Date(2026, 11, 15, 0, 21, 0, 0, time.UTC)
	in := idleMaster()
	in.Agent.DeclaredAt, in.Agent.AnsweredWait = answered, true
	in.Now = answered.Add(14 * time.Minute)
	if got := EvaluateIdleMaster(in); got.Status != OK {
		t.Fatalf("00:35 = %s %q, want ok", got.Status, got.Detail)
	}
	in.Now = answered.Add(15 * time.Minute)
	act := IdleMasterActs(in)
	if act.Wake == nil || act.Person != nil {
		t.Fatalf("00:36 = %+v, want one wake", act)
	}
	if act.Wake.Mark != StallMasterMark || act.Wake.Target != "master" || act.Wake.Episode != IdleMasterEpisode(in) ||
		!strings.Contains(act.Wake.Text, "approved, not pushed: herdr-6c2-2 (fa2d796f)") {
		t.Fatalf("wake = %+v", act.Wake)
	}
	in.Now, in.WokeEpisode, in.WokeAt = in.Now.Add(time.Minute), act.Wake.Episode, in.Now.UnixMilli()
	if act := IdleMasterActs(in); act.Wake != nil || act.Person != nil {
		t.Fatalf("00:37 = %+v, want nothing", act)
	}
}

// §7, episodes: a new item, or one gone, is a new episode; the same items in
// another order are the same one.
func TestIdleMasterEpisode(t *testing.T) {
	in := idleMaster()
	in.Requests = []string{"a", "b"}
	ep := IdleMasterEpisode(in)
	if len(ep) != 16 {
		t.Fatalf("episode %q: want 8 bytes in hex", ep)
	}
	in.Requests = []string{"b", "a"}
	if IdleMasterEpisode(in) != ep {
		t.Fatal("another order changed the episode")
	}
	in.Requests = []string{"a", "b", "c"}
	if IdleMasterEpisode(in) == ep {
		t.Fatal("a new item kept the episode")
	}
	in.Requests = []string{"a"}
	if IdleMasterEpisode(in) == ep {
		t.Fatal("an item gone kept the episode")
	}
}

// §7, second tier: no wake marked, a wake; woken N ago with the same
// episode, one person notice; woken one minute ago, nothing; the wake's
// time unknown, nothing; master cannot act, a notice at once and no wake.
func TestIdleMasterSecondTier(t *testing.T) {
	in := idleMaster()
	ep := IdleMasterEpisode(in)
	in.WokeEpisode, in.WokeAt = "another", imNow.Add(-time.Hour).UnixMilli()
	if act := IdleMasterActs(in); act.Wake == nil || act.Person != nil {
		t.Fatalf("another episode marked = %+v, want a wake", act)
	}
	in.WokeEpisode, in.WokeAt = ep, imNow.Add(-25*time.Minute).UnixMilli()
	act := IdleMasterActs(in)
	if act.Wake != nil || act.Person == nil || act.Person.Mark != StallMasterPersonMark || act.Person.Episode != ep {
		t.Fatalf("woken 25m ago = %+v, want one person notice", act)
	}
	if want := "Stall guard: master was woken 25m ago for: approved, not pushed: herdr-6c2-2 (fa2d796f). It is idle again and nothing moved. Look at master's pane, or tell it in busmon."; act.Person.Text != want {
		t.Fatalf("notice = %q\nwant %q", act.Person.Text, want)
	}
	in.WokeAt = imNow.Add(-time.Minute).UnixMilli()
	if act := IdleMasterActs(in); act.Wake != nil || act.Person != nil {
		t.Fatalf("woken 1m ago = %+v, want nothing", act)
	}
	in.WokeAt = 0
	if act := IdleMasterActs(in); act.Wake != nil || act.Person != nil {
		t.Fatalf("wake time unknown = %+v, want nothing", act)
	}
	in.WokeEpisode, in.CannotAct = "", "master's own tab is blocked"
	act = IdleMasterActs(in)
	if act.Wake != nil || act.Person == nil || !strings.Contains(act.Person.Text, "cannot act (master's own tab is blocked)") {
		t.Fatalf("cannot act = %+v, want a notice and no wake", act)
	}
	in.CannotAct = ""
	in.Agent.State = "working"
	if act := IdleMasterActs(in); act.Wake != nil || act.Person != nil {
		t.Fatalf("no warn = %+v, want nothing", act)
	}
}

// §4: the same what set more than checkback_max times tells the person
// once (the episode is the agent, the what and the count's first set id);
// at or under the max, or with max 0, nothing.
func TestCheckbackPostponed(t *testing.T) {
	first := time.Date(2026, 11, 15, 0, 21, 0, 0, time.Local).UnixMilli()
	in := idleMaster()
	in.CheckbackMax = 3
	in.Checkback = &MasterCheckback{What: "k1/k2 checks", DueMs: imNow.Add(time.Hour).UnixMilli(), SetID: first + 5, FirstSetID: first, Count: 3}
	if n := CheckbackPostponed(in); n != nil {
		t.Fatalf("count 3 = %+v, want nothing", n)
	}
	in.Checkback.Count = 4
	n := CheckbackPostponed(in)
	if n == nil || n.Mark != CheckbackPersonMark || n.Text != `Stall guard: master has postponed "k1/k2 checks" 4 times since 00:21.` {
		t.Fatalf("count 4 = %+v", n)
	}
	in.Checkback.Count = 5
	if n5 := CheckbackPostponed(in); n5 == nil || n5.Episode != n.Episode {
		t.Fatalf("count 5 = %+v, want the same episode as 4 (one notice)", n5)
	}
	in.Checkback.FirstSetID = first + 9
	if n6 := CheckbackPostponed(in); n6 == nil || n6.Episode == n.Episode {
		t.Fatal("a new count kept the episode")
	}
	in.CheckbackMax = 0
	if n := CheckbackPostponed(in); n != nil {
		t.Fatalf("max 0 = %+v, want never", n)
	}
	in.CheckbackMax, in.Checkback = 3, nil
	if n := CheckbackPostponed(in); n != nil {
		t.Fatalf("no check-back = %+v", n)
	}
}
