package peer

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

func runnerBus(t *testing.T) *bus.Bus {
	t.Helper()
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r, "peer"+strconv.FormatInt(time.Now().UnixNano(), 36))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()); _ = r.Close() })
	return b
}

// syncBuf is the pane's transcript, written by the runner's goroutine.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func startRunner(t *testing.T, b *bus.Bus, tr Turn) (*syncBuf, func()) {
	t.Helper()
	out := &syncBuf{}
	r := &Runner{Bus: b, Agent: "local", Consumer: "test", Turn: tr, RetryEvery: 20 * time.Millisecond, LeaseWait: time.Second, Out: out}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	return out, func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("the runner did not stop")
		}
	}
}

// waitThread waits until thread id has n entries.
func waitThread(t *testing.T, b *bus.Bus, id string, n int) []bus.Event {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		evs, err := b.Thread(context.Background(), id)
		if err == nil && len(evs) >= n {
			return evs
		}
	}
	t.Fatalf("thread %s never reached %d entries", id, n)
	return nil
}

func states(t *testing.T, b *bus.Bus) []string {
	t.Helper()
	evs, _, err := b.Recent(context.Background(), []string{"status"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.Agent == "local" {
			out = append(out, e.State)
		}
	}
	return out
}

// An entry sent while the peer was down is drained; one sent later is
// received; each gets a reply on its thread, a report, working then idle.
func TestRunnerAnswersOnTheThread(t *testing.T) {
	b := runnerBus(t)
	ctx := context.Background()
	first, err := b.Cmd(ctx, "master", "local", bus.CmdDirective, "", "What does main.go do?")
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := fakeEndpoint(t, nil, func(chatRequest) (int, string) { return 200, answerJSON })
	out, stop := startRunner(t, b, newTurn(t, srv, time.Minute))
	evs := waitThread(t, b, first, 2)
	if evs[1].Type != bus.CmdReply || evs[1].From != "local" || evs[1].Target != "master" || evs[1].Message != "board.go holds the board." {
		t.Fatalf("reply = %+v", evs[1])
	}
	second, err := b.Cmd(ctx, "master", "local", bus.CmdDirective, "", "And sub/b.txt?")
	if err != nil {
		t.Fatal(err)
	}
	waitThread(t, b, second, 2)
	// A reply on an existing thread is answered on that same thread.
	if _, err := b.Cmd(ctx, "master", "local", bus.CmdReply, first, "Thanks, and the board?"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, b, first, 4)
	stop()
	if got := strings.Join(states(t, b), " "); !strings.Contains(got, "working idle working idle working idle") {
		t.Fatalf("states = %s", got)
	}
	reports, _, err := b.Recent(ctx, []string{"report"}, 10)
	if err != nil || len(reports) != 3 || !strings.Contains(reports[0].Message, "master") {
		t.Fatalf("reports = %+v %v", reports, err)
	}
	if !strings.Contains(out.String(), "What does main.go do?") || !strings.Contains(out.String(), "board.go holds the board.") {
		t.Fatalf("transcript:\n%s", out)
	}
	if armed, _ := b.Armed(ctx, "local"); armed {
		t.Fatal("the lease outlived the runner")
	}
}

// An endpoint fault keeps the entry: blocked with the reason, the entry
// still pending, retried until the endpoint answers; then idle.
func TestRunnerBlocksWhileTheEndpointIsDown(t *testing.T) {
	b := runnerBus(t)
	ctx := context.Background()
	var calls atomic.Int32
	srv, _ := fakeEndpoint(t, nil, func(chatRequest) (int, string) {
		if calls.Add(1) <= 3 {
			return 503, "loading model"
		}
		return 200, answerJSON
	})
	_, stop := startRunner(t, b, newTurn(t, srv, time.Minute))
	id, err := b.Cmd(ctx, "master", "local", bus.CmdDirective, "", "q")
	if err != nil {
		t.Fatal(err)
	}
	waitThread(t, b, id, 2)
	stop()
	got := states(t, b)
	if !strings.Contains(strings.Join(got, " "), "working blocked") || got[len(got)-1] != "idle" {
		t.Fatalf("states = %v, want blocked during the fault, idle after", got)
	}
	evs, _ := b.Thread(ctx, id)
	if len(evs) != 2 {
		t.Fatalf("thread = %d entries, want one reply", len(evs))
	}
}

// A second peer under one name cannot take the lease, says so, and exits.
func TestRunnerRefusesASecondPeer(t *testing.T) {
	b := runnerBus(t)
	held, err := b.TakeReceiver(context.Background(), "local", "first", bus.DefaultHorizon, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	r := &Runner{Bus: b, Agent: "local", Consumer: "second", LeaseWait: 100 * time.Millisecond, Out: &syncBuf{}}
	if err := r.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "another") {
		t.Fatalf("Run = %v, want refused: another peer holds the lease", err)
	}
}
