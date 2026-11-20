package bus

import (
	"context"
	"strings"
	"testing"
	"time"
)

// background-work-visible §4: {p}:bgtasks:<agent>, what the agent's own
// hooks said of its background tasks; written in one script, first_seen kept
// by id hash, the parent's pid only, an hour's life.
func TestBgTasks(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(context.Background(), BgTasksKey(b.Project(), "coder")) })
	if _, ok, err := b.BgTasks(ctx, "coder"); err != nil || ok {
		t.Fatalf("an absent record read %v %v", ok, err)
	}
	two := []BgTask{{IDHash: BgIDHash("a"), Type: "shell", Status: "running"}, {IDHash: BgIDHash("s"), Type: "subagent", AgentType: "Explore", Status: "running"}}
	crons := []BgCron{{IDHash: BgIDHash("c"), Schedule: "*/30 * * * *", Recurring: true}}
	if err := b.WriteBgTasks(ctx, "coder", "Stop", 42, two, crons); err != nil {
		t.Fatal(err)
	}
	r, ok, err := b.BgTasks(ctx, "coder")
	if err != nil || !ok || r.Source != "hooks" || r.Event != "Stop" || r.PID != 42 || r.At == 0 || len(r.Tasks) != 2 || len(r.Crons) != 1 {
		t.Fatalf("record %+v %v %v", r, ok, err)
	}
	first := r.Tasks[0].FirstSeen
	if first == 0 || r.Tasks[1].AgentType != "Explore" || r.Crons[0].Schedule != "*/30 * * * *" || !r.Crons[0].Recurring {
		t.Fatalf("fields %+v", r)
	}
	if ttl := b.r.PTTL(ctx, BgTasksKey(b.Project(), "coder")).Val(); ttl <= 0 || ttl > time.Hour {
		t.Fatalf("ttl %v", ttl)
	}
	// A later SubagentStop keeps first_seen by id hash; a new task gets its own.
	time.Sleep(5 * time.Millisecond)
	later := []BgTask{{IDHash: BgIDHash("a"), Type: "shell", Status: "running"}, {IDHash: BgIDHash("n"), Type: "monitor", Status: "running"}}
	if err := b.WriteBgTasks(ctx, "coder", "SubagentStop", 42, later, nil); err != nil {
		t.Fatal(err)
	}
	r, _, _ = b.BgTasks(ctx, "coder")
	if r.Event != "SubagentStop" || r.Tasks[0].FirstSeen != first || r.Tasks[1].FirstSeen <= first {
		t.Fatalf("first_seen not kept: %+v", r.Tasks)
	}
	// A new pid (a relaunch) starts afresh: nothing kept from the old process.
	if err := b.WriteBgTasks(ctx, "coder", "Stop", 43, later[:1], nil); err != nil {
		t.Fatal(err)
	}
	if r, _, _ = b.BgTasks(ctx, "coder"); r.PID != 43 || r.Tasks[0].FirstSeen == first {
		t.Fatalf("a new pid kept the old first_seen: %+v", r)
	}
	// An empty list is a reading too: no task in flight (the hooks say none).
	if err := b.WriteBgTasks(ctx, "coder", "Stop", 43, nil, nil); err != nil {
		t.Fatal(err)
	}
	if r, ok, _ = b.BgTasks(ctx, "coder"); !ok || len(r.Tasks) != 0 {
		t.Fatalf("empty list: %+v %v", r, ok)
	}
	// SessionStart drops it.
	if err := b.DropBgTasks(ctx, "coder"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ = b.BgTasks(ctx, "coder"); ok {
		t.Fatal("dropped record still read")
	}
}

// The writer refuses what the hook must have cleaned: an unsanitised label.
func TestBgTasksRefusesRawText(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(context.Background(), BgTasksKey(b.Project(), "coder")) })
	for _, bad := range []BgTask{
		{IDHash: "not a hash", Type: "shell", Status: "running"},
		{IDHash: BgIDHash("a"), Type: "make all", Status: "running"},
		{IDHash: BgIDHash("a"), Type: "shell", Status: strings.Repeat("x", 40)},
		{IDHash: BgIDHash("a"), Type: "subagent", AgentType: "rm -rf", Status: "running"},
	} {
		if err := b.WriteBgTasks(ctx, "coder", "Stop", 1, []BgTask{bad}, nil); err == nil {
			t.Errorf("%+v written", bad)
		}
	}
	if err := b.WriteBgTasks(ctx, "coder", "Stop", 1, nil, []BgCron{{IDHash: BgIDHash("c"), Schedule: "every day"}}); err == nil {
		t.Error("a schedule not of cron shape written")
	}
}
