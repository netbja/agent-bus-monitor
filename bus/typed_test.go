package bus

import (
	"context"
	"testing"
)

// person-memory step 1 (spec 2026-11-21-person-memory-design §4.3.1, §10):
// typed=yes marks text the person typed: busmon's directive and broadcast
// (TypedCmd, TypedNotify) and an ask's answer, which also carries said, the
// person's own words. human=yes alone means "written in the person's name"
// and is never enough to remember a text.

// lastOf is the newest entry of one of b's streams.
func lastOf(t *testing.T, b *Bus, stream string) Event {
	t.Helper()
	evs, _, err := b.Recent(context.Background(), []string{stream}, 1)
	if err != nil || len(evs) == 0 {
		t.Fatalf("no %s entry: %v", stream, err)
	}
	return evs[len(evs)-1]
}

func TestTypedWritersMarkTheEntry(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, StreamKey(b.Project(), "cmd"), StreamKey(b.Project(), "notify")) })

	if _, err := b.TypedCmd(ctx, "charlie", "master", "yes, dev first"); err != nil {
		t.Fatal(err)
	}
	if e := lastOf(t, b, "cmd"); !e.Human || !e.Typed || e.Message != "yes, dev first" || e.Type != CmdDirective || e.Said != "" {
		t.Fatalf("typed directive: %+v", e)
	}
	if _, err := b.TypedNotify(ctx, "charlie", "back at 2"); err != nil {
		t.Fatal(err)
	}
	if e := lastOf(t, b, "notify"); !e.Human || !e.Typed || e.Message != "back at 2" {
		t.Fatalf("typed broadcast: %+v", e)
	}
	if _, err := b.TypedCmd(ctx, "Bad Name", "master", "x"); err == nil {
		t.Fatal("an invalid sender was taken")
	}
}

// An ask's answer is typed, and said holds the chosen label or the free
// text alone: never the question part of "Answer to ...".
func TestAnswerIsTypedWithSaid(t *testing.T) {
	b := dialTest(t)
	cleanAsks(t, b)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, StreamKey(b.Project(), "cmd")) })
	def := AskDef{From: "master", Title: "Language", Questions: []AskQuestion{q("lang", "Language", "English", "Any code"), q("why", "Why", "A", "B")}}
	id, _, err := b.Ask(ctx, def, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Answer(ctx, id, "lang", "charlie", "Any code", ""); err != nil {
		t.Fatal(err)
	}
	e := lastOf(t, b, "cmd")
	if !e.Human || !e.Typed || e.Said != "Any code" || e.Message == e.Said {
		t.Fatalf("chosen answer: %+v", e)
	}
	if _, _, err := b.Answer(ctx, id, "why", "charlie", "", "  so the team reads me  "); err != nil {
		t.Fatal(err)
	}
	if e := lastOf(t, b, "cmd"); !e.Typed || e.Said != "so the team reads me" {
		t.Fatalf("free-text answer: %+v", e)
	}
}

// The writers that compose a text in the person's name (spec §4.7) keep
// human=yes and never write typed=yes: one case per writer.
func TestComposedWritersAreNotTyped(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() {
		b.r.Del(ctx, StreamKey(p, "cmd"), StreamKey(p, "notify"), GateKey(p, "foureyes"), PauseKey(p), IdeasKey(p))
	})
	check := func(name, stream string) {
		t.Helper()
		if e := lastOf(t, b, stream); !e.Human || e.Typed || e.Said != "" {
			t.Errorf("%s: human %v typed %v said %q", name, e.Human, e.Typed, e.Said)
		}
	}
	// agentbus resume-all's crash summary.
	if _, err := b.HumanCmd(ctx, "charlie", "master", "3 agents resumed after a crash"); err != nil {
		t.Fatal(err)
	}
	check("resume-all summary (HumanCmd)", "cmd")
	// busmon's close.
	if _, err := b.HumanClose(ctx, "charlie", "master", "1-0", "closed from busmon by charlie"); err != nil {
		t.Fatal(err)
	}
	check("busmon close (HumanClose)", "cmd")
	// The person's CLI notice.
	if _, err := b.PersonNotice(ctx, "charlie", "the suspend could not be cancelled"); err != nil {
		t.Fatal(err)
	}
	check("PersonNotice", "notify")
	// The idea move line.
	cleanIdeas(t, b)
	id, err := b.AddIdea(ctx, IdeaDef{Title: "a voice", By: "master"})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SetIdeaState(ctx, id, IdeaToDiscuss, IdeaDecided, "charlie"); err != nil {
		t.Fatal(err)
	}
	check("idea move", "cmd")
	// A gate closed by a person.
	if err := b.OpenChallenge(ctx, "foureyes", "g9", "master|review it"); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.CloseGate(ctx, "foureyes", "g9", "master|review it", "charlie", "not needed", true); err != nil || !ok {
		t.Fatalf("gate close: %v %v", ok, err)
	}
	check("gate closed by a person", "cmd")
	// The resume directive after a suspend.
	_, raw, _, err := b.Suspend(ctx, suspendSpec())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Unsuspend(ctx, raw, "charlie", ""); err != nil {
		t.Fatal(err)
	}
	check("resume after a suspend", "cmd")
}
