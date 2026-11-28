package attention

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

var t0 = time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

func at(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

func keys(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Key)
	}
	return out
}

func q(answered bool) bus.AskQuestionState {
	s := bus.AskQuestionState{AskQuestion: bus.AskQuestion{ID: "q", Header: "q", Question: "q?"}}
	if answered {
		s.Answer = &bus.AskAnswer{Choice: "a"}
	}
	return s
}

// status-summary §3.2: each broker-only rank, in For you's order, oldest
// first within a rank; what is answered, closed, stale or another's process
// is no item.
func TestItemsRanksAndOrders(t *testing.T) {
	now := at(60)
	blocked := Observe(bus.Observation{State: "blocked", PID: 7, At: at(59).UnixMilli(), Since: at(50).UnixMilli()}, true, 7)
	in := Inputs{
		Person: "charlie",
		Asks: []bus.Ask{
			{ID: "200-0", From: "master", Title: "second", State: "open", CreatedAt: at(20).UnixMilli(), Questions: []bus.AskQuestionState{q(false), q(true)}},
			{ID: "100-0", From: "architect", Title: "first", State: "open", CreatedAt: at(10).UnixMilli(), Questions: []bus.AskQuestionState{q(false)}},
			{ID: "50-0", From: "coder", Title: "closed", State: "closed", CreatedAt: at(5).UnixMilli(), Questions: []bus.AskQuestionState{q(false)}},
			{ID: "60-0", From: "coder", Title: "all answered", State: "open", CreatedAt: at(6).UnixMilli(), Questions: []bus.AskQuestionState{q(true)}},
		},
		Agents: map[string]Agent{
			"coder":    {State: "waiting", Message: "decide", StateAt: at(30)},
			"sentinel": {State: "waiting", Message: "older", StateAt: at(25)},
			"ui-kimi":  {State: "waiting", StateAt: at(1), AnsweredAt: at(2)},                                                  // answered
			"master":   {State: "waiting", StateAt: at(19), OpenAsk: 1},                                                        // its ask is the row
			"ui-grok":  {State: "idle", Obs: blocked},                                                                          // its client's prompt
			"old-grok": {State: "idle", Obs: blocked, JudgedAt: at(59).Add(time.Duration(bus.ObservedTTL) * time.Millisecond)}, // stale reading
			"other":    {State: "idle", Obs: Observe(bus.Observation{State: "blocked", PID: 8, At: at(59).UnixMilli()}, true, 9)},
			"foureyes": {State: "working", Gates: map[string]string{
				"1792000000000-0": "coder|between agents", "pr-9": "charlie|look at PR 9"}},
		},
		PersonGates: map[string]string{"1792000000001-0": "master|sign off"},
		Health: map[string]health.Record{"team-stall": {ID: "team-stall", Status: health.Warn, Reason: "waiting-person",
			Detail: "the team waits on you", Since: at(40).UnixMilli(), ObservedAt: at(59).UnixMilli()}},
		HealthNow: now,
		MergeRead: true,
		Merge: bus.MergeQueueRecord{ItemsAt: at(55).UnixMilli(), Items: []bus.MergeItem{
			{Number: 841, Title: "onboarding", Branch: "feat/o", Head: "abc", ApprovedAt: at(45).UnixMilli(), Approvers: []string{"a", "b"}},
			{Branch: "feat/local", Head: "def", ApprovedAt: at(44).UnixMilli(), Approvers: []string{"a"}}}},
	}
	got := Items(in, now)
	want := []string{"ask:100-0", "ask:200-0",
		"health:team-stall:" + itoa(at(40).UnixMilli()),
		"wait:sentinel:" + itoa(at(25).UnixMilli()), "wait:coder:" + itoa(at(30).UnixMilli()), "prompt:ui-grok:" + itoa(at(50).UnixMilli()),
		"gate:charlie:1792000000001-0", "gate:foureyes:pr-9",
		"merge:branch:feat/local", "merge:pr:841"}
	if g := strings.Join(keys(got), " "); g != strings.Join(want, " ") {
		t.Fatalf("items\n got %s\nwant %s", g, strings.Join(want, " "))
	}
	if got[1].Count != 1 || got[1].Text != "second" || got[1].AskID != "200-0" {
		t.Errorf("ask row %+v", got[1])
	}
	if g := got[5]; g.Kind != KindPrompt || g.Text != "" || !g.At.Equal(at(50)) {
		t.Errorf("prompt row %+v", g)
	}
	if g := got[7]; g.By != "charlie" || g.Text != "look at PR 9" || g.Agent != "foureyes" {
		t.Errorf("gate row %+v", g)
	}
	if g := got[8]; g.Text != "merge feat/local (1 approval)" || g.Head != "def" {
		t.Errorf("merge row %+v", g)
	}
	if g := got[9]; g.Text != "merge PR 841: onboarding" || g.Branch != "feat/o" {
		t.Errorf("merge row %+v", g)
	}
	// No person: no gate is ever the person's.
	in.Person = ""
	for _, it := range Items(in, now) {
		if it.Rank == RankGate {
			t.Fatalf("a gate with no person: %+v", it)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// agent-screen-blocked §3: a screen unknown and unchanged for StuckAfter is
// stuck; shorter, or StuckAfter off, is not; a declared wait or block wins.
func TestItemsStuckScreen(t *testing.T) {
	obs := Observe(bus.Observation{State: "unknown", PID: 3, At: at(59).UnixMilli(), ScreenSince: at(40).UnixMilli()}, true, 3)
	stuck := func(a Agent) bool {
		items := Items(Inputs{Agents: map[string]Agent{"coder": a}}, at(60))
		return len(items) == 1 && items[0].Kind == KindStuck && items[0].Key == "stuck:coder:"+itoa(at(40).UnixMilli())
	}
	if !stuck(Agent{State: "working", Obs: obs, StuckAfter: 10 * time.Minute}) {
		t.Fatal("a screen unchanged for 19 min, stuck_after 10 min, is not stuck")
	}
	if stuck(Agent{State: "working", Obs: obs, StuckAfter: 30 * time.Minute}) || stuck(Agent{State: "working", Obs: obs}) {
		t.Fatal("stuck before stuck_after, or with it off")
	}
	if stuck(Agent{State: "blocked", Obs: obs, StuckAfter: time.Minute}) {
		t.Fatal("a declared block is stuck")
	}
}

// The merge queue's one unknown row, by why it is unknown; none unread.
func TestItemsMergeUnknown(t *testing.T) {
	text := func(in Inputs) string {
		items := Items(in, t0)
		if len(items) != 1 || items[0].Key != MergeUnknownKey {
			t.Fatalf("items %+v", items)
		}
		return items[0].Text
	}
	if g := text(Inputs{MergeRead: true, MergeErr: "read failed"}); g != "merge queue unknown: read failed" {
		t.Errorf("failed read: %q", g)
	}
	if g := text(Inputs{MergeRead: true, Dispatch: "stopped"}); g != "merge queue unknown: project stopped" {
		t.Errorf("stopped: %q", g)
	}
	if g := text(Inputs{MergeRead: true}); !strings.Contains(g, "not computed yet") {
		t.Errorf("never computed: %q", g)
	}
	if got := Items(Inputs{}, t0); len(got) != 0 {
		t.Errorf("an unread queue gives %+v", got)
	}
	failed := bus.MergeQueueRecord{ItemsAt: t0.UnixMilli(), Reason: "gh timed out", Items: []bus.MergeItem{{Number: 3, Head: "h", Branch: "b"}}}
	if got := Items(Inputs{MergeRead: true, Merge: failed}, t0); len(got) != 1 || !strings.Contains(got[0].Text, "(as of ") || !strings.Contains(got[0].Text, "gh timed out") {
		t.Errorf("a failed pass over known items: %+v", got)
	}
}

// Observe: only the agent's current process is its prompt; a reading with
// no since is dated by its time.
func TestObserve(t *testing.T) {
	o := Observe(bus.Observation{State: "blocked", PID: 5, At: at(1).UnixMilli()}, true, 5)
	if !o.Prompt || !o.OwnPID || !o.Since.Equal(at(1)) {
		t.Errorf("own process: %+v", o)
	}
	if o := Observe(bus.Observation{State: "blocked", PID: 5, At: at(1).UnixMilli()}, true, 6); o.Prompt || o.OwnPID {
		t.Errorf("another process: %+v", o)
	}
	if o := Observe(bus.Observation{State: "blocked", PID: 5}, false, 5); o != (Obs{}) {
		t.Errorf("no reading: %+v", o)
	}
	if !o.Fresh(at(1)) || o.Fresh(at(1).Add(time.Duration(bus.ObservedTTL)*time.Millisecond)) || (Obs{}).Fresh(at(1)) {
		t.Error("freshness")
	}
}

// A gate item is dated by its ref, the challenge's stream id (foureyes on
// step 1: the date was untested); a ref that is no id has no date.
func TestGateItemDate(t *testing.T) {
	ref := itoa(at(7).UnixMilli()) + "-0"
	items := gateItems("charlie", "charlie", map[string]string{ref: "master|sign off", "no-id": "master|x"})
	got := map[string]time.Time{}
	for _, it := range items {
		got[it.Key] = it.At
	}
	if !got["gate:charlie:"+ref].Equal(at(7)) || !got["gate:charlie:no-id"].IsZero() {
		t.Fatalf("dates %v", got)
	}
}
