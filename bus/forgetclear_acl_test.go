package bus

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
)

// ACL answer c (master's choice): master's retire --forget runs the script
// without a token, so it never reads the dispatch lease; it must not declare
// it either, or the master user (no write on {p}:dispatch) is refused. The
// dispatcher's fenced forget still declares it and still checks it.
func TestForgetIfClearNeedsNoLeaseWithoutAToken(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	b.Status(ctx, "coder", "idle", "x", AgentIdent{})
	args := []any{"ACL", "SETUSER", "acltest-forget", "reset", "on", "nopass"}
	for _, r := range ACLRules(ACLMaster) {
		args = append(args, r)
	}
	if err := b.r.Do(ctx, args...).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.r.Do(context.Background(), "ACL", "DELUSER", "acltest-forget") })
	opt, err := redis.ParseURL(os.Getenv("AGENTBUS_TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	opt.Username, opt.Password = "acltest-forget", "x"
	rc := redis.NewClient(opt)
	defer rc.Close()
	master, err := Open(rc, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := master.ForgetIfClear(ctx, "coder", "", RetiredAs{}); err != nil {
		t.Fatalf("master's unfenced forget: %v", err)
	}
	b.r.Set(ctx, DispatchKey(p), "the-holder", 0)
	if _, err := b.ForgetIfClearFenced(ctx, "not-the-holder", "coder", "", RetiredAs{}); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("a fenced forget with another token: %v", err)
	}
	if _, err := b.ForgetIfClearFenced(ctx, "the-holder", "coder", "", RetiredAs{}); err != nil {
		t.Fatalf("the holder's fenced forget: %v", err)
	}
	if _, err := master.ForgetIfClearFenced(ctx, "the-holder", "coder", "", RetiredAs{}); err == nil {
		t.Fatal("the master user wrote through the dispatcher's fenced forget")
	}
}
