package plan

import (
	"fmt"
	"strings"
	"testing"
)

const sample = `{
  "plan": "v0.22",
  "goal": "busmon UX, specialist roles, client reload",
  "tasks": [
    {"id": "busmon-ux", "title": "busmon UX items 5-8", "owner": "coder", "board": "busmon-ux-impl"},
    {"id": "roles", "title": "Specialist roles", "owner": "coder", "board": "roles-impl", "after": ["busmon-ux"]},
    {"id": "ux-spec", "title": "busmon UX spec", "owner": "coder", "board": "busmon-ux-spec", "merge": false},
    {"id": "deploy", "title": "Change deploy", "owner": "coder", "mission": "v22/deploy"},
    {"id": "release", "title": "Tag and install", "owner": "person", "after": ["roles"], "merge": true}
  ]
}`

func TestParseAcceptsTheSpecExample(t *testing.T) {
	f, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if f.Plan != "v0.22" || len(f.Tasks) != 5 {
		t.Fatalf("got %+v", f)
	}
	if got := f.Tasks[0].ReviewSubject(); got != "busmon-ux-impl" {
		t.Fatalf("review subject defaults to the board task, got %q", got)
	}
	if !f.Tasks[0].MergeTask() || f.Tasks[2].MergeTask() {
		t.Fatal("merge defaults to true for an agent's task, false when set")
	}
	if f.Tasks[4].MergeTask() {
		t.Fatal("merge is ignored for a person's task")
	}
	if f.Tasks[3].MergeTask() {
		t.Fatal("a mission task is integrated by the conductor, not merged by the person")
	}
}

func TestParseRefuses(t *testing.T) {
	task := func(s string) string { return `{"plan":"p","goal":"g","tasks":[` + s + `]}` }
	many := make([]string, MaxTasks+1)
	for i := range many {
		many[i] = fmt.Sprintf(`{"id":"t%d","title":"x","owner":"coder"}`, i)
	}
	for name, in := range map[string]string{
		"unknown field":       task(`{"id":"a","title":"x","owner":"coder","color":"red"}`),
		"unknown after":       task(`{"id":"a","title":"x","owner":"coder","after":["b"]}`),
		"a cycle":             task(`{"id":"a","title":"x","owner":"coder","after":["b"]},{"id":"b","title":"y","owner":"coder","after":["a"]}`),
		"a self-cycle":        task(`{"id":"a","title":"x","owner":"coder","after":["a"]}`),
		"too many tasks":      task(strings.Join(many, ",")),
		"invalid id":          task(`{"id":"A b","title":"x","owner":"coder"}`),
		"invalid owner":       task(`{"id":"a","title":"x","owner":"Coder!"}`),
		"invalid board":       task(`{"id":"a","title":"x","owner":"coder","board":"x y"}`),
		"invalid review":      task(`{"id":"a","title":"x","owner":"coder","review":"x\ny"}`),
		"invalid mission":     task(`{"id":"a","title":"x","owner":"coder","mission":"nochange"}`),
		"duplicate id":        task(`{"id":"a","title":"x","owner":"coder"},{"id":"a","title":"y","owner":"coder"}`),
		"empty title":         task(`{"id":"a","title":"","owner":"coder"}`),
		"multi-line title":    task(`{"id":"a","title":"x\ny","owner":"coder"}`),
		"no tasks":            `{"plan":"p","goal":"g","tasks":[]}`,
		"invalid plan name":   `{"plan":"P Q","goal":"g","tasks":[{"id":"a","title":"x","owner":"coder"}]}`,
		"trailing data":       task(`{"id":"a","title":"x","owner":"coder"}`) + `{}`,
		"mission and board":   task(`{"id":"a","title":"x","owner":"coder","mission":"c/a","board":"a"}`),
		"person with mission": task(`{"id":"a","title":"x","owner":"person","mission":"c/a"}`),
	} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// plan-chantier §2.1: groups listed once, a task's group names one of them;
// at most 12, a ValidName id unique, a one-line title of 1 to 40
// characters; a listed group with no task is refused (a typo would hide
// it). A plan without groups parses as before.
func TestParseGroups(t *testing.T) {
	grouped := func(groups, taskGroup string) string {
		return `{"plan": "v0.29", "goal": "g", "groups": [` + groups + `], "tasks": [
  {"id": "a", "title": "A", "owner": "coder", "group": "` + taskGroup + `"},
  {"id": "b", "title": "B", "owner": "coder"}]}`
	}
	f, err := Parse([]byte(grouped(`{"id": "herdr-exit", "title": "Leave herdr"}`, "herdr-exit")))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Groups) != 1 || f.Groups[0] != (Group{ID: "herdr-exit", Title: "Leave herdr"}) || f.Tasks[0].Group != "herdr-exit" || f.Tasks[1].Group != "" {
		t.Fatalf("got %+v", f)
	}
	// 13 groups, each with its task: refused by the maximum alone
	// (foureyes, P1); 12 are accepted.
	manyGroups := func(n int) string {
		var gs, ts []string
		for i := 0; i < n; i++ {
			gs = append(gs, fmt.Sprintf(`{"id": "g%d", "title": "G"}`, i))
			ts = append(ts, fmt.Sprintf(`{"id": "t%d", "title": "T", "owner": "coder", "group": "g%d"}`, i, i))
		}
		return `{"plan": "v0.29", "goal": "g", "groups": [` + strings.Join(gs, ", ") + `], "tasks": [` + strings.Join(ts, ", ") + `]}`
	}
	if _, err := Parse([]byte(manyGroups(12))); err != nil {
		t.Fatalf("12 groups: %v", err)
	}
	if _, err := Parse([]byte(manyGroups(13))); err == nil || !strings.Contains(err.Error(), "at most 12 groups") {
		t.Fatalf("13 groups: %v", err)
	}
	for name, data := range map[string]string{
		"unknown group":  grouped(`{"id": "herdr-exit", "title": "Leave herdr"}`, "self-repair"),
		"duplicate id":   grouped(`{"id": "herdr-exit", "title": "A"}, {"id": "herdr-exit", "title": "B"}`, "herdr-exit"),
		"invalid id":     grouped(`{"id": "Herdr Exit", "title": "A"}`, "Herdr Exit"),
		"41-char title":  grouped(`{"id": "herdr-exit", "title": "`+strings.Repeat("x", 41)+`"}`, "herdr-exit"),
		"empty title":    grouped(`{"id": "herdr-exit", "title": ""}`, "herdr-exit"),
		"two-line title": grouped(`{"id": "herdr-exit", "title": "a\nb"}`, "herdr-exit"),
		"an empty group": grouped(`{"id": "herdr-exit", "title": "A"}, {"id": "self-repair", "title": "B"}`, "herdr-exit"),
		"group, no list": `{"plan": "v0.29", "goal": "g", "tasks": [{"id": "a", "title": "A", "owner": "coder", "group": "x"}]}`,
		"unknown field":  `{"plan": "v0.29", "goal": "g", "groups": [{"id": "x", "title": "X", "owner": "coder"}], "tasks": [{"id": "a", "title": "A", "owner": "coder", "group": "x"}]}`,
	} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Parse([]byte(grouped(`{"id": "herdr-exit", "title": "`+strings.Repeat("é", 40)+`"}`, "herdr-exit"))); err != nil {
		t.Fatalf("a 40-character title: %v", err)
	}
	if f, err := Parse([]byte(sample)); err != nil || f.Groups != nil {
		t.Fatalf("no groups: %+v %v", f.Groups, err)
	}
}
