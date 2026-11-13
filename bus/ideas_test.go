package bus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func cleanIdeas(t *testing.T, b *Bus) {
	t.Cleanup(func() { b.r.Del(context.Background(), IdeasKey(b.Project())) })
}

func TestParseIdeaState(t *testing.T) {
	for in, want := range map[string]string{"to discuss": IdeaToDiscuss, "discuss": IdeaToDiscuss, "To-Discuss": IdeaToDiscuss,
		"decided": IdeaDecided, "DOING": IdeaDoing, "dropped": IdeaDropped} {
		if got, ok := ParseIdeaState(in); !ok || got != want {
			t.Fatalf("%q: %q %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "done", "open", "to"} {
		if _, ok := ParseIdeaState(in); ok {
			t.Fatalf("%q accepted", in)
		}
	}
}

func TestValidateIdeaDef(t *testing.T) {
	if err := ValidateIdeaDef(IdeaDef{Title: "t", By: "coder", Text: "line\n\tindented"}); err != nil {
		t.Fatal(err)
	}
	for i, d := range []IdeaDef{
		{Title: "", By: "coder"},
		{Title: "t", By: "Not A Name"},
		{Title: strings.Repeat("t", IdeaMaxTitle+1), By: "coder"},
		{Title: "t", By: "coder", Text: strings.Repeat("x", IdeaMaxText+1)},
		{Title: "t", By: "coder", Source: strings.Repeat("s", IdeaMaxSource+1)},
		{Title: "a\x1b[2Jb", By: "coder"},
		{Title: "two\nlines", By: "coder"},
		{Title: "t", By: "coder", Source: "a\tb"},
		{Title: "t", By: "coder", Text: "bidi \u202e"},
	} {
		if ValidateIdeaDef(d) == nil {
			t.Fatalf("bad def %d accepted", i)
		}
	}
}

// An idea always starts at "to discuss", numbered by the hash's seq, its
// time from Redis TIME; the list orders i2 before i10.
func TestAddIdeaAndList(t *testing.T) {
	b := dialTest(t)
	cleanIdeas(t, b)
	ctx := context.Background()
	var ids []string
	for i := 0; i < 10; i++ {
		id, err := b.AddIdea(ctx, IdeaDef{Title: "idea " + string(rune('a'+i)), By: "coder", Text: "why", Source: "docs/x.md"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if ids[0] != "i1" || ids[9] != "i10" {
		t.Fatalf("ids %v", ids)
	}
	ideas, err := b.Ideas(ctx)
	if err != nil || len(ideas) != 10 {
		t.Fatalf("ideas %v %v", ideas, err)
	}
	if ideas[1].ID != "i2" || ideas[9].ID != "i10" {
		t.Fatalf("order %s %s", ideas[1].ID, ideas[9].ID)
	}
	first := ideas[0]
	if first.State != IdeaToDiscuss || first.Title != "idea a" || first.By != "coder" || first.Text != "why" || first.Source != "docs/x.md" || first.At == 0 || len(first.Log) != 0 {
		t.Fatalf("first %+v", first)
	}
	if _, err := b.AddIdea(ctx, IdeaDef{Title: "", By: "coder"}); err == nil {
		t.Fatal("an empty title was stored")
	}
}

// set is a compare-and-set on the expected state, by a person: the change
// is logged and told to people (wake=no); no state is final.
func TestSetIdeaState(t *testing.T) {
	b := dialTest(t)
	cleanIdeas(t, b)
	ctx := context.Background()
	id, err := b.AddIdea(ctx, IdeaDef{Title: "busmon update without setup.sh", By: "master"})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SetIdeaState(ctx, id, IdeaDecided, IdeaDoing, "charlie"); !errors.Is(err, ErrIdeaRefused) || !strings.Contains(err.Error(), "to discuss") {
		t.Fatalf("a stale expected state was accepted: %v", err)
	}
	if err := b.SetIdeaState(ctx, id, IdeaToDiscuss, IdeaDecided, "charlie"); err != nil {
		t.Fatal(err)
	}
	evs, _, _ := b.Recent(ctx, []string{"cmd"}, 5)
	last := evs[len(evs)-1]
	if last.From != "charlie" || last.Target != PeopleTarget || last.Type != CmdReply || !last.NoWake || !last.Human ||
		!strings.Contains(last.Message, id) || !strings.Contains(last.Message, "to discuss → decided") || !strings.Contains(last.Message, "busmon update") {
		t.Fatalf("set entry %+v", last)
	}
	if err := b.SetIdeaState(ctx, id, IdeaDecided, IdeaDecided, "charlie"); !errors.Is(err, ErrIdeaRefused) {
		t.Fatalf("a set to the same state was accepted: %v", err)
	}
	if err := b.SetIdeaState(ctx, id, IdeaDecided, IdeaDropped, "charlie"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetIdeaState(ctx, id, IdeaDropped, IdeaToDiscuss, "dana"); err != nil {
		t.Fatalf("dropped is not final: %v", err)
	}
	ideas, _ := b.Ideas(ctx)
	got := ideas[0]
	if got.State != IdeaToDiscuss || len(got.Log) != 3 {
		t.Fatalf("idea %+v", got)
	}
	if l := got.Log[0]; l.From != IdeaToDiscuss || l.To != IdeaDecided || l.By != "charlie" || l.At == 0 {
		t.Fatalf("first change %+v", l)
	}
	if got.Log[2].By != "dana" {
		t.Fatalf("last change %+v", got.Log[2])
	}
	if err := b.SetIdeaState(ctx, "i99", IdeaToDiscuss, IdeaDoing, "charlie"); !errors.Is(err, ErrIdeaRefused) {
		t.Fatalf("an unknown idea was set: %v", err)
	}
}

// Only a person sets a state: an agent registered on the project, a
// reserved name or no valid name is refused by the script itself.
func TestSetIdeaStateRefusesAnAgent(t *testing.T) {
	b := dialTest(t)
	cleanIdeas(t, b)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(context.Background(), AgentsKey(b.Project())) })
	if _, err := b.Status(ctx, "coder", "idle", "", AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	id, _ := b.AddIdea(ctx, IdeaDef{Title: "t", By: "coder"})
	for _, by := range []string{"coder", "dispatch", "anonymous", "pilot", "agentbus", "people", "", "Bad Name"} {
		if err := b.SetIdeaState(ctx, id, IdeaToDiscuss, IdeaDecided, by); err == nil {
			t.Fatalf("set by %q accepted", by)
		}
	}
	if ideas, _ := b.Ideas(ctx); ideas[0].State != IdeaToDiscuss || len(ideas[0].Log) != 0 {
		t.Fatalf("a refused set changed %+v", ideas[0])
	}
}

// The audit is capped: the newest changes are kept.
func TestIdeaLogCapped(t *testing.T) {
	b := dialTest(t)
	cleanIdeas(t, b)
	ctx := context.Background()
	id, _ := b.AddIdea(ctx, IdeaDef{Title: "t", By: "coder"})
	cur := IdeaToDiscuss
	for i := 0; i < ideaLogMax+5; i++ {
		next := IdeaDoing
		if cur == IdeaDoing {
			next = IdeaToDiscuss
		}
		if err := b.SetIdeaState(ctx, id, cur, next, "charlie"); err != nil {
			t.Fatal(err)
		}
		cur = next
	}
	ideas, _ := b.Ideas(ctx)
	if n := len(ideas[0].Log); n != ideaLogMax || ideas[0].Log[n-1].To != cur {
		t.Fatalf("log %d, last %+v", n, ideas[0].Log[n-1])
	}
}

// reviewer-codex: an add whose reply is lost after the script committed is
// retried with the same request id and finds its idea; no second one. The
// id is never stored in the definition, and Ideas never lists it.
func TestAddIdeaRetryIsIdempotent(t *testing.T) {
	b := dialTest(t)
	cleanIdeas(t, b)
	ctx := context.Background()
	req := NewIntentNonce()
	first, err := b.AddIdea(ctx, IdeaDef{Title: "voice notes", By: "charlie", Request: req})
	if err != nil {
		t.Fatal(err)
	}
	again, err := b.AddIdea(ctx, IdeaDef{Title: "voice notes", By: "charlie", Request: req})
	if err != nil || again != first {
		t.Fatalf("the retry: %q %v, want %q", again, err, first)
	}
	other, _ := b.AddIdea(ctx, IdeaDef{Title: "voice notes", By: "charlie", Request: NewIntentNonce()})
	none, _ := b.AddIdea(ctx, IdeaDef{Title: "voice notes", By: "charlie"})
	if other == first || none == first || none == other {
		t.Fatalf("distinct requests share an id: %q %q %q", first, other, none)
	}
	ideas, _ := b.Ideas(ctx)
	if len(ideas) != 3 {
		t.Fatalf("%d ideas, want 3: %+v", len(ideas), ideas)
	}
	if strings.Contains(fmt.Sprint(ideaAdd), "EXPIRE") {
		t.Fatal("a field TTL command: the broker floor is 7.2")
	}
	raw, _ := b.r.HGet(ctx, IdeasKey(b.Project()), first).Result()
	if strings.Contains(raw, req) || strings.Contains(raw, "request") {
		t.Fatalf("the request id stored in the definition: %s", raw)
	}
	// Broker floor 7.2 (foureyes): no field TTL. The field holds the idea and
	// its Redis time; an add drops the request fields older than a day, and
	// an expired request is a new add.
	v, _ := b.r.HGet(ctx, IdeasKey(b.Project()), "req|"+req).Result()
	if idea, at, ok := strings.Cut(v, "|"); !ok || idea != first || at == "" {
		t.Fatalf("the request field %q", v)
	}
	old := NewIntentNonce()
	b.r.HSet(ctx, IdeasKey(b.Project()), "req|"+old, first+"|1000")
	got, err := b.AddIdea(ctx, IdeaDef{Title: "after a day", By: "charlie", Request: old})
	if err != nil || got == first {
		t.Fatalf("an expired request: %q %v", got, err)
	}
	stale := NewIntentNonce()
	b.r.HSet(ctx, IdeasKey(b.Project()), "req|"+stale, "i1|1000")
	b.AddIdea(ctx, IdeaDef{Title: "sweeps", By: "charlie"})
	if n, _ := b.r.HExists(ctx, IdeasKey(b.Project()), "req|"+stale).Result(); n {
		t.Fatal("a request field older than a day was kept")
	}
	if _, err := b.AddIdea(ctx, IdeaDef{Title: "t", By: "charlie", Request: "not|valid"}); err == nil {
		t.Fatal("a malformed request id accepted")
	}
}
