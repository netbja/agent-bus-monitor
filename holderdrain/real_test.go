package holderdrain

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/netbja/agent-bus-monitor/termhost"
	"github.com/netbja/agent-bus-monitor/termhost/fake"
)

// background-work-visible §4b: holder drain's own runtime reads the agent's
// declared text as a source, as the dispatcher's does (--ignore-background
// still overrides it: TestHolderDrainIgnoreBackground).
func TestDrainInspectReadsTheDeclaration(t *testing.T) {
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r, "hd"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()); _ = r.Close() })
	ctx := context.Background()
	h := fake.New()
	term, err := h.Start(ctx, termhost.Spec{Project: b.Project(), Name: "coder", Profile: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SetBackground(ctx, "coder", "nightly build", "coder", time.Minute); err != nil {
		t.Fatal(err)
	}
	d := NewDeps(b.Project(), h, b, "cur", func() ([]string, error) { return nil, nil }, func() error { return nil })
	in := d.Inspect(ctx, term)
	if in.Background == nil || in.Background.Source() != agentstate.SourceDeclared {
		t.Fatalf("the drain did not read the declaration: %+v", in.Background)
	}
}
