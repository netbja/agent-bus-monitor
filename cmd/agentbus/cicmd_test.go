package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// agentbus ci reset <pr> (ci-loop §5): refused in an agent's environment,
// the person's deletes the PR's field (the loop re-opened), a PR with no
// field says so, a bad number or form is a usage error.
func TestCICmd(t *testing.T) {
	b, _ := dialMain(t)
	ctx := context.Background()
	env := map[string]string{}
	run := func(args ...string) (int, string) {
		var out, errw bytes.Buffer
		code := runCI(ctx, b, args, func(k string) string { return env[k] }, &out, &errw)
		return code, out.String() + errw.String()
	}
	if ok, err := b.AcquireDispatch(ctx, "tok", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch %v %v", ok, err)
	}
	step := bus.CIStep{PR: 42, Digest: strings.Repeat("a", 40), Cap: 0, Owner: "coder", Master: "master",
		Handoff: [2]string{"h", "h"}, MasterCopy: [2]string{"m", "m"}, StopMaster: "stop", StopNotice: "notice"}
	if _, err := b.FencedCIStep(ctx, "tok", step); err != nil {
		t.Fatal(err)
	}
	env["AGENT_BUS_AGENT"] = "coder"
	if code, msg := run("reset", "42"); code != 2 || !strings.Contains(msg, "only the person") {
		t.Fatalf("an agent's reset: %d %q", code, msg)
	}
	if rec, _ := b.CIRecord(ctx); !rec[42].Stopped {
		t.Fatalf("an agent reset the loop: %+v", rec)
	}
	delete(env, "AGENT_BUS_AGENT")
	if code, msg := run("reset", "42"); code != 0 || !strings.Contains(msg, "PR #42's CI loop re-opened") {
		t.Fatalf("the person's reset: %d %q", code, msg)
	}
	if rec, _ := b.CIRecord(ctx); len(rec) != 0 {
		t.Fatalf("after reset: %+v", rec)
	}
	if code, msg := run("reset", "42"); code != 0 || !strings.Contains(msg, "PR #42 has no CI loop record") {
		t.Fatalf("nothing to reset: %d %q", code, msg)
	}
	for _, args := range [][]string{nil, {"reset"}, {"reset", "0"}, {"reset", "x"}, {"reset", "-3"}, {"reset", "1", "2"}, {"list"}} {
		if code, msg := run(args...); code != 2 || !strings.Contains(msg, "usage: ci reset <pr>") {
			t.Fatalf("%v: %d %q", args, code, msg)
		}
	}
	if readOnlyCommand("ci", []string{"reset", "42"}) {
		t.Fatal("the identity guard: ci reset writes")
	}
}
