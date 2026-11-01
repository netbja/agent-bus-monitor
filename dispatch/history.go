package dispatch

import (
	"context"
	"errors"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/history"
)

// HistoryConfig is the archive chore (history spec): the streams appended to
// day files under Dir every Every, within the lease only. An empty Dir or a
// KeepDays of 0 turns it off: nothing written, nothing pruned.
type HistoryConfig struct {
	Dir      string
	KeepDays int
	MaxBytes int64         // the directory's cap; 0 = none
	Every    time.Duration // 1 min when 0
	Budget   time.Duration // a pass's deadline, 5 s when 0
}

func (c HistoryConfig) on() bool { return c.Dir != "" && c.KeepDays > 0 }

// historyState is what the chore keeps between passes.
type historyState struct {
	w        *history.Writer
	prunedAt time.Time
	lastErr  map[string]string // op -> the last error logged, so each is logged once
}

// busSource reads the project's streams for the writer.
type busSource struct{ b *bus.Bus }

func (s busSource) After(ctx context.Context, stream, after string, count int64) ([]history.Entry, error) {
	raw, err := s.b.StreamAfter(ctx, stream, after, count)
	out := make([]history.Entry, 0, len(raw))
	for _, r := range raw {
		out = append(out, history.Entry{Stream: stream, ID: r.ID, F: r.Fields})
	}
	return out, err
}

func (s busSource) Oldest(ctx context.Context, stream string) (string, bool, error) {
	return s.b.StreamOldest(ctx, stream)
}

// historyLoop runs beside the reconciliation, never inside it: a slow disk
// never delays a wake.
func (l *loop) historyLoop(ctx context.Context) {
	every := l.cfg.History.Every
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		l.historyPass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// historyPass archives once if this dispatcher holds the lease, and prunes
// once a day. A failure is logged once per kind and skipped; the next pass
// resumes from the writer's positions.
func (l *loop) historyPass(ctx context.Context) {
	c := l.cfg.History
	s := &l.history
	if s.w == nil {
		s.w, s.lastErr = &history.Writer{Dir: c.Dir}, map[string]string{}
	}
	budget := c.Budget
	if budget <= 0 {
		budget = 5 * time.Second
	}
	pctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	held, err := l.b.HoldsDispatch(pctx, l.token)
	if err != nil || !held {
		l.historyErr("history lease", err)
		return
	}
	now, err := l.b.ServerTime(pctx)
	if err != nil {
		l.historyErr("history time", err)
		return
	}
	_, err = s.w.Pass(pctx, busSource{l.b}, now.UnixMilli())
	if errors.Is(err, history.ErrLocked) {
		err = nil // another writer has it this minute
	}
	l.historyErr("history archive", err)
	if s.prunedAt.IsZero() || time.Since(s.prunedAt) >= 24*time.Hour {
		err := history.Prune(c.Dir, c.KeepDays, c.MaxBytes, now)
		if errors.Is(err, history.ErrLocked) {
			return // another writer holds it: prune on a later pass
		}
		l.historyErr("history prune", err)
		s.prunedAt = time.Now()
	}
}

// historyErr logs err once per op until the op succeeds again (err nil).
func (l *loop) historyErr(op string, err error) {
	s := &l.history
	if err == nil {
		delete(s.lastErr, op)
		return
	}
	if s.lastErr[op] == err.Error() {
		return
	}
	s.lastErr[op] = err.Error()
	l.logError("", op, err)
}
