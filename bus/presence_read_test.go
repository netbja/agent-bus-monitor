package bus

import (
	"context"
	"slices"
	"testing"
	"time"
)

// ReadOwes on the test broker: a board task (a done one owes nothing), an
// addressed entry; an agent
// owing neither reads nothing but item 5, unknown in this step.
func TestReadOwes(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	for _, a := range []string{"coder", "docs"} {
		if err := b.EnsureGroup(ctx, a, DefaultHorizon); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Request(ctx, "fix-x", "master", "coder", "", "fix x", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Request(ctx, "old-y", "master", "coder", "", "done long ago", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, "old-y", "coder"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CompleteRequest(ctx, "old-y", "coder", "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Cmd(ctx, "master", "coder", "directive", "", "and look at y"); err != nil {
		t.Fatal(err)
	}
	owes := b.ReadOwes(ctx, map[string]AgentSnapshot{"coder": {}, "docs": {}})
	c := owes["coder"]
	if len(c.Tasks) != 1 || c.Tasks[0] != "fix-x (requested)" || c.Messages < 1 {
		t.Fatalf("coder: %+v", c)
	}
	if !slices.Equal(c.Unknown, []string{UnknownLosable}) {
		t.Fatalf("coder's unknown: %v", c.Unknown)
	}
	d := owes["docs"]
	if d.Something() || !slices.Equal(d.Unknown, []string{UnknownLosable}) {
		t.Fatalf("docs: %+v", d)
	}
	if s, r := Presence(PresenceIn{Pane: "k/t1", TermsRead: true, Owes: d}); s != PresenceLost || r != "unknown: losable work" {
		t.Fatalf("docs without a terminal: %s %q", s, r)
	}
}
