package health

import (
	"strings"
	"testing"
	"time"
)

var rqNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// rqIdle is a team idle for an hour, every pane seen not busy.
func rqIdle() ReviewQueueInput {
	at := rqNow.Add(-time.Hour)
	return ReviewQueueInput{Now: rqNow, After: 15 * time.Minute, Agents: []StallAgent{
		{Name: "master", State: "idle", DeclaredAt: at, Pane: PaneIdle},
		{Name: "coder", State: "idle", DeclaredAt: at, Pane: PaneIdle},
		{Name: "foureyes", State: "idle", DeclaredAt: at, Pane: PaneIdle},
		{Name: "reviewer-codex", State: "idle", DeclaredAt: at, Pane: PaneIdle},
	}}
}

func rqVerdict(subject, reviewer, decision, head, ref string, ago time.Duration) ReviewVerdict {
	return ReviewVerdict{Subject: subject, Author: "coder", Reviewer: reviewer, Decision: decision, Head: head, Ref: ref, At: rqNow.Add(-ago)}
}

func TestReviewQueueOffUnknownAndOk(t *testing.T) {
	half := func(in ReviewQueueInput) ReviewQueueInput {
		in.Verdicts = []ReviewVerdict{
			rqVerdict("s", "reviewer-codex", "approve", headA, "", 3*time.Hour),
			rqVerdict("s", "foureyes", "approve", headB, "", 2*time.Hour),
		}
		return in
	}
	cases := []struct {
		name string
		in   func() ReviewQueueInput
		want Status
		has  string
	}{
		{"off", func() ReviewQueueInput { in := half(rqIdle()); in.After = 0; return in }, Unknown, "off"},
		{"read error", func() ReviewQueueInput { in := half(rqIdle()); in.ReadErr = "gates unavailable"; return in }, Unknown, "gates unavailable"},
		{"no agent", func() ReviewQueueInput { in := half(rqIdle()); in.Agents = nil; return in }, Unknown, "no registered agent"},
		{"someone works", func() ReviewQueueInput { in := half(rqIdle()); in.Agents[1].State = "working"; return in }, OK, "coder working"},
		{"idle not long", func() ReviewQueueInput {
			in := half(rqIdle())
			in.Agents[2].DeclaredAt = rqNow.Add(-5 * time.Minute)
			return in
		}, OK, "idle only"},
		{"pane busy", func() ReviewQueueInput { in := half(rqIdle()); in.Agents[3].Pane = PaneBusy; return in }, OK, "busy"},
		{"pane unseen", func() ReviewQueueInput { in := half(rqIdle()); in.Agents[3].Pane = PaneUnseen; return in }, Unknown, "cannot see"},
		{"dormant ignored", func() ReviewQueueInput {
			in := half(rqIdle())
			in.Agents = append(in.Agents, StallAgent{Name: "old", State: "working", Dormant: true})
			return in
		}, Warn, "s"},
		{"other work open", func() ReviewQueueInput { in := half(rqIdle()); in.Open.Requests = 1; return in }, OK, "work is open"},
		{"gates do not count as open", func() ReviewQueueInput { in := half(rqIdle()); in.Open.Gates = 3; return in }, Warn, "s"},
		{"nothing owed", rqIdle, OK, "no review owed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := EvaluateReviewQueue(c.in())
			if out.Status != c.want || !strings.Contains(out.Detail, c.has) {
				t.Fatalf("got %s %q, want %s containing %q", out.Status, out.Detail, c.want, c.has)
			}
		})
	}
}

func TestReviewQueueHalfApproved(t *testing.T) {
	cases := []struct {
		name     string
		verdicts []ReviewVerdict
		gates    []ReviewGate
		chall    map[string][]string
		done     map[string]bool
		owed     bool
	}{
		{name: "other reviewer approved an older head", owed: true, verdicts: []ReviewVerdict{
			rqVerdict("s", "reviewer-codex", "approve", headA, "", 3*time.Hour),
			rqVerdict("s", "foureyes", "approve", headB, "", 2*time.Hour)}},
		{name: "other reviewer approved the same head", verdicts: []ReviewVerdict{
			rqVerdict("s", "reviewer-codex", "approve", headB, "", 3*time.Hour),
			rqVerdict("s", "foureyes", "approve", headB, "", 2*time.Hour)}},
		{name: "an approve with no head settles", verdicts: []ReviewVerdict{
			rqVerdict("s", "reviewer-codex", "approve", "", "", 3*time.Hour),
			rqVerdict("s", "foureyes", "approve", headB, "", 2*time.Hour)}},
		{name: "decider with no head settles every approve", verdicts: []ReviewVerdict{
			rqVerdict("s", "reviewer-codex", "approve", headA, "", 3*time.Hour),
			rqVerdict("s", "foureyes", "approve", "", "", 2*time.Hour)}},
		{name: "other reviewer's reject before the approve", owed: true, verdicts: []ReviewVerdict{
			rqVerdict("s", "reviewer-codex", "reject", headA, "", 3*time.Hour),
			rqVerdict("s", "foureyes", "approve", headB, "", 2*time.Hour)}},
		{name: "its later approve settles its reject", verdicts: []ReviewVerdict{
			rqVerdict("s", "reviewer-codex", "reject", headA, "", 4*time.Hour),
			rqVerdict("s", "reviewer-codex", "approve", headB, "", 3*time.Hour),
			rqVerdict("s", "foureyes", "approve", headB, "", 2*time.Hour)}},
		{name: "challenged under the decider's ref, no verdict", owed: true,
			chall:    map[string][]string{"r1": {"foureyes", "reviewer-codex"}},
			verdicts: []ReviewVerdict{rqVerdict("s", "foureyes", "approve", headB, "r1", 2*time.Hour)}},
		{name: "challenged on a ref of another subject", chall: map[string][]string{"r1": {"reviewer-codex"}, "r2": {"foureyes"}},
			verdicts: []ReviewVerdict{
				rqVerdict("other", "reviewer-codex", "approve", headA, "r1", 5*time.Hour),
				rqVerdict("s", "foureyes", "approve", headB, "r2", 2*time.Hour)}},
		{name: "one reviewer only", verdicts: []ReviewVerdict{rqVerdict("s", "foureyes", "approve", headB, "", 2*time.Hour)}},
		{name: "rejected subject is not half-approved", verdicts: []ReviewVerdict{
			rqVerdict("s", "foureyes", "approve", headB, "", 3*time.Hour),
			rqVerdict("s", "reviewer-codex", "reject", headB, "", 2*time.Hour)}},
		{name: "self-approval is not a decider", verdicts: []ReviewVerdict{
			rqVerdict("s", "reviewer-codex", "approve", headA, "", 3*time.Hour),
			{Subject: "s", Author: "coder", Reviewer: "coder", Decision: "approve", Head: headB, At: rqNow.Add(-2 * time.Hour)}}},
		{name: "older than the window", verdicts: []ReviewVerdict{
			rqVerdict("s", "reviewer-codex", "approve", headA, "", 80*time.Hour),
			rqVerdict("s", "foureyes", "approve", headB, "", 73*time.Hour)}},
		{name: "plan done", done: map[string]bool{"s": true}, verdicts: []ReviewVerdict{
			rqVerdict("s", "reviewer-codex", "approve", headA, "", 3*time.Hour),
			rqVerdict("s", "foureyes", "approve", headB, "", 2*time.Hour)}},
		{name: "an open gate on the subject is case (a), not (b)",
			gates: []ReviewGate{{Reviewer: "reviewer-codex", Ref: "r9", Subject: "s", At: rqNow.Add(-5 * time.Minute), Delivered: true}},
			verdicts: []ReviewVerdict{
				rqVerdict("s", "reviewer-codex", "approve", headA, "", 3*time.Hour),
				rqVerdict("s", "foureyes", "approve", headB, "", 2*time.Hour)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := rqIdle()
			in.Verdicts, in.Gates, in.Challenges, in.DoneSubjects = c.verdicts, c.gates, c.chall, c.done
			out := EvaluateReviewQueue(in)
			if got := out.Status == Warn; got != c.owed {
				t.Fatalf("owed = %v (%s %q), want %v", got, out.Status, out.Detail, c.owed)
			}
			if c.owed && !strings.Contains(out.Detail, "s approved by foureyes, not by reviewer-codex") {
				t.Fatalf("detail %q", out.Detail)
			}
		})
	}
}

func TestReviewQueueOwedGate(t *testing.T) {
	gate := ReviewGate{Reviewer: "reviewer-codex", Ref: "r1", Subject: "focus-notice", At: rqNow.Add(-40 * time.Minute), Delivered: true}
	cases := []struct {
		name string
		edit func(*ReviewGate, *ReviewQueueInput)
		owed bool
	}{
		{"delivered and old", func(*ReviewGate, *ReviewQueueInput) {}, true},
		{"not delivered: the dispatcher's", func(g *ReviewGate, _ *ReviewQueueInput) { g.Delivered = false }, false},
		{"younger than stall_after", func(g *ReviewGate, _ *ReviewQueueInput) { g.At = rqNow.Add(-5 * time.Minute) }, false},
		{"reviewer dormant", func(_ *ReviewGate, in *ReviewQueueInput) { in.Agents[3].Dormant = true }, false},
		{"no subject names the ref", func(g *ReviewGate, _ *ReviewQueueInput) { g.Subject = "" }, true},
		{"delivery unknown is never owed", func(g *ReviewGate, _ *ReviewQueueInput) { g.Delivered, g.DeliveryUnknown = false, true }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, g := rqIdle(), gate
			c.edit(&g, &in)
			in.Gates = []ReviewGate{g}
			out := EvaluateReviewQueue(in)
			if got := out.Status == Warn; got != c.owed {
				t.Fatalf("owed = %v (%s %q), want %v", got, out.Status, out.Detail, c.owed)
			}
			if !c.owed {
				return
			}
			name := g.Subject
			if name == "" {
				name = "gate " + g.Ref
			}
			if want := "reviewer-codex owes " + name + " (40m)"; !strings.Contains(out.Detail, want) {
				t.Fatalf("detail %q, want %q", out.Detail, want)
			}
		})
	}
}

func TestReviewQueueWakes(t *testing.T) {
	in := rqIdle()
	in.Verdicts = []ReviewVerdict{
		rqVerdict("s", "reviewer-codex", "approve", headA, "", 3*time.Hour),
		rqVerdict("s", "foureyes", "approve", headB, "", 2*time.Hour)}
	w := ReviewQueueWakes(in, "master")
	if len(w) != 1 || w[0].Target != "master" || w[0].Mark != "stall-review" || w[0].Episode == "" ||
		!strings.Contains(w[0].Text, "s approved by foureyes, not by reviewer-codex") {
		t.Fatalf("wakes %+v", w)
	}
	again := ReviewQueueWakes(in, "master")
	if again[0].Episode != w[0].Episode {
		t.Fatal("the same owed set must keep its episode")
	}
	in.Gates = []ReviewGate{{Reviewer: "foureyes", Ref: "r7", Subject: "t", At: rqNow.Add(-time.Hour), Delivered: true}}
	if more := ReviewQueueWakes(in, "master"); more[0].Episode == w[0].Episode {
		t.Fatal("a new owed review must be a new episode")
	}
	in.Agents[0].State = "working"
	if none := ReviewQueueWakes(in, "master"); len(none) != 0 {
		t.Fatalf("no wake unless the check warns: %+v", none)
	}
}

// A gate whose delivery cannot be read is not judged: unknown when nothing
// is owed, named beside what is owed otherwise (reviewer-codex).
func TestReviewQueueUnjudgedGate(t *testing.T) {
	in := rqIdle()
	in.Gates = []ReviewGate{{Reviewer: "reviewer-codex", Ref: "r1", Subject: "x", DeliveryUnknown: true}}
	out := EvaluateReviewQueue(in)
	if out.Status != Unknown || !strings.Contains(out.Detail, "not judged: reviewer-codex's gate r1 (challenge no longer retained)") {
		t.Fatalf("unjudged alone: %s %q", out.Status, out.Detail)
	}
	if w := ReviewQueueWakes(in, "master"); len(w) != 0 {
		t.Fatalf("an unjudged gate woke master: %+v", w)
	}
	in.Verdicts = []ReviewVerdict{
		rqVerdict("s", "reviewer-codex", "approve", headA, "", 3*time.Hour),
		rqVerdict("s", "foureyes", "approve", headB, "", 2*time.Hour)}
	out = EvaluateReviewQueue(in)
	if out.Status != Warn || !strings.Contains(out.Detail, "s approved by foureyes") || !strings.Contains(out.Detail, "not judged: reviewer-codex's gate r1") {
		t.Fatalf("owed and unjudged: %s %q", out.Status, out.Detail)
	}
}
