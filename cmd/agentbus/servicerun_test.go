package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
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

// runUntilCleanup runs serviceRun in the background until the test ends,
// then stops it and waits for it to return. Its clean exit still writes the
// restart budget under the test's temp dir (budget.End); a test that returned
// without waiting raced that write against the dir's removal ("directory not
// empty"). Cleanups run last-in first-out, so this one ends before the
// fixture's temp dir is removed.
func runUntilCleanup(t *testing.T, b *bus.Bus, kind string, args []string, d serviceRunDeps) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		var log bytes.Buffer
		serviceRun(ctx, b, kind, args, d, &log)
	}()
	t.Cleanup(func() { cancel(); <-done })
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

// A clean run leaves nothing in the budget; a run that fails leaves its start.
func TestServiceRunCountsOnlyRunsThatDoNotEndCleanly(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	budget := service.Budget{Path: d.Paths.BudgetFile(b.Project(), service.Dispatch)}
	d.RunDispatch = func(ctx context.Context, _ *bus.Bus, _ string, onLease func(bool), _ io.Writer) int { return 0 }
	for i := 0; i < service.BudgetStarts+1; i++ {
		var log bytes.Buffer
		serviceRun(context.Background(), b, service.Dispatch, nil, d, &log)
	}
	if budget.Spent(time.Now()) {
		t.Fatal("clean exits spent the budget")
	}
	d.RunDispatch = func(context.Context, *bus.Bus, string, func(bool), io.Writer) int { return 1 }
	var log bytes.Buffer
	if code := serviceRun(context.Background(), b, service.Dispatch, nil, d, &log); code != 1 {
		t.Fatalf("code = %d", code)
	}
	raw, _ := os.ReadFile(budget.Path)
	if n := len(strings.Fields(string(raw))); n != 1 {
		t.Fatalf("budget holds %d entries after one failure, want 1", n)
	}
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

// Losing the lease leaves "ready" at once: it re-acquires before it is ready again.
func TestServiceRunDispatchLeavesReadyWhenTheLeaseIsLost(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	lose := make(chan struct{})
	d.RunDispatch = func(ctx context.Context, _ *bus.Bus, _ string, onLease func(bool), _ io.Writer) int {
		onLease(true)
		<-lose
		onLease(false)
		<-ctx.Done()
		return 0
	}
	runUntilCleanup(t, b, service.Dispatch, nil, d)
	waitState(t, b, service.Dispatch, "ready")
	close(lose)
	if r := waitState(t, b, service.Dispatch, "starting"); !strings.Contains(r.Detail, "lease") {
		t.Fatalf("record = %+v", r)
	}
}

// The conductor logs only incidents (a failed pass, ops, the inbox); the
// latest one is published with its state, so a failing conductor never looks
// merely busy.
func TestServiceRunMissionsPublishesTheConductorsLastIncident(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	d.RunMissions = func(ctx context.Context, _ *bus.Bus, cfg mission.RunConfig) error {
		cfg.Log("pass: %v", "git merge: conflict in a.go")
		<-ctx.Done()
		return nil
	}
	runUntilCleanup(t, b, service.Missions, []string{"--repo", repo}, d)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r := record(t, b, service.Missions); r.State == "idle" && strings.Contains(r.Error, "conflict in a.go") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("record = %+v", record(t, b, service.Missions))
}

func TestServiceRunFailureCountsAgainstTheBudget(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	d.RunDispatch = func(_ context.Context, _ *bus.Bus, _ string, _ func(bool), log io.Writer) int {
		io.WriteString(log, "error: broker went away\n")
		return 1
	}
	for i := 0; i < service.BudgetStarts-1; i++ {
		var log bytes.Buffer
		if code := serviceRun(context.Background(), b, service.Dispatch, nil, d, &log); code != 1 {
			t.Fatalf("run %d: code = %d, want 1 (restart)", i, code)
		}
		if r := record(t, b, service.Dispatch); r.State != "failed" || !strings.Contains(r.Error, "broker went away") {
			t.Fatalf("run %d: record = %+v", i, r)
		}
	}
	// The failure that spends the budget gives up in this very process:
	// systemd's start limit refuses a sixth start, so no later run would say it.
	var log bytes.Buffer
	if code := serviceRun(context.Background(), b, service.Dispatch, nil, d, &log); code != 0 {
		t.Fatalf("fifth failure: code = %d, want 0 (no restart)", code)
	}
	if r := record(t, b, service.Dispatch); r.State != "gave-up" || !strings.Contains(r.Detail, "agentbus up") || !strings.Contains(r.Error, "broker went away") {
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
	go func() {
		var log bytes.Buffer
		done <- serviceRun(context.Background(), b, service.Dispatch, nil, d, &log)
	}()
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

func TestServiceRunMissionsRefusesNoRepoWithoutRestart(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	ran := false
	d.RunMissions = func(context.Context, *bus.Bus, mission.RunConfig) error { ran = true; return nil }
	var log bytes.Buffer
	if code := serviceRun(context.Background(), b, service.Missions, nil, d, &log); code != 0 || ran {
		t.Fatalf("code = %d ran = %v", code, ran)
	}
	if r := record(t, b, service.Missions); r.State != "failed" || !strings.Contains(r.Error, "invalid configuration") {
		t.Fatalf("record = %+v", r)
	}
}

// A directory that is not a Git repository yet: missions waits, publishing
// waiting-git, spends no restart, and starts the conductor after git init.
func TestServiceRunMissionsWaitsForGitThenStarts(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	repo := t.TempDir()
	started := make(chan string, 1)
	d.RunMissions = func(ctx context.Context, _ *bus.Bus, cfg mission.RunConfig) error {
		started <- cfg.Repo
		<-ctx.Done()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var log bytes.Buffer
	go func() { done <- serviceRun(ctx, b, service.Missions, []string{"--repo", repo}, d, &log) }()
	r := waitState(t, b, service.Missions, gitWaitState)
	if !strings.Contains(r.Detail, "git init") || r.Error != "" {
		t.Fatalf("record = %+v", r)
	}
	budget := service.Budget{Path: d.Paths.BudgetFile(b.Project(), service.Missions)}
	if n, _ := budget.FailureCountChecked(time.Now()); n != 0 {
		t.Fatalf("waiting holds %d budget entries, want 0", n)
	}
	select {
	case <-started:
		t.Fatal("the conductor started without a repository")
	case <-time.After(100 * time.Millisecond):
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	select {
	case got := <-started:
		if got != repo {
			t.Fatalf("repo = %q", got)
		}
		if n, _ := budget.FailureCountChecked(time.Now()); n != 1 {
			t.Fatalf("a running conductor holds %d budget entries, want 1 (a crash must count)", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the conductor never started after git init")
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("code = %d", code)
	}
	if n, _ := (service.Budget{Path: d.Paths.BudgetFile(b.Project(), service.Missions)}).FailureCountChecked(time.Now()); n != 0 {
		t.Fatalf("budget entries = %d", n)
	}
}

// Stopped while it waits: a clean, final exit.
func TestServiceRunMissionsStopsWhileWaitingForGit(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	d.RunMissions = func(context.Context, *bus.Bus, mission.RunConfig) error { t.Error("conductor started"); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var log bytes.Buffer
	go func() { done <- serviceRun(ctx, b, service.Missions, []string{"--repo", t.TempDir()}, d, &log) }()
	waitState(t, b, service.Missions, gitWaitState)
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("code = %d", code)
	}
	waitState(t, b, service.Missions, "stopped")
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
	go func() {
		var log bytes.Buffer
		done <- serviceRun(ctx, b, service.Missions, []string{"--repo", repo}, d, &log)
	}()
	if r := waitState(t, b, service.Missions, "idle"); !strings.Contains(r.Detail, "waiting for missions") {
		t.Fatalf("record = %+v", r)
	}
	cancel()
	<-done
	// Another conductor first: standby, then the run once it has gone.
	d.Standby = 20 * time.Millisecond
	var calls atomic.Int32
	d.RunMissions = func(ctx context.Context, _ *bus.Bus, _ mission.RunConfig) error {
		if calls.Add(1) == 1 {
			return mission.ErrAlreadyRunning
		}
		<-ctx.Done()
		return nil
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	go func() {
		var log bytes.Buffer
		done <- serviceRun(ctx2, b, service.Missions, []string{"--repo", repo}, d, &log)
	}()
	waitState(t, b, service.Missions, "standby")
	waitState(t, b, service.Missions, "idle")
	cancel2()
	if code := <-done; code != 0 || calls.Load() != 2 {
		t.Fatalf("code %d, runs %d", code, calls.Load())
	}
}

// A service that finds another instance holding the lease (an old tab) waits
// in standby and takes over once the lease is free, instead of exiting for
// good: a closed tab must not leave the project without a dispatcher.
func TestServiceRunWaitsInStandbyThenTakesOver(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	d.Standby = 20 * time.Millisecond
	ctx := context.Background()
	// The "tab" holds the dispatch lease for a while.
	if ok, err := b.AcquireDispatch(ctx, "tab-token", 300*time.Millisecond); err != nil || !ok {
		t.Fatalf("acquire = %v %v", ok, err)
	}
	var calls atomic.Int32
	d.RunDispatch = func(ctx context.Context, b *bus.Bus, _ string, onLease func(bool), _ io.Writer) int {
		if calls.Add(1) == 1 {
			return 0 // like superviseDispatch on ErrAlreadyRunning
		}
		onLease(true)
		<-ctx.Done()
		return 0
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan int, 1)
	go func() { var log bytes.Buffer; done <- serviceRun(runCtx, b, service.Dispatch, nil, d, &log) }()
	waitState(t, b, service.Dispatch, "standby")
	waitState(t, b, service.Dispatch, "ready") // the tab's lease lapsed: taken over
	cancel()
	if code := <-done; code != 0 || calls.Load() != 2 {
		t.Fatalf("code %d, runs %d", code, calls.Load())
	}
}

// A herdr probe in flight when the lease is lost must not put "ready" back:
// its answer is for a lease generation that no longer holds.
func TestServiceRunAProbeInFlightCannotRestoreReady(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	var blocking atomic.Bool
	entered, release := make(chan struct{}, 1), make(chan struct{})
	d.HerdrPing = func(context.Context) error {
		if blocking.Load() {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
		}
		return nil
	}
	lose, lost := make(chan struct{}), make(chan struct{})
	d.RunDispatch = func(ctx context.Context, _ *bus.Bus, _ string, onLease func(bool), _ io.Writer) int {
		onLease(true)
		<-lose
		onLease(false)
		close(lost)
		<-ctx.Done()
		return 0
	}
	runUntilCleanup(t, b, service.Dispatch, nil, d)
	waitState(t, b, service.Dispatch, "ready")
	blocking.Store(true)
	<-entered   // the heartbeat is probing with the lease held
	close(lose) // the lease is lost meanwhile
	<-lost      // and the loss is recorded before the probe answers
	blocking.Store(false)
	close(release) // the probe answers "herdr is fine"
	time.Sleep(100 * time.Millisecond)
	for i := 0; i < 5; i++ {
		if r := record(t, b, service.Dispatch); r.State == "ready" {
			t.Fatalf("ready came back without the lease: %+v", r)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// A panic unwinds through serviceRun's deferred functions: it must leave its
// start counted, or launchd would restart a panicking service forever.
func TestServiceRunAPanicKeepsItsBudgetEntry(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	d.RunDispatch = func(context.Context, *bus.Bus, string, func(bool), io.Writer) int { panic("boom") }
	func() {
		defer func() { recover() }()
		var log bytes.Buffer
		serviceRun(context.Background(), b, service.Dispatch, nil, d, &log)
	}()
	raw, _ := os.ReadFile(service.Budget{Path: d.Paths.BudgetFile(b.Project(), service.Dispatch)}.Path)
	if n := len(strings.Fields(string(raw))); n != 1 {
		t.Fatalf("budget entries after a panic = %d, want 1", n)
	}
}

// A marker read that fails (the broker answers, but refuses it) is a failed
// start and counts.
func TestServiceRunAFailedMarkerReadCounts(t *testing.T) {
	b, d := runFixture(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	closed, _ := bus.Open(c, b.Project())
	var log bytes.Buffer
	if code := serviceRun(context.Background(), closed, service.Dispatch, nil, d, &log); code != 1 {
		t.Fatalf("code = %d", code)
	}
	raw, _ := os.ReadFile(service.Budget{Path: d.Paths.BudgetFile(b.Project(), service.Dispatch)}.Path)
	if n := len(strings.Fields(string(raw))); n != 1 {
		t.Fatalf("budget entries after a failed marker read = %d, want 1", n)
	}
}

// The target up recorded in the environment file reaches the dispatcher; an
// invalid one is a configuration error, final, not a restart.
func TestServiceRunTakesTheRecordedEscalationTarget(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	got := ""
	d.RunDispatch = func(_ context.Context, _ *bus.Bus, to string, _ func(bool), _ io.Writer) int { got = to; return 0 }
	t.Setenv(service.EscalateEnv, "claude-hl-master")
	var log bytes.Buffer
	if code := serviceRun(context.Background(), b, service.Dispatch, nil, d, &log); code != 0 || got != "claude-hl-master" {
		t.Fatalf("code %d, escalate-to %q", code, got)
	}
	got = "unset"
	if code := serviceRun(context.Background(), b, service.Dispatch, []string{"--escalate-to", "lead"}, d, &log); code != 0 || got != "lead" {
		t.Fatalf("an explicit argument must win: code %d, %q", code, got)
	}
	got = "unset"
	t.Setenv(service.EscalateEnv, "Bad Name")
	if code := serviceRun(context.Background(), b, service.Dispatch, nil, d, &log); code != 0 || got != "unset" {
		t.Fatalf("invalid target: code %d, ran %q", code, got)
	}
	if r := record(t, b, service.Dispatch); r.State != "failed" || !strings.Contains(r.Detail+r.Error, "AGENT_BUS_ESCALATE_TO") {
		t.Fatalf("record = %+v", r)
	}
}
