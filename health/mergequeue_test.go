package health

import (
	"strings"
	"testing"
)

func TestEvaluateMergeQueue(t *testing.T) {
	item := MergeQueueEntry{Number: 309, Title: "request priority", Branch: "feat/rp"}
	cases := []struct {
		name    string
		rec     MergeQueueInput
		readErr string
		want    Status
		has     string
	}{
		{"never computed", MergeQueueInput{}, "", Unknown, "not computed"},
		{"read failed", MergeQueueInput{ItemsAt: 1}, "merge queue unavailable", Unknown, "unavailable"},
		{"nothing waits", MergeQueueInput{ItemsAt: 1}, "", OK, "nothing waits"},
		{"one waits", MergeQueueInput{ItemsAt: 1, Items: []MergeQueueEntry{item}}, "", Warn, "#309 request priority"},
		{"git fallback", MergeQueueInput{ItemsAt: 1, Items: []MergeQueueEntry{{Branch: "feat/x"}}}, "", Warn, "feat/x"},
		{"a failed pass", MergeQueueInput{ItemsAt: 1, Reason: "gh timed out", Items: []MergeQueueEntry{item}}, "", Unknown, "gh timed out"},
		{"a sole unapproved PR", MergeQueueInput{ItemsAt: 1, Unapproved: 1}, "", OK, "nothing waits for a merge (1 open PR not on an approved head)"},
		{"unapproved counted", MergeQueueInput{ItemsAt: 1, Items: []MergeQueueEntry{item}, Unapproved: 2}, "", Warn, "2 open PRs not on an approved head"},
	}
	for _, c := range cases {
		out := EvaluateMergeQueue(c.rec, c.readErr)
		if out.Status != c.want || !strings.Contains(out.Detail, c.has) {
			t.Errorf("%s: %s %q", c.name, out.Status, out.Detail)
		}
		if out.Status == Fail {
			t.Errorf("%s: never fail", c.name)
		}
	}
	d, ok := ByID("merge-queue")
	if !ok || d.Command != "agentbus merge-queue" || len(d.Person) == 0 {
		t.Fatalf("definition %+v", d)
	}
}
