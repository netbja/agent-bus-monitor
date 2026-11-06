package bus

import "testing"

// stale-waiting §3: a waiting the person answered, with no open question,
// reads as answered from the answer's time; nothing else proves it.
func TestWaitAnswered(t *testing.T) {
	const decl = int64(1000)
	answered := func(id, from string, at int64, withdrawn bool) Ask {
		return Ask{ID: id, From: from, State: "answered", CreatedAt: 900, Questions: []AskQuestionState{
			{AskQuestion: AskQuestion{ID: "q1"}, Answer: &AskAnswer{By: "charlie", At: at, Withdrawn: withdrawn}},
		}}
	}
	human := func(id, target, typ, ref string) Event {
		return Event{Kind: "cmd", ID: id, From: "charlie", Target: target, Type: typ, Ref: ref, Human: true}
	}
	cases := []struct {
		name string
		open int
		asks []Ask
		cmds []Event
		want int64
	}{
		{"nothing proves it", 0, nil, nil, 0},
		{"an answered ask after the declaration", 0, []Ask{answered("900-0", "master", 1500, false)}, nil, 1500},
		{"an answered ask before it: asking again", 0, []Ask{answered("900-0", "master", 999, false)}, nil, 0},
		{"the same millisecond", 0, []Ask{answered("900-0", "master", 1000, false)}, nil, 0},
		{"another agent's ask", 0, []Ask{answered("900-0", "coder", 1500, false)}, nil, 0},
		{"an open question left", 1, []Ask{answered("900-0", "master", 1500, false)}, nil, 0},
		{"withdrawn only", 0, []Ask{answered("900-0", "master", 1500, true)}, nil, 0},
		{"expired", 0, []Ask{{ID: "900-0", From: "master", State: "expired"}}, nil, 0},
		{"replaced", 0, []Ask{func() Ask { a := answered("900-0", "master", 1500, false); a.State = "replaced"; return a }()}, nil, 0},
		{"the latest person answer of an ask", 0, []Ask{{ID: "900-0", From: "master", State: "answered", Questions: []AskQuestionState{
			{AskQuestion: AskQuestion{ID: "q1"}, Answer: &AskAnswer{By: "charlie", At: 1200}},
			{AskQuestion: AskQuestion{ID: "q2"}, Answer: &AskAnswer{By: "charlie", At: 1700}},
			{AskQuestion: AskQuestion{ID: "q3"}, Answer: &AskAnswer{By: "master", At: 1900, Withdrawn: true}},
		}}}, nil, 1700},
		{"a human message to the agent", 0, nil, []Event{human("1300-0", "master", CmdDirective, "")}, 1300},
		{"a human reply on the agent's thread", 0, nil, []Event{
			{Kind: "cmd", ID: "950-0", From: "master", Target: "coder", Type: CmdDirective},
			human("1400-0", "coder", CmdReply, "950-0"),
		}, 1400},
		{"a human reply on another thread", 0, nil, []Event{human("1400-0", "coder", CmdReply, "950-0")}, 0},
		{"an agent's message is no answer", 0, nil, []Event{{Kind: "cmd", ID: "1300-0", From: "coder", Target: "master", Type: CmdDirective}}, 0},
		{"a human message before the declaration", 0, nil, []Event{human("999-0", "master", CmdDirective, "")}, 0},
		{"the earliest of two proofs", 0, []Ask{answered("900-0", "master", 1500, false)},
			[]Event{human("1300-0", "master", CmdDirective, "")}, 1300},
		{"the reply trimmed, the ask kept", 0, []Ask{answered("900-0", "master", 1600, false)},
			[]Event{human("1800-0", "master", CmdDirective, "")}, 1600},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := WaitAnswered("master", decl, c.open, c.asks, c.cmds); got != c.want {
				t.Fatalf("WaitAnswered = %d, want %d", got, c.want)
			}
		})
	}
}

// OpenQuestions counts an agent's open questions across its open asks.
func TestOpenQuestionsOf(t *testing.T) {
	asks := []Ask{
		{ID: "1-0", From: "master", State: "open", Questions: []AskQuestionState{{}, {Answer: &AskAnswer{}}}},
		{ID: "2-0", From: "coder", State: "open", Questions: []AskQuestionState{{}}},
		{ID: "3-0", From: "master", State: "answered", Questions: []AskQuestionState{{Answer: &AskAnswer{}}}},
	}
	if n := OpenQuestionsOf("master", asks); n != 1 {
		t.Fatalf("master %d, want 1", n)
	}
}
