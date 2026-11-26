package health

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// board-drift §8: the answer guard's judgement, on the stall guard's seam.
func answerInput(now time.Time, cmds ...AnswerCmd) PersonUnansweredInput {
	return PersonUnansweredInput{Now: now, After: 10 * time.Minute, Person: "charlie", Cmds: cmds,
		Agents: []AnswerAgent{{Name: "coder", State: "idle"}, {Name: "docs", State: "working", NoTerminal: true}}}
}

func answerID(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) + "-0" }

func personSaid(at time.Time, target, text string) AnswerCmd {
	return AnswerCmd{ID: answerID(at), From: "charlie", Target: target, Type: "directive", Human: true, Typed: true, Text: text}
}

func TestPersonUnansweredWarnsAndWakesOnce(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	d := personSaid(now.Add(-11*time.Minute), "coder", "please rebase")
	in := answerInput(now, d)
	o := EvaluatePersonUnanswered(in)
	if o.Status != Warn || !strings.Contains(o.Detail, "coder: the person's directive "+d.ID+" unanswered for 11m") {
		t.Fatalf("outcome %+v", o)
	}
	w := PersonUnansweredWakes(in)
	want := `Answer guard: the person's directive ` + d.ID + ` ("please rebase") has no answer from you after 11m. Answer on the bus: agentbus report coder --to charlie "…", or agentbus reply --ref ` + d.ID + ` charlie "…".`
	if len(w) != 1 || w[0].Mark != "answer-wake:coder" || w[0].Episode != d.ID || w[0].Target != "coder" || w[0].Text != want {
		t.Fatalf("wakes %+v", w)
	}
	if n := PersonUnansweredNotices(in); len(n) != 0 {
		t.Fatalf("the person told before twice answer_after: %+v", n)
	}
	// Under answer_after: nothing yet.
	early := answerInput(now, personSaid(now.Add(-9*time.Minute), "coder", "x"))
	if o := EvaluatePersonUnanswered(early); o.Status != OK || len(PersonUnansweredWakes(early)) != 0 {
		t.Fatalf("early %+v", o)
	}
}

func TestPersonUnansweredTellsThePersonAtTwice(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	long := strings.Repeat("x", 100)
	d := personSaid(now.Add(-21*time.Minute), "docs", long)
	in := answerInput(now, d)
	n := PersonUnansweredNotices(in)
	want := `Answer guard: docs has not answered your directive ` + d.ID + ` ("` + strings.Repeat("x", 80) + `…") for 21m (working; no terminal). Look at its pane in busmon.`
	if len(n) != 1 || n[0].Mark != "answer-person:docs" || n[0].Episode != d.ID || n[0].Text != want {
		t.Fatalf("notices %+v", n)
	}
	if len(PersonUnansweredWakes(in)) != 1 {
		t.Fatal("the wake is still owed (once-only by its mark)")
	}
	// The oldest unanswered directive is the one judged.
	in2 := answerInput(now, d, personSaid(now.Add(-12*time.Minute), "docs", "newer"))
	if w := PersonUnansweredWakes(in2); len(w) != 1 || w[0].Episode != d.ID {
		t.Fatalf("judged %+v", w)
	}
}

func TestPersonUnansweredClearedByAnAnswer(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	d1 := personSaid(now.Add(-30*time.Minute), "coder", "one")
	d2 := personSaid(now.Add(-25*time.Minute), "coder", "two")
	for name, answer := range map[string]AnswerCmd{
		"reply":  {ID: answerID(now.Add(-20 * time.Minute)), From: "coder", Target: "charlie", Type: "reply"},
		"report": {ID: answerID(now.Add(-20 * time.Minute)), From: "coder", Target: "charlie", Type: "directive"},
	} {
		in := answerInput(now, d1, d2, answer)
		if o := EvaluatePersonUnanswered(in); o.Status != OK || len(PersonUnansweredWakes(in)) != 0 || len(PersonUnansweredNotices(in)) != 0 {
			t.Errorf("%s: one answer covers every earlier directive: %+v", name, o)
		}
	}
	// An answer older than the directive answers nothing; one to someone else neither.
	old := AnswerCmd{ID: answerID(now.Add(-40 * time.Minute)), From: "coder", Target: "charlie", Type: "reply"}
	other := AnswerCmd{ID: answerID(now.Add(-5 * time.Minute)), From: "coder", Target: "master", Type: "directive"}
	in := answerInput(now, old, d1, other)
	if w := PersonUnansweredWakes(in); len(w) != 1 || w[0].Episode != d1.ID {
		t.Fatalf("wakes %+v", w)
	}
	// A directive after the answer is judged on its own.
	ans := AnswerCmd{ID: answerID(now.Add(-28 * time.Minute)), From: "coder", Target: "charlie", Type: "reply"}
	in = answerInput(now, d1, ans, d2)
	if w := PersonUnansweredWakes(in); len(w) != 1 || w[0].Episode != d2.ID {
		t.Fatalf("after an answer %+v", w)
	}
}

func TestPersonUnansweredJudgesOnlyThePersonsTypedDirectives(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	at := now.Add(-30 * time.Minute)
	for name, c := range map[string]AnswerCmd{
		"composed (human, not typed)": {ID: answerID(at), From: "charlie", Target: "coder", Type: "directive", Human: true},
		"wake=no":                     {ID: answerID(at), From: "charlie", Target: "coder", Type: "directive", Human: true, Typed: true, NoWake: true},
		"another sender":              {ID: answerID(at), From: "master", Target: "coder", Type: "directive", Human: true, Typed: true},
		"a reply":                     {ID: answerID(at), From: "charlie", Target: "coder", Type: "reply", Human: true, Typed: true},
		"not human":                   {ID: answerID(at), From: "charlie", Target: "coder", Type: "directive", Typed: true},
		"to no agent":                 {ID: answerID(at), From: "charlie", Target: "ghost", Type: "directive", Human: true, Typed: true},
	} {
		in := answerInput(now, c)
		if o := EvaluatePersonUnanswered(in); o.Status != OK || len(PersonUnansweredWakes(in)) != 0 {
			t.Errorf("%s judged: %+v", name, o)
		}
	}
}

func TestPersonUnansweredOffAndUnread(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	d := personSaid(now.Add(-30*time.Minute), "coder", "x")
	in := answerInput(now, d)
	in.After = 0
	if o := EvaluatePersonUnanswered(in); o.Status != Unknown || o.Detail != "off (answer_after = 0)" || len(PersonUnansweredWakes(in)) != 0 || len(PersonUnansweredNotices(in)) != 0 {
		t.Fatalf("off %+v", o)
	}
	in = answerInput(now, d)
	in.ReadErr = "cmd history unavailable"
	if o := EvaluatePersonUnanswered(in); o.Status != Unknown || len(PersonUnansweredWakes(in)) != 0 || len(PersonUnansweredNotices(in)) != 0 {
		t.Fatalf("unread %+v", o)
	}
	in = answerInput(now, d)
	in.Person = ""
	if o := EvaluatePersonUnanswered(in); o.Status != Unknown || len(PersonUnansweredWakes(in)) != 0 {
		t.Fatalf("no person %+v", o)
	}
}
