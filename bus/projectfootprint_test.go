package bus

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// ProjectFootprint counts every {p}:* key and its streams' lengths, never a
// sibling's (projects-admin, the Redis page).
func TestProjectFootprint(t *testing.T) {
	testredis.Configure(t)
	c, err := Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	p := "fp" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Cleanup(func() {
		keys, _ := ProjectKeys(ctx, c, p)
		c.Del(ctx, append(keys, p+"-2:status")...)
	})
	b, _ := Open(c, p)
	for range 3 {
		if _, err := b.Status(ctx, "coder", "working", "x", AgentIdent{}); err != nil {
			t.Fatal(err)
		}
	}
	c.XAdd(ctx, &redis.XAddArgs{Stream: p + "-2:status", Values: map[string]any{"a": 1}})
	keys, _ := ProjectKeys(ctx, c, p)
	fp, err := ProjectFootprint(ctx, c, p)
	if err != nil || fp.Keys != len(keys) || fp.Keys < 2 || fp.Streams["status"] != 3 || fp.Streams["report"] != 0 {
		t.Fatalf("footprint %+v %v", fp, err)
	}
	if _, err := ProjectFootprint(ctx, c, "Bad*"); err == nil {
		t.Fatal("an invalid name was scanned")
	}
}
