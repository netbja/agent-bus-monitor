package dispatch

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
)

func xadd(stream string, v map[string]interface{}) *redis.XAddArgs {
	return &redis.XAddArgs{Stream: stream, Values: v}
}

// replies are the dispatcher's replies to target, oldest first.
func hostReplies(t *testing.T, b *bus.Bus, target string) []string {
	t.Helper()
	evs, _, err := b.Recent(context.Background(), []string{"cmd"}, 200)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.From == bus.HostTarget && e.Target == target && e.Type == bus.CmdReply {
			out = append(out, e.Message)
		}
	}
	return out
}

func hostLoop(b *bus.Bus, run func(context.Context, string, bus.HostOp) (string, string)) *loop {
	var log bytes.Buffer
	return &loop{b: b, token: "host-holder", last: map[string]string{}, cfg: Config{Log: &log, EscalateTo: "master", HostOps: HostOpsConfig{Run: run}}}
}

// herdr exit §4.2: with the lease, a directive in the set is accepted (its
// reply written before anything runs), queued, run once on the worker and
// closed by its outcome; one outside the set is refused and never run;
// without the lease nothing is read, accepted or run.
func TestHostOpsAcceptRunOutcome(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	var ran []string
	l := hostLoop(b, func(_ context.Context, token string, o bus.HostOp) (string, string) {
		ran = append(ran, token+":"+o.Op+":"+o.Agent)
		return bus.HostDone, o.Agent + " relaunched"
	})
	st := l.newHostOpsState(ctx)
	if _, err := b.SendHostOp(ctx, bus.HostOp{Op: bus.HostRelaunch, Agent: "coder", From: "master"}); err != nil {
		t.Fatal(err)
	}
	l.hostOpsPass(ctx, st)
	if len(st.queue) != 0 || len(hostReplies(t, b, "master")) != 0 {
		t.Fatal("accepted without the lease")
	}
	if ok, err := b.AcquireDispatch(ctx, "host-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	// A directive outside the set, written raw as a stranger could.
	if _, err := b.Cmd(ctx, "master", bus.HostTarget, bus.CmdDirective, "", "host exec rm"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendHostOp(ctx, bus.HostOp{Op: bus.HostReload, Agent: "foureyes", From: "sentinel"}); err != nil {
		t.Fatal(err)
	}
	l.hostOpsPass(ctx, st)
	if r := hostReplies(t, b, "master"); len(r) != 1 || !strings.HasPrefix(r[0], "accepted") {
		t.Fatalf("master's replies %q", r)
	}
	if len(st.queue) != 2 || len(ran) != 0 {
		t.Fatalf("queued %d, ran %v: accepting runs nothing", len(st.queue), ran)
	}
	l.hostOpsWork(ctx, st, 2)
	if strings.Join(ran, ",") != "host-holder:relaunch:coder,host-holder:reload:foureyes" {
		t.Fatalf("ran %v: in acceptance order, once each", ran)
	}
	if r := hostReplies(t, b, "master"); len(r) != 2 || r[1] != "done: coder relaunched" {
		t.Fatalf("master's replies %q", r)
	}
	if open, _ := b.UnfinishedHostOps(ctx); len(open) != 0 {
		t.Fatalf("unfinished %+v", open)
	}
	l.hostOpsPass(ctx, st)
	if len(st.queue) != 0 {
		t.Fatal("a directive accepted twice")
	}
}

// §4.2: a directive the set refuses gets the refusal as its reply.
func TestHostOpsRefusesOutsideTheSet(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "host-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	l := hostLoop(b, func(context.Context, string, bus.HostOp) (string, string) { t.Fatal("ran"); return "", "" })
	st := l.newHostOpsState(ctx)
	if err := r.XAdd(ctx, xadd(bus.StreamKey(b.Project(), "cmd"), map[string]interface{}{"from": "coder", "target": bus.HostTarget, "type": bus.CmdHost,
		"op": "exec", "command": "host exec", "wake": "no"})).Err(); err != nil {
		t.Fatal(err)
	}
	l.hostOpsPass(ctx, st)
	if r := hostReplies(t, b, "coder"); len(r) != 1 || !strings.HasPrefix(r[0], `refused: host: "exec" is not a host operation`) {
		t.Fatalf("replies %q", r)
	}
	if len(st.queue) != 0 {
		t.Fatal("queued a refused directive")
	}
}

// §4.2: a dispatcher starting reports each accepted operation it never
// finished, to its sender and to master, as not run; it never runs it late.
func TestHostOpsStartReportsUnfinished(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "host-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	id, err := b.SendHostOp(ctx, bus.HostOp{Op: bus.HostReload, Agent: "coder", From: "sentinel"})
	if err != nil {
		t.Fatal(err)
	}
	if st, err := b.FencedHostAccept(ctx, "host-holder", bus.HostOp{ID: id, Op: bus.HostReload, Agent: "coder", From: "sentinel"}, bus.HostAcceptWindow); err != nil || st != bus.HostAccepted {
		t.Fatalf("accept %q %v", st, err)
	}
	l := hostLoop(b, func(context.Context, string, bus.HostOp) (string, string) { t.Fatal("ran late"); return "", "" })
	st := l.newHostOpsState(ctx)
	l.hostOpsPass(ctx, st)
	if r := hostReplies(t, b, "sentinel"); len(r) != 2 || r[1] != "not-run: the dispatcher stopped before running it" {
		t.Fatalf("sentinel's replies %q", r)
	}
	evs, _, _ := b.Recent(ctx, []string{"cmd"}, 200)
	told := false
	for _, e := range evs {
		told = told || e.Target == "master" && e.From == bus.HostTarget && strings.Contains(e.Message, "reload coder from sentinel was not run")
	}
	if !told {
		t.Fatal("master not told")
	}
	if len(st.queue) != 0 {
		t.Fatal("queued an unfinished operation")
	}
}
