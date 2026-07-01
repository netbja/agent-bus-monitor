package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
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
// (tests need the client to pre-create consumer groups). Skips if Redis is down.
func dialMain(t *testing.T) (*bus.Bus, *redis.Client) {
	t.Helper()
	r, err := bus.Connect("")
	if err != nil {
		t.Skipf("Redis unavailable (run docker compose up -d): %v", err)
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
	code := runSubscribe(context.Background(), nil, "Bad Agent", "host-1", time.Second, "0", false, &buf)
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
	code := runSubscribe(ctx, b, "dev", "host-1", 4*time.Second, "0", false, &buf)
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
	oldID, err := b.Cmd(ctx, "hermes", "dev", bus.CmdDirective, "", "OLD")
	if err != nil {
		t.Fatalf("Cmd OLD: %v", err)
	}
	if _, err := b.Cmd(ctx, "hermes", "dev", bus.CmdDirective, "", "NEW"); err != nil {
		t.Fatalf("Cmd NEW: %v", err)
	}
	var buf bytes.Buffer
	code := runSubscribe(ctx, b, "dev", "host-1", 4*time.Second, oldID, false, &buf)
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
	code := runSubscribe(context.Background(), b, "dev", "host-1", 1*time.Second, "0", false, &buf)
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
		if _, err := b.Cmd(ctx, "hermes", "dev", bus.CmdDirective, "", "do "+strconv.Itoa(i)); err != nil {
			t.Fatalf("Cmd: %v", err)
		}
	}

	buf := &syncBuf{}
	done := make(chan int, 1)
	go func() { done <- runSubscribe(ctx, b, "dev", "host-1", 2*time.Second, "0", true, buf) }()

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
