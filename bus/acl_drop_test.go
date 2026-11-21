package bus

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// ACL answer e: dropping a head is master's, and the barrier is the
// broker's ACL (bus.ACLRules), live once broker.acl is on (step 4b); the
// CLI's refusal is the courtesy message. The agent user (docs included) may
// not drop; master may; the dispatcher prunes old drops.
func TestACLDropIsMasters(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { b.r.Del(context.Background(), MergeQueueDroppedKey(p)) })
	as := func(role string) *Bus {
		name := "acldrop-" + role
		args := []any{"ACL", "SETUSER", name, "reset", "on", "nopass"}
		for _, r := range ACLRules(role) {
			args = append(args, r)
		}
		if err := b.r.Do(ctx, args...).Err(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { b.r.Do(context.Background(), "ACL", "DELUSER", name) })
		opt, err := redis.ParseURL(os.Getenv("AGENTBUS_TEST_REDIS_URL"))
		if err != nil {
			t.Fatal(err)
		}
		opt.Username, opt.Password = name, "x"
		c := redis.NewClient(opt)
		t.Cleanup(func() { c.Close() })
		x, err := Open(c, p)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	const head = "fa2d796f00000000000000000000000000000000"
	if err := as(ACLAgent).DropHead(ctx, head, "r", "docs"); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("agent's drop: %v", err)
	}
	if err := as(ACLMaster).DropHead(ctx, head, "r", "master"); err != nil {
		t.Fatalf("master's drop: %v", err)
	}
	dispatcherCanPrune := false
	for _, r := range ACLRules(ACLDispatcher) {
		dispatcherCanPrune = dispatcherCanPrune || r == "~*:mergequeue:dropped"
	}
	if !dispatcherCanPrune {
		t.Error("the dispatcher may not prune old drops")
	}
}
