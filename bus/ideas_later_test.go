package bus

import (
	"context"
	"slices"
	"testing"
)

// ideas-triage: "later" is a state of its own (yes / later / no triage),
// between doing and dropped in the order of life, typed as it reads.
func TestIdeaLaterState(t *testing.T) {
	if got, ok := ParseIdeaState("Later"); !ok || got != IdeaLater {
		t.Fatalf("later parsed as %q %v", got, ok)
	}
	want := []string{IdeaToDiscuss, IdeaDecided, IdeaDoing, IdeaLater, IdeaDropped}
	if !slices.Equal(IdeaStates, want) {
		t.Fatalf("states %v, want %v", IdeaStates, want)
	}
	b := dialTest(t)
	cleanIdeas(t, b)
	ctx := context.Background()
	id, err := b.AddIdea(ctx, IdeaDef{Title: "a voice for busmon", By: "master"})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SetIdeaState(ctx, id, IdeaToDiscuss, IdeaLater, "charlie"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetIdeaState(ctx, id, IdeaLater, IdeaToDiscuss, "charlie"); err != nil {
		t.Fatalf("later is not final: %v", err)
	}
	ideas, err := b.Ideas(ctx)
	if err != nil || len(ideas) != 1 || len(ideas[0].Log) != 2 || ideas[0].Log[0].To != IdeaLater {
		t.Fatalf("ideas %+v %v", ideas, err)
	}
}
