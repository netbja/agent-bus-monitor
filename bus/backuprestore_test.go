package bus

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// snapshotKeys is every key of project with its DUMP, to compare a
// keyspace before and after.
func snapshotKeys(t *testing.T, b *Bus, project string) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := ProjectKeys(ctx, b.r, project)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, k := range keys {
		out[k], _ = b.r.Dump(ctx, k).Result()
	}
	return out
}

// archived dumps p's keys, then deletes the project: what a restore starts from.
func archived(t *testing.T, b *Bus) ([]DumpedKey, map[string]string) {
	t.Helper()
	ctx := context.Background()
	b.Cmd(ctx, "master", "coder", "directive", "", "do it")
	b.r.HSet(ctx, BoardKey(b.project), "t1", `{"owner":"coder"}`)
	b.r.Set(ctx, PauseKey(b.project), "x", 0)
	before := snapshotKeys(t, b, b.project)
	d, err := DumpProject(ctx, b.r, b.project)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteProject(ctx, b.r, b.project); err != nil {
		t.Fatal(err)
	}
	return d.Keys, before
}

func TestRestoreProjectRoundTrip(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { DeleteProject(ctx, b.r, b.project) })
	keys, before := archived(t, b)
	n, err := RestoreProject(ctx, b.r, b.project, keys, nil)
	if err != nil || n != len(keys) {
		t.Fatalf("restore: %d, %v", n, err)
	}
	after := snapshotKeys(t, b, b.project)
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s not restored byte for byte", k)
		}
	}
}

// All or nothing: a key that exists, or a corrupt payload in the middle,
// leaves the keyspace exactly as it was.
func TestRestoreProjectAllOrNothing(t *testing.T) {
	for name, arrange := range map[string]func(b *Bus, keys []DumpedKey) []DumpedKey{
		"a key that exists": func(b *Bus, keys []DumpedKey) []DumpedKey {
			b.r.HSet(context.Background(), BoardKey(b.project), "other", "1")
			return keys
		},
		"a corrupt payload": func(b *Bus, keys []DumpedKey) []DumpedKey {
			bad := slices.Clone(keys)
			bad = append(bad[:1], append([]DumpedKey{{Key: b.project + ":junk", Type: "string", Dump: []byte("not a dump")}}, bad[1:]...)...)
			return bad
		},
		"a lease held": func(b *Bus, keys []DumpedKey) []DumpedKey {
			b.r.Set(context.Background(), ConductorKey(b.project), "tok", 0)
			return keys
		},
		"a key of another project": func(b *Bus, keys []DumpedKey) []DumpedKey {
			return append(slices.Clone(keys), DumpedKey{Key: "other:board", Dump: keys[0].Dump})
		},
	} {
		b := dialTest(t)
		ctx := context.Background()
		t.Cleanup(func() { DeleteProject(ctx, b.r, b.project) })
		keys, _ := archived(t, b)
		keys = arrange(b, keys)
		before := snapshotKeys(t, b, b.project)
		if _, err := RestoreProject(ctx, b.r, b.project, keys, nil); err == nil {
			t.Errorf("%s: restored", name)
		}
		after := snapshotKeys(t, b, b.project)
		if len(after) != len(before) {
			t.Errorf("%s: keyspace %d keys, was %d", name, len(after), len(before))
		}
		for k, v := range before {
			if after[k] != v {
				t.Errorf("%s: %s changed", name, k)
			}
		}
		if b.r.Exists(ctx, "other:board").Val() != 0 {
			t.Errorf("%s: another project's key written", name)
		}
	}
}

// --force deletes the keys it names in the same script; a failure after
// keeps them deleted (the person asked) but writes none of the archive's.
func TestRestoreProjectForce(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { DeleteProject(ctx, b.r, b.project) })
	keys, before := archived(t, b)
	b.r.HSet(ctx, BoardKey(b.project), "new", "1")
	b.r.Set(ctx, StreamKey(b.project, "extra"), "1", 0)
	present, _ := ProjectKeys(ctx, b.r, b.project)
	if _, err := RestoreProject(ctx, b.r, b.project, keys, present); err != nil {
		t.Fatal(err)
	}
	after := snapshotKeys(t, b, b.project)
	if len(after) != len(before) || after[BoardKey(b.project)] != before[BoardKey(b.project)] {
		t.Fatalf("after force: %d keys, board restored=%v", len(after), after[BoardKey(b.project)] == before[BoardKey(b.project)])
	}
	// A force-delete list naming another project's key is refused whole.
	if _, err := RestoreProject(ctx, b.r, b.project, nil, []string{"other:board"}); err == nil ||
		!strings.Contains(err.Error(), "other:board") {
		t.Fatalf("a foreign delete: %v", err)
	}
}

// foureyes: a forced restore whose RESTORE fails mid-way puts back every key
// it deleted, values and TTLs, so the project is exactly as before.
func TestRestoreProjectForcedFailureRollsBack(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { DeleteProject(ctx, b.r, b.project) })
	keys, _ := archived(t, b)
	b.r.HSet(ctx, BoardKey(b.project), "now", "1")
	b.r.Set(ctx, PlanKey(b.project), "plan-now", 0)
	b.r.Set(ctx, StreamKey(b.project, "ttl"), "x", time.Hour)
	before := snapshotKeys(t, b, b.project)
	present, _ := ProjectKeys(ctx, b.r, b.project)
	bad := append(slices.Clone(keys[:1]), DumpedKey{Key: b.project + ":junk", Dump: []byte("not a dump")})
	bad = append(bad, keys[1:]...)
	if _, err := RestoreProject(ctx, b.r, b.project, bad, present); err == nil {
		t.Fatal("a refused payload restored")
	}
	after := snapshotKeys(t, b, b.project)
	if len(after) != len(before) {
		t.Fatalf("keyspace %d keys, was %d", len(after), len(before))
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s not put back as it was", k)
		}
	}
	if ttl := b.r.PTTL(ctx, StreamKey(b.project, "ttl")).Val(); ttl <= 0 || ttl > time.Hour {
		t.Errorf("the TTL key came back with %v", ttl)
	}
	// A key of the archive present and not named for deletion refuses
	// before anything is deleted.
	b.r.Set(ctx, b.project+":junk", "j", 0)
	if _, err := RestoreProject(ctx, b.r, b.project, bad, []string{BoardKey(b.project)}); err == nil {
		t.Fatal("restored over a present key")
	}
	if b.r.Exists(ctx, BoardKey(b.project)).Val() != 1 {
		t.Fatal("the refusal ran after a delete")
	}
}
