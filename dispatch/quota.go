package dispatch

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/quota"
)

// Quota policy (spec 2026-10-15-quota-awareness-design.md §7): readings are
// collected every QuotaEvery, published to {p}:budget, and turned into one
// warn per window and reset, a per-provider hold (no prompt, no escalation for
// its agents) and, for the coordinator's own provider, a project pause. Every
// write is fenced. Unknown or aged readings never warn, hold or release.

// QuotaConfig enables the policy; a nil Read disables it.
type QuotaConfig struct {
	Read    func(ctx context.Context) []quota.Reading
	Every   time.Duration // collection interval (default 60 s)
	WarnPct float64       // default 85
	HoldPct float64       // default 95
	MaxAge  time.Duration // a reading older than this is unknown for policy (default 30 min)
	// LongHold (v0.15 §5): a hold whose reset is further away than this asks
	// for a decision, once per episode (default 2 h; negative disables).
	LongHold time.Duration
	Now      func() time.Time
}

func (q QuotaConfig) filled() QuotaConfig {
	if q.Every <= 0 {
		q.Every = time.Minute
	}
	if q.WarnPct <= 0 {
		q.WarnPct = 85
	}
	if q.HoldPct <= 0 {
		q.HoldPct = 95
	}
	if q.MaxAge <= 0 {
		q.MaxAge = 30 * time.Minute
	}
	if q.LongHold == 0 {
		q.LongHold = 2 * time.Hour
	}
	if q.Now == nil {
		q.Now = time.Now
	}
	return q
}

// resumeMargin: resume a little after a reset, never into a window that has
// not reopened yet.
const resumeMargin = 2 * time.Minute

type quotaState struct {
	readings map[string]quota.Reading
	at       time.Time
	held     map[string]bool // provider → held this pass
}

// quotaPass collects (when due), publishes, warns, holds and pauses. It runs
// at the top of every reconcile, before the pause check, so a quota pause can
// also end early.
func (l *loop) quotaPass(ctx context.Context, snaps map[string]bus.AgentSnapshot) error {
	q := l.cfg.Quota
	if q.Read == nil {
		return nil
	}
	now := q.Now()
	if l.quota.readings == nil || now.Sub(l.quota.at) >= q.Every {
		l.quota.readings = map[string]quota.Reading{}
		for _, r := range q.Read(ctx) {
			l.quota.readings[r.Provider] = r
		}
		l.quota.at = now
	}
	used := map[string]bool{}
	for _, s := range snaps {
		if s.Provider != "" {
			used[s.Provider] = true
		}
	}
	coord := snaps[l.cfg.EscalateTo].Provider
	// v0.15 §8: the provider of the dispatcher's own quota pause is
	// reconciled even when no registered agent uses it any more (master was
	// moved to another provider), so that pause is never orphaned.
	paused := ""
	if pr, err := l.b.Pause(ctx); err == nil {
		paused = bus.QuotaPauseProvider(pr)
	}
	if paused != "" {
		used[paused] = true
	}
	// v0.15.1: a role swapped away keeps its own provider watched, so its
	// release (and the swap-back notice) is seen even when nobody else uses it.
	if fbs, err := l.b.Fallbacks(ctx); err == nil {
		for _, f := range fbs {
			if bus.ValidName(f.OwnProvider) {
				used[f.OwnProvider] = true
			}
		}
	}
	resetOf := map[string]int64{}

	budgets := map[string]bus.BudgetSnapshot{}
	for p := range used {
		if r, ok := l.quota.readings[p]; ok {
			budgets[p] = budgetOf(r, now)
		}
	}
	if err := l.b.FencedPutBudgets(ctx, l.token, budgets); err != nil {
		return err
	}
	// The account windows' history ({p}:quota:hist) only records: a failure
	// never gates the policy below. Only a lost lease ends the pass.
	if err := l.quotaHist(ctx, budgets); errors.Is(err, bus.ErrNotHolder) {
		return err
	} else if err != nil {
		l.logError("", "quota-hist", err)
	}
	marks, err := l.b.QuotaMarks(ctx)
	if err != nil {
		return err
	}
	l.quota.held = map[string]bool{}
	for p := range used {
		r := l.quota.readings[p]
		usable := r.Usable(now, q.MaxAge)
		for _, w := range usable {
			if w.UsedPct < q.WarnPct {
				continue
			}
			field := "warn:" + p + ":" + w.Name
			reset := strconv.FormatInt(w.ResetsAt, 10)
			if marks[field] == reset {
				continue
			}
			msg := fmt.Sprintf("Quota %s %.0f%% (%s, resets %s): finish what is in flight, start nothing new.",
				p, w.UsedPct, w.Name, w.Reset().Local().Format("15:04"))
			if _, err := l.b.FencedCmd(ctx, l.token, dispatchFrom, l.cfg.EscalateTo, bus.CmdDirective, msg); err != nil {
				return err
			}
			if err := l.b.FencedQuotaSet(ctx, l.token, field, reset); err != nil {
				return err
			}
			l.log(map[string]any{"action": "quota-warn", "provider": p, "window": w.Name, "used_pct": w.UsedPct})
		}

		holdField := "hold:" + p
		recorded, _ := strconv.ParseInt(marks[holdField], 10, 64)
		tight, ok := quota.Tightest(usable)
		// v0.15.3: the window that set the hold, read at any age. An aged
		// reading of it does not end the hold because another window is low:
		// within a window, use only grows until its reset.
		heldWin, heldKnown := windowAt(r, recorded)
		heldOpen := heldKnown && !windowClear(usable, heldWin.Name, q.HoldPct)
		switch {
		case ok && tight.UsedPct >= q.HoldPct:
			if recorded != tight.ResetsAt {
				if recorded == 0 { // a new episode: the last one's resume marks go
					if err := l.clearEpisodeMarks(ctx, p, tight.ResetsAt, marks); err != nil {
						return err
					}
				}
				if err := l.b.FencedQuotaSet(ctx, l.token, holdField, strconv.FormatInt(tight.ResetsAt, 10)); err != nil {
					return err
				}
				l.log(map[string]any{"action": "quota-hold", "provider": p, "window": tight.Name, "used_pct": tight.UsedPct})
			}
			recorded = tight.ResetsAt
			l.quota.held[p] = true
			if err := l.longHold(ctx, p, tight, now, coord, snaps); err != nil {
				return err
			}
		case ok && !heldOpen: // a usable reading under the threshold releases, and ends a quota pause early
			if recorded != 0 {
				if err := l.release(ctx, p, recorded, snaps, p == coord, ""); err != nil {
					return err
				}
			} else if p == coord {
				if _, err := l.b.FencedQuotaUnpause(ctx, l.token, p, dispatchFrom); err != nil {
					return err
				}
			}
			recorded = 0
		case recorded != 0 && r.ServedSince(now, q.MaxAge) && (!heldKnown || servedPast(r, heldWin)):
			// No window to read, but the provider served a call after the
			// limit hit (a quota reset granted early): the hold is over. An
			// aged reading of the held window counts only at the limit itself
			// and only when the answer came after it.
			if err := l.release(ctx, p, recorded, snaps, p == coord, "served after the limit hit"); err != nil {
				return err
			}
			recorded = 0
		default: // unknown: a recorded hold stands until its reset
			if recorded != 0 && time.Unix(recorded, 0).Before(now) {
				if err := l.release(ctx, p, recorded, snaps, false, "reset passed"); err != nil {
					return err
				}
				recorded = 0
			} else if recorded != 0 {
				l.quota.held[p] = true
			}
		}
		resetOf[p] = recorded
		// A hold that expired at its reset leaves the quota pause to
		// FencedResume, which ends it at reset + resumeMargin.
		if p == coord && l.quota.held[p] {
			until := time.Unix(recorded, 0).Add(resumeMargin).UnixMilli()
			if wrote, err := l.b.FencedQuotaPause(ctx, l.token, p, l.cfg.EscalateTo, until); err != nil {
				return err
			} else if wrote {
				l.logOnce("quota-pause:"+p, map[string]any{"action": "quota-pause", "provider": p, "until": time.UnixMilli(until).Format(time.RFC3339)})
			}
		}
	}
	return l.coordinatorMoved(ctx, paused, coord, resetOf)
}

// coordinatorMoved (v0.15 §8): the project is paused for provider paused's
// quota, but the coordinator now runs on coord. If coord is free, the pause
// ends with its resume directive; if coord is held too, the pause is
// replaced by one for coord, without a resume. A person's pause is never
// touched (QuotaPauseProvider is "" for it).
func (l *loop) coordinatorMoved(ctx context.Context, paused, coord string, resetOf map[string]int64) error {
	if paused == "" || coord == "" || paused == coord {
		return nil
	}
	if l.quota.held[coord] {
		until := time.Unix(resetOf[coord], 0).Add(resumeMargin).UnixMilli()
		ok, err := l.b.FencedQuotaRepause(ctx, l.token, paused, coord, until)
		if err != nil {
			return err
		}
		if ok {
			l.log(map[string]any{"action": "quota-repause", "from": paused, "provider": coord,
				"until": time.UnixMilli(until).Format(time.RFC3339)})
		}
		return nil
	}
	why := fmt.Sprintf("%s now runs on %s, which is not at its limit.", l.cfg.EscalateTo, coord)
	ok, err := l.b.FencedQuotaUnpauseWhy(ctx, l.token, paused, dispatchFrom, why)
	if err != nil {
		return err
	}
	if ok {
		l.log(map[string]any{"action": "quota-unpause", "provider": paused, "reason": "coordinator moved to " + coord})
	}
	return nil
}

// release ends provider p's hold of episode recorded. The hold mark is what
// sends a pass down a release branch, so it goes last: the resumes first
// (each marked), then the coordinator's quota pause, then the mark, only once
// every resume is written. A crash or a failed write anywhere leaves the mark,
// and the next pass redoes what is missing and nothing else.
func (l *loop) release(ctx context.Context, p string, recorded int64, snaps map[string]bus.AgentSnapshot, unpause bool, reason string) error {
	complete, err := l.resumeAfterHold(ctx, p, recorded, snaps)
	if err != nil {
		return err
	}
	if ok, err := l.backNotices(ctx, p, recorded); err != nil {
		return err
	} else if !ok {
		complete = false
	}
	if unpause {
		if _, err := l.b.FencedQuotaUnpause(ctx, l.token, p, dispatchFrom); err != nil {
			return err
		}
	}
	if !complete {
		l.logOnce("quota-release:"+p, map[string]any{"action": "quota-release-pending", "provider": p, "reason": "a resume was not written; retried next pass"})
		return nil
	}
	if err := l.b.FencedQuotaDel(ctx, l.token, "hold:"+p); err != nil {
		return err
	}
	entry := map[string]any{"action": "quota-release", "provider": p}
	if reason != "" {
		entry["reason"] = reason
	}
	l.log(entry)
	return nil
}

// windowAt is the reading's window whose reset is the recorded hold's, at
// any age; none when the hold's window is gone (renewed) or never recorded.
func windowAt(r quota.Reading, recorded int64) (quota.Window, bool) {
	if recorded == 0 || r.Unknown || r.Unlimited {
		return quota.Window{}, false
	}
	for _, w := range r.Windows {
		if w.ResetsAt == recorded {
			return w, true
		}
	}
	return quota.Window{}, false
}

// windowClear: a usable reading of the named window is under the threshold.
func windowClear(usable []quota.Window, name string, hold float64) bool {
	for _, w := range usable {
		if w.Name == name && w.UsedPct < hold {
			return true
		}
	}
	return false
}

// servedPast: the held window was read at its limit and the provider
// answered after that reading.
func servedPast(r quota.Reading, w quota.Window) bool {
	return w.UsedPct >= 100 && r.AvailableAt > w.Observed(r).UnixMilli()
}

// heldBy reports whether agent's provider is held this pass.
func (l *loop) heldBy(snap *bus.AgentSnapshot) bool {
	return snap != nil && snap.Provider != "" && l.quota.held[snap.Provider]
}

// budgetOf is a reading as {p}:budget carries it; the legacy percent fields
// only when a 5h / 7d window exists, never a 0 placeholder.
func budgetOf(r quota.Reading, now time.Time) bus.BudgetSnapshot {
	s := bus.BudgetSnapshot{Provider: r.Provider, Source: r.Source, TS: now.UnixMilli(),
		ObservedAt: r.ObservedAt, CollectedAt: r.CollectedAt, Unknown: r.Unknown, Unlimited: r.Unlimited}
	for _, w := range r.Windows {
		s.Windows = append(s.Windows, bus.BudgetWindow{Name: w.Name, UsedPct: w.UsedPct, ResetsAt: w.ResetsAt, Minutes: w.Minutes,
			ObservedAt: w.Observed(r).UnixMilli()})
		reset := w.Reset().UTC().Format(time.RFC3339)
		switch w.Name {
		case "5h":
			s.SessionPct, s.SessionReset = w.UsedPct, reset
		case "7d", "weekly":
			s.WeeklyPct, s.WeeklyReset = w.UsedPct, reset
		}
	}
	return s
}

// logOnce logs v once per key until the key's value changes.
func (l *loop) logOnce(key string, v map[string]any) {
	l.logMu.Lock()
	val := fmt.Sprint(v)
	same := l.last[key] == val
	l.last[key] = val
	l.logMu.Unlock()
	if !same {
		l.log(v)
	}
}
