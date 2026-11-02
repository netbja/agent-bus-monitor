package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// A pop registered, idle and seen ready reads done through ReadPopFacts; each
// open item a later reader would see is read, not assumed (pop-auto-retire §2).
func TestReadPopFacts(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	if _, err := b.Status(ctx, "security", "idle", "review sent", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(ctx, "security", bus.AgentIdent{Pane: "w1:p3", PID: 42, PIDStart: 4200, Tier: "pop", Provider: "openai"}); err != nil {
		t.Fatal(err)
	}
	rt := newFakeRT(func(string, int) Inspection {
		return Inspection{State: "idle", Ready: true, PIDs: []int{42}, Starts: map[int]uint64{42: 4200}}
	})
	opt := PopReadOptions{Now: time.Now().Add(time.Hour), RetireAfter: 15 * time.Minute,
		Missions: func(context.Context, string) ([]string, error) { return nil, nil }}
	f := ReadPopFacts(ctx, b, rt, "security", opt)
	if done, why := PopDone(f); !done {
		t.Fatalf("kept: %s (%+v)", why, f.Unknown)
	}
	// Work arriving is read: a directive, an ask, a task, a hold, a pause.
	if _, err := b.Cmd(ctx, "master", "security", "directive", "", "one more"); err != nil {
		t.Fatal(err)
	}
	if done, why := PopDone(ReadPopFacts(ctx, b, rt, "security", opt)); done || !strings.Contains(why, "messages wait") {
		t.Fatalf("a directive after the judgement: done=%v why=%q", done, why)
	}
	// A failed reader is unknown, never done.
	opt.Missions = func(context.Context, string) ([]string, error) { return nil, errors.New("down") }
	if done, why := PopDone(ReadPopFacts(ctx, b, rt, "security", opt)); done || !strings.Contains(why, "unknown: missions") {
		t.Fatalf("a failed read: done=%v why=%q", done, why)
	}
}
