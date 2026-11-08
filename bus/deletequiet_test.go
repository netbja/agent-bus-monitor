package bus

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// quietWorld is a project with a few keys and a sibling that must survive.
func quietWorld(t *testing.T) (*redis.Client, string, context.Context) {
	t.Helper()
	testredis.Configure(t)
	c, err := Connect("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	ctx := context.Background()
	p := "dq" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, _ := Open(c, p)
	if _, err := b.Status(ctx, "coder", "working", "x", AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	sib, _ := Open(c, p+"-2")
	if _, err := sib.Status(ctx, "coder", "working", "x", AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{p, p + "-2"} {
			keys, _ := ProjectKeys(ctx, c, q)
			if len(keys) > 0 {
				c.Del(ctx, keys...)
			}
		}
		beforeQuietScript, afterQuietScript = nil, nil
	})
	return c, p, ctx
}

func keysOf(t *testing.T, ctx context.Context, c *redis.Client, p string) []string {
	t.Helper()
	keys, err := ProjectKeys(ctx, c, p)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// projects-admin §5.3: the final check and the DEL are one script: a lease
// or a fresh record of a live service refuses and deletes nothing; a
// stopped or stale record does not; a sibling is never touched.
func TestDeleteProjectIfQuiet(t *testing.T) {
	c, p, ctx := quietWorld(t)
	before := len(keysOf(t, ctx, c, p))
	now := func() int64 {
		tm, _ := c.Time(ctx).Result()
		return tm.UnixMilli()
	}
	refuse := func(name string, set func(), what, host string) {
		t.Helper()
		set()
		var q *DeleteRefusal
		if err := ProjectQuiet(ctx, c, p); !errors.As(err, &q) || q.What != what || q.Host != host {
			t.Fatalf("%s: ProjectQuiet %v", name, err)
		}
		_, _, err := DeleteProjectIfQuiet(ctx, c, p)
		var r *DeleteRefusal
		if !errors.As(err, &r) || r.What != what || r.Host != host {
			t.Fatalf("%s: err %v", name, err)
		}
		if n := len(keysOf(t, ctx, c, p)); n < before {
			t.Fatalf("%s: keys deleted although refused (%d of %d left)", name, n, before)
		}
	}
	refuse("dispatch", func() { c.Set(ctx, DispatchKey(p), "tok", time.Minute) }, "dispatch", "")
	c.Del(ctx, DispatchKey(p))
	refuse("conductor", func() { c.Set(ctx, ConductorKey(p), "tok", time.Minute) }, "conductor", "")
	c.Del(ctx, ConductorKey(p))
	refuse("service", func() {
		c.HSet(ctx, ServicesKey(p), "dispatch", `{"state":"ready","host":"vdr","at":`+strconv.FormatInt(now(), 10)+`}`)
	}, "dispatch", "vdr")

	// A stopped service's last record, fresh, and a stale live one: quiet.
	c.HSet(ctx, ServicesKey(p), "dispatch", `{"state":"stopped","host":"vdr","at":`+strconv.FormatInt(now(), 10)+`}`,
		"missions", `{"state":"ready","host":"vdr","at":`+strconv.FormatInt(now()-31_000, 10)+`}`,
		ServicesInstalledField, `{"kinds":["dispatch"]}`)
	if err := ProjectQuiet(ctx, c, p); err != nil {
		t.Fatalf("a stopped and a stale record: %v", err)
	}
	all := keysOf(t, ctx, c, p)
	n, left, err := DeleteProjectIfQuiet(ctx, c, p)
	if err != nil || n != int64(len(all)) || len(left) != 0 {
		t.Fatalf("delete: %d of %d, left %v, err %v", n, len(all), left, err)
	}
	if k := keysOf(t, ctx, c, p); len(k) != 0 {
		t.Fatalf("left %v", k)
	}
	if k := keysOf(t, ctx, c, p+"-2"); len(k) == 0 {
		t.Fatal("the sibling lost its keys")
	}
	if _, _, err := DeleteProjectIfQuiet(ctx, c, "Bad*"); err == nil {
		t.Fatal("an invalid name was deleted")
	}
}

// reviewer-codex: a lease taken between the Go scan and the script is
// refused by the script; foureyes: a key written after the script is
// reported, never a clean delete.
func TestDeleteProjectIfQuietRaces(t *testing.T) {
	c, p, ctx := quietWorld(t)
	beforeQuietScript = func() { c.Set(ctx, DispatchKey(p), "tok", time.Minute) }
	var r *DeleteRefusal
	if _, _, err := DeleteProjectIfQuiet(ctx, c, p); !errors.As(err, &r) || r.What != "dispatch" {
		t.Fatalf("lease in the window: %v", err)
	}
	beforeQuietScript = nil
	c.Del(ctx, DispatchKey(p))
	afterQuietScript = func() { c.XAdd(ctx, &redis.XAddArgs{Stream: p + ":status", Values: map[string]any{"agent": "coder"}}) }
	n, left, err := DeleteProjectIfQuiet(ctx, c, p)
	if err != nil || n == 0 || !slices.Equal(left, []string{p + ":status"}) {
		t.Fatalf("came back: %d %v %v", n, left, err)
	}
}

// foureyes: after the script deleted, a failed re-scan is never "nothing
// deleted": the count comes back with a RescanError. A record named
// "installed" is the install marker, never a service; gave-up is not live.
func TestDeleteProjectIfQuietAfterTheScript(t *testing.T) {
	c, p, ctx := quietWorld(t)
	now, _ := c.Time(ctx).Result()
	c.HSet(ctx, ServicesKey(p), ServicesInstalledField, `{"kinds":["dispatch"],"at":`+strconv.FormatInt(now.UnixMilli(), 10)+`}`,
		"missions", `{"state":"gave-up","host":"vdr","at":`+strconv.FormatInt(now.UnixMilli(), 10)+`}`)
	if err := ProjectQuiet(ctx, c, p); err != nil {
		t.Fatalf("an install marker or a gave-up service read as running: %v", err)
	}
	all := keysOf(t, ctx, c, p)
	cctx, cancel := context.WithCancel(ctx)
	afterQuietScript = cancel // the re-scan fails: its context is gone
	n, left, err := DeleteProjectIfQuiet(cctx, c, p)
	var re *RescanError
	if !errors.As(err, &re) || n != int64(len(all)) || re.Deleted != n || left != nil {
		t.Fatalf("re-scan failure: n %d of %d, left %v, err %v", n, len(all), left, err)
	}
}
