package plan

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// onboarding §9.2: an agent task with a check is done only when its
// approving verdict quotes the check at the verdict's head and that head is
// the task's result (ResultHead); otherwise it stays in review, with why.

const theCheck = "go test ./plan -run TestX"

func checked(t *Task) { t.Check = theCheck }

func approvedAt(id, subject, head, text string) bus.Verdict {
	v := verdict(id, subject, "approve")
	v.Head, v.Message = head, text
	return v
}

func evidence(head string) string { return "approve\ncheck: " + theCheck + " => exit 0 at " + head }

// A merge:false task delivered at a commit, approved with the evidence there.
func noMergeChecked(commit string) (File, Inputs) {
	tk := task("spec", noMerge, checked)
	in := ok()
	e := deliveredEntry()
	e.Request.Commit = commit
	in.Board["spec-impl"] = e
	in.Verdicts["spec-impl"] = []bus.Verdict{approvedAt("5-0", "spec-impl", headA, evidence(headA))}
	return File{Plan: "p", Tasks: []Task{tk}}, in
}

func TestDeriveCheckDone(t *testing.T) {
	f, in := noMergeChecked(headA)
	if r := derive1(t, f, in, "spec"); r.State != Done || r.Verdict != Approved {
		t.Fatalf("approved with the check at the delivered head: %+v", r)
	}
	// Without the check the same inputs are done too: the rule is the check's.
	f.Tasks[0].Check = ""
	in.Verdicts["spec-impl"][0].Message = "approve"
	if r := derive1(t, f, in, "spec"); r.State != Done {
		t.Fatalf("no check: %+v", r)
	}
}

func TestDeriveCheckInReview(t *testing.T) {
	notQuoted := "the approving verdict does not quote the check at its head"
	for name, c := range map[string]struct {
		mod           func(*Inputs)
		label, reason string
	}{
		"no evidence line": {func(in *Inputs) { in.Verdicts["spec-impl"][0].Message = "approve" }, CheckNotRun, notQuoted},
		"exit 1": {func(in *Inputs) {
			in.Verdicts["spec-impl"][0].Message = "check: " + theCheck + " => exit 1 at " + headA
		}, CheckFailed, "the approving verdict quotes the check failing at its head"},
		"another check": {func(in *Inputs) {
			in.Verdicts["spec-impl"][0].Message = "check: go test ./plan => exit 0 at " + headA
		}, CheckNotRun, notQuoted},
		"a verdict without a head": {func(in *Inputs) { in.Verdicts["spec-impl"][0].Head = "" }, CheckNotRun, notQuoted},
		"a line at another head": {func(in *Inputs) {
			in.Verdicts["spec-impl"][0].Message = evidence(headB)
		}, CheckNotRun, notQuoted},
		"delivered at another commit": {func(in *Inputs) { in.Board["spec-impl"].Request.Commit = headB }, HeadChanged, "re-review: approved at aaaaaaa, now bbbbbbb"},
		"delivered without --commit":  {func(in *Inputs) { in.Board["spec-impl"].Request.Commit = "" }, Approved, "delivered without --commit: the checked head is unknown"},
		"delivered at a short commit": {func(in *Inputs) { in.Board["spec-impl"].Request.Commit = "aaaaaaa" }, Approved, "delivered without --commit: the checked head is unknown"},
	} {
		f, in := noMergeChecked(headA)
		c.mod(&in)
		r := derive1(t, f, in, "spec")
		if r.State != InReview || r.Verdict != c.label || r.Reason != c.reason {
			t.Errorf("%s: %s %q %q, want review %q %q", name, r.State, r.Verdict, r.Reason, c.label, c.reason)
		}
	}
	// Approved before it is delivered: in progress, no reason yet.
	f, in := noMergeChecked("")
	in.Board["spec-impl"] = entry("accepted", &bus.RequestInfo{ID: "1-0", AcceptedAt: 1000})
	if r := derive1(t, f, in, "spec"); r.State != InProgress || r.Reason != "" {
		t.Fatalf("approved, still being delivered: %+v", r)
	}
	// The whitespace of the check is collapsed, not its words.
	f, in = noMergeChecked(headA)
	in.Verdicts["spec-impl"][0].Message = "check:  go test   ./plan -run TestX => exit 0 at " + headA
	if r := derive1(t, f, in, "spec"); r.State != Done {
		t.Fatalf("spaces collapsed: %+v", r)
	}
}

// A merge task: the queued head must be the approved one before it is the
// person's; a recorded done counts only at the decider's head.
func TestDeriveCheckMergeHeads(t *testing.T) {
	tk := task("roles", checked)
	f := File{Plan: "p", Tasks: []Task{tk}}
	in := ok()
	in.Board["roles-impl"] = deliveredEntry()
	in.Verdicts["roles-impl"] = []bus.Verdict{approvedAt("5-0", "roles-impl", headA, evidence(headA))}
	queue := func(head string) {
		in.Merge = MergeBySubject([]bus.MergeItem{{Number: 311, Branch: "feat/roles", Head: head, Subjects: []string{"roles-impl"}}})
	}
	queue(headA)
	if r := derive1(t, f, in, "roles"); r.State != Waiting || r.Reason != "merge PR 311" {
		t.Fatalf("queued at the approved head: %+v", r)
	}
	// Stale head: the PR moved after the approval.
	queue(headB)
	if r := derive1(t, f, in, "roles"); r.State != InReview || r.Verdict != HeadChanged || r.Reason != "re-review: approved at aaaaaaa, now bbbbbbb" {
		t.Fatalf("queued at a new head: %+v", r)
	}
	// A new approval with the evidence at the new head: the person's again.
	in.Verdicts["roles-impl"] = append(in.Verdicts["roles-impl"], approvedAt("6-0", "roles-impl", headB, evidence(headB)))
	if r := derive1(t, f, in, "roles"); r.State != Waiting {
		t.Fatalf("re-reviewed at the new head: %+v", r)
	}
	// Merged: a recorded done at the decider's head is done; at another, or
	// with none, it is not.
	in.Merge = nil
	in.Facts["roles"] = Fact{Done: 9000, Head: headB}
	if r := derive1(t, f, in, "roles"); r.State != Done || !r.Recorded {
		t.Fatalf("recorded at the approved head: %+v", r)
	}
	for _, h := range []string{headA, ""} {
		in.Facts["roles"] = Fact{Done: 9000, Head: h}
		r := derive1(t, f, in, "roles")
		if r.State != InReview || !strings.Contains(r.Reason, "recorded done without an approved check at the merged head") {
			t.Errorf("recorded at %q: %+v", h, r)
		}
	}
	// A recorded done with no approval at all is not done either.
	in.Verdicts["roles-impl"] = nil
	in.Facts["roles"] = Fact{Done: 9000, Head: headB}
	if r := derive1(t, f, in, "roles"); r.State == Done {
		t.Fatalf("recorded done, never approved: %+v", r)
	}
}

// ResultHead per kind of task (§9.1's table).
func TestResultHead(t *testing.T) {
	in := ok()
	e := deliveredEntry()
	e.Request.Commit = headA
	in.Board["spec-impl"] = e
	if h, known := ResultHead(task("spec", noMerge), Fact{}, in); !known || h != headA {
		t.Errorf("merge:false: %q %v", h, known)
	}
	in.Merge = MergeBySubject([]bus.MergeItem{{Head: headB, Subjects: []string{"roles-impl"}}})
	if h, known := ResultHead(task("roles"), Fact{Head: headA}, in); !known || h != headB {
		t.Errorf("queued: %q %v", h, known)
	}
	in.Merge = nil
	if h, known := ResultHead(task("roles"), Fact{Head: headA}, in); !known || h != headA {
		t.Errorf("merged: %q %v", h, known)
	}
	if _, known := ResultHead(task("roles"), Fact{PR: 4}, in); known {
		t.Error("linked, neither queued nor merged: known")
	}
}

// The page's evidence row: the deciding approval's line, the passing one
// before an earlier failed run; none without an approval or a check.
func TestCheckProof(t *testing.T) {
	tk := task("roles", checked)
	in := ok()
	in.Verdicts["roles-impl"] = []bus.Verdict{approvedAt("5-0", "roles-impl", headA,
		"check: "+theCheck+" => exit 1 at "+headA+"\nfixed the flake\ncheck: "+theCheck+" => exit 0 at "+headA)}
	if e, ok := CheckProof(tk, Fact{}, in); !ok || e.Exit != 0 || e.String() != "check: "+theCheck+" => exit 0 at aaaaaaa" {
		t.Fatalf("the passing line: %+v %v", e, ok)
	}
	in.Verdicts["roles-impl"][0].Message = "check: " + theCheck + " => exit 1 at " + headA
	if e, ok := CheckProof(tk, Fact{}, in); !ok || e.Exit != 1 {
		t.Fatalf("only a failed line: %+v %v", e, ok)
	}
	in.Verdicts["roles-impl"][0].Decision = "reject"
	if _, ok := CheckProof(tk, Fact{}, in); ok {
		t.Fatal("a reject proves nothing")
	}
	in.Verdicts["roles-impl"][0].Decision = "approve"
	if _, ok := CheckProof(task("roles"), Fact{}, in); ok {
		t.Fatal("a task without a check has no proof")
	}
}

// A person's task with a check derives as today in this step: its proof
// is the person's typed passes (§9.4, a later slice), not a verdict.
func TestDeriveCheckLeavesPersonTasks(t *testing.T) {
	withCheck := task("release", person, func(t *Task) { t.Check = "open busmon, the tab shows v1" })
	f := File{Plan: "p", Tasks: []Task{withCheck}}
	in := ok()
	in.Facts["release"] = Fact{Done: 9000}
	if r := derive1(t, f, in, "release"); r.State != Done || !r.Recorded {
		t.Fatalf("a person's recorded done: %+v", r)
	}
}
