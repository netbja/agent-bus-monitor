package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/models"
	"github.com/netbja/agent-bus-monitor/usage"
)

func usageRefresh(b *bus.Bus) func(context.Context, string) error {
	return func(ctx context.Context, token string) error {
		installed, err := b.ServicesInstalled(ctx)
		if err != nil || !installed {
			return err
		}
		agents, err := b.Agents(ctx)
		if err != nil {
			return err
		}
		rows, _ := collectAgentUsage(agents, usage.ProjectsRoot())
		names := make([]string, 0, len(rows))
		for name := range rows {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if err := b.FencedSetUsage(ctx, token, name, rows[name]); err != nil {
				return err
			}
		}
		return nil
	}
}

// modelsRefresh is the dispatcher's daily model refresh. G7 (model-detection
// §3.5): it no longer waits for the project's services, so a dispatcher run
// by hand refreshes too; the table is the machine's, not the project's.
func modelsRefresh() func(context.Context) error {
	return func(ctx context.Context) error { return dailyModelsRefresh(ctx) }
}

// dailyModelsRefresh is refreshModelsDaily (a test replaces it).
var dailyModelsRefresh = refreshModelsDaily

func refreshModelsDaily(ctx context.Context) error {
	cfg, err := loadModelsConfig(models.ConfigPath())
	if err != nil {
		return err
	}
	readers := modelReaders(models.RealEnv(), manifestFromEnv, io.Discard) // §3.9: adapters too
	return refreshModelsUnderLock(ctx, models.CachePath(), cfg.Tiers, readers, time.Now())
}

func refreshModelsUnderLock(ctx context.Context, path string, tiers models.TierMap, readers []models.Reader, now time.Time) error {
	_, _, err := refreshTable(ctx, path, tiers, readers, now, 0)
	return err
}

// refreshTable refreshes the table at path under its lock when its newest
// refresh attempt is 24 h old or more (or in the future). held: another
// process holds the lock, and nothing was done. budget > 0 bounds the whole
// refresh (model-detection §3.5: `agentbus models`' automatic refresh): a
// reader still running then keeps its provider's rows and is a failure, and
// the rest is saved. failed: the providers whose read failed, with why.
func refreshTable(ctx context.Context, path string, tiers models.TierMap, readers []models.Reader, now time.Time, budget time.Duration) (failed map[string]string, held bool, err error) {
	unlock, held, err := lockTable(path)
	if err != nil || held {
		return nil, held, err
	}
	defer unlock()
	table, err := models.Load(path)
	if err != nil {
		return nil, false, err
	}
	if since := now.Sub(time.UnixMilli(table.RefreshedAt())); table.RefreshedAt() != 0 && since >= 0 && since < 24*time.Hour {
		return nil, false, nil
	}
	rctx := ctx
	if budget > 0 {
		var cancel context.CancelFunc
		rctx, cancel = context.WithTimeout(ctx, budget)
		defer cancel()
	}
	failed = table.Refresh(rctx, readers, tiers, now)
	if ctx.Err() != nil {
		return failed, false, ctx.Err()
	}
	return failed, false, table.Save(path)
}

// lockTable takes the model table's lock without waiting: held when another
// process has it (nothing to do then).
func lockTable(path string) (unlock func(), held bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); lock.Close() }, false, nil
}
