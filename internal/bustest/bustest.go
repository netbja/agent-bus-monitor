// Package bustest opens test buses on the explicit test broker only
// (internal/testredis), for the tests of more than one package.
package bustest

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// Open returns a project-scoped Bus on a throwaway project plus a raw client
// (tests need the client to pre-create consumer groups). Requires explicit test
// configuration; an unavailable configured broker fails rather than skips.
func Open(t *testing.T) (*bus.Bus, *redis.Client) {
	t.Helper()
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatalf("explicit test Redis unavailable: %v", err)
	}
	project := "t" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, err := bus.Open(r, project)
	if err != nil {
		t.Fatalf("Open(%q): %v", project, err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		r.Del(ctx, bus.StreamKey(project, "cmd"), bus.ArmedKey(project, "dev"))
		r.Close()
	})
	return b, r
}

// Leased is a Bus on a throwaway project whose dispatch lease is held with
// the token "tok", deleted at the end of the test.
func Leased(t *testing.T) *bus.Bus {
	t.Helper()
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r, "notify"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()); _ = r.Close() })
	if ok, err := b.AcquireDispatch(context.Background(), "tok", time.Minute); err != nil || !ok {
		t.Fatalf("lease %v %v", ok, err)
	}
	return b
}
