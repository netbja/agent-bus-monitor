package models

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Moved from cmd/agentbus unchanged (dispatchsvc P4): the model table's
// refresh under its host lock.

// RefreshTable refreshes the table at path under its lock when its newest
// refresh attempt is 24 h old or more (or in the future). held: another
// process holds the lock, and nothing was done. budget > 0 bounds the whole
// refresh (model-detection §3.5: `agentbus models`' automatic refresh): a
// reader still running then keeps its provider's rows and is a failure, and
// the rest is saved. failed: the providers whose read failed, with why.
func RefreshTable(ctx context.Context, path string, tiers TierMap, readers []Reader, now time.Time, budget time.Duration) (failed map[string]string, held bool, err error) {
	unlock, held, err := LockTable(path)
	if err != nil || held {
		return nil, held, err
	}
	defer unlock()
	table, err := Load(path)
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

// LockTable takes the model table's lock without waiting: held when another
// process has it (nothing to do then).
func LockTable(path string) (unlock func(), held bool, err error) {
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

// RefreshUnderLock is the daily chore's refresh: RefreshTable with no budget.
func RefreshUnderLock(ctx context.Context, path string, tiers TierMap, readers []Reader, now time.Time) error {
	_, _, err := RefreshTable(ctx, path, tiers, readers, now, 0)
	return err
}
