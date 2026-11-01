package bus

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestStreamAfterAndOldest(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, ok, err := b.StreamOldest(ctx, "health:log"); ok || err != nil {
		t.Fatalf("an absent stream: ok %v err %v", ok, err)
	}
	var ids []string
	for _, m := range []string{"a", "b", "c"} {
		id, err := b.r.XAdd(ctx, &redis.XAddArgs{Stream: StreamKey(b.project, "health:log"), Values: map[string]any{"id": m}}).Result()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if id, ok, err := b.StreamOldest(ctx, "health:log"); !ok || err != nil || id != ids[0] {
		t.Fatalf("oldest %q %v %v", id, ok, err)
	}
	es, err := b.StreamAfter(ctx, "health:log", ids[0], 5)
	if err != nil || len(es) != 2 || es[0].ID != ids[1] || es[1].Fields["id"] != "c" {
		t.Fatalf("after the first: %+v %v", es, err)
	}
	if _, err := b.StreamAfter(ctx, "*", "", 1); err == nil {
		t.Fatal("a glob is not a stream kind")
	}
}
