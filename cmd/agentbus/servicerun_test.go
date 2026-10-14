package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/mission"
	"github.com/netbja/agent-bus-monitor/service"
)

func runFixture(t *testing.T) (*bus.Bus, serviceRunDeps) {
	t.Helper()
	b, r := dialMain(t)
	t.Cleanup(func() { r.Del(context.Background(), bus.ServicesKey(b.Project()), bus.DispatchKey(b.Project())) })
	root := t.TempDir()
	d := serviceRunDeps{
		Paths:     service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"},
		Host:      "h1",
		Heartbeat: 20 * time.Millisecond,
		HerdrPing: func(context.Context) error { return nil },
		Now:       time.Now,
	}
	return b, d
}

func mark(t *testing.T, b *bus.Bus) {
	t.Helper()
	if err := b.MarkServicesInstalled(context.Background(), bus.ServicesInstall{Kinds: service.Kinds, Manager: "systemd", Host: "h1"}); err != nil {
		t.Fatal(err)
	}
}

func record(t *testing.T, b *bus.Bus, kind string) bus.ServiceRecord {
	t.Helper()
	_, recs, err := b.ServiceStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return recs[kind]
}

func waitState(t *testing.T, b *bus.Bus, kind, state string) bus.ServiceRecord {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r := record(t, b, kind); r.State == state {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never reached %q (last %+v)", kind, state, record(t, b, kind))
	return bus.ServiceRecord{}
}

func TestServiceRunExitsWithoutTheMarker(t *testing.T) {
	b, d := runFixture(t)
	ran := false
	d.RunDispatch = func(context.Context, *bus.Bus, string, func(bool), io.Writer) int { ran = true; return 0 }
	var log bytes.Buffer
	if code := serviceRun(context.Background(), b, service.Dispatch, nil, d, &log); code != 0 || ran {
		t.Fatalf("code = %d, ran = %v; a deleted project must exit 0 without running", code, ran)
	}
	if ok, _ := b.ServicesInstalled(context.Background()); ok {
		t.Fatal("the runner recreated the marker")
	}
	if !strings.Contains(log.String(), "project gone") {
		t.Fatalf("log = %q", log.String())
	}
}

func TestServiceRunDispatchReadyWaitingHerdrStopped(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	var herdrDown atomic.Bool
	d.HerdrPing = func(context.Context) error {
		if herdrDown.Load() {
			return errors.New("herdr: no server")
		}
		return nil
	}
	d.RunDispatch = func(ctx context.Context, _ *bus.Bus, _ string, onLease func(bool), _ io.Writer) int {
		onLease(true)
		<-ctx.Done()
		onLease(false)
		return 0
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { var log bytes.Buffer; done <- serviceRun(ctx, b, service.Dispatch, nil, d, &log) }()
	r := waitState(t, b, service.Dispatch, "ready")
	if r.Host != "h1" || r.PID == 0 || !strings.HasSuffix(r.Log, "dispatch.log") {
		t.Fatalf("record = %+v", r)
	}
	herdrDown.Store(true)
	if r := waitState(t, b, service.Dispatch, "waiting-herdr"); !strings.Contains(r.Error, "no server") {
		t.Fatalf("record = %+v", r)
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("code = %d", code)
	}
	waitState(t, b, service.Dispatch, "stopped")
}

func TestServiceRunFailureCountsAgainstTheBudget(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	d.RunDispatch = func(_ context.Context, _ *bus.Bus, _ string, _ func(bool), log io.Writer) int {
		io.WriteString(log, "error: broker went away\n")
		return 1
	}
	for i := 0; i < service.BudgetStarts; i++ {
		var log bytes.Buffer
		if code := serviceRun(context.Background(), b, service.Dispatch, nil, d, &log); code != 1 {
			t.Fatalf("run %d: code = %d, want 1 (restart)", i, code)
		}
		if r := record(t, b, service.Dispatch); r.State != "failed" || !strings.Contains(r.Error, "broker went away") {
			t.Fatalf("run %d: record = %+v", i, r)
		}
	}
	var log bytes.Buffer
	if code := serviceRun(context.Background(), b, service.Dispatch, nil, d, &log); code != 0 {
		t.Fatalf("budget spent: code = %d, want 0 (no restart)", code)
	}
	if r := record(t, b, service.Dispatch); r.State != "gave-up" || !strings.Contains(r.Detail, "agentbus up") {
		t.Fatalf("record = %+v", r)
	}
}

func TestServiceRunStopsWhenTheProjectIsDeleted(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	d.RunDispatch = func(ctx context.Context, _ *bus.Bus, _ string, onLease func(bool), _ io.Writer) int {
		onLease(true)
		<-ctx.Done()
		return 0
	}
	done := make(chan int, 1)
	go func() { var log bytes.Buffer; done <- serviceRun(context.Background(), b, service.Dispatch, nil, d, &log) }()
	waitState(t, b, service.Dispatch, "ready")
	b.ClearServices(context.Background()) // busmon --delete
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("code = %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the service outlived its project")
	}
	if ok, _ := b.ServicesInstalled(context.Background()); ok {
		t.Fatal("the marker came back")
	}
}

func TestServiceRunMissionsRefusesABadRepoWithoutRestart(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	ran := false
	d.RunMissions = func(context.Context, *bus.Bus, mission.RunConfig) error { ran = true; return nil }
	var log bytes.Buffer
	if code := serviceRun(context.Background(), b, service.Missions, []string{"--repo", t.TempDir()}, d, &log); code != 0 || ran {
		t.Fatalf("code = %d ran = %v", code, ran)
	}
	if r := record(t, b, service.Missions); r.State != "failed" || !strings.Contains(r.Error, "invalid configuration") {
		t.Fatalf("record = %+v", r)
	}
}

func TestServiceRunMissionsIdleThenStandby(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	d.RunMissions = func(ctx context.Context, _ *bus.Bus, cfg mission.RunConfig) error {
		if cfg.Repo != repo || cfg.Log == nil {
			t.Errorf("cfg = %+v", cfg)
		}
		<-ctx.Done()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { var log bytes.Buffer; done <- serviceRun(ctx, b, service.Missions, []string{"--repo", repo}, d, &log) }()
	if r := waitState(t, b, service.Missions, "idle"); !strings.Contains(r.Detail, "waiting for missions") {
		t.Fatalf("record = %+v", r)
	}
	cancel()
	<-done
	d.RunMissions = func(context.Context, *bus.Bus, mission.RunConfig) error { return mission.ErrAlreadyRunning }
	var log bytes.Buffer
	if code := serviceRun(context.Background(), b, service.Missions, []string{"--repo", repo}, d, &log); code != 0 {
		t.Fatalf("code = %d", code)
	}
	waitState(t, b, service.Missions, "standby")
}
