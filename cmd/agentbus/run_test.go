package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// agentbus run (run-budget §5): no run yet; a run's state, elapsed and
// deliveries against the limits, a limit reached named; run reset refused to
// an agent, allowed to the person, after which a new run starts.
func TestRunCmd(t *testing.T) {
	b, rdb := dialMain(t)
	ctx := context.Background()
	env := map[string]string{"HOME": t.TempDir(), "XDG_CONFIG_HOME": t.TempDir()}
	run := func(args ...string) (int, string) {
		var out, errw bytes.Buffer
		code := runRun(ctx, b, args, func(k string) string { return env[k] }, &out, &errw)
		return code, out.String() + errw.String()
	}
	if code, msg := run(); code != 0 || !strings.Contains(msg, "no run yet") {
		t.Fatalf("no run: %d %q", code, msg)
	}
	if ok, err := b.AcquireDispatch(ctx, "tok", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch %v %v", ok, err)
	}
	head := strings.Repeat("a", 40)
	if _, _, err := b.FencedRunPass(ctx, "tok", nil, true, 15*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.FencedRunPass(ctx, "tok", []string{head}, true, 15*time.Minute); err != nil {
		t.Fatal(err)
	}
	code, msg := run()
	if code != 0 || !strings.Contains(msg, "state       running since ") || !strings.Contains(msg, "elapsed     0m (no limit)") ||
		!strings.Contains(msg, "deliveries  1 (no limit)") || strings.Contains(msg, "limit reached") {
		t.Fatalf("a run: %d %q", code, msg)
	}
	env["AGENT_BUS_RUN_MAX_DELIVERIES"], env["AGENT_BUS_RUN_MAX_DURATION"] = "1", "6h"
	if code, msg = run(); code != 0 || !strings.Contains(msg, "elapsed     0m of 6h") || !strings.Contains(msg, "deliveries  1 of 1") ||
		!strings.Contains(msg, "limit reached: deliveries") {
		t.Fatalf("limits: %d %q", code, msg)
	}
	// Two hours on, past a 1h limit: the duration named too.
	rec, err := b.Run(ctx)
	if err != nil || rec == nil {
		t.Fatalf("Run %v %v", rec, err)
	}
	if err := rdb.HSet(ctx, bus.RunKey(b.Project()), "started_at", rec.StartedAt-(2*time.Hour).Milliseconds()).Err(); err != nil {
		t.Fatal(err)
	}
	env["AGENT_BUS_RUN_MAX_DURATION"] = "1h"
	if code, msg = run(); code != 0 || !strings.Contains(msg, "elapsed     2h of 1h") || !strings.Contains(msg, "limit reached: duration") {
		t.Fatalf("duration reached: %d %q", code, msg)
	}
	env["AGENT_BUS_AGENT"] = "master"
	if code, msg := run("reset"); code != 2 || !strings.Contains(msg, "only the person") {
		t.Fatalf("an agent's reset: %d %q", code, msg)
	}
	if rec, _ := b.Run(ctx); rec == nil {
		t.Fatal("an agent reset the run")
	}
	delete(env, "AGENT_BUS_AGENT")
	if code, msg := run("reset"); code != 0 || !strings.Contains(msg, "the next merge-queue pass starts a new run") {
		t.Fatalf("the person's reset: %d %q", code, msg)
	}
	if rec, _ := b.Run(ctx); rec != nil {
		t.Fatalf("after reset: %+v", rec)
	}
	if !readOnlyCommand("run", nil) || readOnlyCommand("run", []string{"reset"}) {
		t.Fatal("the identity guard: run reads, run reset writes")
	}
	if code, _ := run("bogus"); code != 2 {
		t.Fatalf("a bad argument: %d", code)
	}
}
