package mission

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func runConfig(t *testing.T, repo string) RunConfig {
	cfg := DefaultRunConfig()
	cfg.Repo, cfg.StateHome = repo, t.TempDir()
	cfg.Tick, cfg.Lease, cfg.Renew, cfg.TestLimit = 100*time.Millisecond, 2*time.Second, 200*time.Millisecond, time.Minute
	return cfg
}

// startRun runs Run in a goroutine; the returned channel yields its error.
func startRun(t *testing.T, ctx context.Context, b *bus.Bus, cfg RunConfig) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, b, cfg) }()
	waitFor(t, 5*time.Second, "conductor up", func() bool { ok, _ := b.ConductorAlive(context.Background()); return ok })
	return done
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(25 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestRunSecondConductorExits(t *testing.T) {
	b, _ := dialBus(t)
	_, repo := newRepo(t)
	cfg := runConfig(t, repo)
	ctx, cancel := context.WithCancel(context.Background())
	done := startRun(t, ctx, b, cfg)
	if err := Run(context.Background(), b, cfg); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Run: %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if alive, _ := b.ConductorAlive(context.Background()); alive {
		t.Fatal("lease not released on exit")
	}
}

func TestRunFlockBlocksEvenWithoutLease(t *testing.T) {
	b, r := dialBus(t)
	_, repo := newRepo(t)
	cfg := runConfig(t, repo)
	cfg.Renew = time.Hour // the first conductor will not notice the deleted lease
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startRun(t, ctx, b, cfg)
	r.Del(context.Background(), bus.ConductorKey(b.Project()))
	if err := Run(context.Background(), b, cfg); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Run with the lock held: %v", err)
	}
}

func TestRunInboxKeepsConductorArmed(t *testing.T) {
	b, _ := dialBus(t)
	_, repo := newRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startRun(t, ctx, b, runConfig(t, repo))
	waitFor(t, 5*time.Second, "conductor armed", func() bool { ok, _ := b.Armed(context.Background(), "conductor"); return ok })
}

// loadViaOp loads chg through the operator queue while Run runs.
func loadViaOp(t *testing.T, b *bus.Bus, commit string) {
	t.Helper()
	id, err := b.SubmitMissionOp(context.Background(), "master", "load", []string{"docs/changes/chg/missions.json@" + commit})
	if err != nil {
		t.Fatal(err)
	}
	ok, msg, found, err := b.WaitMissionOpResult(context.Background(), id, 10*time.Second)
	if err != nil || !found || !ok {
		t.Fatalf("load: %v %q %v %v", ok, msg, found, err)
	}
}

func TestRunLeaseLostKillsTest(t *testing.T) {
	b, r := dialBus(t)
	_, repo := newRepo(t)
	pidFile := filepath.Join(t.TempDir(), "test.pid")
	commitFile(t, repo, "docs/changes/chg/plan.md", "# plan\n", "plan")
	commit := commitFile(t, repo, "docs/changes/chg/missions.json", missionsFile("echo $$ > "+pidFile+"; sleep 60"), "missions")
	cfg := runConfig(t, repo)
	done := startRun(t, context.Background(), b, cfg)
	loadViaOp(t, b, commit)
	ctx := context.Background()
	waitFor(t, 5*time.Second, "t1 requested", func() bool { bd, _ := b.Board(ctx); _, ok := bd["chg-t1-1"]; return ok })
	b.AcceptRequest(ctx, "chg-t1-1", "kimi")
	g := Git{Repo: repo}
	base, _ := g.ResolveCommit(ctx, "change/chg")
	wt := filepath.Join(t.TempDir(), "agent")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "mission/x", wt, base)
	sha := commitFile(t, wt, "src/t1/a.go", "x", "work")
	b.CompleteRequestAt(ctx, "chg-t1-1", "kimi", "done", sha, "mission/x")
	waitFor(t, 10*time.Second, "test started", func() bool { _, err := os.Stat(pidFile); return err == nil })
	r.Del(ctx, bus.ConductorKey(b.Project()))
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "lease lost") {
			t.Fatalf("Run returned %v, want lease lost", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return after losing its lease")
	}
	b2, _ := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b2)))
	waitFor(t, 3*time.Second, "test process gone", func() bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) })
}

func TestRunLeaseLossReleasesInbox(t *testing.T) {
	b, r := dialBus(t)
	_, repo := newRepo(t)
	before := runtime.NumGoroutine()
	done := startRun(t, context.Background(), b, runConfig(t, repo))
	waitFor(t, 5*time.Second, "armed", func() bool { ok, _ := b.Armed(context.Background(), "conductor"); return ok })
	r.Del(context.Background(), bus.ConductorKey(b.Project()))
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}
	waitFor(t, 5*time.Second, "receiver released", func() bool { ok, _ := b.Armed(context.Background(), "conductor"); return !ok })
	waitFor(t, 5*time.Second, "goroutines gone", func() bool { return runtime.NumGoroutine() <= before+2 })
}
