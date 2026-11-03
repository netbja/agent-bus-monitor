package dispatch

import (
	"context"
	"time"
)

// BackupConfig is the daily backup chore (backup spec, "Schedule"): every
// hour, while this dispatcher holds its lease, it calls Pass, which judges
// on its own whether a backup is due (backup.schedule, coverage, the host
// lock) and makes it. The dispatcher only paces it, as the history chore.
type BackupConfig struct {
	Every time.Duration // one hour when 0
	Pass  func(ctx context.Context) error
}

func (c BackupConfig) on() bool { return c.Pass != nil }

// backupLoop runs beside the reconciliation: a backup's EXEC and its files
// never delay a wake.
func (l *loop) backupLoop(ctx context.Context) {
	every := l.cfg.Backup.Every
	if every <= 0 {
		every = time.Hour
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		l.backupPass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// backupPass calls the pass once if this dispatcher holds the lease; a
// failure is logged once until it changes (the pass records it itself).
func (l *loop) backupPass(ctx context.Context) {
	held, err := l.b.HoldsDispatch(ctx, l.token)
	if err != nil || !held {
		return
	}
	if err := l.cfg.Backup.Pass(ctx); err != nil {
		l.logOnce("backup", map[string]any{"action": "backup-failed", "error": err.Error()})
		return
	}
	l.logOnce("backup", map[string]any{"action": "backup-ok"})
}
