package plan

import (
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/mission"
)

func task(id string, mods ...func(*Task)) Task {
	t := Task{ID: id, Title: id, Owner: "coder", Board: id + "-impl"}
	for _, m := range mods {
		m(&t)
	}
	return t
}

func noMerge(t *Task) { f := false; t.Merge = &f }
func person(t *Task)  { t.Owner = Person; t.Board = "" }
func after(ids ...string) func(*Task) {
	return func(t *Task) { t.After = ids }
}

func entry(state string, req *bus.RequestInfo) bus.BoardEntry {
	return bus.BoardEntry{Owner: "coder", State: state, Updated: 100, Request: req}
}

func deliveredEntry() bus.BoardEntry {
	return entry("done", &bus.RequestInfo{ID: "1-0", AcceptedAt: 1000, RespondedAt: 2000})
}

func verdict(id, subject, decision string) bus.Verdict {
	return bus.Verdict{ID: id, Subject: subject, Author: "coder", Reviewer: "foureyes", Decision: decision}
}

func ok() Inputs {
	return Inputs{Board: map[string]bus.BoardEntry{}, Facts: map[string]Fact{}, Verdicts: map[string][]bus.Verdict{},
		Missions: map[string]mission.State{}, BoardOK: true, FactsOK: true, VerdictsOK: true, AsksOK: true, MissionsOK: true}
}

func derive1(t *testing.T, f File, in Inputs, id string) Row {
	t.Helper()
	for _, r := range Derive(f, in) {
		if r.Task.ID == id {
			return r
		}
	}
	t.Fatalf("no row %s", id)
	return Row{}
}

func TestDeriveTheTeamsRealCases(t *testing.T) {
	roles := task("roles")
	f := File{Plan: "p", Tasks: []Task{roles}}

	in := ok()
	in.Board["roles-impl"] = deliveredEntry()
	if r := derive1(t, f, in, "roles"); r.State != InReview || r.Verdict != NoVerdict {
		t.Fatalf("a delivered task with no verdict: %+v", r)
	}

	in.Verdicts["roles-impl"] = []bus.Verdict{verdict("5-0", "roles-impl", "approve")}
	if r := derive1(t, f, in, "roles"); r.State != Waiting || r.Verdict != Approved {
		t.Fatalf("approved and not merged waits for the person: %+v", r)
	}

	in.Facts["roles"] = Fact{Done: 9000}
	if r := derive1(t, f, in, "roles"); r.State != Done || !r.Recorded {
		t.Fatalf("a recorded merge is done, as a recorded claim: %+v", r)
	}

	delete(in.Facts, "roles")
	in.Facts["roles"] = Fact{PR: 102}
	in.Verdicts["pr:102"] = []bus.Verdict{verdict("6-0", "pr:102", "reject")}
	if r := derive1(t, f, in, "roles"); r.State != InReview || r.Verdict != ChangesAsked || r.PR != 102 {
		t.Fatalf("approve on the board subject then reject on the PR: %+v", r)
	}
}

func TestDeriveNoMergeTask(t *testing.T) {
	spec := task("spec", noMerge)
	f := File{Plan: "p", Tasks: []Task{spec}}
	in := ok()
	in.Board["spec-impl"] = deliveredEntry()
	if r := derive1(t, f, in, "spec"); r.State != InReview {
		t.Fatalf("delivered, unreviewed: %+v", r)
	}
	in.Verdicts["spec-impl"] = []bus.Verdict{verdict("5-0", "spec-impl", "approve")}
	if r := derive1(t, f, in, "spec"); r.State != Done || r.Recorded {
		t.Fatalf("merge:false, delivered and approved is done: %+v", r)
	}
}

func TestDeriveAsksByFieldOnly(t *testing.T) {
	f := File{Plan: "p", Tasks: []Task{task("roles")}}
	in := ok()
	in.Asks = []bus.Ask{{ID: "7-0", From: "master", Title: "about roles and release", State: "open",
		Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "q", Context: "roles"}}}}}
	if r := derive1(t, f, in, "roles"); r.State != Upcoming {
		t.Fatalf("an ask mentioning the id in its text: %+v", r)
	}
	in.Asks[0].Task = "roles"
	if r := derive1(t, f, in, "roles"); r.State != Waiting || r.Ask != "7-0" {
		t.Fatalf("an open ask with task=roles: %+v", r)
	}
	in.Asks[0].State = "answered"
	if r := derive1(t, f, in, "roles"); r.State != Upcoming {
		t.Fatalf("an answered ask: %+v", r)
	}
}

func TestDeriveOrder(t *testing.T) {
	f := File{Plan: "p", Tasks: []Task{task("roles")}}
	in := ok()
	in.Board["roles-impl"] = entry("blocked", &bus.RequestInfo{ID: "1-0", BlockedReason: "waits for a decision"})
	if r := derive1(t, f, in, "roles"); r.State != Blocked || r.Reason != "waits for a decision" {
		t.Fatalf("blocked: %+v", r)
	}
	in.Verdicts["roles-impl"] = []bus.Verdict{verdict("5-0", "roles-impl", "approve")}
	if r := derive1(t, f, in, "roles"); r.State != Waiting {
		t.Fatalf("approved, unrecorded and also blocked: first match is waiting for you, got %+v", r)
	}
}

func TestDeriveProgressAndUpcoming(t *testing.T) {
	f := File{Plan: "p", Tasks: []Task{task("a"), task("b", after("a")), task("release", person, after("a", "b"))}}
	in := ok()
	if r := derive1(t, f, in, "b"); r.State != Upcoming || len(r.WaitingOn) != 1 || r.WaitingOn[0] != "a" {
		t.Fatalf("b waits on a: %+v", r)
	}
	if r := derive1(t, f, in, "release"); r.State != Upcoming {
		t.Fatalf("release waits: %+v", r)
	}
	in.Board["a-impl"] = entry("accepted", &bus.RequestInfo{ID: "1-0", AcceptedAt: 1})
	if r := derive1(t, f, in, "a"); r.State != InProgress {
		t.Fatalf("accepted: %+v", r)
	}
	in.Board["a-impl"] = entry("working", nil)
	if r := derive1(t, f, in, "a"); r.State != InProgress {
		t.Fatalf("claimed (working): %+v", r)
	}
	in.Board["a-impl"] = entry("review", nil)
	if r := derive1(t, f, in, "a"); r.State != InReview {
		t.Fatalf("board state review: %+v", r)
	}
	in.Facts["a"] = Fact{Done: 1}
	in.Facts["b"] = Fact{Done: 2}
	if r := derive1(t, f, in, "release"); r.State != Waiting {
		t.Fatalf("a person's task whose after are done waits for the person: %+v", r)
	}
	in.Facts["release"] = Fact{Done: 3}
	if r := derive1(t, f, in, "release"); r.State != Done {
		t.Fatalf("recorded: %+v", r)
	}
}

func TestDeriveMissions(t *testing.T) {
	want := map[mission.State]State{
		mission.Pending: Upcoming, mission.Ready: Upcoming,
		mission.Requested: InProgress, mission.Accepted: InProgress, mission.Delivered: InProgress, mission.Checking: InProgress,
		mission.InReview: InReview, mission.Approved: InReview, mission.Integrating: InReview,
		mission.Integrated: Done, mission.Blocked: Blocked, mission.Aborted: Done,
	}
	f := File{Plan: "p", Tasks: []Task{{ID: "m", Title: "m", Owner: "coder", Mission: "c/m"}}}
	for _, s := range mission.AllStates {
		in := ok()
		in.Missions["c/m"] = s
		r := derive1(t, f, in, "m")
		if r.State != want[s] {
			t.Errorf("mission %s: got %s want %s", s, r.State, want[s])
		}
		if s == mission.Aborted && r.Reason != "aborted" {
			t.Errorf("aborted reads done with the note aborted: %+v", r)
		}
	}
	in := ok()
	if r := derive1(t, f, in, "m"); r.State != Upcoming || r.Reason != "mission not loaded" {
		t.Fatalf("a mission not loaded: %+v", r)
	}
}

func TestDeriveFailedReadsAreUnknown(t *testing.T) {
	f := File{Plan: "p", Tasks: []Task{task("a"), {ID: "m", Title: "m", Owner: "coder", Mission: "c/m"}}}
	for name, in := range map[string]func(*Inputs){
		"board":    func(in *Inputs) { in.BoardOK = false },
		"facts":    func(in *Inputs) { in.FactsOK = false },
		"verdicts": func(in *Inputs) { in.VerdictsOK = false },
		"asks":     func(in *Inputs) { in.AsksOK = false },
	} {
		i := ok()
		in(&i)
		if r := derive1(t, f, i, "a"); r.State != Unknown {
			t.Errorf("%s never read: %+v", name, r)
		}
	}
	i := ok()
	i.MissionsOK = false
	if r := derive1(t, f, i, "m"); r.State != Unknown {
		t.Errorf("missions never read: %+v", r)
	}
	if r := derive1(t, f, i, "a"); r.State == Unknown {
		t.Errorf("a failed missions read does not touch a board task: %+v", r)
	}
	i = ok()
	i.Facts["a"] = Fact{Done: 1}
	i.BoardOK = false
	if r := derive1(t, f, i, "a"); r.State != Done {
		t.Errorf("a recorded done needs only the facts: %+v", r)
	}
}

func TestDeriveMissingDataIsNeverDone(t *testing.T) {
	f := File{Plan: "p", Tasks: []Task{task("a"), task("p", person)}}
	in := ok()
	for _, r := range Derive(f, in) {
		if r.State == Done {
			t.Fatalf("missing data read as done: %+v", r)
		}
	}
}
