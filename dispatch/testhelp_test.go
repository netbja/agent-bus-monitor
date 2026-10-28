package dispatch

import (
	"bytes"
	"context"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// dialRun opens a Bus on a unique throwaway project of the isolated test broker
// and deletes every {p}:* key on cleanup. It never touches the production
// broker: testredis.Configure skips without AGENTBUS_TEST_REDIS_URL.
func dialRun(t *testing.T) (*bus.Bus, *redis.Client) {
	t.Helper()
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatalf("explicit test Redis unavailable: %v", err)
	}
	project := "d" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, err := bus.Open(r, project)
	if err != nil {
		t.Fatalf("Open(%q): %v", project, err)
	}
	t.Cleanup(func() {
		_, _ = bus.DeleteProject(context.Background(), r, project)
		r.Close()
	})
	return b, r
}

// syncBuf is a goroutine-safe log sink.
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

// fastConfig is DefaultConfig at millisecond scale. Retry and EscalateAfter are
// long unless a test shortens them, so a single prompt or escalation is stable.
func fastConfig() Config {
	c := DefaultConfig()
	c.Retry = time.Hour
	c.EscalateAfter = time.Hour
	c.Tick = 20 * time.Millisecond
	c.Lease = 2 * time.Second
	c.Renew = 100 * time.Millisecond
	c.MinPTTL = 300 * time.Millisecond
	c.Log = &syncBuf{}
	return c
}

type promptRec struct {
	Pane, Text, Nonce string
	At                time.Time
}

// fakeRT is a scripted Runtime: script(pane, n) answers the n-th Inspect of
// that pane (1-based); prompts are recorded.
type fakeRT struct {
	mu       sync.Mutex
	script   func(pane string, n int) Inspection
	calls    map[string]int
	prompts  []promptRec
	observed bool
	// reserved answers whether a clear of pane is reserved on the bus; a
	// Clear without it, or unreserved, breaks the dispatchTypes invariant.
	reserved func(pane string) bool
	clearErr error
}

// wakeLinePattern is WakeText's format: the dispatchTypes invariant (lifecycle
// part B) is that every Runtime write in every test of this package is a wake
// line or exactly ClearText, and a clear only follows its reservation.
var wakeLinePattern = regexp.MustCompile(`^\[agentbus#\S+\] \S+: \d+ message\(s\) waiting for \S+\. Run: agentbus --project \S+ subscribe --drain \S+$`)

func newFakeRT(script func(pane string, n int) Inspection) *fakeRT {
	return &fakeRT{script: script, calls: map[string]int{}, observed: true}
}

func (f *fakeRT) Inspect(_ context.Context, pane string) Inspection {
	f.mu.Lock()
	f.calls[pane]++
	n := f.calls[pane]
	f.mu.Unlock()
	return f.script(pane, n)
}

func (f *fakeRT) Prompt(_ context.Context, pane, text, nonce string) (bool, error) {
	if !wakeLinePattern.MatchString(text) {
		panic("dispatchTypes: the dispatcher typed something other than a wake line: " + text)
	}
	f.mu.Lock()
	f.prompts = append(f.prompts, promptRec{Pane: pane, Text: text, Nonce: nonce, At: time.Now()})
	f.mu.Unlock()
	return f.observed, nil
}

func (f *fakeRT) Clear(_ context.Context, pane string) error {
	if f.reserved == nil || !f.reserved(pane) {
		panic("dispatchTypes: /clear typed into " + pane + " without its reservation")
	}
	f.mu.Lock()
	f.prompts = append(f.prompts, promptRec{Pane: pane, Text: ClearText, At: time.Now()})
	f.mu.Unlock()
	return f.clearErr
}

func (f *fakeRT) Prompts() []promptRec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]promptRec(nil), f.prompts...)
}

func (f *fakeRT) Inspections(pane string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[pane]
}

const testPID, testStart = 4242, 777

// readyIn is a settled, idle, unfocused claude pane whose foreground pid
// matches register's snapshot.
func readyIn() Inspection {
	return Inspection{State: "idle", Client: "claude", PIDs: []int{testPID},
		Starts: map[int]uint64{testPID: testStart}, Ready: true}
}

func alwaysReady(string, int) Inspection { return readyIn() }

// register publishes an idle status with a verified identity in pane.
func register(t *testing.T, b *bus.Bus, agent, pane string) {
	t.Helper()
	if _, err := b.Status(context.Background(), agent, "idle", "", bus.AgentIdent{Pane: pane, PID: testPID, PIDStart: testStart}); err != nil {
		t.Fatalf("Status(%s): %v", agent, err)
	}
}

func send(t *testing.T, b *bus.Bus, target, body string) string {
	t.Helper()
	id, err := b.Cmd(context.Background(), "peer", target, bus.CmdDirective, "", body)
	if err != nil {
		t.Fatalf("Cmd: %v", err)
	}
	return id
}

// runner is one Run in the background.
type runner struct {
	cancel context.CancelFunc
	done   chan error
}

func start(t *testing.T, b *bus.Bus, rt Runtime, cfg Config) *runner {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	return startWith(t, ctx, cancel, b, rt, cfg)
}

// startWith runs under a context the caller already holds, so a hook can
// cancel it without racing on the runner variable.
func startWith(t *testing.T, ctx context.Context, cancel context.CancelFunc, b *bus.Bus, rt Runtime, cfg Config) *runner {
	t.Helper()
	r := &runner{cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- Run(ctx, b, rt, cfg) }()
	t.Cleanup(func() { r.stop(t) })
	return r
}

// wait returns Run's result, failing the test after d.
func (r *runner) wait(t *testing.T, d time.Duration) error {
	t.Helper()
	select {
	case err := <-r.done:
		r.done <- err // keep it for stop/cleanup
		return err
	case <-time.After(d):
		t.Fatalf("Run did not return within %v", d)
		return nil
	}
}

func (r *runner) stop(t *testing.T) error {
	t.Helper()
	r.cancel()
	return r.wait(t, 5*time.Second)
}

// withHooks installs crash-injection hooks for the Runs started while set.
func withHooks(t *testing.T, h hooks) {
	t.Helper()
	testHooks = h
	t.Cleanup(func() { testHooks = hooks{} })
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %s", d, what)
}

// entries returns the retained entries of {p}:kind.
func entries(t *testing.T, r *redis.Client, b *bus.Bus, kind string) []redis.XMessage {
	t.Helper()
	msgs, err := r.XRange(context.Background(), bus.StreamKey(b.Project(), kind), "-", "+").Result()
	if err != nil {
		t.Fatalf("XRANGE %s: %v", kind, err)
	}
	return msgs
}

// escalations counts escalation cmds about agent (body prefix "<agent>:").
func escalations(t *testing.T, r *redis.Client, b *bus.Bus, agent string) []redis.XMessage {
	t.Helper()
	var out []redis.XMessage
	for _, m := range entries(t, r, b, "cmd") {
		c, _ := m.Values["command"].(string)
		if m.Values["type"] == bus.CmdEscalation && strings.HasPrefix(c, agent+":") {
			out = append(out, m)
		}
	}
	return out
}

// notifies counts dispatcher notifies about agent.
func notifies(t *testing.T, r *redis.Client, b *bus.Bus, agent string) []redis.XMessage {
	t.Helper()
	var out []redis.XMessage
	for _, m := range entries(t, r, b, "notify") {
		msg, _ := m.Values["message"].(string)
		if m.Values["from"] == "dispatch" && strings.Contains(msg, agent+":") {
			out = append(out, m)
		}
	}
	return out
}

func wakeOf(t *testing.T, b *bus.Bus, agent string) (bus.WakeRecord, bool) {
	t.Helper()
	w, ok, err := b.Wake(context.Background(), agent)
	if err != nil {
		t.Fatalf("Wake(%s): %v", agent, err)
	}
	return w, ok
}
