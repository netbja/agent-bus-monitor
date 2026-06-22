package bus

import (
	"context"
	"testing"
	"time"
)

func TestIDLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"9-0", "10-0", true},  // numeric, not lexicographic: 9 < 10
		{"10-0", "9-0", false}, // and the reverse
		{"5-1", "5-2", true},   // same ms, seq tiebreak
		{"5-2", "5-1", false},  // reverse tiebreak
		{"5-2", "5-2", false},  // equal is not less
		{"100-0", "100-1", true},
	}
	for _, c := range cases {
		if got := idLess(c.a, c.b); got != c.want {
			t.Errorf("idLess(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// seedSpaced publishes the given (kind,message) entries with a short gap between
// each so their stream IDs are strictly increasing across streams — making the
// merged chronological order deterministic for assertions.
func seedSpaced(t *testing.T, b *Bus, entries [][2]string) {
	t.Helper()
	ctx := context.Background()
	for _, e := range entries {
		var err error
		switch e[0] {
		case "status":
			_, err = b.Status(ctx, "dev", "working", e[1], "")
		case "report":
			_, err = b.Report(ctx, "dev", "note", e[1])
		case "notify":
			_, err = b.Notify(ctx, "x", e[1])
		case "cmd":
			_, err = b.Cmd(ctx, "hermes", "dev", CmdDirective, "", e[1])
		}
		if err != nil {
			t.Fatalf("seed %s %q: %v", e[0], e[1], err)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestRecentMergesAcrossStreamsAndCaps(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	kinds := []string{"status", "report", "notify"}
	// Chronological insertion: a,b,c,d,e across three streams.
	seedSpaced(t, b, [][2]string{
		{"status", "a"},
		{"notify", "b"},
		{"report", "c"},
		{"status", "d"},
		{"notify", "e"},
	})

	events, cursors, err := b.Recent(ctx, kinds, 3)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	// Last 3 merged chronologically = c,d,e (spanning report/status/notify).
	if len(events) != 3 {
		t.Fatalf("Recent returned %d events, want 3", len(events))
	}
	wantMsg := []string{"c", "d", "e"}
	for i, e := range events {
		if e.Message != wantMsg[i] {
			t.Errorf("events[%d].Message = %q, want %q (order = %+v)", i, e.Message, wantMsg[i],
				[]string{events[0].Message, events[1].Message, events[2].Message})
		}
	}
	// Every non-empty stream gets a cursor = its newest entry ID, so a follow-on
	// tail starts after it and never replays.
	for _, k := range kinds {
		if _, ok := cursors[StreamKey(b.project, k)]; !ok {
			t.Errorf("cursor missing for %s stream", k)
		}
	}
}

func TestRecentFewerThanLimit(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	seedSpaced(t, b, [][2]string{{"status", "only"}})
	events, cursors, err := b.Recent(ctx, []string{"status", "report"}, 25)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(events) != 1 || events[0].Message != "only" {
		t.Fatalf("Recent = %+v, want one 'only' event", events)
	}
	if _, ok := cursors[StreamKey(b.project, "report")]; ok {
		t.Error("empty report stream must not yield a cursor")
	}
}

func TestTailFromDoesNotReplay(t *testing.T) {
	b := dialTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := b.Status(ctx, "dev", "working", "old", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, cursors, err := b.Recent(ctx, []string{"status"}, 25)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}

	got := make(chan Event, 4)
	go func() {
		_ = b.TailFrom(ctx, cursors, []string{"status"}, func(e Event) {
			select {
			case got <- e:
			default:
			}
		})
	}()
	// Give the tail a moment to block, then publish a fresh entry.
	time.Sleep(100 * time.Millisecond)
	if _, err := b.Status(ctx, "dev", "idle", "new", ""); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case e := <-got:
		if e.Message != "new" {
			t.Fatalf("TailFrom delivered %q, want %q (the 'old' entry was replayed)", e.Message, "new")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("TailFrom produced no event within 3s")
	}
}

func TestPurgeClearsStreamsKeepsGroups(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	kinds := []string{"status", "report", "notify", "cmd"}
	seedSpaced(t, b, [][2]string{
		{"status", "a"}, {"report", "b"}, {"notify", "c"}, {"cmd", "d"},
	})
	// A consumer group on the cmd stream must survive the purge (XTRIM keeps
	// groups; cmd at-least-once delivery is unaffected).
	cmdKey := StreamKey(b.project, "cmd")
	if err := b.r.XGroupCreateMkStream(ctx, cmdKey, "dev", "$").Err(); err != nil {
		t.Fatalf("create group: %v", err)
	}

	removed, err := b.Purge(ctx, kinds)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if removed != 4 {
		t.Errorf("Purge removed %d entries, want 4", removed)
	}
	for _, k := range kinds {
		if n, _ := b.r.XLen(ctx, StreamKey(b.project, k)).Result(); n != 0 {
			t.Errorf("%s stream has %d entries after purge, want 0", k, n)
		}
	}
	groups, err := b.r.XInfoGroups(ctx, cmdKey).Result()
	if err != nil {
		t.Fatalf("XInfoGroups after purge: %v", err)
	}
	if len(groups) != 1 || groups[0].Name != "dev" {
		t.Errorf("cmd consumer group did not survive purge: %+v", groups)
	}
}
