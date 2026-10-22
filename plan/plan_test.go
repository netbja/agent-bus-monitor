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
