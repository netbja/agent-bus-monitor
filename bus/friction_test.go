package bus

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// tick lets the next stream id fall in a later millisecond than the last.
func tick() { time.Sleep(3 * time.Millisecond) }

// frictionKit writes each signal kind for agent, as the bus writes them.
type frictionKit struct {
	t *testing.T
	b *Bus
}

func (k frictionKit) reject(author string) string {
	k.t.Helper()
	id, err := k.b.AppendVerdict(context.Background(), Verdict{Subject: "s", Author: author, Reviewer: "foureyes", Decision: "reject", Message: "no"})
	if err != nil {
		k.t.Fatal(err)
	}
	return id
}

func (k frictionKit) approve(author string) string {
	k.t.Helper()
	id, err := k.b.AppendVerdict(context.Background(), Verdict{Subject: "s", Author: author, Reviewer: "foureyes", Decision: "approve", Message: "ok"})
	if err != nil {
		k.t.Fatal(err)
	}
	return id
}

// ci writes the owner's hand-off and master's copy as the CI loop does.
func (k frictionKit) ci(owner string) string {
	k.t.Helper()
	ctx := context.Background()
	id, err := k.b.r.XAdd(ctx, xadd(k.b, "cmd", "from", "dispatch", "target", owner, "type", CmdDirective, "ref", "",
		"command", "CI failed on PR #9 (hand-off 1)", "text_complete", "yes", "episode", "ci:pr:9=abc")).Result()
	if err != nil {
		k.t.Fatal(err)
	}
	if _, err := k.b.r.XAdd(ctx, xadd(k.b, "cmd", "from", "dispatch", "target", "master", "type", CmdDirective, "ref", "",
		"command", "copy", "text_complete", "yes", "episode", "ci:pr:9=abc", "wake", "no")).Result(); err != nil {
		k.t.Fatal(err)
	}
	return id
}

func (k frictionKit) event(kind, agent string) string {
	k.t.Helper()
	id, err := k.b.AppendEvent(context.Background(), TermEvent{Kind: kind, Agent: agent, By: "dispatch"})
	if err != nil {
		k.t.Fatal(err)
	}
	return id
}

func (k frictionKit) escalation(agent string) string {
	k.t.Helper()
	ok, err := k.b.AcquireDispatch(context.Background(), "tok", time.Minute)
	if err != nil {
		k.t.Fatal(err)
	}
	_ = ok // already held by this test's token
	id, err := k.b.FencedEscalation(context.Background(), "tok", "dispatch", "master", agent, agent+": unread; 1 waiting")
	if err != nil {
		k.t.Fatal(err)
	}
	return id
}

// every writes one of each signal for agent and returns them in kind order.
func (k frictionKit) every(agent string) []FrictionSignal {
	var out []FrictionSignal
	out = append(out, FrictionSignal{FrictionReject, k.reject(agent), SignalVerdicts})
	tick()
	out = append(out, FrictionSignal{FrictionCI, k.ci(agent), SignalCmd})
	tick()
	out = append(out, FrictionSignal{FrictionStall, k.event(EventBlocked, agent), SignalEvents})
	tick()
	out = append(out, FrictionSignal{FrictionStall, k.event(EventWakeNotLanded, agent), SignalEvents})
	tick()
	out = append(out, FrictionSignal{FrictionStall, k.escalation(agent), SignalCmd})
	tick()
	return out
}

func startTask(t *testing.T, b *Bus, task, owner string) {
	t.Helper()
	ctx := context.Background()
	if _, err := b.Request(ctx, task, "master", owner, "", "do it", 0); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, task, owner); err != nil {
		t.Fatal(err)
	}
	tick()
}

func doneTask(t *testing.T, b *Bus, task, owner string) {
	t.Helper()
	tick()
	if _, err := b.CompleteRequest(context.Background(), task, owner, "done"); err != nil {
		t.Fatal(err)
	}
	tick()
}

// TaskFriction counts the owner's signals inside the request's life, from
// accept to done; the same signals outside it, or for another agent, do not
// count, and approvals or master's CI copy never do.
func TestTaskFriction(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	k := frictionKit{t, b}
	k.every("coder") // before the window
	tick()
	startTask(t, b, "parser", "coder")
	want := k.every("coder")
	k.every("coder2") // another agent, inside the window
	k.approve("coder")
	if _, err := b.AppendVerdict(ctx, Verdict{Subject: "s", Author: "foureyes", Reviewer: "coder", Decision: "reject", Message: "x"}); err != nil {
		t.Fatal(err) // coder as the reviewer: not its friction
	}
	doneTask(t, b, "parser", "coder")
	k.every("coder") // after done

	got, err := b.TaskFriction(ctx, "parser")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("friction\n got %v\nwant %v", got, want)
	}
	if s := FrictionSummary(got); s != "1 reject, 1 ci, 3 stall" {
		t.Errorf("summary %q", s)
	}
}

// A green task, a request never accepted, and a board task that is not a
// tracked request have no friction; an unknown task is an error.
func TestTaskFrictionNone(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	startTask(t, b, "green", "coder")
	doneTask(t, b, "green", "coder")
	if got, err := b.TaskFriction(ctx, "green"); err != nil || len(got) != 0 {
		t.Errorf("green: %v %v", got, err)
	}
	if _, err := b.Request(ctx, "unaccepted", "master", "coder", "", "x", 0); err != nil {
		t.Fatal(err)
	}
	frictionKit{t, b}.reject("coder")
	if got, err := b.TaskFriction(ctx, "unaccepted"); err != nil || len(got) != 0 {
		t.Errorf("unaccepted: %v %v", got, err)
	}
	if _, err := b.TaskFriction(ctx, "nosuch"); err == nil {
		t.Error("unknown task: no error")
	}
}

// While the task is still open the window runs to now.
func TestTaskFrictionOpenTask(t *testing.T) {
	b := dialTest(t)
	startTask(t, b, "open", "coder")
	id := frictionKit{t, b}.reject("coder")
	got, err := b.TaskFriction(context.Background(), "open")
	if err != nil || !reflect.DeepEqual(got, []FrictionSignal{{FrictionReject, id, SignalVerdicts}}) {
		t.Errorf("open task: %v %v", got, err)
	}
}

// done with no friction sends nothing; with friction exactly one
// retro-<task>, from agentbus to the owner, listing the signals; a repeated
// done or a done of the retro itself sends nothing more.
func TestRetroDue(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	k := frictionKit{t, b}

	startTask(t, b, "green", "coder")
	doneTask(t, b, "green", "coder")
	if d, err := b.RetroDue(ctx, "green"); err != nil || d.Task != "" {
		t.Fatalf("green: %+v %v", d, err)
	}

	startTask(t, b, "rough", "coder")
	rej := k.reject("coder")
	tick()
	ci := k.ci("coder")
	doneTask(t, b, "rough", "coder")
	d, err := b.RetroDue(ctx, "rough")
	if err != nil || d.Task != "retro-rough" || !d.Sent || len(d.Signals) != 2 {
		t.Fatalf("rough: %+v %v", d, err)
	}
	board, _ := b.Board(ctx)
	e, ok := board["retro-rough"]
	if !ok || e.Request == nil || e.Request.From != SystemSender || e.Request.Target != "coder" || e.State != "requested" {
		t.Fatalf("retro request %+v", e)
	}
	msgs, _ := b.r.XRange(ctx, StreamKey(b.project, "cmd"), e.Request.ID, e.Request.ID).Result()
	body, _ := msgs[0].Values["command"].(string)
	for _, s := range []string{"rough", "reject " + rej, "ci " + ci, "agentbus retro evidence rough"} {
		if !strings.Contains(body, s) {
			t.Errorf("retro body lacks %q:\n%s", s, body)
		}
	}

	again, err := b.RetroDue(ctx, "rough")
	if err != nil || again.Task != "retro-rough" || again.Sent {
		t.Errorf("second done: %+v %v", again, err)
	}
	if n := countTasks(t, b, "retro-"); n != 1 {
		t.Errorf("%d retro requests", n)
	}

	// The retro's own done: no retro of a retro, even with friction.
	if err := b.AcceptRequest(ctx, "retro-rough", "coder"); err != nil {
		t.Fatal(err)
	}
	k.reject("coder")
	doneTask(t, b, "retro-rough", "coder")
	if d, err := b.RetroDue(ctx, "retro-rough"); err != nil || d.Task != "" {
		t.Errorf("retro of a retro: %+v %v", d, err)
	}
	if n := countTasks(t, b, "retro-"); n != 1 {
		t.Errorf("%d retro requests after the retro's done", n)
	}
}

func countTasks(t *testing.T, b *Bus, prefix string) int {
	t.Helper()
	board, err := b.Board(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for task := range board {
		if strings.HasPrefix(task, prefix) {
			n++
		}
	}
	return n
}

// The retro's name is retro-<task>; a long task keeps a prefix and gains a
// hash of the whole name, so two long tasks never share a retro.
func TestRetroTaskName(t *testing.T) {
	if got := RetroTaskName("parser"); got != "retro-parser" {
		t.Errorf("%q", got)
	}
	if got := RetroTaskName("a2345678901234567890123456"); got != "retro-a2345678901234567890123456" {
		t.Errorf("26 characters fit: %q", got)
	}
	x, y := "aaaaaaaaaaaaaaaaaaaaaaaaaax", "aaaaaaaaaaaaaaaaaaaaaaaaaay"
	rx, ry := RetroTaskName(x), RetroTaskName(y)
	if rx == ry || !ValidName(rx) || !ValidName(ry) || !strings.HasPrefix(rx, "retro-aaaa") {
		t.Errorf("long: %q %q", rx, ry)
	}
	if RetroTaskName(x) != rx {
		t.Error("not stable")
	}
	if !IsRetroTask("retro-x") || IsRetroTask("retrox") {
		t.Error("IsRetroTask")
	}
}

// Two long tasks with the same first characters each get their own retro,
// and retro none of one never closes the other's.
func TestRetroDueLongTasks(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	x, y := "aaaaaaaaaaaaaaaaaaaaaaaaaax", "aaaaaaaaaaaaaaaaaaaaaaaaaay"
	for _, task := range []string{x, y} {
		startTask(t, b, task, "coder")
		frictionKit{t, b}.reject("coder")
		doneTask(t, b, task, "coder")
		d, err := b.RetroDue(ctx, task)
		if err != nil || !d.Sent || d.Task != RetroTaskName(task) {
			t.Fatalf("%s: %+v %v", task, d, err)
		}
	}
	if n := countTasks(t, b, "retro-"); n != 2 {
		t.Errorf("%d retros, want 2", n)
	}
}

// A retro name already on the board for another task is refused, never
// taken as this task's retro: an entry not sent by agentbus, or agentbus's
// retro of another task.
func TestRetroDueRefusesAnotherTasksRetro(t *testing.T) {
	cases := []struct{ from, body string }{
		{"master", retroBodyPrefix("rough") + "1 reject). a look-alike"},
		{SystemSender, retroBodyPrefix("other") + "1 reject). another task's retro"},
	}
	for _, c := range cases {
		b := dialTest(t)
		ctx := context.Background()
		if _, err := b.Request(ctx, RetroTaskName("rough"), c.from, "coder", "", c.body, 0); err != nil {
			t.Fatal(err)
		}
		startTask(t, b, "rough", "coder")
		frictionKit{t, b}.reject("coder")
		doneTask(t, b, "rough", "coder")
		if d, err := b.RetroDue(ctx, "rough"); err == nil {
			t.Errorf("from %s: another entry taken as the retro: %+v", c.from, d)
		}
	}
}

// A stall from the events stream and one from the cmd stream may share an
// id: each signal names its stream and its text is read from that one.
func TestSignalTextSameIDTwoStreams(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", time.Minute); !ok || err != nil {
		t.Fatal(ok, err)
	}
	startTask(t, b, "same", "coder")
	id := "9999999999999-0"
	if _, err := b.r.XAdd(ctx, &redis.XAddArgs{Stream: EventsKey(b.project), ID: id,
		Values: []any{"kind", EventBlocked, "agent", "coder2", "detail", "other agent"}}).Result(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.r.XAdd(ctx, &redis.XAddArgs{Stream: StreamKey(b.project, "cmd"), ID: id,
		Values: []any{"from", "dispatch", "target", "master", "type", CmdEscalation, "ref", "", "command", "coder: unread", "agent", "coder"}}).Result(); err != nil {
		t.Fatal(err)
	}
	got, err := b.SignalText(ctx, FrictionSignal{Kind: FrictionStall, ID: id, Stream: SignalCmd})
	if err != nil || got != "coder: unread" {
		t.Errorf("cmd stall: %q %v", got, err)
	}
	got, err = b.SignalText(ctx, FrictionSignal{Kind: FrictionStall, ID: id, Stream: SignalEvents})
	if err != nil || got != "blocked other agent" {
		t.Errorf("events stall: %q %v", got, err)
	}
}

func TestSignalText(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	k := frictionKit{t, b}
	cases := []struct {
		s    FrictionSignal
		want string
	}{
		{FrictionSignal{FrictionReject, k.reject("coder"), SignalVerdicts}, "foureyes rejected coder's s: no"},
		{FrictionSignal{FrictionCI, k.ci("coder"), SignalCmd}, "CI failed on PR #9 (hand-off 1)"},
		{FrictionSignal{FrictionStall, k.event(EventBlocked, "coder"), SignalEvents}, "blocked"},
		{FrictionSignal{FrictionStall, k.escalation("coder"), SignalCmd}, "coder: unread; 1 waiting"},
		{FrictionSignal{FrictionPerson, "-", ""}, ""},
	}
	for _, c := range cases {
		got, err := b.SignalText(ctx, c.s)
		if err != nil || got != c.want {
			t.Errorf("%v: %q %v, want %q", c.s, got, err, c.want)
		}
	}
	if _, err := b.SignalText(ctx, FrictionSignal{FrictionCI, "1-0", SignalCmd}); err == nil {
		t.Error("a trimmed entry: no error")
	}
}

// When master owns the task, the CI loop's wake=no copy to master is not a
// second hand-off.
func TestTaskFrictionMasterOwnsCICopy(t *testing.T) {
	b := dialTest(t)
	startTask(t, b, "mtask", "master")
	id := frictionKit{t, b}.ci("master")
	got, err := b.TaskFriction(context.Background(), "mtask")
	if err != nil || !reflect.DeepEqual(got, []FrictionSignal{{FrictionCI, id, SignalCmd}}) {
		t.Errorf("master's task: %v %v", got, err)
	}
}

func xadd(b *Bus, kind string, kv ...any) *redis.XAddArgs {
	return &redis.XAddArgs{Stream: StreamKey(b.project, kind), Values: kv}
}

// An escalation names its agent in an additive field.
func TestFencedEscalationAgentField(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.FencedEscalation(ctx, "tok", "dispatch", "master", "coder", "x"); err != ErrNotHolder {
		t.Fatalf("without the lease: %v", err)
	}
	if ok, err := b.AcquireDispatch(ctx, "tok", time.Minute); !ok || err != nil {
		t.Fatal(ok, err)
	}
	id, err := b.FencedEscalation(ctx, "tok", "dispatch", "master", "coder", "coder: unread")
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := b.r.XRange(ctx, StreamKey(b.project, "cmd"), id, id).Result()
	want := map[string]any{"from": "dispatch", "target": "master", "type": CmdEscalation, "ref": "",
		"command": "coder: unread", "text_complete": "yes", "agent": "coder"}
	if len(msgs) != 1 || !reflect.DeepEqual(msgs[0].Values, want) {
		t.Errorf("entry %v, want %v", msgs, want)
	}
	if _, err := b.FencedEscalation(ctx, "tok", "dispatch", "master", "Bad Agent", "x"); err == nil {
		t.Error("invalid agent accepted")
	}
}
