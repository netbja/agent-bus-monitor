package holder

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/holderdrain"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// serveBuild serves project's holder of build in the run directory getenv names.
func serveBuild(t *testing.T, project, build string, getenv func(string) string) *Host {
	t.Helper()
	sock, err := SocketPath(getenv, project, service.BuildKey(build))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Config{Project: project, Build: build, Socket: sock, Launch: testRoles(t), Grace: time.Second,
		SubmitWait: time.Second, PersonQuiet: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.Serve(ctx)
	t.Cleanup(func() { cancel(); s.Shutdown() })
	h, err := NewHost(project, build, getenv)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// The drain against real holders (herdr exit 6d2): agent fake runs on the old
// build's holder; the current build's holder is up beside it. holder drain
// moves fake by the reload protocol: its reload intent, its old terminal
// closed, its new one started on the current build, its proof (the agent's
// verified status from the new terminal, as agentbus status writes it), the
// reloaded entry; then the gc and the events. Redis is the
// disposable test broker.
func TestHolderDrainAgainstRealHolders(t *testing.T) {
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatalf("explicit test Redis unavailable: %v", err)
	}
	project := "d" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, err := bus.Open(r, project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = bus.DeleteProject(context.Background(), r, project)
		r.Close()
	})
	_, getenv := shortRundir(t)
	ctx := context.Background()
	oldHost := serveBuild(t, project, "old", getenv)
	old, err := oldHost.Start(ctx, termhost.Spec{Project: project, Name: "fake", Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := oldHost.WaitScreen(ctx, old.ID, "─", 5*time.Second); !ok || err != nil {
		t.Fatalf("the fake client never drew its box: %v %v", ok, err)
	}
	if _, err := b.Status(ctx, "fake", "idle", "", bus.AgentIdent{Pane: string(old.ID), PID: old.PID, PIDStart: old.PIDStart}); err != nil {
		t.Fatal(err)
	}
	cur := serveBuild(t, project, "cur", getenv)

	// The agent proves itself from its new terminal once it is there.
	proved := make(chan termhost.Term, 1)
	pctx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		for pctx.Err() == nil {
			terms, _ := cur.List(pctx, project)
			for _, tm := range terms {
				if tm.Name == "fake" && tm.Alive && tm.Build == "cur" {
					if ok, _ := cur.WaitScreen(pctx, tm.ID, "─", 5*time.Second); ok {
						_, _ = b.Status(pctx, "fake", "idle", "", bus.AgentIdent{Pane: string(tm.ID), PID: tm.PID, PIDStart: tm.PIDStart})
						proved <- tm
						return
					}
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	gcs := 0
	d := holderdrain.NewDeps(project, cur, b, "cur", cur.SocketFiles, func() error { gcs++; return nil })
	d.ProofWait = 20 * time.Second
	var out bytes.Buffer
	// agy has no authoritative background source: unknown, so skipped,
	// unless the person says --ignore-background.
	if code := holderdrain.Run(ctx, d, &out); code != holderdrain.Skipped || !strings.Contains(out.String(), "fake skipped: background work") {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	if terms, _ := cur.List(ctx, project); len(terms) != 1 || terms[0].ID != old.ID || !terms[0].Alive {
		t.Fatalf("a skipped agent was touched: %+v", terms)
	}
	d.IgnoreBackground = true
	out.Reset()
	if code := holderdrain.Run(ctx, d, &out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	var nt termhost.Term
	select {
	case nt = <-proved:
	default:
		t.Fatal("the drain ended before the agent proved itself")
	}
	terms, err := cur.List(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	var live []string
	for _, tm := range terms {
		if tm.Alive {
			live = append(live, string(tm.ID)+"@"+tm.Build)
		}
		if tm.ID == old.ID && tm.Alive {
			t.Fatalf("the old terminal still runs: %+v", tm)
		}
	}
	if len(live) != 1 || live[0] != string(nt.ID)+"@cur" {
		t.Fatalf("live terminals %v, want only %s on cur", live, nt.ID)
	}
	if nt.Profile != "agy" {
		t.Fatalf("the new terminal's client profile %q, want agy", nt.Profile)
	}
	if gcs != 1 {
		t.Fatalf("gc ran %d times", gcs)
	}
	evs, err := b.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind+":"+e.Agent)
	}
	if got := strings.Join(kinds, ","); got != "holder-drained:,relaunch:fake" {
		t.Fatalf("events %s\n%s", got, out.String())
	}
	log, err := b.SwapLog(ctx, "fake", 10)
	if err != nil {
		t.Fatal(err)
	}
	var swaps []string
	for _, e := range log {
		swaps = append(swaps, e.Event+"/"+e.Kind)
	}
	if got := strings.Join(swaps, ","); got != "reloaded/reload,begin/reload" {
		t.Fatalf("swap ledger %s", got)
	}
	if on, err := b.Swapping(ctx, "fake"); err != nil || on {
		t.Fatalf("the reload intent was left: %v %v", on, err)
	}
}
