package mergequeue

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// What may be undelivered (master-idle §3.1), before the git test: an
// approved head within 72 h, no tip on origin, not dropped, its subject not
// done; its local branch named when one has it as its tip.
func TestUndeliveredCandidates(t *testing.T) {
	h := func(c string) string { return strings.Repeat(c, 40) }
	now := time.UnixMilli(1_000_000_000_000)
	ok := func(subject, head string, ago time.Duration) []bus.Verdict {
		at := now.Add(-ago).UnixMilli()
		return []bus.Verdict{
			{Subject: subject, Author: "coder", Reviewer: "foureyes", Decision: "approve", Head: head, TS: at},
			{Subject: subject, Author: "coder", Reviewer: "reviewer-codex", Decision: "approve", Head: head, TS: at},
		}
	}
	var vs []bus.Verdict
	vs = append(vs, ok("pushed", h("a"), time.Hour)...)
	vs = append(vs, ok("waiting", h("b"), time.Hour)...)
	vs = append(vs, ok("old", h("c"), 73*time.Hour)...)
	vs = append(vs, ok("dropped", h("d"), time.Hour)...)
	vs = append(vs, ok("done", h("e"), time.Hour)...)
	vs = append(vs, ok("nobranch", h("f"), 2*time.Hour)...)
	vs = append(vs, bus.Verdict{Subject: "rejected", Author: "coder", Reviewer: "foureyes", Decision: "reject", Head: h("9"), TS: now.UnixMilli()})
	got := UndeliveredCandidates(UndeliveredInput{
		Verdicts: vs, Now: now, Window: 72 * time.Hour,
		Remote:  []Tip{{Branch: "feat/pushed", Head: h("a")}},
		Local:   []Tip{{Branch: "feat/pushed", Head: h("a")}, {Branch: "feat/waiting", Head: h("b")}},
		Dropped: map[string]bool{h("d"): true},
		Done:    map[string]bool{"done": true},
	})
	if len(got) != 2 || got[0] != (bus.MergeUndelivered{Subject: "nobranch", Head: h("f"), ApprovedAt: now.Add(-2 * time.Hour).UnixMilli()}) ||
		got[1] != (bus.MergeUndelivered{Subject: "waiting", Head: h("b"), Branch: "feat/waiting", ApprovedAt: now.Add(-time.Hour).UnixMilli()}) {
		t.Fatalf("candidates %+v", got)
	}
}
