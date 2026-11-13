package health

import (
	"strings"
	"testing"
	"time"
)

// git rev-list --first-parent --format=%s prints "commit <sha>" then the
// subject. Only GitHub's merge-commit form is a signal (the architect's
// plan-drift design): a squash "title (#N)" or anything else is none.
func TestParseMerges(t *testing.T) {
	out := "commit aaaa1111\nMerge pull request #465 from netbja/feat/self-repair-7\n" +
		"commit bbbb2222\nMerge pull request #464 from netbja/feat/herdr-5-emptyinput\n" +
		"commit cccc3333\nFix the thing (#463)\n" +
		"commit dddd4444\nMerge branch 'main' into feat/x\n" +
		"commit eeee5555\nMerge pull request #0 from netbja/x\n" +
		"commit ffff6666\nMerge pull request #462 from netbja/feat/a b\n"
	got := ParseMerges(out)
	if len(got) != 2 || got[0] != (Merge{PR: 465, Branch: "feat/self-repair-7", SHA: "aaaa1111"}) ||
		got[1] != (Merge{PR: 464, Branch: "feat/herdr-5-emptyinput", SHA: "bbbb2222"}) {
		t.Fatalf("%+v", got)
	}
	if len(ParseMerges("")) != 0 {
		t.Fatal("nothing parsed from nothing")
	}
	if got := ParseMerges("Merge pull request #5 from o/feat/x\n"); len(got) != 0 {
		t.Fatalf("a subject with no commit line: %+v", got)
	}
}

func driftIn() PlanDriftInput {
	return PlanDriftInput{
		Planned: true, FetchKnown: true, FetchAge: 10 * time.Minute,
		Merges: []Merge{
			{PR: 465, Branch: "feat/self-repair-7"},
			{PR: 464, Branch: "feat/herdr-5-emptyinput"},
		},
		Links:    map[string]int{"self-repair-7": 465, "emptyinput": 464},
		Branches: map[string]string{"self-repair-7": "feat/self-repair-7"},
		Ignored:  map[int]bool{},
	}
}

// The check (warn, never fail): ok when every merge since the plan's load
// is linked by a task (or ignored) and every recorded branch agrees;
// unknown, never ok, when origin/main is not fresh (FETCH_HEAD older than an
// hour) or git could not be read.
func TestEvaluatePlanDrift(t *testing.T) {
	cases := []struct {
		name   string
		change func(*PlanDriftInput)
		want   Status
		words  []string
	}{
		{"all linked", func(*PlanDriftInput) {}, OK, []string{"2 merges since the plan's load, all linked"}},
		{"no plan", func(in *PlanDriftInput) { in.Planned = false }, OK, []string{"no plan loaded"}},
		{"a merge not in the plan", func(in *PlanDriftInput) {
			in.Merges = append(in.Merges, Merge{PR: 460, Branch: "feat/x"}, Merge{PR: 458, Branch: "docs/notes"})
		}, Warn, []string{"2 merges not in the plan: #460 #458", "plan link", "plan ignore"}},
		{"an ignored merge", func(in *PlanDriftInput) {
			in.Merges = append(in.Merges, Merge{PR: 458, Branch: "docs/notes"})
			in.Ignored[458] = true
		}, OK, nil},
		{"a branch mismatch", func(in *PlanDriftInput) { in.Branches["self-repair-7"] = "feat/other" },
			Warn, []string{"task self-repair-7 linked to #465, merged from feat/self-repair-7, delivered on feat/other"}},
		{"superseded by a later PR of its branch stem", func(in *PlanDriftInput) {
			in.Links["herdr-5a"] = 440
			in.Branches["herdr-5a"] = "feat/herdr-5a"
			in.Merges = append(in.Merges, Merge{PR: 462, Branch: "feat/herdr-5a-2"})
			in.Ignored[462] = true // its unlinked warning is not the point here
		}, Warn, []string{"task herdr-5a linked to #440, but #462 merged from feat/herdr-5a-2: relink"}},
		{"a stale fetch", func(in *PlanDriftInput) { in.FetchAge = 2 * time.Hour }, Unknown, []string{"origin/main last fetched 2h ago"}},
		{"no fetch read", func(in *PlanDriftInput) { in.FetchKnown = false }, Unknown, []string{"FETCH_HEAD"}},
		{"git unreadable", func(in *PlanDriftInput) { in.GitErr = "rev-list failed" }, Unknown, []string{"rev-list failed"}},
	}
	for _, c := range cases {
		in := driftIn()
		c.change(&in)
		out := EvaluatePlanDrift(in)
		if out.Status != c.want {
			t.Errorf("%s: %s %q", c.name, out.Status, out.Detail)
			continue
		}
		for _, w := range c.words {
			if !strings.Contains(out.Detail, w) {
				t.Errorf("%s: %q lacks %q", c.name, out.Detail, w)
			}
		}
	}
}

// The wake (like quiet-agent's): one fixed line to master per drift, its
// episode the drift itself, so the same drift wakes once.
func TestPlanDriftWakes(t *testing.T) {
	in := driftIn()
	if w := PlanDriftWakes(in, "master"); len(w) != 0 {
		t.Fatalf("no drift woke %+v", w)
	}
	in.Merges = append(in.Merges, Merge{PR: 460, Branch: "feat/x"})
	w := PlanDriftWakes(in, "master")
	if len(w) != 1 || w[0].Target != "master" || w[0].Mark != "plan-drift" || w[0].Episode == "" ||
		!strings.Contains(w[0].Text, "#460") || !strings.Contains(w[0].Text, "agentbus plan ignore") {
		t.Fatalf("%+v", w)
	}
	again := PlanDriftWakes(in, "master")
	in.Merges = append(in.Merges, Merge{PR: 459, Branch: "feat/y"})
	other := PlanDriftWakes(in, "master")
	if again[0].Episode != w[0].Episode || other[0].Episode == w[0].Episode {
		t.Fatal("the episode is not the drift")
	}
	in.FetchAge = 3 * time.Hour
	if w := PlanDriftWakes(in, "master"); len(w) != 0 {
		t.Fatal("an unknown drift woke master")
	}
}
