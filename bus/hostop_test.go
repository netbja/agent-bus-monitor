package bus

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// herdr exit §4.2 (the relay): a host directive is a closed set; anything
// else is refused before it is written.
func TestHostOpValidate(t *testing.T) {
	ok := []HostOp{
		{Op: HostUp, From: "master"},
		{Op: HostRelaunch, Agent: "coder", From: "master"},
		{Op: HostReload, Agent: "coder", From: "sentinel"},
		{Op: HostSwap, Agent: "coder", Profile: "codex", From: "master"},
		{Op: HostPop, Agent: "docs", From: "master"},
		{Op: HostRetire, Agent: "docs", From: "master", IgnoreBackground: true},
		{Op: HostSuspend, From: "master"}, {Op: HostResume, From: "master"}, {Op: HostDrain, From: "master"},
	}
	for _, o := range ok {
		if err := o.Validate(); err != nil {
			t.Errorf("%+v refused: %v", o, err)
		}
	}
	bad := []HostOp{
		{Op: "exec", From: "master"},
		{Op: HostRelaunch, From: "master"},                               // no agent
		{Op: HostUp, Agent: "coder", From: "master"},                     // an agent where none is taken
		{Op: HostRelaunch, Agent: "Coder;rm", From: "master"},            // not a name
		{Op: HostRelaunch, Agent: "coder", Profile: "x", From: "master"}, // a profile outside swap and pop
		{Op: HostSwap, Agent: "coder", Profile: "a b", From: "master"},
		{Op: HostRelaunch, Agent: "coder", From: ""},
	}
	for _, o := range bad {
		if err := o.Validate(); err == nil {
			t.Errorf("%+v accepted", o)
		}
	}
}

func hostReplies(t *testing.T, b *Bus, ref string) []map[string]interface{} {
	t.Helper()
	evs, err := b.r.XRange(context.Background(), StreamKey(b.project, "cmd"), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]interface{}
	for _, e := range evs {
		if e.Values["type"] == CmdReply && e.Values["ref"] == ref {
			out = append(out, e.Values)
		}
	}
	return out
}

// §4.2: the directive is read back as written; accepting is fenced, happens
// once, and writes its reply in the same script; an outcome closes it.
func TestHostOpAcceptAndOutcome(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	id, err := b.SendHostOp(ctx, HostOp{Op: HostRelaunch, Agent: "coder", From: "master", IgnoreBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	ops, last, err := b.HostOpsAfter(ctx, "0-0")
	if err != nil || len(ops) != 1 || last != id {
		t.Fatalf("read %+v %q %v", ops, last, err)
	}
	if o := ops[0]; o.ID != id || o.Op != HostRelaunch || o.Agent != "coder" || o.From != "master" || !o.IgnoreBackground {
		t.Fatalf("read back %+v", o)
	}
	if _, err := b.FencedHostAccept(ctx, "bad", ops[0], HostAcceptWindow); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("without the lease: %v", err)
	}
	if r := hostReplies(t, b, id); len(r) != 0 {
		t.Fatalf("a reply without the lease: %v", r)
	}
	st, err := b.FencedHostAccept(ctx, "tok", ops[0], HostAcceptWindow)
	if err != nil || st != HostAccepted {
		t.Fatalf("accept = %q, %v", st, err)
	}
	if st, err := b.FencedHostAccept(ctx, "tok", ops[0], HostAcceptWindow); err != nil || st != HostSeen {
		t.Fatalf("accept twice = %q, %v", st, err)
	}
	r := hostReplies(t, b, id)
	if len(r) != 1 || r[0]["target"] != "master" || r[0]["from"] != HostTarget || !strings.HasPrefix(r[0]["command"].(string), "accepted") {
		t.Fatalf("replies %v", r)
	}
	open, err := b.UnfinishedHostOps(ctx)
	if err != nil || len(open) != 1 || open[0].ID != id || open[0].Op != HostRelaunch || open[0].From != "master" {
		t.Fatalf("unfinished %+v %v", open, err)
	}
	if err := b.FencedHostOutcome(ctx, "tok", ops[0], HostDone, "coder relaunched"); err != nil {
		t.Fatal(err)
	}
	if open, _ := b.UnfinishedHostOps(ctx); len(open) != 0 {
		t.Fatalf("still unfinished after its outcome: %+v", open)
	}
	if r := hostReplies(t, b, id); len(r) != 2 || r[1]["command"] != "done: coder relaunched" || r[1]["wake"] != "no" {
		t.Fatalf("outcome reply %v", r)
	}
}

// §4.2: past 5 s of its entry id, by Redis TIME, a directive is never
// accepted: the reply says expired and nothing is queued.
func TestHostOpExpired(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	now, err := b.ServerTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := strconv.FormatInt(now.Add(-6*time.Second).UnixMilli(), 10) + "-0"
	if err := b.r.XAdd(ctx, &redis.XAddArgs{Stream: StreamKey(b.project, "cmd"), ID: old,
		Values: map[string]interface{}{"from": "master", "target": HostTarget, "type": CmdHost, "op": HostUp, "command": "host up", "wake": "no"}}).Err(); err != nil {
		t.Fatal(err)
	}
	ops, _, err := b.HostOpsAfter(ctx, "0-0")
	if err != nil || len(ops) != 1 {
		t.Fatalf("read %+v %v", ops, err)
	}
	if st, err := b.FencedHostAccept(ctx, "tok", ops[0], HostAcceptWindow); err != nil || st != HostExpired {
		t.Fatalf("accept an old one = %q, %v", st, err)
	}
	if r := hostReplies(t, b, old); len(r) != 1 || r[0]["command"] != "expired" {
		t.Fatalf("replies %v", r)
	}
	if open, _ := b.UnfinishedHostOps(ctx); len(open) != 0 {
		t.Fatalf("an expired directive is unfinished: %+v", open)
	}
}

// §4.2: a directive refused before acceptance (not in the set, or the
// command's own refusal) gets the refusal as its reply, once.
func TestHostOpRefuse(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	o := HostOp{ID: "1-0", Op: "exec", From: "master"}
	if err := b.FencedHostRefuse(ctx, "tok", o, "not a host operation"); err != nil {
		t.Fatal(err)
	}
	if err := b.FencedHostRefuse(ctx, "tok", o, "not a host operation"); err != nil {
		t.Fatal(err)
	}
	if r := hostReplies(t, b, "1-0"); len(r) != 1 || r[0]["command"] != "refused: not a host operation" {
		t.Fatalf("replies %v", r)
	}
}

// reviewer-codex on 10a: an operation is marked running, under the lease,
// before it runs; only an accepted one can be; the sweep tells running
// (it may have run: unknown) from accepted (it never started: not run).
func TestHostOpRunning(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	var ops []HostOp
	for _, a := range []string{"coder", "foureyes"} {
		id, err := b.SendHostOp(ctx, HostOp{Op: HostRelaunch, Agent: a, From: "master"})
		if err != nil {
			t.Fatal(err)
		}
		o := HostOp{ID: id, Op: HostRelaunch, Agent: a, From: "master"}
		if st, err := b.FencedHostAccept(ctx, "tok", o, HostAcceptWindow); err != nil || st != HostAccepted {
			t.Fatalf("accept %q %v", st, err)
		}
		ops = append(ops, o)
	}
	if err := b.FencedHostRunning(ctx, "bad", ops[0]); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("running without the lease: %v", err)
	}
	if err := b.FencedHostRunning(ctx, "tok", ops[0]); err != nil {
		t.Fatal(err)
	}
	if err := b.FencedHostRunning(ctx, "tok", ops[0]); err == nil {
		t.Fatal("marked running twice")
	}
	open, err := b.UnfinishedHostOps(ctx)
	if err != nil || len(open) != 2 || open[0].State != HostRunning || open[1].State != HostAccepted {
		t.Fatalf("unfinished %+v %v", open, err)
	}
	if err := b.FencedHostOutcome(ctx, "tok", ops[0], HostUnknown, "it may have run"); err != nil {
		t.Fatal(err)
	}
	if err := b.FencedHostRunning(ctx, "tok", ops[0]); err == nil {
		t.Fatal("a closed operation marked running")
	}
}

// §4.2 (foureyes): done does not wake its sender; refused, failed, not run
// and unknown do: each leaves something to do.
func TestHostOpOutcomeWakes(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	for i, st := range []string{HostDone, HostRefused, HostFailed, HostNotRun, HostUnknown} {
		o := HostOp{ID: strconv.Itoa(i+1) + "-0", Op: HostUp, From: "master"}
		if err := b.FencedHostOutcome(ctx, "tok", o, st, "x"); err != nil {
			t.Fatal(err)
		}
		r := hostReplies(t, b, o.ID)
		if len(r) != 1 || r[0]["command"] != st+": x" || (r[0]["wake"] == "no") != (st == HostDone) {
			t.Errorf("%s: %v", st, r)
		}
	}
	if err := b.FencedHostOutcome(ctx, "tok", HostOp{ID: "9-0", Op: HostUp, From: "master"}, HostAccepted, "x"); err == nil {
		t.Fatal("accepted taken for an outcome")
	}
}
