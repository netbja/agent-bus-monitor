package plan

import (
	"errors"
	"strings"
	"testing"
)

// onboarding §9.4: a person's task with a check is done only with the
// person's typed passes to that exact check, bound by the question id
// check-<16 hex of SHA-256(plan, task, check)>.

func TestPersonCheckQID(t *testing.T) {
	f := File{Plan: "v1"}
	tk := task("release", person, func(t *Task) { t.Check = "open busmon, the tab shows v1" })
	q := PersonCheckQID(f, tk)
	if !strings.HasPrefix(q, "check-") || len(q) != len("check-")+16 {
		t.Fatalf("qid %q", q)
	}
	other := tk
	other.Check = "open busmon, the tab shows v2"
	renamed := tk
	renamed.ID = "release2"
	for name, got := range map[string]string{
		"another check": PersonCheckQID(f, other),
		"another task":  PersonCheckQID(f, renamed),
		"another plan":  PersonCheckQID(File{Plan: "v2"}, tk),
		// the 0x00 separators: "v1"+"release" is not "v1r"+"elease"
		"a shifted boundary": PersonCheckQID(File{Plan: "v1r"}, Task{ID: "elease", Owner: Person, Check: tk.Check}),
	} {
		if got == q {
			t.Errorf("%s: the same id %s", name, q)
		}
	}
	if PersonCheckQID(f, tk) != q {
		t.Error("not a function of its inputs")
	}
}

func TestDerivePersonCheck(t *testing.T) {
	tk := task("release", person, func(t *Task) { t.Check = "open busmon, the tab shows v1" })
	f := File{Plan: "v1", Tasks: []Task{tk}}
	qid := PersonCheckQID(f, tk)
	in := ok()
	in.Facts["release"] = Fact{Done: 9000, Approved: "77-0", ApprovedQID: qid}
	if r := derive1(t, f, in, "release"); r.State != Done || !r.Recorded {
		t.Fatalf("approved at the current id: %+v", r)
	}
	const why = "recorded done without the person's passes to the current check"
	for name, fact := range map[string]Fact{
		"no approval":        {Done: 9000},
		"an old check's id":  {Done: 9000, Approved: "77-0", ApprovedQID: "check-0000000000000000"},
		"an id, no approval": {Done: 9000, ApprovedQID: qid},
	} {
		in.Facts["release"] = fact
		if r := derive1(t, f, in, "release"); r.State != Answered || r.Reason != why {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// A plan reload that changes the check un-does it (same task id).
	in.Facts["release"] = Fact{Done: 9000, Approved: "77-0", ApprovedQID: qid}
	f2 := File{Plan: "v1", Tasks: []Task{task("release", person, func(t *Task) { t.Check = "open busmon, the tab shows v2" })}}
	if r := derive1(t, f2, in, "release"); r.State == Done {
		t.Fatalf("a changed check still done: %+v", r)
	}
	// The board path: delivered, no fact, is not done with a check.
	delete(in.Facts, "release")
	withBoard := tk
	withBoard.Board = "release-impl"
	f3 := File{Plan: "v1", Tasks: []Task{withBoard}}
	in.Board["release-impl"] = deliveredEntry()
	if r := derive1(t, f3, in, "release"); r.State == Done {
		t.Fatalf("board-delivered, no fact: %+v", r)
	}
	// Without a check, both paths are as before.
	plain := task("release", person)
	plain.Board = "release-impl"
	if r := derive1(t, File{Plan: "v1", Tasks: []Task{plain}}, in, "release"); r.State != Done {
		t.Fatalf("no check, delivered: %+v", r)
	}
	in.Facts["release"] = Fact{Done: 9000}
	if r := derive1(t, File{Plan: "v1", Tasks: []Task{task("release", person)}}, in, "release"); r.State != Done {
		t.Fatalf("no check, recorded: %+v", r)
	}
}

// §7.3: a mission task's check is its missions.json test, character for
// character; a differing test, a mission missing from the file, or a file
// that cannot be read is refused; a mission task without a check, or a plan
// with none, reads nothing.
func TestCheckMissions(t *testing.T) {
	mt := func(id, mission, check string) Task {
		return Task{ID: id, Title: id, Owner: "coder", Mission: mission, Check: check}
	}
	tests := map[string]map[string]string{"c1": {"a": "go test ./a", "b": "go test ./b"}}
	read := 0
	missions := func(change string) (map[string]string, error) {
		read++
		if m, ok := tests[change]; ok {
			return m, nil
		}
		return nil, errors.New("docs/changes/" + change + "/missions.json not found")
	}
	if err := CheckMissions(File{Tasks: []Task{mt("a", "c1/a", "go test ./a"), mt("b", "c1/b", "")}}, missions); err != nil {
		t.Fatalf("matching: %v", err)
	}
	for name, c := range map[string]struct {
		f    File
		want string
	}{
		"a differing test":   {File{Tasks: []Task{mt("a", "c1/a", "go test ./a -run X")}}, `task a: its check differs from mission c1/a's test "go test ./a"`},
		"a missing mission":  {File{Tasks: []Task{mt("z", "c1/z", "go test ./z")}}, "task z: mission c1/z is not in c1's missions.json"},
		"an unreadable file": {File{Tasks: []Task{mt("a", "c2/a", "go test ./a")}}, "task a: cannot read c2's missions.json"},
		"a trailing space":   {File{Tasks: []Task{mt("a", "c1/a", "go test ./a ")}}, "differs"},
	} {
		if err := CheckMissions(c.f, missions); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	read = 0
	if err := CheckMissions(File{Tasks: []Task{mt("b", "c2/b", ""), {ID: "x", Owner: "coder", Check: "go test"}}}, missions); err != nil || read != 0 {
		t.Errorf("no mission task with a check: %v, %d reads", err, read)
	}
}
