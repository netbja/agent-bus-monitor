package bus

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// aclClientAs is a client of the disposable broker logged in as a user with
// role's rules (bus.ACLRules), removed at the test's end.
func aclClientAs(t *testing.T, admin *redis.Client, role string) *redis.Client {
	t.Helper()
	ctx := context.Background()
	name := "acltest-" + role + "-" + strings.ToLower(t.Name())
	args := []any{"ACL", "SETUSER", name, "reset", "on", "nopass"}
	for _, r := range ACLRules(role) {
		args = append(args, r)
	}
	if err := admin.Do(ctx, args...).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Do(context.Background(), "ACL", "DELUSER", name) })
	opt, err := redis.ParseURL(os.Getenv("AGENTBUS_TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	opt.Username, opt.Password = name, "x"
	c := redis.NewClient(opt)
	t.Cleanup(func() { c.Close() })
	return c
}

// ACL answer b: the plan's writes are master's, and the barrier is the
// broker's ACL (bus.ACLRules), live once broker.acl is on (step 4b); the
// CLI's refusal is the courtesy message. The agent user is refused every
// plan writer; the master user may.
func TestACLPlanWritesAreMasters(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	cleanPlan(t, b)
	b.Status(ctx, "master", "idle", "x", AgentIdent{})
	file := []byte(`{"plan":"v","tasks":[{"id":"t1","title":"x","owner":"coder"}]}`)
	open := func(role string) *Bus {
		x, err := Open(aclClientAs(t, b.r, role), p)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	agent, master := open(ACLAgent), open(ACLMaster)
	if err := master.LoadPlan(ctx, "p.json", "c1", file, []string{"t1"}); err != nil {
		t.Fatalf("master's load: %v", err)
	}
	for name, write := range map[string]func(*Bus) error{
		"LoadPlan":   func(x *Bus) error { return x.LoadPlan(ctx, "p.json", "c2", file, []string{"t1"}) },
		"PlanLink":   func(x *Bus) error { return x.PlanLink(ctx, "t1", 4) },
		"PlanDone":   func(x *Bus) error { return x.PlanDone(ctx, "t1") },
		"PlanNote":   func(x *Bus) error { return x.PlanNote(ctx, "t1", "n") },
		"PlanIgnore": func(x *Bus) error { return x.PlanIgnore(ctx, 12) },
		"ClearPlan":  func(x *Bus) error { return x.ClearPlan(ctx) },
	} {
		if err := write(agent); err == nil || !strings.Contains(err.Error(), "NOPERM") {
			t.Errorf("%s as agent: %v", name, err)
		}
		if name == "ClearPlan" {
			continue // master's clear would take the plan the others need
		}
		if err := write(master); err != nil {
			t.Errorf("%s as master: %v", name, err)
		}
	}
	if err := master.ClearPlan(ctx); err != nil {
		t.Errorf("ClearPlan as master: %v", err)
	}
	// Each kind on its own (a writer touches several): the agent may write
	// none of them, master all.
	for _, kind := range []string{"plan", "plan:facts", "plan:ignore"} {
		if got := aclDryRun(t, b.r, ACLAgent, "set", p+":"+kind, "x"); got == "OK" {
			t.Errorf("agent may write {p}:%s", kind)
		}
		if got := aclDryRun(t, b.r, ACLMaster, "set", p+":"+kind, "x"); got != "OK" {
			t.Errorf("master may not write {p}:%s: %s", kind, got)
		}
	}
}

// aclDryRun is ACL DRYRUN of args as a user with role's rules: "OK", or the
// refusal the broker gives.
func aclDryRun(t *testing.T, admin *redis.Client, role string, args ...string) string {
	t.Helper()
	ctx := context.Background()
	name := "acldry-" + role
	set := []any{"ACL", "SETUSER", name, "reset", "on", "nopass"}
	for _, r := range ACLRules(role) {
		set = append(set, r)
	}
	if err := admin.Do(ctx, set...).Err(); err != nil {
		t.Fatal(err)
	}
	defer admin.Do(ctx, "ACL", "DELUSER", name)
	dry := []any{"ACL", "DRYRUN", name}
	for _, a := range args {
		dry = append(dry, a)
	}
	got, err := admin.Do(ctx, dry...).Text()
	if err != nil {
		return err.Error()
	}
	return got
}
