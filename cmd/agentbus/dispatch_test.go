package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
)

// dialDispatchMain is dialMain plus a DeleteProject cleanup (dispatch writes
// lease and wake keys dialMain does not know about).
func dialDispatchMain(t *testing.T) *bus.Bus {
	t.Helper()
	b, r := dialMain(t)
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()) })
	return b
}

// noRuntime fails the test if the dispatcher ever reaches a pane.
type noRuntime struct{ t *testing.T }

func (n noRuntime) Inspect(context.Context, string) dispatch.Inspection {
	n.t.Error("Inspect called")
	return dispatch.Inspection{}
}
func (n noRuntime) Prompt(context.Context, string, string, string) (bool, error) {
	n.t.Error("Prompt called")
	return false, nil
}

func TestDispatchStatusExitCodes(t *testing.T) {
	b := dialDispatchMain(t)
	ctx := context.Background()
	var out, errb bytes.Buffer
	if code := runDispatch(ctx, b, []string{"--status"}, noRuntime{t}, &out, &errb); code != 1 || strings.TrimSpace(out.String()) != "dispatch down" {
		t.Fatalf("status without lease = %d %q, want 1 \"dispatch down\"", code, out.String())
	}
	if ok, _ := b.AcquireDispatch(ctx, "tok", 15*time.Second); !ok {
		t.Fatal("AcquireDispatch failed")
	}
	out.Reset()
	if code := runDispatch(ctx, b, []string{"--status"}, noRuntime{t}, &out, &errb); code != 0 || strings.TrimSpace(out.String()) != "dispatch up" {
		t.Fatalf("status with lease = %d %q, want 0 \"dispatch up\"", code, out.String())
	}
}

func TestDispatchAlreadyRunningExit0(t *testing.T) {
	b := dialDispatchMain(t)
	ctx := context.Background()
	if ok, _ := b.AcquireDispatch(ctx, "other", 15*time.Second); !ok {
		t.Fatal("AcquireDispatch failed")
	}
	var out, errb bytes.Buffer
	if code := runDispatch(ctx, b, nil, noRuntime{t}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errb.String())
	}
	if !strings.Contains(errb.String(), "already running") {
		t.Fatalf("stderr = %q, want an already-running message", errb.String())
	}
}

func TestDispatchBadFlags(t *testing.T) {
	var out, errb bytes.Buffer
	for _, args := range [][]string{{"--horizon", "soon"}, {"--escalate-to", "Bad Name"}, {"--bogus"}, {"--escalate-after"}} {
		errb.Reset()
		if code := runDispatch(context.Background(), nil, args, nil, &out, &errb); code != 2 {
			t.Errorf("%v: exit = %d, want 2 (usage)", args, code)
		}
	}
}
