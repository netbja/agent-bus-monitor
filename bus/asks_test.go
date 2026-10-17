package bus

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func q(id, header string, labels ...string) AskQuestion {
	var opts []AskOption
	for _, l := range labels {
		opts = append(opts, AskOption{Label: l})
	}
	return AskQuestion{ID: id, Header: header, Question: header + "?", Options: opts}
}

func cleanAsks(t *testing.T, b *Bus) {
	t.Cleanup(func() { b.r.Del(context.Background(), AsksKey(b.Project())) })
}

func TestValidateAskDef(t *testing.T) {
	ok := AskDef{From: "master", Title: "t", Questions: []AskQuestion{q("t", "Date", "A", "B")}}
	if err := ValidateAskDef(ok); err != nil {
		t.Fatal(err)
	}
	bad := []AskDef{
		{From: "master", Title: "t"},
		{From: "master", Title: "t", Questions: []AskQuestion{q("t", "Date"), q("t", "Date", "A")}},
		{From: "master", Title: "t", Questions: []AskQuestion{q("t", "Date", "A", "A")}},
		{From: "master", Title: "t", Questions: []AskQuestion{q("Bad Id", "Date", "A")}},
		{From: "master", Title: "t", Questions: []AskQuestion{q("t", strings.Repeat("h", 17), "A")}},
		{From: "master", Title: "t", Questions: []AskQuestion{{ID: "t", Header: "h", Question: "q?", Options: []AskOption{{Label: "a", Recommended: true}, {Label: "b", Recommended: true}}}}},
	}
	for i, d := range bad {
		if ValidateAskDef(d) == nil {
			t.Fatalf("bad def %d accepted", i)
		}
	}
}

func TestAskAnswerReplaceWithdraw(t *testing.T) {
	b := dialTest(t)
	cleanAsks(t, b)
	ctx := context.Background()
	def := AskDef{From: "master", Title: "Opening", Questions: []AskQuestion{q("amend", "Amendment", "Yes", "No"), q("date", "Date T", "Mon", "Wed"), q("sha", "Code", "X", "Y")}}
	id, state, err := b.Ask(ctx, def, "")
	if err != nil || state != "open" {
		t.Fatalf("ask: %v %s", err, state)
	}
	if _, _, err := b.Ask(ctx, def, ""); !errors.Is(err, ErrAskRefused) {
		t.Fatalf("a second open ask was accepted: %v", err)
	}
	evs, _, _ := b.Recent(ctx, []string{"cmd"}, 5)
	last := evs[len(evs)-1]
	if last.ID != id || last.Type != CmdAsk || last.Target != PeopleTarget || last.Ref != "" || !last.NoWake {
		t.Fatalf("ask entry %+v", last)
	}
	if targets, _ := b.CmdTargets(ctx); strings.Contains(strings.Join(targets, ","), PeopleTarget) {
		t.Fatal("people is a cmd target")
	}

	if _, _, err := b.Answer(ctx, id, "amend", "bernard", "Maybe", ""); err == nil {
		t.Fatal("a choice that is not a label was accepted")
	}
	got, left, err := b.Answer(ctx, id, "amend", "bernard", "Yes", "")
	if err != nil || got != id || left != 2 {
		t.Fatalf("answer: %v %s %d", err, got, left)
	}
	if _, _, err := b.Answer(ctx, id, "amend", "bernard", "No", ""); !errors.Is(err, ErrAskRefused) {
		t.Fatalf("second answer: %v", err)
	}
	evs, _, _ = b.Recent(ctx, []string{"cmd"}, 5)
	r := evs[len(evs)-1]
	if r.Type != CmdReply || r.Ref != id || r.Target != "master" || r.From != "bernard" || !r.Human || r.NoWake ||
		!strings.Contains(r.Message, `Answer to "Amendment": Yes (2 question(s) still open)`) {
		t.Fatalf("reply %+v", r)
	}

	// Replace: date unchanged (carried, unanswered), sha reworded → refused, then a new id.
	def2 := def
	def2.Questions = []AskQuestion{q("amend", "Amendment", "Yes", "No"), q("date", "Date T", "Mon", "Wed"), q("sha", "Code", "X", "Z")}
	if _, _, err := b.Ask(ctx, def2, id); err == nil {
		t.Fatal("a reworded question kept its id")
	}
	def2.Questions[2] = q("sha2", "Code", "X", "Z")
	id2, state, err := b.Ask(ctx, def2, id)
	if err != nil || state != "open" {
		t.Fatalf("replace: %v %s", err, state)
	}
	// An answer on the old ask follows the replacement for an unchanged question...
	if got, left, err := b.Answer(ctx, id, "date", "bernard", "Mon", ""); err != nil || got != id2 || left != 1 {
		t.Fatalf("answer via the old id: %v %s %d", err, got, left)
	}
	// ...but not for one the replacement dropped.
	if _, _, err := b.Answer(ctx, id, "sha", "bernard", "X", ""); !errors.Is(err, ErrAskRefused) {
		t.Fatalf("answer to a dropped question: %v", err)
	}
	if _, left, err := b.Answer(ctx, id2, "sha2", "bernard", "", "neither, ship dev"); err != nil || left != 0 {
		t.Fatalf("free text: %v %d", err, left)
	}
	asks, _ := b.Asks(ctx)
	if len(asks) != 2 || asks[0].State != "replaced" || asks[0].ReplacedBy != id2 || asks[1].State != "answered" || asks[1].Open() != 0 {
		t.Fatalf("asks %+v", asks)
	}
	if a := asks[1].Questions[0].Answer; a == nil || a.Choice != "Yes" || a.By != "bernard" || a.At == 0 {
		t.Fatalf("carried answer %+v", a)
	}
	// Answered: a new ask is allowed; withdraw it.
	id3, _, err := b.Ask(ctx, AskDef{From: "master", Title: "Other", Questions: []AskQuestion{q("x", "X", "a")}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Withdraw(ctx, "coder", id3); err == nil {
		t.Fatal("another agent withdrew it")
	}
	if err := b.Withdraw(ctx, "master", id3); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Answer(ctx, id3, "x", "bernard", "a", ""); !errors.Is(err, ErrAskRefused) {
		t.Fatalf("answer to a withdrawn ask: %v", err)
	}
}

func TestReplacementFullyAnsweredIsAnswered(t *testing.T) {
	b := dialTest(t)
	cleanAsks(t, b)
	ctx := context.Background()
	def := AskDef{From: "master", Title: "T", Questions: []AskQuestion{q("a", "A", "1"), q("b", "B", "1")}}
	id, _, _ := b.Ask(ctx, def, "")
	b.Answer(ctx, id, "a", "bernard", "1", "")
	_, state, err := b.Ask(ctx, AskDef{From: "master", Title: "T", Questions: []AskQuestion{q("a", "A", "1")}}, id)
	if err != nil || state != "answered" {
		t.Fatalf("replacement keeping only the answered question: %v %s", err, state)
	}
}

// Fix 5: a withdraw sweeps expired groups too; a partly answered withdrawn ask
// keeps its answers until then.
func TestWithdrawSweepsExpiredAsks(t *testing.T) {
	b := dialTest(t)
	cleanAsks(t, b)
	ctx := context.Background()
	k := AsksKey(b.Project())
	b.r.HSet(ctx, k, "1-0", `{"from":"x","title":"old","questions":[]}`, "1-0|s", "answered", "1-0|e", "1", "1-0|a|q", `{"choice":"a"}`)
	id, _, err := b.Ask(ctx, AskDef{From: "master", Title: "T", Questions: []AskQuestion{q("a", "A", "1"), q("b", "B", "1")}}, "")
	if err != nil {
		t.Fatal(err)
	}
	b.r.HSet(ctx, k, "2-0", `{}`, "2-0|e", "1") // expired again after the create swept
	if _, _, err := b.Answer(ctx, id, "a", "bernard", "1", ""); err != nil {
		t.Fatal(err)
	}
	b.r.HSet(ctx, k, "3-0", `{}`, "3-0|e", "1")
	if err := b.Withdraw(ctx, "master", id); err != nil {
		t.Fatal(err)
	}
	if n, _ := b.r.HExists(ctx, k, "3-0").Result(); n {
		t.Fatal("withdraw did not sweep an expired group")
	}
	asks, _ := b.Asks(ctx)
	if len(asks) != 1 || asks[0].State != "withdrawn" || asks[0].Questions[0].Answer == nil {
		t.Fatalf("withdrawn ask %+v: its answer must stay", asks)
	}
}

// Fix 6: the reply, suffix included, must fit CmdMaxRunes.
func TestAnswerTextFitsTheCapWithItsSuffix(t *testing.T) {
	b := dialTest(t)
	cleanAsks(t, b)
	ctx := context.Background()
	id, _, _ := b.Ask(ctx, AskDef{From: "master", Title: "T", Questions: []AskQuestion{q("a", "A", "1"), q("b", "B", "1")}}, "")
	if _, _, err := b.Answer(ctx, id, "a", "bernard", "", strings.Repeat("x", CmdMaxRunes-20)); err == nil {
		t.Fatal("an answer that only fits without the suffix was accepted")
	}
}

// v0.15.1 §11.4: master closes only the questions the person answered
// elsewhere; the others stay open, and the ask ends with the last one.
func TestWithdrawOneQuestion(t *testing.T) {
	b := dialTest(t)
	cleanAsks(t, b)
	ctx := context.Background()
	def := AskDef{From: "master", Title: "GO", Questions: []AskQuestion{q("go", "GO execution", "Yes", "No"), q("date", "Date", "Mon", "Wed")}}
	id, _, err := b.Ask(ctx, def, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.WithdrawQuestion(ctx, "coder", id, "go", "x"); !errors.Is(err, ErrAskRefused) {
		t.Fatalf("another agent withdrew a question: %v", err)
	}
	left, err := b.WithdrawQuestion(ctx, "master", id, "go", "answered by message 1792-0: oui GO")
	if err != nil || left != 1 {
		t.Fatalf("withdraw: %v, %d left", err, left)
	}
	if _, err := b.WithdrawQuestion(ctx, "master", id, "go", "again"); !errors.Is(err, ErrAskRefused) {
		t.Fatalf("withdrew a closed question twice: %v", err)
	}
	asks, _ := b.Asks(ctx)
	if len(asks) != 1 || asks[0].State != "open" || asks[0].Open() != 1 {
		t.Fatalf("after one withdrawal: %+v", asks)
	}
	if v := asks[0].Questions[0].Answer.Value(); !strings.Contains(v, "withdrawn by master") {
		t.Fatalf("value %q", v)
	}
	if _, _, err := b.Answer(ctx, id, "date", "bernard", "Mon", ""); err != nil {
		t.Fatal(err)
	}
	asks, _ = b.Asks(ctx)
	if asks[0].State != "answered" || asks[0].Open() != 0 {
		t.Fatalf("after the last answer: %+v", asks[0])
	}
	// Every question withdrawn: the ask ends withdrawn.
	id2, _, err := b.Ask(ctx, AskDef{From: "master", Title: "One", Questions: []AskQuestion{q("a", "A", "Yes", "No")}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if left, err := b.WithdrawQuestion(ctx, "master", id2, "a", "answered by message"); err != nil || left != 0 {
		t.Fatalf("%v %d", err, left)
	}
	for _, a := range func() []Ask { x, _ := b.Asks(ctx); return x }() {
		if a.ID == id2 && a.State != "withdrawn" {
			t.Fatalf("all-withdrawn ask state %q", a.State)
		}
	}
}
