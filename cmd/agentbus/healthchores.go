package main

import (
	"context"
	"errors"
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

func modelsRefresh(b *bus.Bus) func(context.Context) error {
	return func(ctx context.Context) error {
		installed, err := b.ServicesInstalled(ctx)
		if err != nil || !installed {
			return err
		}
		return refreshModelsDaily(ctx)
	}
}

func refreshModelsDaily(ctx context.Context) error {
	cfg, err := models.LoadConfig(models.ConfigPath())
	if err != nil {
		return err
	}
	return refreshModelsUnderLock(ctx, models.CachePath(), cfg.Tiers, models.Readers(models.RealEnv()), time.Now())
}

func refreshModelsUnderLock(ctx context.Context, path string, tiers models.TierMap, readers []models.Reader, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil
		}
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if st, err := os.Stat(path); err == nil && now.Sub(st.ModTime()) >= 0 && now.Sub(st.ModTime()) < 24*time.Hour {
		return nil
	}
	table, err := models.Load(path)
	if err != nil {
		return err
	}
	table.Refresh(ctx, readers, tiers, now)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return table.Save(path)
}
