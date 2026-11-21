package bus

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
)

// ACL answer a (broker-hardening step 4, the person's yes): any agent sends
// a request, so publishRequest reads the plan and the run outside its
// script and declares only what it writes. A user that may write the board,
// cmd and swap keys, and only read the rest, sends one at its plan task's
// priority, and is still refused while the run drains.
func TestRequestNeedsNoPlanOrRunWrite(t *testing.T) {
	b := dialTest(t)
	cleanPlan(t, b)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { b.r.Del(context.Background(), BoardKey(p), StreamKey(p, "cmd"), RunKey(p)) })
	if err := b.r.Do(ctx, "ACL", "SETUSER", "acltest-request", "reset", "on", "nopass", "+@all", "-@dangerous",
		"%R~*", "~"+p+":board", "~"+p+":cmd", "~"+p+":swap:*").Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.r.Do(context.Background(), "ACL", "DELUSER", "acltest-request") })
	opt, err := redis.ParseURL(os.Getenv("AGENTBUS_TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	opt.Username, opt.Password = "acltest-request", "x"
	rc := redis.NewClient(opt)
	defer rc.Close()
	b.Status(ctx, "master", "idle", "x", AgentIdent{}) // LoadPlan wants the project on the broker
	file := `{"plan":"v","tasks":[{"id":"t-high","title":"x","owner":"coder"},{"id":"t-file","title":"x","owner":"coder","priority":"low"}]}`
	if err := b.LoadPlan(ctx, "p.json", "c1", []byte(file), []string{"t-high", "t-file"}); err != nil {
		t.Fatal(err)
	}
	agent, err := Open(rc, p)
	if err != nil {
		t.Fatal(err)
	}
	b.SetPlanPriority(ctx, "t-high", PriorityHigh, "charlie")
	for _, c := range []struct{ task, want, by string }{{"t-high", PriorityHigh, "charlie"}, {"t-file", PriorityLow, ""}, {"free", "", ""}} {
		if _, err := agent.RequestBy(ctx, c.task, "coder", "reviewer", "", "do it", 0, false); err != nil {
			t.Fatalf("%s as a user without plan or run write: %v", c.task, err)
		}
		board, _ := b.Board(ctx)
		if r := board[c.task].Request; r == nil || r.Priority != c.want || r.PriorityBy != c.by {
			t.Fatalf("%s: %+v, want %q by %q", c.task, r, c.want, c.by)
		}
	}
	b.r.HSet(ctx, RunKey(p), "state", "draining")
	if _, err := agent.RequestBy(ctx, "late", "coder", "reviewer", "", "do it", 0, false); !errors.Is(err, ErrRunDraining) {
		t.Fatalf("draining: %v", err)
	}
	if _, err := agent.RequestBy(ctx, "persons", "coder", "reviewer", "", "do it", 0, true); err != nil {
		t.Fatalf("the person's while draining: %v", err)
	}
}
