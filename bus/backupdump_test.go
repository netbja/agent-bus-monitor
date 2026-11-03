package bus

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Only PTTL = -1 is kept: a key a millisecond from expiring (0), one with any
// expiry, and an absent one (-2) are skipped (backup spec, "Redis").
func TestKeepDump(t *testing.T) {
	for pttl, want := range map[time.Duration]bool{-1: true, 0: false, 1: false, 15 * time.Second: false, -2: false} {
		if got := keepDump(pttl); got != want {
			t.Errorf("keepDump(%v) = %v, want %v", pttl, got, want)
		}
	}
}

func TestDumpProject(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	r, p := b.r, b.project
	sib := p + "-2"
	t.Cleanup(func() { DeleteProject(ctx, r, p); DeleteProject(ctx, r, sib) })
	// A stream with a consumer group and a pending entry, a hash, a string,
	// a key with a TTL, runtime keys, and a sibling project's key.
	if _, err := b.Cmd(ctx, "master", "coder", "directive", "", "do it"); err != nil {
		t.Fatal(err)
	}
	r.XGroupCreateMkStream(ctx, StreamKey(p, "cmd"), "coder", "0")
	r.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "coder", Consumer: "coder", Streams: []string{StreamKey(p, "cmd"), ">"}, Count: 1})
	r.HSet(ctx, BoardKey(p), "t1", `{"owner":"coder","state":"accepted"}`)
	r.Set(ctx, PauseKey(p), `{"until":1}`, 0)
	r.Set(ctx, DispatchKey(p), "token", time.Minute)
	r.HSet(ctx, ServicesKey(p), "installed", "1")
	r.HSet(ctx, WakeKey(p, "coder"), "state", "prompting")
	r.HSet(ctx, BoardKey(sib), "x", "y")

	d, err := DumpProject(ctx, r, p)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]DumpedKey{}
	for _, k := range d.Keys {
		got[k.Key] = k
	}
	for _, k := range []string{StreamKey(p, "cmd"), BoardKey(p), PauseKey(p), ServicesKey(p), WakeKey(p, "coder")} {
		if _, ok := got[k]; !ok {
			t.Errorf("%s missing from the dump", k)
		}
	}
	if _, ok := got[DispatchKey(p)]; ok {
		t.Error("a key with a TTL was dumped")
	}
	if len(d.Expiring) != 1 || d.Expiring[0] != DispatchKey(p) {
		t.Errorf("expiring = %q", d.Expiring)
	}
	for k := range got {
		if c, _ := ClassOf(sib, k); c != KeyUnknown {
			t.Errorf("the sibling's %s was read", k)
		}
	}
	if got[StreamKey(p, "cmd")].Type != "stream" || got[BoardKey(p)].Type != "hash" || got[PauseKey(p)].Type != "string" {
		t.Errorf("types %+v", got)
	}
	if got[BoardKey(p)].Class != KeyRecord || got[ServicesKey(p)].Class != KeyRuntime {
		t.Errorf("classes: board %v, services %v", got[BoardKey(p)].Class, got[ServicesKey(p)].Class)
	}
	if d.Exec <= 0 {
		t.Errorf("exec time %v", d.Exec)
	}
	// The payload is the server's own DUMP: restored elsewhere, the stream
	// keeps its group's cursor and pending entry.
	cmd := got[StreamKey(p, "cmd")]
	dst := p + ":copy"
	t.Cleanup(func() { r.Del(ctx, dst) })
	if err := r.Restore(ctx, dst, 0, string(cmd.Dump)).Err(); err != nil {
		t.Fatal(err)
	}
	pend, err := r.XPending(ctx, dst, "coder").Result()
	if err != nil || pend.Count != 1 {
		t.Fatalf("restored group pending = %+v, %v", pend, err)
	}
}

func TestDumpProjectRejectsBadNames(t *testing.T) {
	if _, err := DumpProject(context.Background(), nil, "*"); err == nil {
		t.Fatal("a glob project was accepted")
	}
}
