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
	// A gate foureyes takes part in, architect's own open ask, sentinel's
	// hand-off of its own process (foureyes on cleaning-1a).
	b.r.HSet(ctx, GateKey(b.project, "foureyes"), "ref1", "coder|review x")
	if _, _, err := b.Ask(ctx, AskDef{From: "architect", Title: "Which?", Questions: []AskQuestion{q("a", "A", "one", "two")}}, ""); err != nil {
		t.Fatal(err)
	}
	done, _, err := b.Ask(ctx, AskDef{From: "sec", Title: "Done?", Questions: []AskQuestion{q("a", "A", "yes", "no")}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Answer(ctx, done, "a", "person", "yes", ""); err != nil { // answered: sec owes no ask
		t.Fatal(err)
	}
	if err := b.Bind(ctx, "sentinel", AgentIdent{Pane: "k/t3", PID: 42, PIDStart: 7}); err != nil {
		t.Fatal(err)
	}
	if err := b.ProveSession(ctx, "sentinel", 42, 7, "session-0001", 100); err != nil {
		t.Fatal(err)
	}
	rep, err := b.Report(ctx, "sentinel", ReportNote, "hand-off: next y")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.DeclareHandoff(ctx, "sentinel", rep, AgentIdent{PID: 42, PIDStart: 7, Session: "session-0001"}); err != nil {
		t.Fatal(err)
	}
	snaps, err := b.Agents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owes := b.ReadOwes(ctx, map[string]AgentSnapshot{"coder": {}, "docs": {}, "foureyes": {}, "architect": {}, "sentinel": snaps["sentinel"], "sec": {}})
	if o := owes["foureyes"]; o.Gates != 1 || o.Ask || o.Handoff {
		t.Errorf("foureyes: %+v", o)
	}
	if o := owes["sec"]; o.Ask {
		t.Errorf("sec's answered ask is owed: %+v", o)
	}
	if o := owes["architect"]; !o.Ask || o.Gates != 0 {
		t.Errorf("architect: %+v", o)
	}
	if o := owes["sentinel"]; !o.Handoff || o.Ask {
		t.Errorf("sentinel: %+v", o)
	}
	// The same hand-off seen from another process is void: taken.
	other := snaps["sentinel"]
	other.PID = 43
	if o := b.ReadOwes(ctx, map[string]AgentSnapshot{"sentinel": other})["sentinel"]; o.Handoff {
		t.Errorf("a relaunched sentinel still owes the hand-off: %+v", o)
	}
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
