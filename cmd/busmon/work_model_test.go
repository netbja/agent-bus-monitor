package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/memory"
	"github.com/netbja/agent-bus-monitor/plan"
)

// workFixture is the mock-ups' situation with a really open subject (the
// person's note on the fixture): the DeepSeek ideas, still waiting for the
// person, as the open question; a plan of five tasks.
func workFixture(now time.Time) workInput {
	task := func(id, title, owner, board string, after ...string) plan.Task {
		return plan.Task{ID: id, Title: title, Owner: owner, Board: board, After: after}
	}
	rows := []plan.Row{
		{Task: task("t6", "idle-breathing", "coder", "idle-breathing"), State: plan.Done},
		{Task: task("t7", "compose-at-file", "coder", "compose-at-file"), State: plan.InProgress},
		{Task: task("t5", "Decide: notifications order", plan.Person, ""), State: plan.Waiting, LastFact: now.Add(-24 * time.Hour).UnixMilli()},
		{Task: task("t9", "Decide: review lot 1", plan.Person, ""), State: plan.Waiting, Ask: "a1"}, // its ask is the row
		{Task: task("t8", "requests-plan redesign", "coder", "requests-plan", "t7"), State: plan.Upcoming, WaitingOn: []string{"t7"}},
		{Task: task("t11", "v0.29 tag", plan.Person, "", "t7", "t8"), State: plan.Upcoming, WaitingOn: []string{"t7", "t8"}},
	}
	reqs := []request{
		{Task: "kimi-quota", Tracked: true, State: "requested", From: "master", Target: "coder", Created: now.Add(-2 * time.Hour)}, // before the one in hand
		{Task: "compose-at-file", Tracked: true, State: "accepted", From: "master", Target: "coder", Accepted: now.Add(-50 * time.Minute)},
		{Task: "requests-plan", Tracked: true, State: "requested", From: "master", Target: "coder"},
	}
	gateRef := func(ago time.Duration) string { return msID(now.Add(-ago)) }
	agents := map[string]*agentState{
		"master":   {state: "working", message: "pushing PR 391", lastSeen: now},
		"coder":    {state: "working", message: "compose-at-file step 2", lastSeen: now},
		"foureyes": {state: "working", lastSeen: now, gates: map[string]string{gateRef(12 * time.Minute): "coder|[subject compose-at-file-home] review"}},
		"sentinel": {state: "idle", lastSeen: now},
		"ui-kimi":  {state: "idle", lastSeen: now},
	}
	items := []attentionItem{
		{Rank: rankAsk, Key: "ask:a1", Agent: "master", Text: "review lot 1?", AskID: "a1", At: now.Add(-4 * time.Minute)},
		{Rank: rankMerge, Key: "merge:abc", Agent: "you", Text: "merge PR 392: compose-at-file step 1"},
		{Rank: rankMerge, Key: mergeUnknownKey, Agent: "you", Text: "merge queue unknown"},
		{Rank: rankUnread, Key: "unread:x", Agent: "master", Text: "a report"},
	}
	return workInput{forYou: items, requests: reqs, agents: agents, pilot: "master", now: now,
		plan: planState{loaded: true, rows: rows},
		ideas: []memory.Question{
			{ID: "2026-11-03-deepseek-superagent-ideas", Title: "Three ideas taken from DeepSeek's Super-Agent prototype", Path: "docs/memory/open-questions/2026-11-03-deepseek-superagent-ideas.md"},
			{ID: "2026-11-01-notif", Title: "Notifications order", Path: "docs/memory/open-questions/2026-11-01-notif.md"}, // t5 covers it
		}}
}

func msID(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) + "-0" }

func youVerbs(w workModel) []string {
	var out []string
	for _, it := range w.You {
		out = append(out, it.Verb+" "+it.Text)
	}
	return out
}

// Correction 2, the sort rule: an unblocked person task is YOU (decide), a
// blocked one NEXT; an ask is answer, an approved head merge; an unread line
// or the unknown merge queue never counts.
func TestWorkYouSection(t *testing.T) {
	w := workOf(workFixture(shellNow))
	want := []string{"answer review lot 1?", "merge merge PR 392: compose-at-file step 1", "decide Decide: notifications order"}
	if got := youVerbs(w); !slices.Equal(got, want) {
		t.Fatalf("YOU = %q, want %q", got, want)
	}
	if w.youCount() != 3 {
		t.Fatalf("youCount = %d", w.youCount())
	}
}

// Correction 3: one counter. The strip's decide + merge is YOU's count.
func TestWorkStripIsTheOneCounter(t *testing.T) {
	w := workOf(workFixture(shellNow))
	q := w.strip(4)
	if q.decide+q.merge != w.youCount() || q.merge != 1 || q.unread != 4 {
		t.Fatalf("strip %+v, YOU %d", q, w.youCount())
	}
}

func TestWorkNextSection(t *testing.T) {
	w := workOf(workFixture(shellNow))
	var got []string
	for _, it := range w.Next {
		got = append(got, it.Key+" "+it.Who)
	}
	want := []string{"plan:t8 coder", "plan:t11 person", "req:kimi-quota coder"} // requests-plan is t8's: not twice
	if !slices.Equal(got, want) {
		t.Fatalf("NEXT = %q, want %q", got, want)
	}
	if w.Next[0].Req == nil || w.Next[0].Req.Task != "requests-plan" {
		t.Fatalf("t8 must carry its tracked request for + -: %+v", w.Next[0].Req)
	}
	if !slices.Equal(w.Next[1].After, []string{"t7", "t8"}) {
		t.Fatalf("t11 waits on %v", w.Next[1].After)
	}
	if w.Done != 1 {
		t.Fatalf("done = %d", w.Done)
	}
}

// Q2: a note a plan task covers shows once, as the task.
func TestWorkIdeasDedupedAgainstThePlan(t *testing.T) {
	w := workOf(workFixture(shellNow))
	if len(w.Ideas) != 1 || w.Ideas[0].ID != "2026-11-03-deepseek-superagent-ideas" || w.Ideas[0].Where == "" {
		t.Fatalf("IDEAS = %+v", w.Ideas)
	}
	in := workFixture(shellNow)
	in.plan.rows = append(in.plan.rows, plan.Row{Task: plan.Task{ID: "t12", Title: "x", Owner: plan.Person, Board: "2026-11-03-deepseek-superagent-ideas"}, State: plan.Upcoming})
	if w := workOf(in); len(w.Ideas) != 0 {
		t.Fatalf("a task whose board names the note covers it: %+v", w.Ideas)
	}
}

// Correction 5: TEAM's review column holds the reviewers and the gate's age;
// the idle agents are one line.
func TestWorkTeam(t *testing.T) {
	w := workOf(workFixture(shellNow))
	byName := map[string]teamRow{}
	var names []string
	for _, r := range w.Team {
		byName[r.Agent] = r
		names = append(names, r.Agent)
	}
	if !slices.Contains(names, "coder") || !slices.Contains(names, "foureyes") || !slices.Contains(names, "master") {
		t.Fatalf("team = %v", names)
	}
	c := byName["coder"]
	if c.Task == nil || c.Task.Task != "compose-at-file" || c.Plan != "t7" {
		t.Fatalf("coder's task in hand: %+v plan %q", c.Task, c.Plan)
	}
	if len(c.Reviewers) != 1 || c.Reviewers[0].Who != "foureyes" || shellNow.Sub(c.Reviewers[0].Since) != 12*time.Minute {
		t.Fatalf("coder's reviewers: %+v", c.Reviewers)
	}
	f := byName["foureyes"]
	if len(f.Reviewing) != 1 || f.Reviewing[0].Who != "coder" || f.Reviewing[0].Subject != "compose-at-file-home" {
		t.Fatalf("foureyes reviewing: %+v", f.Reviewing)
	}
	if !slices.Equal(w.Idle, []string{"sentinel", "ui-kimi"}) {
		t.Fatalf("idle = %v", w.Idle)
	}
}

// A request owed by the person is answer in YOU; a challenge to the person
// stays For you's review row, never twice.
func TestWorkOwedThreadsAreAnswer(t *testing.T) {
	in := workFixture(shellNow)
	r := request{Task: "sign-off", Tracked: true, State: "requested", From: "master", Target: "charlie"}
	in.reqs = reqView{groups: map[reqSection][]reqGroup{secOwed: {
		{Section: secOwed, Threads: []reqThread{{ID: "req:sign-off", From: "master", Kind: kindRequest, Subject: "sign-off", Request: &r}}},
		{Section: secOwed, Threads: []reqThread{{ID: "1-0", From: "master", Kind: kindChallenge, Subject: "x"}}},
	}}}
	w := workOf(in)
	got := youVerbs(w)
	if !slices.Contains(got, "answer sign-off") || len(got) != 4 {
		t.Fatalf("YOU = %q", got)
	}
}

func TestWorkNoPlanNoIdeasError(t *testing.T) {
	in := workFixture(shellNow)
	in.plan = planState{}
	in.ideas, in.ideasErr = nil, true
	w := workOf(in)
	if !w.IdeasErr || w.Done != 0 || len(w.Next) != 2 { // both requests: no plan names them
		t.Fatalf("%+v", w)
	}
}

// Correction 1: the same verbs everywhere. A For you row's word is the
// Work model's verb for its rank (an ask is answer, never ask or decide). A
// merge row has no word: its text starts with the verb ("merge PR 392").
func TestForYouWordsAreTheWorkVerbs(t *testing.T) {
	for _, rank := range []int{rankAsk, rankHealth, rankWait, rankGate} {
		it := attentionItem{Rank: rank}
		if got, want := forYouKinds[rank].word, workVerbOf(it); got != want {
			t.Errorf("rank %d: For you says %q, Work says %q", rank, got, want)
		}
	}
}

// A failed read keeps the last IDEAS and says so; the next good read clears
// the flag.
func TestReadIdeasKeepsTheLastOnFailure(t *testing.T) {
	q := []memory.Question{{ID: "a", Title: "A", Path: "docs/memory/open-questions/a.md"}}
	var err error
	st := &shared{ideasOf: func() ([]memory.Question, error) { return q, err }}
	readIdeas(st)
	if len(st.ideas) != 1 || st.ideasErr {
		t.Fatalf("first read: %+v %v", st.ideas, st.ideasErr)
	}
	err = errors.New("permission denied")
	q = nil
	readIdeas(st)
	if len(st.ideas) != 1 || !st.ideasErr {
		t.Fatalf("failed read: %+v %v", st.ideas, st.ideasErr)
	}
	err = nil
	readIdeas(st)
	if len(st.ideas) != 0 || st.ideasErr {
		t.Fatalf("good read: %+v %v", st.ideas, st.ideasErr)
	}
}

// IDEAS come from the template's working_dir on this host; no template, none.
func TestLocalIdeasOfReadsTheTemplatesTree(t *testing.T) {
	cfg, repo := t.TempDir(), t.TempDir()
	dir := filepath.Join(cfg, "agentbus", "projects")
	notes := filepath.Join(repo, "docs", "memory", "open-questions")
	for _, d := range []string{dir, notes} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "demo.toml"), []byte("name = \"demo\"\nworking_dir = \""+repo+"\"\n"), 0o644)
	os.WriteFile(filepath.Join(notes, "q.md"), []byte("---\nid: q\nstatus: active\n---\n# Q\n"), 0o644)
	env := func(k string) string { return map[string]string{"HOME": "/h", "XDG_CONFIG_HOME": cfg}[k] }
	if got, err := localIdeasOf("demo", env)(); err != nil || len(got) != 1 || got[0].ID != "q" {
		t.Fatalf("demo: %v %v", got, err)
	}
	if got, err := localIdeasOf("other", env)(); err != nil || len(got) != 0 {
		t.Fatalf("no template: %v %v", got, err)
	}
}

// Several gates: the oldest first, so the review column reads in order.
func TestWorkTeamGatesOldestFirst(t *testing.T) {
	in := workFixture(shellNow)
	in.agents["reviewer-codex"] = &agentState{state: "working", lastSeen: shellNow,
		gates: map[string]string{msID(shellNow.Add(-30 * time.Minute)): "coder|[subject old] review",
			msID(shellNow.Add(-time.Minute)): "coder|[subject tie] review"}} // the same age as foureyes' new one: by name
	in.agents["foureyes"].gates[msID(shellNow.Add(-time.Minute))] = "coder|[subject new] review"
	for _, r := range workOf(in).Team {
		if r.Agent != "coder" {
			continue
		}
		var got []string
		for _, g := range r.Reviewers {
			got = append(got, g.Who+" "+g.Subject)
		}
		want := []string{"reviewer-codex old", "foureyes compose-at-file-home", "foureyes new", "reviewer-codex tie"}
		if !slices.Equal(got, want) {
			t.Fatalf("reviewers %q, want %q", got, want)
		}
		return
	}
	t.Fatal("no coder row")
}

// reviewer-codex: a tracked request owed by the person is YOU's answer row
// only, never also in NEXT (the sections are a partition).
func TestWorkOwedRequestNotAlsoNext(t *testing.T) {
	in := workFixture(shellNow)
	r := request{Task: "sign-off", Tracked: true, State: "requested", From: "master", Target: "charlie", Created: shellNow.Add(-time.Hour)}
	in.requests = append(in.requests, r)
	in.reqs = requestGroups(reqInput{reqs: in.requests, self: "charlie", now: shellNow})
	w := workOf(in)
	if !slices.Contains(youVerbs(w), "answer sign-off") {
		t.Fatalf("YOU = %q", youVerbs(w))
	}
	for _, it := range w.Next {
		if it.Key == "req:sign-off" {
			t.Fatalf("sign-off in both YOU and NEXT: %+v", w.Next)
		}
	}
}

// The same partition from the plan's side: a plan task whose board request
// the person owes is that request's answer row, not also a decide or a
// NEXT row.
func TestWorkOwedRequestCoversItsPlanTask(t *testing.T) {
	for _, st := range []plan.State{plan.Waiting, plan.Upcoming} {
		in := workFixture(shellNow)
		r := request{Task: "sign-off", Tracked: true, State: "requested", From: "master", Target: "charlie"}
		in.requests = append(in.requests, r)
		in.plan.rows = append(in.plan.rows, plan.Row{Task: plan.Task{ID: "t20", Title: "Decide: sign-off", Owner: plan.Person, Board: "sign-off"}, State: st})
		in.reqs = requestGroups(reqInput{reqs: in.requests, self: "charlie", now: shellNow})
		w := workOf(in)
		n := 0
		for _, it := range append(append([]workItem{}, w.You...), w.Next...) {
			if it.Plan == "t20" || (it.Req != nil && it.Req.Task == "sign-off") {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("%s: sign-off listed %d times: YOU %q NEXT %+v", st, n, youVerbs(w), w.Next)
		}
	}
}
