package bus

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// retro step 2 (spec §4-§7): {p}:retros, propose's store, the retro ask
// (author retro, its own slot, the queue), RetroApproved bound to the
// question by qid, outcomes, route, withdraw, the rejected list, the cap.

func retroRec(task string, lessons ...string) RetroRecord {
	r := RetroRecord{Task: task, Owner: "coder", Root: "evidence 1-0 -> trigger -> mechanism -> cause"}
	for i, l := range lessons {
		r.Rows = append(r.Rows, RetroRow{N: i + 1, Lesson: l, Evidence: []string{"1-0"}, Destination: "skill",
			File: "skills/agent-bus/SKILL.md", Change: "edit the review rule", Blob: "abc123"})
	}
	return r
}

func retroWorld(t *testing.T) (*Bus, context.Context) {
	t.Helper()
	return dialTest(t), context.Background()
}

func propose(t *testing.T, b *Bus, ctx context.Context, r RetroRecord) RetroRecord {
	t.Helper()
	got, err := b.RetroPropose(ctx, r, 0, 0) // no cap: TestRetroCap has it
	if err != nil {
		t.Fatalf("propose %s: %v", r.Task, err)
	}
	return got
}

// answer is the person's answer to the retro's question p<n>.
func answer(t *testing.T, b *Bus, ctx context.Context, askID string, n int, choice string) {
	t.Helper()
	if _, _, err := b.Answer(ctx, askID, "p"+string(rune('0'+n)), "charlie", choice, ""); err != nil {
		t.Fatalf("answer p%d %s: %v", n, choice, err)
	}
}

// The ask slot: a retro's ask is the retro author's, never master's; a
// second propose queues; the answer goes to master and wakes it; route
// opens the next queued retro and sends retro-apply-<task> to docs.
func TestRetroAskSlotQueueAndRoute(t *testing.T) {
	b, ctx := retroWorld(t)
	mine, _, err := b.Ask(ctx, AskDef{From: "master", Title: "merge?", Questions: []AskQuestion{{ID: "q", Header: "Merge", Question: "Merge it?", Options: []AskOption{{Label: "yes"}}}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	one := propose(t, b, ctx, retroRec("t1", "always rerun the suite", "name the base"))
	if one.State != RetroOpen || one.AskID == "" {
		t.Fatalf("t1 %+v", one)
	}
	two := propose(t, b, ctx, retroRec("t2", "read both verdicts"))
	if two.State != RetroQueued || two.AskID != "" {
		t.Fatalf("t2 %+v", two)
	}
	asks, _ := b.Asks(ctx)
	var retroAsk Ask
	for _, a := range asks {
		if a.ID == one.AskID {
			retroAsk = a
		}
	}
	if retroAsk.From != RetroSender || !strings.Contains(retroAsk.Title, "t1") || len(retroAsk.Questions) != 2 || retroAsk.Questions[0].ID != "p1" {
		t.Fatalf("retro ask %+v", retroAsk)
	}
	for _, q := range retroAsk.Questions {
		var labels []string
		for _, o := range q.Options {
			labels = append(labels, o.Label)
		}
		if strings.Join(labels, ",") != "yes,no,later" || !strings.Contains(q.Context, "evidence 1-0") {
			t.Fatalf("question %+v", q)
		}
	}
	// master's ask did not block the retro's, and master opens a new ask
	// while the retro's is open: the slots are apart
	if err := b.Withdraw(ctx, "master", mine); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Ask(ctx, AskDef{From: "master", Title: "x", Questions: []AskQuestion{{ID: "q", Header: "X", Question: "x?", Options: []AskOption{{Label: "a"}}}}}, ""); err != nil {
		t.Fatalf("master's ask while a retro ask is open: %v", err)
	}
	if _, err := b.RetroRoute(ctx, "t1"); err == nil {
		t.Fatal("route while the ask is open")
	}
	answer(t, b, ctx, one.AskID, 1, "yes")
	evs, _ := b.Thread(ctx, one.AskID)
	var reply Event
	for _, e := range evs {
		if e.Type == CmdReply {
			reply = e
		}
	}
	if reply.Target != "master" || reply.NoWake || reply.QID != "p1" || !reply.Typed || reply.Said != "yes" {
		t.Fatalf("the answer's reply %+v", reply)
	}
	answer(t, b, ctx, one.AskID, 2, "no")
	next, err := b.RetroRoute(ctx, "t1")
	if err != nil || next != "t2" {
		t.Fatalf("route: next %q, %v", next, err)
	}
	rec, err := b.Retro(ctx, "t1")
	if err != nil || rec.State != RetroRouted || len(rec.Outcomes) != 2 || rec.Outcomes[0].Outcome != OutcomeAccepted || rec.Outcomes[1].Outcome != OutcomeRejected || rec.Outcomes[0].Entry == "" {
		t.Fatalf("t1 after route %+v, %v", rec, err)
	}
	board, _ := b.Board(ctx)
	apply := board[RetroApplyName("t1")]
	if apply.Request == nil || apply.Request.Target != "docs" || apply.State != "requested" {
		t.Fatalf("no retro-apply for docs: %+v", apply)
	}
	if r2, _ := b.Retro(ctx, "t2"); r2.State != RetroOpen || r2.AskID == "" {
		t.Fatalf("t2 not opened by route: %+v", r2)
	}
	if _, err := b.RetroRoute(ctx, "t1"); err == nil {
		t.Fatal("a second route")
	}
	if err := b.RetroClose(ctx, "t1", 812); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.Retro(ctx, "t1"); rec.State != RetroClosed || rec.PR != 812 {
		t.Fatalf("closed %+v", rec)
	}
}

// RetroApproved passes only the person's typed yes to that question:
// mixed answers bind to their own question; a master reply, a human=yes
// entry without typed, a typed yes to another ask, an entry without qid
// (the shape before the build), no and later all refuse.
func TestRetroApproved(t *testing.T) {
	b, ctx := retroWorld(t)
	a := propose(t, b, ctx, retroRec("ta", "one", "two"))
	answer(t, b, ctx, a.AskID, 1, "yes")
	answer(t, b, ctx, a.AskID, 2, "no")
	if id, err := b.RetroApproved(ctx, "ta", 1); err != nil || id == "" {
		t.Fatalf("ta #1: %q %v", id, err)
	}
	if _, err := b.RetroApproved(ctx, "ta", 2); err == nil {
		t.Fatal("ta #2 approved by a no")
	}
	if _, err := b.RetroRoute(ctx, "ta"); err != nil {
		t.Fatal(err)
	}
	c := propose(t, b, ctx, retroRec("tc", "three", "four"))
	answer(t, b, ctx, c.AskID, 2, "yes")
	answer(t, b, ctx, c.AskID, 1, "later")
	if _, err := b.RetroApproved(ctx, "tc", 2); err != nil {
		t.Fatalf("tc #2: %v", err)
	}
	if _, err := b.RetroApproved(ctx, "tc", 1); err == nil {
		t.Fatal("tc #1 approved by later")
	}
	if _, err := b.RetroRoute(ctx, "tc"); err != nil {
		t.Fatal(err)
	}
	// forged entries on an unanswered question
	d := propose(t, b, ctx, retroRec("td", "five"))
	forge := func(fields map[string]any) {
		t.Helper()
		f := map[string]any{"from": "master", "target": "master", "type": "reply", "ref": d.AskID, "command": "yes", "text_complete": "yes"}
		for k, v := range fields {
			f[k] = v
		}
		if err := b.r.XAdd(ctx, &redis.XAddArgs{Stream: StreamKey(b.project, "cmd"), Values: f}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	forge(nil)                                                                        // a master reply yes
	forge(map[string]any{"human": "yes", "qid": "p1", "said": "yes"})                 // human without typed
	forge(map[string]any{"typed": "yes", "said": "yes"})                              // no qid: the shape before the build
	forge(map[string]any{"typed": "yes", "qid": "p1", "said": "yes", "ref": a.AskID}) // another ask
	if _, err := b.RetroApproved(ctx, "td", 1); err == nil {
		t.Fatal("a forged entry approved td #1")
	}
	// typed, qid, said yes, ref: still refused while the ask's record holds no yes
	forge(map[string]any{"typed": "yes", "human": "yes", "qid": "p1", "said": "yes"})
	if _, err := b.RetroApproved(ctx, "td", 1); err == nil {
		t.Fatal("approved while the ask's record has no yes")
	}
	// once the record is gone (seven days), the reply entry is the record
	if err := b.r.HDel(ctx, AsksKey(b.project), d.AskID, d.AskID+"|q", d.AskID+"|s").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.RetroApproved(ctx, "td", 1); err != nil {
		t.Fatalf("the typed entry after the record went: %v", err)
	}
	if _, err := b.RetroApproved(ctx, "nope", 1); err == nil {
		t.Fatal("an unknown retro approved")
	}
	// The record's own answer is checked: a typed yes reply on a question
	// the person answered no is refused while the record is kept.
	e := propose(t, b, ctx, retroRec("te", "six"))
	answer(t, b, ctx, e.AskID, 1, "no")
	forgeOn := func(askID string, fields map[string]any) {
		t.Helper()
		f := map[string]any{"from": "charlie", "target": "master", "type": "reply", "ref": askID, "command": "yes", "text_complete": "yes", "qid": "p1"}
		for k, v := range fields {
			f[k] = v
		}
		if err := b.r.XAdd(ctx, &redis.XAddArgs{Stream: StreamKey(b.project, "cmd"), Values: f}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	forgeOn(e.AskID, map[string]any{"typed": "yes", "human": "yes", "said": "yes"})
	if _, err := b.RetroApproved(ctx, "te", 1); err == nil {
		t.Fatal("a typed yes over the record's no approved te #1")
	}
	// Once the record is gone, only the person's typed yes approves: an
	// entry without typed, or a typed one that said no, does not.
	for _, c := range []struct {
		task   string
		fields map[string]any
	}{
		{"tf", map[string]any{"human": "yes", "said": "yes"}},
		{"tg", map[string]any{"typed": "yes", "human": "yes", "said": "no"}},
	} {
		r := propose(t, b, ctx, retroRec(c.task, "seven"))
		if err := b.RetroWithdraw(ctx, c.task); err != nil {
			t.Fatal(err)
		}
		if err := b.r.HDel(ctx, AsksKey(b.project), r.AskID, r.AskID+"|q", r.AskID+"|s").Err(); err != nil {
			t.Fatal(err)
		}
		forgeOn(r.AskID, c.fields)
		if _, err := b.RetroApproved(ctx, c.task, 1); err == nil {
			t.Errorf("%s: approved by %v", c.task, c.fields)
		}
		if _, err := b.RetroRoute(ctx, c.task); err != nil { // frees the slot for the next
			t.Fatal(err)
		}
	}
}

// A withdrawn ask routes too: its questions unanswered, docs still told.
// later is not rejected; no is, and a repeat of it (case and spaces aside)
// is refused, a later one is not.
func TestRetroWithdrawRejectedAndLater(t *testing.T) {
	b, ctx := retroWorld(t)
	w := propose(t, b, ctx, retroRec("tw", "never mind"))
	if err := b.RetroWithdraw(ctx, "tw"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.RetroRoute(ctx, "tw"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.Retro(ctx, "tw"); len(rec.Outcomes) != 1 || rec.Outcomes[0].Outcome != OutcomeUnanswered {
		t.Fatalf("withdrawn %+v", rec)
	}
	_ = w
	n := propose(t, b, ctx, retroRec("tn", "Always Rerun  the suite", "wait for it"))
	answer(t, b, ctx, n.AskID, 1, "no")
	answer(t, b, ctx, n.AskID, 2, "later")
	if _, err := b.RetroRoute(ctx, "tn"); err != nil {
		t.Fatal(err)
	}
	if rej, err := b.RetroRejected(ctx, "skills/agent-bus/SKILL.md"); err != nil || len(rej) != 1 || rej[0].Lesson != "always rerun the suite" {
		t.Fatalf("rejected %+v, %v", rej, err)
	}
	if _, err := b.RetroPropose(ctx, retroRec("tr", "always rerun the  SUITE"), 0, 0); err == nil || !strings.Contains(err.Error(), "row 1 repeats a rejected proposal") {
		t.Fatalf("a rejected repeat: %v", err)
	}
	if _, err := b.RetroPropose(ctx, retroRec("tl", "wait for it"), 0, 0); err != nil {
		t.Fatalf("a later row proposed again: %v", err)
	}
	if _, err := b.RetroPropose(ctx, retroRec("tl", "again"), 0, 0); err == nil {
		t.Fatal("a second retro of tl")
	}
}

// The cap: retros that reach the person since the day's start; over it,
// propose refuses and stores nothing.
func TestRetroCap(t *testing.T) {
	b, ctx := retroWorld(t)
	dayStart := time.Now().Add(-time.Hour).UnixMilli()
	for _, task := range []string{"c1", "c2"} {
		if _, err := b.RetroPropose(ctx, retroRec(task, "x "+task), 2, dayStart); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.RetroPropose(ctx, retroRec("c3", "x c3"), 2, dayStart); err == nil || !strings.Contains(err.Error(), "retro.max_per_day") {
		t.Fatalf("over the cap: %v", err)
	}
	if _, err := b.Retro(ctx, "c3"); err == nil {
		t.Fatal("a capped retro was stored")
	}
	if _, err := b.RetroPropose(ctx, retroRec("c3", "x c3"), 2, time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatalf("a new day: %v", err)
	}
}
