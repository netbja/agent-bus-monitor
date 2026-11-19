package main

import (
	"bytes"
	"context"
	"errors"
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
	calls := strings.Join(hm.Calls, "\n")
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
	if len(hm.Calls) != 0 || out.Len() != 0 {
		t.Fatalf("calls %v, out %q", hm.Calls, out.String())
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
	t.Cleanup(open)                    // after a failure too: the run's stop needs RunDispatch past the grant
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

// The service's closure: up's manager and the unit's working directory reach
// ensureOwnHolder; no manager is an error, not a panic.
func TestServiceEnsureHolderWiring(t *testing.T) {
	_, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	exe := filepath.Join(t.TempDir(), "agentbus")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	deps.Exe = func() (string, error) { return exe, nil }
	hm := &fakeHolderManager{fakeManager: u.m, holders: map[string]service.HolderSpec{}, active: map[string]bool{}}
	deps.Services = func() (service.Manager, string) { return hm, "" }
	deps.HolderBuild = func() string { return "v0.29.0" }
	deps.HolderAnswers = func(project, k string) bool { return hm.active[k] }
	deps.Sleep = func(time.Duration) {}
	work := t.TempDir()
	t.Chdir(work)
	var out bytes.Buffer
	if err := serviceEnsureHolder("demo", func(io.Writer) upDeps { return deps })(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	key := service.BuildKey("v0.29.0")
	if s, ok := hm.holders[key]; !ok || s.WorkDir != work || s.Project != "demo" {
		t.Fatalf("holder spec %+v (installed %v)", s, ok)
	}
	deps.Services = func() (service.Manager, string) { return nil, "no systemd" }
	if err := serviceEnsureHolder("demo", func(io.Writer) upDeps { return deps })(context.Background(), &out); err == nil || !strings.Contains(err.Error(), "no systemd") {
		t.Fatalf("no manager: %v", err)
	}
}

// foureyes: the binary the service would copy must be the one it runs. An
// update that replaced the installed agentbus between the service's start
// and its first lease would otherwise copy the new binary under the old
// build's key: refused, with the restart named.
func TestServiceEnsureHolderRefusesAReplacedBinary(t *testing.T) {
	for _, how := range []string{"renamed over", "rewritten in place"} {
		t.Run(how, func(t *testing.T) {
			_, _, _, _, deps := upFixture(t)
			u := withServices(t, &deps)
			dir := t.TempDir()
			exe := filepath.Join(dir, "agentbus")
			if err := os.WriteFile(exe, []byte("#!/bin/sh\n# v0.29.0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			deps.Exe = func() (string, error) { return exe, nil }
			hm := &fakeHolderManager{fakeManager: u.m, holders: map[string]service.HolderSpec{}, active: map[string]bool{}}
			deps.Services = func() (service.Manager, string) { return hm, "" }
			deps.HolderBuild = func() string { return "v0.29.0" }
			deps.HolderAnswers = func(string, string) bool { return false }
			deps.Sleep = func(time.Duration) {}
			ensure := serviceEnsureHolder("demo", func(io.Writer) upDeps { return deps }) // the service's start
			if how == "renamed over" {
				next := filepath.Join(dir, "agentbus.new")
				if err := os.WriteFile(next, []byte("#!/bin/sh\n# v0.30.0, a longer one\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(next, exe); err != nil {
					t.Fatal(err)
				}
			} else {
				later := time.Now().Add(time.Hour)
				if err := os.WriteFile(exe, []byte("#!/bin/sh\n# v0.30.0, a longer one\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				_ = os.Chtimes(exe, later, later)
			}
			var out bytes.Buffer
			err := ensure(context.Background(), &out)
			if err == nil || !strings.Contains(err.Error(), "replaced since the dispatcher started") {
				t.Fatalf("err = %v", err)
			}
			if len(hm.Calls) != 0 {
				t.Fatalf("installed from a replaced binary: %v", hm.Calls)
			}
		})
	}
}

// foureyes: the service's deps carry the probe and the ensure, so neither
// can be unhooked from mainServiceRun silently.
func TestServiceRunDepsForTheDispatchService(t *testing.T) {
	d := serviceRunDepsFor("demo", "", "h1", service.Paths{Home: t.TempDir()})
	if d.EnsureHolder == nil || d.HostPing == nil || d.RunDispatch == nil || d.RunMissions == nil {
		t.Fatalf("deps lack a hook: ensure %v, ping %v, dispatch %v, missions %v", d.EnsureHolder != nil, d.HostPing != nil, d.RunDispatch != nil, d.RunMissions != nil)
	}
	if d.Heartbeat <= 0 || d.EnsureRetry <= 0 {
		t.Fatalf("heartbeat %v, retry %v", d.Heartbeat, d.EnsureRetry)
	}
}

// foureyes's note: a failed ensure (a transient systemctl error) is tried
// again after EnsureRetry while the lease is held; a success is final.
func TestServiceRunRetriesAFailedEnsure(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	var calls atomic.Int32
	d.EnsureRetry = 60 * time.Millisecond
	d.EnsureHolder = func(context.Context, io.Writer) error {
		if calls.Add(1) == 1 {
			return errors.New("systemctl: transient")
		}
		return nil
	}
	d.RunDispatch = func(ctx context.Context, _ *bus.Bus, _ string, onLease func(bool), _ io.Writer) int {
		onLease(true)
		<-ctx.Done()
		onLease(false)
		return 0
	}
	runUntilCleanup(t, b, service.Dispatch, nil, d)
	waitState(t, b, service.Dispatch, "ready")
	time.Sleep(30 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("retried before EnsureRetry: %d calls", n)
	}
	time.Sleep(250 * time.Millisecond)
	if n := calls.Load(); n != 2 {
		t.Fatalf("calls %d, want 2 (one retry, then success is final)", n)
	}
}
