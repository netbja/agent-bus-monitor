package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestRearmSentinel(t *testing.T) {
	cases := []struct {
		name           string
		outcome        watchOutcome
		ref, from, msg string
		wantLine       string
		wantCode       int
	}{
		{"cmd", outcomeCmd, "C1", "hermes", "", "__AGENTBUS__ event=cmd rearm=1 ref=C1 from=hermes", 0},
		{"heartbeat", outcomeHeartbeat, "", "", "", "__AGENTBUS__ event=heartbeat rearm=1", 64},
		{"transient", outcomeTransient, "", "", "broker down", "__AGENTBUS__ event=error rearm=1 msg=broker down", 75},
		{"fatal", outcomeFatal, "", "", "invalid agent", "__AGENTBUS__ event=fatal rearm=0 msg=invalid agent", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line, code := rearmSentinel(c.outcome, c.ref, c.from, c.msg)
			if line != c.wantLine {
				t.Errorf("line = %q, want %q", line, c.wantLine)
			}
			if code != c.wantCode {
				t.Errorf("code = %d, want %d", code, c.wantCode)
			}
		})
	}
}

func TestPrintCmd(t *testing.T) {
	var buf bytes.Buffer
	printCmd(&buf, bus.Event{Type: "directive", From: "hermes", Target: "dev", Message: "run it"})
	if got := buf.String(); got != "[directive hermes->dev] run it\n" {
		t.Fatalf("printCmd = %q", got)
	}
	buf.Reset()
	printCmd(&buf, bus.Event{Type: "challenge", From: "review", Target: "dev", Ref: "C1", Message: "justify"})
	if got := buf.String(); got != "[challenge review->dev ref=C1] justify\n" {
		t.Fatalf("printCmd with ref = %q", got)
	}
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
	code := runSubscribe(context.Background(), nil, "Bad Agent", "host-1", time.Second, false, &buf)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (fatal)", code)
	}
	if !strings.Contains(buf.String(), "__AGENTBUS__ event=fatal rearm=0") {
		t.Errorf("missing fatal sentinel: %q", buf.String())
	}
}

func TestRunSubscribeDelivers(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	stream := bus.StreamKey(b.Project(), "cmd")
	// Pre-create dev's group at "0" so the already-published entry is delivered
	// deterministically (no race with WatchCmd's own MKSTREAM at "$").
	if err := r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatalf("XGroupCreate: %v", err)
	}
	if _, err := b.Cmd(ctx, "review", "dev", bus.CmdChallenge, "C1", "justify X"); err != nil {
		t.Fatalf("Cmd: %v", err)
	}

	var buf bytes.Buffer
	code := runSubscribe(ctx, b, "dev", "host-1", 4*time.Second, false, &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (delivered)", code)
	}
	out := buf.String()
	if !strings.Contains(out, "[challenge review->dev ref=C1] justify X") {
		t.Errorf("output missing cmd line: %q", out)
	}
	if !strings.Contains(out, "__AGENTBUS__ event=cmd rearm=1 ref=C1 from=review") {
		t.Errorf("output missing cmd sentinel: %q", out)
	}
}

func TestRunSubscribeHeartbeat(t *testing.T) {
	b, _ := dialMain(t)
	var buf bytes.Buffer
	// No cmd published: WatchCmd creates the group at "$", blocks, the 1s idle
	// window elapses → heartbeat.
	code := runSubscribe(context.Background(), b, "dev", "host-1", 1*time.Second, false, &buf)
	if code != 64 {
		t.Fatalf("exit code = %d, want 64 (heartbeat)", code)
	}
	out := buf.String()
	if !strings.Contains(out, "__HEARTBEAT__") {
		t.Errorf("missing deprecated __HEARTBEAT__: %q", out)
	}
	if !strings.Contains(out, "__AGENTBUS__ event=heartbeat rearm=1") {
		t.Errorf("missing heartbeat sentinel: %q", out)
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
	go func() { done <- runSubscribe(ctx, b, "dev", "host-1", 2*time.Second, true, buf) }()

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
