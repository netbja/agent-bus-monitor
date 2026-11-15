package holder

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// dispatchWorld is the dispatcher's host path against a test holder (herdr
// exit step 9): agent fake runs the fake client (agy's input box) in a
// terminal of a real holder of a fresh project, its status verified against
// the holder's child, a message waiting for it. Redis is the disposable test
// broker.
type dispatchWorld struct {
	b    *bus.Bus
	h    *Host
	sock string
	term termhost.Term
}

func newDispatchWorld(t *testing.T) *dispatchWorld {
	t.Helper()
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatalf("explicit test Redis unavailable: %v", err)
	}
	project := "h" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, err := bus.Open(r, project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = bus.DeleteProject(context.Background(), r, project)
		r.Close()
	})
	_, getenv := shortRundir(t)
	sock, err := SocketPath(getenv, project, service.BuildKey("test"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Config{Project: project, Build: "test", Socket: sock, Launch: testRoles(t), Grace: time.Second,
		SubmitWait: time.Second, PersonQuiet: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	sctx, scancel := context.WithCancel(context.Background())
	go s.Serve(sctx)
	t.Cleanup(func() { scancel(); s.Shutdown() })
	h, err := NewHost(project, "test", getenv)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	term, err := h.Start(ctx, termhost.Spec{Project: project, Name: "fake", Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := h.WaitScreen(ctx, term.ID, "─", 5*time.Second); !ok || err != nil {
		t.Fatalf("the fake client never drew its box: %v %v", ok, err)
	}
	if _, err := b.Status(ctx, "fake", "idle", "", bus.AgentIdent{Pane: string(term.ID), PID: term.PID, PIDStart: term.PIDStart}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Cmd(ctx, "peer", "fake", bus.CmdDirective, "", "build it"); err != nil {
		t.Fatal(err)
	}
	return &dispatchWorld{b: b, h: h, sock: sock, term: term}
}

// run starts the dispatcher on HostRuntime until the test ends.
func (w *dispatchWorld) run(t *testing.T) {
	cfg := dispatch.DefaultConfig()
	cfg.Retry, cfg.EscalateAfter = time.Hour, time.Hour
	cfg.Tick, cfg.Lease, cfg.Renew, cfg.MinPTTL = 50*time.Millisecond, 2*time.Second, 100*time.Millisecond, 300*time.Millisecond
	cfg.Log = io.Discard
	rt := &dispatch.HostRuntime{Host: w.h, Project: w.b.Project(), Client: func(string) string { return "agy" }}
	rctx, rcancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- dispatch.Run(rctx, w.b, rt, cfg) }()
	t.Cleanup(func() { rcancel(); <-done })
}

// submitted: the program shows a submitted wake line for fake within d.
func (w *dispatchWorld) submitted(t *testing.T, d time.Duration) (bool, string) {
	var text string
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		sc, err := w.h.Screen(context.Background(), w.term.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		text = strings.Join(sc.Lines, "\n")
		if strings.Contains(text, "SUBMITTED: [agentbus#") && strings.Contains(strings.ReplaceAll(text, "\n", ""), "subscribe --drain fake") {
			return true, text
		}
	}
	return false, text
}

// Run on HostRuntime wakes the agent: the holder types the wake line
// (bracketed paste, seen in the input, then CR), the program receives it as
// submitted, and {p}:events records it.
func TestDispatcherWakesThroughATestHolder(t *testing.T) {
	w := newDispatchWorld(t)
	w.run(t)
	if ok, text := w.submitted(t, 10*time.Second); !ok {
		t.Fatalf("no wake line submitted:\n%s", text)
	}
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		evs, _ := w.b.RecentEvents(context.Background(), 10)
		if len(evs) > 0 && evs[0].Kind == bus.EventWakeTyped && evs[0].Agent == "fake" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no wake event: %+v", evs)
		}
	}
}

// A person attached (a viewer not proven unfocused, spec 4.5;
// reviewer-codex on herdr-9): the wake waits, deferred as "pane focused",
// never attempted; once the viewer detaches it goes through.
func TestDispatcherWaitsForAnAttachedPerson(t *testing.T) {
	w := newDispatchWorld(t)
	_, local, _ := strings.Cut(string(w.term.ID), "/")
	v := attached(t, w.sock, Attach{Term: local, Cols: 100, Rows: 30})
	w.run(t)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		r, ok, err := w.b.Wake(context.Background(), "fake")
		if err == nil && ok && r.State == "deferred" && r.Reason == "pane focused" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not deferred for the person: %+v %v %v", r, ok, err)
		}
	}
	if ok, text := w.submitted(t, 500*time.Millisecond); ok {
		t.Fatalf("typed with a person attached:\n%s", text)
	}
	if r, _, _ := w.b.Wake(context.Background(), "fake"); r.Tries != 0 {
		t.Fatalf("attempted with a person attached: %+v", r)
	}
	control(t, v, Detach{})
	v.Close()
	if ok, text := w.submitted(t, 10*time.Second); !ok {
		t.Fatalf("no wake once the person left:\n%s", text)
	}
}
