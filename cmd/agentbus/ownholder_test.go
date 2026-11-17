package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// The person's yes to Q1 (agent-screen-blocked spec §1): a dispatch service
// whose own build has no holder that answers installs and starts it, as up
// does, beside the older builds' holders, which it never stops.
func TestEnsureOwnHolderInstallsTheMissingBuild(t *testing.T) {
	_, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	exe := filepath.Join(t.TempDir(), "agentbus")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	deps.Exe = func() (string, error) { return exe, nil }
	older := service.BuildKey("v0.28.0-888")
	hm := &fakeHolderManager{fakeManager: u.m, holders: map[string]service.HolderSpec{}, active: map[string]bool{older: true},
		units: []service.HolderUnit{{Project: "demo", Key: older, Raw: "v0.28.0-888"}}}
	raw := "v0.29.0"
	key := service.BuildKey(raw)
	deps.HolderBuild = func() string { return raw }
	deps.HolderAnswers = func(project, k string) bool { return k == older || (k == key && hm.active[key]) }
	deps.Sleep = func(time.Duration) {}
	var out bytes.Buffer
	if err := ensureOwnHolder(deps, hm, "demo", "/work/demo", &out); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(hm.calls, "\n")
	for _, c := range []string{"install-holder demo " + key, "start-holder demo " + key} {
		if !strings.Contains(calls, c) {
			t.Fatalf("missing %q in\n%s", c, calls)
		}
	}
	if strings.Contains(calls, older) {
		t.Fatalf("the older build's holder was touched:\n%s", calls)
	}
	if s := hm.holders[key]; s.WorkDir != "/work/demo" || s.EnvFile != u.paths.EnvFile("demo") {
		t.Fatalf("holder spec %+v", s)
	}
	if !strings.Contains(out.String(), "no holder of this build (v0.29.0) answers") {
		t.Fatalf("out = %q", out.String())
	}
}

// Its own build's holder already answering: nothing is installed or started.
func TestEnsureOwnHolderLeavesAnAnsweringHolder(t *testing.T) {
	_, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	raw := "v0.29.0"
	hm := &fakeHolderManager{fakeManager: u.m, holders: map[string]service.HolderSpec{}, active: map[string]bool{}}
	deps.HolderBuild = func() string { return raw }
	deps.HolderAnswers = func(project, k string) bool { return k == service.BuildKey(raw) }
	var out bytes.Buffer
	if err := ensureOwnHolder(deps, hm, "demo", "/work/demo", &out); err != nil {
		t.Fatal(err)
	}
	if len(hm.calls) != 0 || out.Len() != 0 {
		t.Fatalf("calls %v, out %q", hm.calls, out.String())
	}
}

// The service asks once, after it first holds the lease: a standby instance
// (another host) installs nothing, and the missions service never asks.
func TestServiceRunEnsuresItsHolderOnceWithTheLease(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	var calls atomic.Int32
	d.EnsureHolder = func(context.Context, io.Writer) error { calls.Add(1); return nil }
	grant := make(chan struct{})
	d.RunDispatch = func(ctx context.Context, _ *bus.Bus, _ string, onLease func(bool), _ io.Writer) int {
		<-grant
		onLease(true)
		<-ctx.Done()
		onLease(false)
		return 0
	}
	var granted sync.Once
	open := func() { granted.Do(func() { close(grant) }) }
	runUntilCleanup(t, b, service.Dispatch, nil, d)
	t.Cleanup(open) // after a failure too: the run's stop needs RunDispatch past the grant
	time.Sleep(100 * time.Millisecond) // several heartbeats without the lease
	if n := calls.Load(); n != 0 {
		t.Fatalf("ensured %d times without the lease", n)
	}
	open()
	waitState(t, b, service.Dispatch, "ready")
	time.Sleep(100 * time.Millisecond) // several heartbeats with it
	if n := calls.Load(); n != 1 {
		t.Fatalf("ensured %d times, want once", n)
	}
}
