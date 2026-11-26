package health

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The answer guard (board-drift §8, check person-unanswered): an agent
// answers every directive the person typed to it on the bus. For each
// agent, the oldest such directive after its last answer to the person is
// judged: past After it warns and wakes the agent once; past twice After
// the person is told once. One answer covers every earlier directive.

// AnswerCmd is one retained cmd entry as the answer guard reads it.
type AnswerCmd struct {
	ID, From, Target, Type, Text string
	Human, Typed, NoWake         bool
}

// AnswerAgent is one agent: its state, and whether its terminal is gone.
type AnswerAgent struct {
	Name, State string
	NoTerminal  bool
}

// PersonUnansweredInput is everything the answer guard judges from.
type PersonUnansweredInput struct {
	Now     time.Time
	After   time.Duration // dispatch.answer_after; 0 = off
	ReadErr string
	Person  string      // identity.as: whose directives are judged
	Cmds    []AnswerCmd // in stream order, oldest first
	Agents  []AnswerAgent
}

// answerHit is one agent owing an answer: the directive judged, and its age.
type answerHit struct {
	AnswerAgent
	D   AnswerCmd
	Age time.Duration
}

// personDirective: c is a directive the person typed, which wakes.
func personDirective(c AnswerCmd, person string) bool {
	return c.Type == "directive" && c.Human && c.Typed && !c.NoWake && c.From == person
}

// unanswered is the answer guard's judgement per agent, those past After.
func unanswered(in PersonUnansweredInput) []answerHit {
	if in.After <= 0 || in.ReadErr != "" {
		return nil // no person: personDirective matches nothing
	}
	var hits []answerHit
	for _, a := range in.Agents {
		lastAnswer := ""
		for _, c := range in.Cmds {
			if c.From == a.Name && c.Target == in.Person && (c.Type == "reply" || c.Type == "directive") {
				lastAnswer = c.ID // the newest: Cmds are in stream order
			}
		}
		var d AnswerCmd
		found := false
		for _, c := range in.Cmds {
			if c.Target == a.Name && personDirective(c, in.Person) && streamIDLess(lastAnswer, c.ID) {
				d, found = c, true // the oldest after the last answer
				break
			}
		}
		if !found {
			continue
		}
		age := in.Now.Sub(time.UnixMilli(streamMillis(d.ID)))
		if age < in.After {
			continue
		}
		hits = append(hits, answerHit{AnswerAgent: a, D: d, Age: age})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Name < hits[j].Name })
	return hits
}

// EvaluatePersonUnanswered warns while an agent owes the person an answer
// past After; it never fails.
func EvaluatePersonUnanswered(in PersonUnansweredInput) Outcome {
	switch {
	case in.After <= 0:
		return Outcome{Status: Unknown, Detail: "off (answer_after = 0)"}
	case in.ReadErr != "":
		return Outcome{Status: Unknown, Detail: truncateDetail(in.ReadErr)}
	case in.Person == "":
		return Outcome{Status: Unknown, Detail: "no person recorded (identity.as)"}
	}
	var owed []string
	for _, h := range unanswered(in) {
		owed = append(owed, fmt.Sprintf("%s: the person's directive %s unanswered for %s", h.Name, h.D.ID, minutes(h.Age)))
	}
	if len(owed) == 0 {
		return Outcome{Status: OK}
	}
	return Outcome{Status: Warn, Detail: truncateDetail(strings.Join(owed, "; "))}
}

// quoteStart is a directive's first 80 characters, … when cut.
func quoteStart(text string) string {
	r := []rune(text)
	if len(r) > 80 {
		return string(r[:80]) + "…"
	}
	return text
}

// PersonUnansweredWakes: each agent owing an answer is woken once per
// directive (mark answer-wake:<agent>, episode its id).
func PersonUnansweredWakes(in PersonUnansweredInput) []StallWake {
	var out []StallWake
	for _, h := range unanswered(in) {
		out = append(out, StallWake{Mark: "answer-wake:" + h.Name, Episode: h.D.ID, Target: h.Name, Text: fmt.Sprintf(
			"Answer guard: the person's directive %s (%q) has no answer from you after %s. Answer on the bus: agentbus report %s --to %s \"…\", or agentbus reply --ref %s %s \"…\".",
			h.D.ID, quoteStart(h.D.Text), minutes(h.Age), h.Name, in.Person, h.D.ID, in.Person)})
	}
	return out
}

// PersonUnansweredNotices: past twice After, the person is told once per
// directive (mark answer-person:<agent>); Target is unused.
func PersonUnansweredNotices(in PersonUnansweredInput) []StallWake {
	var out []StallWake
	for _, h := range unanswered(in) {
		if h.Age < 2*in.After {
			continue
		}
		state := h.State
		if h.NoTerminal {
			state += "; no terminal"
		}
		out = append(out, StallWake{Mark: "answer-person:" + h.Name, Episode: h.D.ID, Text: fmt.Sprintf(
			"Answer guard: %s has not answered your directive %s (%q) for %s (%s). Look at its pane in busmon.",
			h.Name, h.D.ID, quoteStart(h.D.Text), minutes(h.Age), state)})
	}
	return out
}
