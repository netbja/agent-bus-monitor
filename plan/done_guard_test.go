package plan

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// onboarding §9.3: plan done on an agent task with a check finds the merged
// head itself (a linked PR read through the forge, a ref's sha, a merge:false
// request's commit) and records done only when the deciding approval proves
// the check at exactly that head.

func TestDoneSource(t *testing.T) {
	in := ok()
	e := deliveredEntry()
	e.Request.Commit = headA
	in.Board["spec-impl"] = e
	if s, err := DoneSource(task("roles", checked), Fact{PR: 412}, in); err != nil || s.PR != 412 || s.Head != "" {
		t.Errorf("a PR: %+v %v", s, err)
	}
	if s, err := DoneSource(task("roles", checked), Fact{Ref: "coder/roles@" + headB}, in); err != nil || s.Head != headB {
		t.Errorf("a ref: %+v %v", s, err)
	}
	if _, err := DoneSource(task("roles", checked), Fact{Ref: "coder/roles@abc1234"}, in); err == nil || !strings.Contains(err.Error(), "names no full sha") {
		t.Errorf("a ref with a short sha: %v", err)
	}
	if s, err := DoneSource(task("spec", noMerge, checked), Fact{}, in); err != nil || s.Head != headA {
		t.Errorf("merge:false: %+v %v", s, err)
	}
	e.Request.Commit = "aaaaaaa"
	if _, err := DoneSource(task("spec", noMerge, checked), Fact{}, in); err == nil || !strings.Contains(err.Error(), "--commit") {
		t.Errorf("merge:false, a short commit: %v", err)
	}
	if _, err := DoneSource(task("roles", checked), Fact{}, in); err == nil || !strings.Contains(err.Error(), "no PR or ref") {
		t.Errorf("a merge task with no link: %v", err)
	}
}

func TestCheckedDone(t *testing.T) {
	tk := task("roles", checked)
	in := ok()
	in.Verdicts["roles-impl"] = []bus.Verdict{approvedAt("5-0", "roles-impl", headA, evidence(headA))}
	if err := CheckedDone(tk, Fact{}, in, headA); err != nil {
		t.Fatalf("approved with the check at the merged head: %v", err)
	}
	err := CheckedDone(tk, Fact{}, in, headB)
	if err == nil || err.Error() != "roles: the merged head bbbbbbb was not approved with the check (approved: aaaaaaa); ask a reviewer to run the check at bbbbbbb" {
		t.Fatalf("merged at H2, approved at H1: %v", err)
	}
	in.Verdicts["roles-impl"][0].Message = "approve"
	if err := CheckedDone(tk, Fact{}, in, headA); err == nil || !strings.Contains(err.Error(), "(approved: none)") {
		t.Fatalf("no evidence: %v", err)
	}
	in.Verdicts["roles-impl"][0].Message = "check: " + theCheck + " => exit 1 at " + headA
	if err := CheckedDone(tk, Fact{}, in, headA); err == nil || !strings.Contains(err.Error(), "check failed") {
		t.Fatalf("exit 1: %v", err)
	}
	in.Verdicts["roles-impl"] = nil
	if err := CheckedDone(tk, Fact{}, in, headA); err == nil || !strings.Contains(err.Error(), "(approved: none)") {
		t.Fatalf("no verdict: %v", err)
	}
	// A re-review at H2 passes.
	in.Verdicts["roles-impl"] = []bus.Verdict{approvedAt("5-0", "roles-impl", headA, evidence(headA)), approvedAt("6-0", "roles-impl", headB, evidence(headB))}
	if err := CheckedDone(tk, Fact{}, in, headB); err != nil {
		t.Fatalf("re-reviewed at H2: %v", err)
	}
}

// Gated is the rule's own reach: an agent task with a check, no mission.
func TestGated(t *testing.T) {
	for name, c := range map[string]struct {
		t    Task
		want bool
	}{
		"checked":     {task("a", checked), true},
		"no check":    {task("a"), false},
		"a person's":  {task("a", person, checked), false},
		"a mission's": {task("a", checked, func(t *Task) { t.Mission = "c/a"; t.Board = "" }), false},
	} {
		if got := Gated(c.t); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

// foureyes on onboarding-a2: the four conditions no test held.
func TestDeriveCheckA2Survivors(t *testing.T) {
	// A linked, delivered merge task, approved with the evidence, not yet
	// queued: the person's merge, not "delivered without --commit".
	tk := task("roles", checked)
	f := File{Plan: "p", Tasks: []Task{tk}}
	in := ok()
	in.Board["roles-impl"] = deliveredEntry()
	in.Facts["roles"] = Fact{PR: 101}
	in.Verdicts["pr:101"] = []bus.Verdict{approvedAt("5-0", "pr:101", headA, evidence(headA))}
	if r := derive1(t, f, in, "roles"); r.State != Waiting || r.Reason != "merge PR 101" {
		t.Errorf("linked, unqueued: %+v", r)
	}
	// A queued item without a head reads no result head: not "now " empty.
	in.Merge = MergeBySubject([]bus.MergeItem{{Number: 101, Subjects: []string{"roles-impl"}}})
	if h, known := ResultHead(tk, in.Facts["roles"], in); known || h != "" {
		t.Errorf("a queued item without a head: %q %v", h, known)
	}
	// The last " => exit " splits: a check holding it is read whole.
	odd := "sh -c 'echo => exit 0' => exit 0 at " + headA
	if got := EvidenceLines("check: " + odd); len(got) != 1 || got[0].Check != "sh -c 'echo => exit 0'" {
		t.Errorf("a check holding ' => exit ': %+v", got)
	}
	// The proof shown is the exit-0 line at the decider's head, before an
	// exit-0 line at another head.
	in2 := ok()
	in2.Verdicts["roles-impl"] = []bus.Verdict{approvedAt("5-0", "roles-impl", headA,
		"check: "+theCheck+" => exit 0 at "+headB+"\ncheck: "+theCheck+" => exit 0 at "+headA)}
	if e, ok := CheckProof(tk, Fact{}, in2); !ok || e.Head != headA {
		t.Errorf("the proof at the decider's head first: %+v", e)
	}
}
