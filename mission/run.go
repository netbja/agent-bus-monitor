package mission

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// RunConfig configures the conductor process.
type RunConfig struct {
	Repo, StateHome, EscalateTo   string
	Tick, Lease, Renew, TestLimit time.Duration
	Log                           func(format string, args ...any) // optional
}

// DefaultRunConfig: a 2 s tick, the dispatcher's lease timings, 20 min per
// test, escalations to master, state under $XDG_STATE_HOME/agentbus.
func DefaultRunConfig() RunConfig {
	home := os.Getenv("XDG_STATE_HOME")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, ".local", "state")
		}
	}
	return RunConfig{StateHome: filepath.Join(home, "agentbus"), EscalateTo: "master",
		Tick: 2 * time.Second, Lease: 15 * time.Second, Renew: 5 * time.Second, TestLimit: 20 * time.Minute}
}

// ErrAlreadyRunning: another conductor holds the lock or the lease.
var ErrAlreadyRunning = errors.New("another conductor holds the lease")

const conductorGroup = "conductor"

// Run is the conductor process (spec §5.2): one per project, guarded by a
// local flock (so Git is never touched by two conductors, even while a lease
// lapses) and by the Redis lease. Losing the lease cancels everything Run
// started, kills the commands in flight, and returns "lease lost".
func Run(ctx context.Context, b *bus.Bus, cfg RunConfig) (err error) {
	logf := func(format string, args ...any) {
		if cfg.Log != nil {
			cfg.Log(format, args...)
		}
	}
	projectRoot := filepath.Join(cfg.StateHome, b.Project())
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(projectRoot, "conductor.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrAlreadyRunning
		}
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	token := randomToken()
	if ok, err := b.AcquireConductor(ctx, token, cfg.Lease); err != nil {
		return err
	} else if !ok {
		return ErrAlreadyRunning
	}
	defer b.ReleaseConductor(context.WithoutCancel(ctx), token)

	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var wg sync.WaitGroup
	defer wg.Wait()
	lost := errors.New("lease lost")

	wg.Add(1)
	go func() { // renewal: a lost lease stops everything
		defer wg.Done()
		t := time.NewTicker(cfg.Renew)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
				held, err := b.RenewConductor(runCtx, token, cfg.Lease)
				if err != nil {
					logf("renew lease: %v", err)
					continue // a lease that truly lapses fails the next renew
				}
				if !held {
					cancel(lost)
					return
				}
			}
		}
	}()

	kick := make(chan struct{}, 1)
	consumer := "conductor-" + token[:8]
	wg.Add(1)
	go func() { // the inbox: agents' replies wake a pass; holding the receiver
		defer wg.Done() // lease keeps the dispatcher from ever waking us
		for runCtx.Err() == nil {
			err := b.WatchCmdDelivery(runCtx, "conductor", consumer, "", func(bus.Event) (bool, error) {
				select {
				case kick <- struct{}{}:
				default:
				}
				return false, nil
			})
			if err != nil && runCtx.Err() == nil {
				logf("inbox: %v", err)
				select {
				case <-runCtx.Done():
				case <-time.After(time.Second):
				}
			}
		}
	}()

	c := &Conductor{Bus: b, Store: Store{B: b, Token: token}, Git: Git{Repo: cfg.Repo}, ProjectRoot: projectRoot,
		EscalateTo: cfg.EscalateTo, Self: "conductor", TestLimit: cfg.TestLimit, Log: cfg.Log}
	if err := c.Startup(runCtx); err != nil {
		logf("startup: %v", err)
	}
	tick := time.NewTicker(cfg.Tick)
	defer tick.Stop()
	for {
		ops, err := b.ReadMissionOps(runCtx, conductorGroup, consumer, 0)
		if err != nil && runCtx.Err() == nil {
			logf("read ops: %v", err)
		}
		for _, op := range ops {
			ok, msg := c.HandleOp(runCtx, op)
			if err := b.MissionOpResult(runCtx, token, op.ID, ok, msg); err != nil {
				logf("op %s result: %v", op.ID, err)
				break // unacknowledged: the next reader redoes it
			}
			if err := b.AckMissionOp(runCtx, token, conductorGroup, op.ID); err != nil {
				logf("op %s ack: %v", op.ID, err)
			}
		}
		if err := c.Pass(runCtx); err != nil && runCtx.Err() == nil {
			logf("pass: %v", err)
		}
		select {
		case <-runCtx.Done():
			if cause := context.Cause(runCtx); errors.Is(cause, lost) {
				return fmt.Errorf("%w: %w", lost, bus.ErrNotHolder)
			}
			return nil
		case <-tick.C:
		case <-kick:
		}
	}
}

func randomToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
