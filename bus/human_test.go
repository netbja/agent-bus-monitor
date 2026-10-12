package bus

import (
	"context"
	"testing"
)

func TestParseEntryReadsHuman(t *testing.T) {
	e := ParseEntry("p:cmd", "1-0", map[string]string{"from": "bernard", "target": "codex", "type": "directive", "command": "x", "human": "yes"})
	if !e.Human {
		t.Fatal("human=yes not parsed")
	}
	if ParseEntry("p:notify", "1-0", map[string]string{"from": "codex", "message": "x"}).Human {
		t.Fatal("an agent entry parsed as human")
	}
}

func TestHumanWritesAreMarkedRedis(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.HumanCmd(ctx, "bernard", "codex", "relis t4"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.HumanNotify(ctx, "pilot", "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.HumanCmd(ctx, "Bad Name", "codex", "x"); err == nil {
		t.Fatal("HumanCmd accepted an invalid sender")
	}
	evs, _, err := b.Recent(ctx, []string{"cmd", "notify"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("events = %+v", evs)
	}
	for _, e := range evs {
		if !e.Human {
			t.Fatalf("not marked human: %+v", e)
		}
	}
	if evs[0].Type != CmdDirective && evs[1].Type != CmdDirective {
		t.Fatalf("the human cmd is not a directive: %+v", evs)
	}
}
