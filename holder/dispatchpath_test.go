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

// herdr exit step 9: the dispatcher's host path against a test holder. Agent
// fake runs the fake client (agy's input box) in a terminal of a real
// holder; a message waits for it; Run on HostRuntime wakes it: the holder
// types the wake line (bracketed paste, seen in the input, then CR) and the
// program receives it as submitted. Redis is the disposable test broker.
func TestDispatcherWakesThroughATestHolder(t *testing.T) {
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

	cfg := dispatch.DefaultConfig()
	cfg.Retry, cfg.EscalateAfter = time.Hour, time.Hour
	cfg.Tick, cfg.Lease, cfg.Renew, cfg.MinPTTL = 50*time.Millisecond, 2*time.Second, 100*time.Millisecond, 300*time.Millisecond
	cfg.Log = io.Discard
	rt := &dispatch.HostRuntime{Host: h, Project: project, Client: func(string) string { return "agy" }}
	rctx, rcancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- dispatch.Run(rctx, b, rt, cfg) }()
	t.Cleanup(func() { rcancel(); <-done })

	want := "subscribe --drain fake"
	var text string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		sc, err := h.Screen(ctx, term.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		text = strings.Join(sc.Lines, "\n")
		if strings.Contains(text, "SUBMITTED: [agentbus#") && strings.Contains(strings.ReplaceAll(text, "\n", ""), want) {
			break
		}
	}
	if !strings.Contains(text, "SUBMITTED: [agentbus#") {
		t.Fatalf("no wake line submitted:\n%s", text)
	}
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		evs, _ := b.RecentEvents(ctx, 10)
		if len(evs) > 0 && evs[0].Kind == bus.EventWakeTyped && evs[0].Agent == "fake" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no wake event: %+v", evs)
		}
	}
}
