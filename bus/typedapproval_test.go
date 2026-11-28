package bus

import (
	"context"
	"errors"
	"testing"

	"github.com/redis/go-redis/v9"
)

// onboarding §8.4 (spec docs/superpowers/specs/2026-11-27-project-onboarding-design.md):
// TypedApproval is the one check of an approval: the kept record's answer to
// qid is choice, not withdrawn, and not the asker's own; the last typed reply
// entry to that ask and qid says choice; anything else is refused, and a
// failed read is an error, never a yes.
func TestTypedApproval(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	ask := func(from string) string {
		t.Helper()
		id, _, err := b.Ask(ctx, AskDef{From: from, Title: "Check", Questions: []AskQuestion{
			{ID: "check-1", Header: "Check", Question: "Does it pass?", Options: []AskOption{{Label: "passes"}, {Label: "fails"}}},
			{ID: "check-2", Header: "Other", Question: "And this?", Options: []AskOption{{Label: "passes"}, {Label: "fails"}}}}}, "")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	reply := func(askID string, fields map[string]any) {
		t.Helper()
		f := map[string]any{"from": "charlie", "target": "master", "type": "reply", "ref": askID, "command": "passes", "text_complete": "yes"}
		for k, v := range fields {
			f[k] = v
		}
		if err := b.r.XAdd(ctx, &redis.XAddArgs{Stream: StreamKey(b.project, "cmd"), Values: f}).Err(); err != nil {
			t.Fatal(err)
		}
	}

	a := ask("master")
	if _, err := b.TypedApproval(ctx, a, "check-1", "passes"); err == nil {
		t.Fatal("unanswered: approved")
	}
	if _, _, err := b.Answer(ctx, a, "check-1", "charlie", "passes", ""); err != nil {
		t.Fatal(err)
	}
	id, err := b.TypedApproval(ctx, a, "check-1", "passes")
	if err != nil || id == "" {
		t.Fatalf("the person's typed passes: %q %v", id, err)
	}
	for name, c := range map[string]struct{ ask, qid, choice string }{
		"another qid":    {a, "check-2", "passes"},
		"another choice": {a, "check-1", "fails"},
		"another ask":    {ask("docs"), "check-1", "passes"},
	} {
		if _, err := b.TypedApproval(ctx, c.ask, c.qid, c.choice); err == nil {
			t.Errorf("%s: approved", name)
		}
	}
	// said=no on the record: refused, whatever a later typed entry says.
	n := ask("coder")
	if _, _, err := b.Answer(ctx, n, "check-1", "charlie", "fails", ""); err != nil {
		t.Fatal(err)
	}
	reply(n, map[string]any{"typed": "yes", "human": "yes", "qid": "check-1", "said": "passes"})
	if _, err := b.TypedApproval(ctx, n, "check-1", "passes"); err == nil {
		t.Error("a kept record answering fails, the entry passes: approved")
	}
	// An untyped reply, once the record is gone, approves nothing.
	u := ask("sec")
	reply(u, map[string]any{"human": "yes", "qid": "check-1", "said": "passes"})
	b.r.HDel(ctx, AsksKey(b.project), u, u+"|q", u+"|s")
	if _, err := b.TypedApproval(ctx, u, "check-1", "passes"); err == nil {
		t.Error("an untyped reply approved")
	}
	// The asker answering its own question never approves.
	self := ask("architect")
	if _, _, err := b.Answer(ctx, self, "check-1", "architect", "passes", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.TypedApproval(ctx, self, "check-1", "passes"); err == nil {
		t.Error("By == From approved")
	}
	// A withdrawn question approves nothing.
	w := ask("sentinel")
	if _, err := b.WithdrawQuestion(ctx, "sentinel", w, "check-1", "answered in the chat"); err != nil {
		t.Fatal(err)
	}
	reply(w, map[string]any{"typed": "yes", "human": "yes", "qid": "check-1", "said": "passes"})
	if _, err := b.TypedApproval(ctx, w, "check-1", "passes"); err == nil {
		t.Error("a withdrawn question approved")
	}
	// A failed archive read is an error, never a yes.
	b.SetRetroArchive(func(context.Context, string) ([]Event, error) { return nil, errors.New("disk") })
	if _, err := b.TypedApproval(ctx, a, "check-1", "passes"); err == nil {
		t.Error("a failed archive read approved")
	}
}
