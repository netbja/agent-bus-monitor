package dispatchsvc

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// background-work-visible step 1: the dispatcher reads an agent's hook
// record as its hooks' facts, for the agent's own process only.
func TestBgHooksFromTheRecord(t *testing.T) {
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r, "bg"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()); _ = r.Close() })
	ctx := context.Background()
	hooks := bgHooks(b)
	if _, ok := hooks("coder", 42); ok {
		t.Fatal("no record read as a reading")
	}
	tasks := []bus.BgTask{{IDHash: bus.BgIDHash("a"), Type: "shell", Status: "running"}, {IDHash: bus.BgIDHash("b"), Type: "shell", Status: "running"},
		{IDHash: bus.BgIDHash("s"), Type: "subagent", Status: "running"}}
	if err := b.WriteBgTasks(ctx, "coder", "Stop", 42, tasks, []bus.BgCron{{IDHash: bus.BgIDHash("c"), Schedule: "*/30 * * * *"}}); err != nil {
		t.Fatal(err)
	}
	h, ok := hooks("coder", 42)
	if !ok || h.Subagents != 1 || h.Tasks != 2 { // a cron is no running work
		t.Fatalf("facts %+v %v", h, ok)
	}
	if _, ok := hooks("coder", 43); ok {
		t.Fatal("another process's record read")
	}
	b.WriteBgTasks(ctx, "coder", "Stop", 42, nil, nil)
	if h, ok := hooks("coder", 42); !ok || h.Subagents != 0 || h.Tasks != 0 {
		t.Fatalf("an empty list is a reading of none: %+v %v", h, ok)
	}
}

// Config's runtime reads the hook records: wireRuntime sets its Hooks (foureyes G13).
func TestWireRuntimeSetsTheHooks(t *testing.T) {
	var h dispatch.HostRuntime
	wireRuntime(&h, nil)
	if h.Hooks == nil {
		t.Fatal("the dispatcher's runtime reads no hook record")
	}
}
