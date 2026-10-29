package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"

	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// lastEvent parses the final non-empty JSON line emitted by runSubscribe.
func lastEvent(t *testing.T, out string) subEvent {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var ev subEvent
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &ev); err != nil {
		t.Fatalf("output last line not JSON: %q (%v)", out, err)
	}
	return ev
}

// syncBuf is a goroutine-safe io.Writer so the --loop test can read what the
// background subscriber has written without a data race.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// dialMain returns a project-scoped Bus on a throwaway project plus a raw client
// (tests need the client to pre-create consumer groups). Requires explicit test
// configuration; an unavailable configured broker fails rather than skips.
func dialMain(t *testing.T) (*bus.Bus, *redis.Client) {
	t.Helper()
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatalf("explicit test Redis unavailable: %v", err)
	}
	project := "t" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, err := bus.Open(r, project)
	if err != nil {
		t.Fatalf("Open(%q): %v", project, err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		r.Del(ctx, bus.StreamKey(project, "cmd"), bus.ArmedKey(project, "dev"))
		r.Close()
	})
	return b, r
}

func TestRunSubscribeFatalOnBadAgent(t *testing.T) {
	var buf bytes.Buffer
	// b is nil on purpose: ValidName rejects the agent before any Redis call.
	code := runSubscribe(context.Background(), nil, "Bad Agent", "host-1", time.Second, "0", false, false, &buf)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (fatal)", code)
	}
	ev := lastEvent(t, buf.String())
	if ev.Event != "fatal" || ev.Rearm == nil || *ev.Rearm {
		t.Errorf("event = %+v, want event=fatal rearm=false", ev)
	}
}

func TestRunSubscribeDelivers(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	stream := bus.StreamKey(b.Project(), "cmd")
	// Pre-create dev's group at "0" so the already-published entry is delivered
	// deterministically; floor "0" disables the skip-backlog filter.
	if err := r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatalf("XGroupCreate: %v", err)
	}
	if _, err := b.Cmd(ctx, "review", "dev", bus.CmdChallenge, "C1", "justify X"); err != nil {
		t.Fatalf("Cmd: %v", err)
	}

	var buf bytes.Buffer
	code := runSubscribe(ctx, b, "dev", "host-1", 4*time.Second, "0", false, false, &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (delivered)", code)
	}
	ev := lastEvent(t, buf.String())
	if ev.Event != "cmd" || ev.Rearm == nil || !*ev.Rearm {
		t.Fatalf("event = %+v, want cmd rearm=true", ev)
	}
	if ev.From != "review" || ev.Target != "dev" || ev.Type != "challenge" || ev.Ref != "C1" || ev.Body != "justify X" {
		t.Errorf("payload = %+v, want review/dev/challenge/C1/justify X", ev)
	}
	if ev.ID == "" {
		t.Error("cmd event missing id (cursor)")
	}
}

func TestRunSubscribeFloorSkipsBacklog(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	stream := bus.StreamKey(b.Project(), "cmd")
	if err := r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatalf("XGroupCreate: %v", err)
	}
	oldID, err := b.Cmd(ctx, "helper", "dev", bus.CmdDirective, "", "OLD")
	if err != nil {
		t.Fatalf("Cmd OLD: %v", err)
	}
	if _, err := b.Cmd(ctx, "helper", "dev", bus.CmdDirective, "", "NEW"); err != nil {
		t.Fatalf("Cmd NEW: %v", err)
	}
	var buf bytes.Buffer
	code := runSubscribe(ctx, b, "dev", "host-1", 4*time.Second, oldID, false, false, &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	ev := lastEvent(t, buf.String())
	if ev.Body != "NEW" {
		t.Fatalf("delivered body=%q, want NEW (OLD skipped by floor)", ev.Body)
	}
}

func TestRunSubscribeHeartbeat(t *testing.T) {
	b, _ := dialMain(t)
	var buf bytes.Buffer
	// No cmd published: WatchCmd blocks, the 1s idle window elapses → heartbeat.
	code := runSubscribe(context.Background(), b, "dev", "host-1", 1*time.Second, "0", false, false, &buf)
	if code != 64 {
		t.Fatalf("exit code = %d, want 64 (heartbeat)", code)
	}
	ev := lastEvent(t, buf.String())
	if ev.Event != "heartbeat" || ev.Rearm == nil || !*ev.Rearm {
		t.Errorf("event = %+v, want heartbeat rearm=true", ev)
	}
}

func TestEmitStampsProtocolVersion(t *testing.T) {
	var buf bytes.Buffer
	emit(&buf, subEvent{Event: "cmd"}) // V intentionally left 0 — emit must stamp it
	var got subEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got); err != nil {
		t.Fatalf("emit output not JSON: %q (%v)", buf.String(), err)
	}
	if got.V != bus.ProtocolVersion {
		t.Fatalf("emit did not stamp v: got %d, want %d", got.V, bus.ProtocolVersion)
	}
}

func TestEmittedVariantsCarryVersion(t *testing.T) {
	// Every subEvent variant goes through emit, so each must carry "v":1.
	for _, ev := range []subEvent{
		{Event: "cmd"},
		{Event: "heartbeat", Rearm: boolPtr(true)},
		{Event: "error", Rearm: boolPtr(true), Msg: "boom"},
		{Event: "fatal", Rearm: boolPtr(false), Msg: "invalid agent"},
	} {
		var buf bytes.Buffer
		emit(&buf, ev)
		if !strings.Contains(buf.String(), `"v":1`) {
			t.Fatalf("%s event missing v:1: %q", ev.Event, buf.String())
		}
	}
}

func TestRunSubscribeLoopDeliversMany(t *testing.T) {
	b, r := dialMain(t)
	ctx, cancel := context.WithCancel(context.Background())
	stream := bus.StreamKey(b.Project(), "cmd")
	if err := r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatalf("XGroupCreate: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := b.Cmd(ctx, "helper", "dev", bus.CmdDirective, "", "do "+strconv.Itoa(i)); err != nil {
			t.Fatalf("Cmd: %v", err)
		}
	}

	buf := &syncBuf{}
	done := make(chan int, 1)
	go func() { done <- runSubscribe(ctx, b, "dev", "host-1", 2*time.Second, "0", false, true, buf) }()

	deadline := time.After(5 * time.Second)
	for !(strings.Contains(buf.String(), "do 0") && strings.Contains(buf.String(), "do 1")) {
		select {
		case <-deadline:
			cancel()
			t.Fatalf("loop did not deliver both cmds: %q", buf.String())
		case <-time.After(50 * time.Millisecond):
		}
	}
	cancel() // stop the loop
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("loop exit code = %d, want 0 on ctx cancel", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("loop did not return after ctx cancel")
	}
}

func TestSubscribeDrainJSONLAndExitCodes(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(context.Background(), bus.ReceiverKey(b.Project(), "dev")) })

	var empty bytes.Buffer
	if code := runDrain(ctx, b, "dev", "host-1", &empty); code != 64 {
		t.Fatalf("empty drain exit = %d, want 64", code)
	}
	if empty.Len() != 0 {
		t.Fatalf("empty drain wrote %q, want nothing", empty.String())
	}

	for _, msg := range []string{"one", "two"} {
		if _, err := b.Cmd(ctx, "helper", "dev", bus.CmdDirective, "", msg); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if code := runDrain(ctx, b, "dev", "host-1", &buf); code != 0 {
		t.Fatalf("drain exit = %d, want 0 (output %q)", code, buf.String())
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("drain wrote %d lines, want 2: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		if !strings.Contains(line, `"v":1`) {
			t.Fatalf("line %d missing v:1: %q", i, line)
		}
		if strings.Contains(line, `"rearm"`) {
			t.Fatalf("drain line %d carries rearm: %q", i, line)
		}
		var ev subEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line %d not JSON: %q", i, line)
		}
		if ev.Event != "cmd" || ev.Body != []string{"one", "two"}[i] || ev.ID == "" {
			t.Fatalf("line %d = %+v, want cmd %q", i, ev, []string{"one", "two"}[i])
		}
	}
	var again bytes.Buffer
	if code := runDrain(ctx, b, "dev", "host-1", &again); code != 64 || again.Len() != 0 {
		t.Fatalf("second drain exit=%d out=%q, want 64 and nothing (queue emptied)", code, again.String())
	}
}

func TestSubscribeDrainStaleFields(t *testing.T) {
	ev := cmdEvent(bus.Event{ID: "1-0", Stale: true, AgeS: 90000}, nil)
	var buf bytes.Buffer
	if err := emit(&buf, ev); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"stale":true`) || !strings.Contains(buf.String(), `"age_s":90000`) {
		t.Fatalf("stale fields not emitted: %q", buf.String())
	}
	buf.Reset()
	_ = emit(&buf, cmdEvent(bus.Event{ID: "1-0"}, boolPtr(true)))
	if strings.Contains(buf.String(), "stale") || strings.Contains(buf.String(), "age_s") {
		t.Fatalf("one-shot event grew drain fields: %q", buf.String())
	}
}

func TestSubscribeDrainRejectsIncompatibleFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--drain", "--since", "0", "dev"},
		{"--drain", "--loop", "dev"},
		{"--drain", "dev", "30"},
		{"--since", "123-0", "--drain", "dev"},
	} {
		if _, err := parseSubscribeArgs(args); !errors.Is(err, errDrainUsage) {
			t.Errorf("parseSubscribeArgs(%q) err = %v, want errDrainUsage", args, err)
		}
	}
	a, err := parseSubscribeArgs([]string{"--drain", "dev"})
	if err != nil || !a.drain || a.agent != "dev" {
		t.Fatalf("parseSubscribeArgs(--drain dev) = %+v, %v", a, err)
	}
	a, err = parseSubscribeArgs([]string{"--loop", "--since", "5", "dev", "30"})
	if err != nil || a.drain || !a.loop || a.since != "5" || a.agent != "dev" || a.idle != "30" {
		t.Fatalf("plain subscribe args = %+v, %v", a, err)
	}
	if _, err := parseSubscribeArgs(nil); err == nil || errors.Is(err, errDrainUsage) {
		t.Fatalf("missing agent err = %v, want a plain usage error", err)
	}
}

// leaseStealer writes successfully, then deletes the receiver lease so the
// following ACK is fenced off: output is complete, ACK fails.
type leaseStealer struct {
	r     *redis.Client
	key   string
	lines int
	buf   bytes.Buffer
}

func (w *leaseStealer) Write(p []byte) (int, error) {
	w.lines++
	n, err := w.buf.Write(p)
	w.r.Del(context.Background(), w.key)
	return n, err
}

func TestSubscribeDrainOutputThenAckFailureExit75(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	for _, msg := range []string{"one", "two"} {
		if _, err := b.Cmd(ctx, "helper", "dev", bus.CmdDirective, "", msg); err != nil {
			t.Fatal(err)
		}
	}
	w := &leaseStealer{r: r, key: bus.ReceiverKey(b.Project(), "dev")}
	if code := runDrain(ctx, b, "dev", "host-1", w); code != 75 {
		t.Fatalf("exit = %d, want 75 (ACK failed after output)", code)
	}
	if w.lines != 1 || strings.Count(w.buf.String(), "\n") != 1 || !strings.Contains(w.buf.String(), `"body":"one"`) {
		t.Fatalf("output = %q (%d writes), want exactly the first cmd line", w.buf.String(), w.lines)
	}
	p, err := r.XPending(ctx, bus.StreamKey(b.Project(), "cmd"), "dev").Result()
	if err != nil || p.Count != 1 {
		t.Fatalf("pending = %+v err=%v, want the un-ACKed entry left pending", p, err)
	}
}

func TestSubscribeDrainReceiverBusyExit75(t *testing.T) {
	// The bus test covers the bounded wait; here the CLI mapping:
	// ErrReceiverBusy → one error line without rearm, zero cmd lines, exit 75.
	var buf bytes.Buffer
	code := drainExit(&buf, 0, bus.ErrReceiverBusy)
	if code != 75 {
		t.Fatalf("exit = %d, want 75", code)
	}
	ev := lastEvent(t, buf.String())
	if ev.Event != "error" || ev.Rearm != nil || ev.Msg == "" || strings.Count(buf.String(), "\n") != 1 {
		t.Fatalf("busy output = %q, want one error event without rearm", buf.String())
	}
}

func TestCmdEventCarriesHuman(t *testing.T) {
	ev := cmdEvent(bus.Event{ID: "1-0", Type: bus.CmdDirective, From: "charlie", Target: "codex", Message: "x", Human: true}, nil)
	var buf bytes.Buffer
	if err := emit(&buf, ev); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"human":true`) {
		t.Fatalf("subscribe JSON lacks human: %s", buf.String())
	}
	buf.Reset()
	_ = emit(&buf, cmdEvent(bus.Event{ID: "1-0", Type: bus.CmdDirective, From: "codex", Target: "claude", Message: "x"}, nil))
	if strings.Contains(buf.String(), `"human"`) {
		t.Fatalf("an agent's cmd carries human: %s", buf.String())
	}
}

// A plain subscribe (no --since) skips the backlog but never a person's waking
// entry: an answer that arrived while the agent was busy is delivered.
func TestRunSubscribeFromNowKeepsAPersonsEntry(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	stream := bus.StreamKey(b.Project(), "cmd")
	if err := r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatalf("XGroupCreate: %v", err)
	}
	if _, err := b.Cmd(ctx, "helper", "dev", bus.CmdDirective, "", "OLD from an agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.HumanClose(ctx, "charlie", "dev", "1-0", "closed by a person"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.HumanCmd(ctx, "charlie", "dev", "ANSWER"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond) // the floor is "<ms>-0": entries of its own millisecond count as new
	floor, err := b.ServerFloor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if code := runSubscribe(ctx, b, "dev", "host-1", 2*time.Second, floor, true, false, &buf); code != 0 {
		t.Fatalf("exit code = %d, want 0: %s", code, buf.String())
	}
	if ev := lastEvent(t, buf.String()); ev.Body != "ANSWER" || !ev.Human {
		t.Fatalf("delivered %+v, want the person's ANSWER", ev)
	}
	buf.Reset()
	if code := runSubscribe(ctx, b, "dev", "host-1", time.Second, floor, true, false, &buf); code != 64 {
		t.Fatalf("second plain subscribe: exit %d, want 64 (agent backlog and the close skipped): %s", code, buf.String())
	}

	// No group yet (the agent never listened): a person's entry written just
	// before its first plain subscribe is delivered, an agent's is not.
	if _, err := b.Cmd(ctx, "helper", "fresh", bus.CmdDirective, "", "agent backlog"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.HumanCmd(ctx, "charlie", "fresh", "FIRST ANSWER"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	fresh, err := b.ServerFloor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if code := runSubscribe(ctx, b, "fresh", "host-1", 2*time.Second, fresh, true, false, &buf); code != 0 {
		t.Fatalf("first plain subscribe of a new agent: exit %d: %s", code, buf.String())
	}
	if ev := lastEvent(t, buf.String()); ev.Body != "FIRST ANSWER" {
		t.Fatalf("new agent got %+v, want FIRST ANSWER", ev)
	}
	buf.Reset()
	if code := runSubscribe(ctx, b, "fresh", "host-1", time.Second, fresh, true, false, &buf); code != 64 {
		t.Fatalf("new agent, second plain subscribe: exit %d, want 64: %s", code, buf.String())
	}

	// An explicit cursor is the agent's own record of what it handled: a
	// person's entry at or below it is not delivered again.
	id, err := b.HumanCmd(ctx, "charlie", "dev", "HANDLED")
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if code := runSubscribe(ctx, b, "dev", "host-1", time.Second, id, false, false, &buf); code != 64 {
		t.Fatalf("--since past a person's entry: exit %d, want 64: %s", code, buf.String())
	}
}
