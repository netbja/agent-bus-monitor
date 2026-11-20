package cmdtest

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// StallWorld: a project with master and coder idle, their panes seen idle
// at Later (20 minutes ahead), and a plan whose task t1 is ready for coder;
// the dispatcher holds the lease as "stall-holder". The stall reader, which
// is the dispatcher's, is added by the tests that read it.
type StallWorld struct {
	T     *testing.T
	B     *bus.Bus
	Later time.Time
}

func NewStallWorld(t *testing.T) *StallWorld {
	t.Helper()
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r, "stall"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()); _ = r.Close() })
	ctx := context.Background()
	who := bus.AgentIdent{PID: os.Getpid(), Provider: "anthropic"}
	for _, a := range []string{"master", "coder"} {
		if _, err := b.Status(ctx, a, "idle", "", who); err != nil {
			t.Fatal(err)
		}
	}
	later := time.Now().Add(20 * time.Minute)
	if ok, err := b.AcquireDispatch(ctx, "stall-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	seen := bus.Observation{State: "idle", At: later.UnixMilli()}
	if err := b.FencedPutObserved(ctx, "stall-holder", map[string]bus.Observation{"master": seen, "coder": seen}); err != nil {
		t.Fatal(err)
	}
	file := []byte(`{"plan":"t","goal":"g","tasks":[{"id":"t1","title":"Next task","owner":"coder"}]}`)
	if err := b.LoadPlan(ctx, "docs/plans/t.json", "0123456789abcdef0123456789abcdef01234567", file, []string{"t1"}); err != nil {
		t.Fatal(err)
	}
	return &StallWorld{T: t, B: b, Later: later}
}

// PutStall stores a team-stall fail, as the health loop does.
func (w *StallWorld) PutStall() health.Record {
	w.T.Helper()
	ctx := context.Background()
	if err := w.B.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}, Manager: "test"}); err != nil {
		w.T.Fatal(err)
	}
	rec := health.Record{ID: "team-stall", Status: health.Fail, Detail: "team idle 18m, nothing open; next ready: t1 (coder)", Fingerprint: "f"}
	if err := w.B.FencedHealthPut(ctx, "stall-holder", "master", rec); err != nil {
		w.T.Fatal(err)
	}
	recs, err := w.B.Health(ctx)
	if err != nil {
		w.T.Fatal(err)
	}
	return recs["team-stall"]
}

func (w *StallWorld) PersonNotices() []string {
	w.T.Helper()
	evs, _, err := w.B.Recent(context.Background(), []string{"notify"}, 100)
	if err != nil {
		w.T.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.For == bus.ForPerson {
			out = append(out, e.Message)
		}
	}
	return out
}

// DispatchDirectives is every waking directive from the dispatcher to target.
func (w *StallWorld) DispatchDirectives(target string) []string {
	w.T.Helper()
	evs, _, err := w.B.Recent(context.Background(), []string{"cmd"}, 200)
	if err != nil {
		w.T.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.From == "dispatch" && e.Target == target && e.Type == bus.CmdDirective && !e.NoWake {
			out = append(out, e.Message)
		}
	}
	return out
}
