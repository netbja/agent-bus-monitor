package bus

import (
	"context"
	"strings"
	"testing"
	"time"
)

// master-idle §4: a check-back is one field per agent, its times Redis's; the
// same what again counts up and keeps the first set id, another what or a
// clear starts at 1; the text is plain and at most 200 runes.
func TestCheckback(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	c1, err := b.SetCheckback(ctx, "master", 30*time.Minute, "k1/k2 checks on 7d27262c")
	if err != nil || c1.Count != 1 || c1.SetID <= 0 || c1.FirstSetID != c1.SetID || c1.DueMs != c1.SetID+30*60*1000 || c1.What != "k1/k2 checks on 7d27262c" {
		t.Fatalf("first %+v %v", c1, err)
	}
	time.Sleep(2 * time.Millisecond)
	c2, err := b.SetCheckback(ctx, "master", time.Hour, "  K1/K2 checks on 7d27262c ")
	if err != nil || c2.Count != 2 || c2.FirstSetID != c1.SetID || c2.SetID <= c1.SetID || c2.DueMs != c2.SetID+3600*1000 {
		t.Fatalf("same what again %+v %v", c2, err)
	}
	c3, _ := b.SetCheckback(ctx, "master", time.Minute, "another thing")
	if c3.Count != 1 || c3.FirstSetID != c3.SetID {
		t.Fatalf("another what %+v", c3)
	}
	if _, err := b.SetCheckback(ctx, "coder", time.Minute, "a coder's"); err != nil {
		t.Fatal(err)
	}
	all, err := b.Checkbacks(ctx)
	if err != nil || len(all) != 2 || all["master"].What != "another thing" || all["coder"].Agent != "coder" {
		t.Fatalf("list %+v %v", all, err)
	}
	if ok, err := b.ClearCheckback(ctx, "master"); err != nil || !ok {
		t.Fatalf("clear %v %v", ok, err)
	}
	if c, _ := b.SetCheckback(ctx, "master", time.Minute, "another thing"); c.Count != 1 {
		t.Fatalf("after a clear %+v", c)
	}
	for _, bad := range []string{strings.Repeat("x", 201), "line\x1bescape", "", "token ghp_0123456789abcdefghijklmnopqrstuvwxyzAB"} {
		if _, err := b.SetCheckback(ctx, "master", time.Minute, bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := b.SetCheckback(ctx, "master", 0, "x"); err == nil {
		t.Error("accepted a zero duration")
	}
	// Forgetting the agent clears its check-back.
	if _, err := b.Cmd(ctx, "master", "coder", "directive", "", "a stream to forget from"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ForgetAgent(ctx, "coder"); err != nil {
		t.Fatal(err)
	}
	if all, _ := b.Checkbacks(ctx); len(all) != 1 {
		t.Fatalf("after forget %+v", all)
	}
}

// retire --forget's fenced path clears it too.
func TestForgetIfClearClearsTheCheckback(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	own := retiredPop(t, b, "pop-cb")
	if _, err := b.SetCheckback(ctx, "pop-cb", time.Minute, "a build"); err != nil {
		t.Fatal(err)
	}
	if res, err := b.ForgetIfClear(ctx, "pop-cb", own, retiredAs(t, b, "pop-cb")); err != nil || !res.Forgot {
		t.Fatalf("ForgetIfClear = %+v, %v", res, err)
	}
	if all, _ := b.Checkbacks(ctx); len(all) != 0 {
		t.Fatalf("the check-back survived the retire: %+v", all)
	}
}
