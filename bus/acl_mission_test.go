package bus

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// ACL answer d: the conductor's operations are master's, and the barrier is
// the broker's ACL (bus.ACLRules), live once broker.acl is on (step 4b); the
// CLI's refusal is the courtesy message. The agent user may not queue one;
// master and the dispatcher (the conductor) may; missionops:results stays
// the dispatcher's.
func TestACLMissionOpsAreMasters(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { b.r.Del(context.Background(), MissionOpsKey(p)) })
	as := func(role string) *Bus {
		name := "aclmission-" + role
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
	if _, err := as(ACLAgent).SubmitMissionOp(ctx, "coder", "retry", []string{"chg/t1"}); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("agent's op: %v", err)
	}
	for _, role := range []string{ACLMaster, ACLDispatcher} {
		if _, err := as(role).SubmitMissionOp(ctx, role, "retry", []string{"chg/t1"}); err != nil {
			t.Errorf("%s's op: %v", role, err)
		}
	}
	for role, want := range map[string]bool{ACLAgent: false, ACLMaster: false, ACLDispatcher: true} {
		if got := slicesHas(ACLRules(role), "~*:missionops:results"); got != want {
			t.Errorf("%s writes missionops:results: %v, want %v", role, got, want)
		}
	}
}

func slicesHas(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
