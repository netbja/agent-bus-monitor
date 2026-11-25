package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
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
	Read     func(ctx context.Context) []quota.Reading
	Every    time.Duration // collection interval (default 60 s)
	WarnPct  float64       // default 85
	DrainPct float64       // start nothing new above (quota settings §2.1); default WarnPct
	HoldPct  float64       // default 95
	MaxAge   time.Duration // a reading older than this is unknown for policy (default 30 min)
	// LongHold (v0.15 §5): a hold whose reset is further away than this asks
	// for a decision, once per episode (default 2 h; negative disables).
	LongHold time.Duration
	Now      func() time.Time
	// DateFormat is display.date_format, read at each message (quota
	// settings §2.3); nil: the relative default.
	DateFormat func() string
	// Thresholds reads the thresholds from the project's files (quota
	// settings §2.2), with the readings, every Every; nil: the fields above,
	// fixed. An error keeps the last values; a broken order (warn <= drain
	// <= hold) uses the defaults for all three.
	Thresholds func() (QuotaThresholds, error)
}

// QuotaThresholds are the project's quota thresholds, as read: the
// defaults, and a provider's own values (quota-per-provider, the person
// 2026-11-25), each 0 when unset.
type QuotaThresholds struct {
	Warn, Drain, Hold float64
	MaxAge            time.Duration
	// DrainSet: a file set the drain; unset, the drain follows the warning in
	// force, the provider's own included.
	DrainSet  bool
	Providers map[string]QuotaLevels
}

// QuotaLevels are one provider's thresholds; 0 is the default's.
type QuotaLevels struct{ Warn, Drain, Hold float64 }

// The sources For names: the defaults, a provider's own value, or the
// defaults because the order resolved was broken.
const (
	QuotaFromDefault  = ""
	QuotaFromProvider = "provider"
	QuotaFromOrder    = "order"
)

// ordered: warn <= drain <= hold, above 0.
func (l QuotaLevels) ordered() bool { return l.Warn > 0 && l.Warn <= l.Drain && l.Drain <= l.Hold }

// defaults are the thresholds in force for a provider with no value of its
// own, and whether a file set the drain: the registry's, drain unset, when
// their order is broken (what refreshThresholds keeps, so the dispatcher and
// agentbus quota resolve alike, reviewer-codex).
func (th QuotaThresholds) defaults() (d QuotaLevels, drainSet, ok bool) {
	d = QuotaLevels{th.Warn, th.Drain, th.Hold}
	if !d.ordered() {
		return QuotaLevels{defaultWarnPct, defaultDrainPct, defaultHoldPct}, false, false
	}
	return d, th.DrainSet, true
}

// For is provider p's thresholds in force and where they come from: its own
// values over the defaults; an unset drain follows the warning in force
// unless a file set it, then it is raised to that warning at least. A broken
// order uses the defaults, for that provider alone; broken defaults are the
// registry's, a provider's own values still over them.
func (th QuotaThresholds) For(p string) (QuotaLevels, string) {
	d, drainSet, ok := th.defaults()
	own := th.Providers[p]
	if own == (QuotaLevels{}) {
		if !ok {
			return d, QuotaFromOrder
		}
		return d, QuotaFromDefault
	}
	l := d
	if own.Warn > 0 {
		l.Warn = own.Warn
	}
	if own.Hold > 0 {
		l.Hold = own.Hold
	}
	switch {
	case own.Drain > 0:
		l.Drain = own.Drain
	case drainSet:
		l.Drain = max(d.Drain, l.Warn)
	default:
		l.Drain = l.Warn
	}
	if !l.ordered() {
		return d, QuotaFromOrder
	}
	return l, QuotaFromProvider
}

// The thresholds a broken order falls back to (the registry's defaults).
const (
	defaultWarnPct  = 85
	defaultDrainPct = 85
	defaultHoldPct  = 95
	defaultMaxAge   = 30 * time.Minute
)

// resetStamp is a window's reset in a stored message: the absolute date and
// time in the person's format, never today or tomorrow (master's decision,
// 2026-11-16: a directive is read later, maybe after midnight).
func (q QuotaConfig) resetStamp(w quota.Window, now time.Time) string {
	format := ""
	if q.DateFormat != nil {
		format = q.DateFormat()
	}
	return quota.ResetStamp(w.Reset(), now, format)
}

func (q QuotaConfig) filled() QuotaConfig {
	if q.Every <= 0 {
		q.Every = time.Minute
	}
	if q.WarnPct <= 0 {
		q.WarnPct = defaultWarnPct
	}
	if q.DrainPct <= 0 {
		q.DrainPct = q.WarnPct // unset: today's behaviour, one threshold
	}
	if q.HoldPct <= 0 {
		q.HoldPct = 95
	}
	if q.MaxAge <= 0 {
		q.MaxAge = defaultMaxAge
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
	th       QuotaThresholds // in force, refreshed with the readings
	thSet    bool
}

// refreshThresholds reads the thresholds with the readings (quota settings
// §2.2): an error keeps the last values (logged once), a broken order uses
// the defaults for all three (logged once per episode, never the last good
// set: the file the person sees is what is judged).
func (l *loop) refreshThresholds(q QuotaConfig) {
	if !l.quota.thSet {
		l.quota.thSet = true
		if q.Thresholds == nil {
			l.quota.th = QuotaThresholds{Warn: q.WarnPct, Drain: q.DrainPct, Hold: q.HoldPct, MaxAge: q.MaxAge}
		} else { // the files decide: a first read that fails keeps the registry defaults (reviewer-codex)
			l.quota.th = QuotaThresholds{Warn: defaultWarnPct, Drain: defaultDrainPct, Hold: defaultHoldPct, MaxAge: defaultMaxAge}
		}
	}
	if q.Thresholds == nil {
		return
	}
	th, err := q.Thresholds()
	if err != nil {
		l.logOnce("quota-thresholds", map[string]any{"action": "quota-thresholds-unread", "error": err.Error(), "kept": fmt.Sprint(l.quota.th)})
		return
	}
	l.forget("quota-thresholds")
	if th.MaxAge <= 0 {
		th.MaxAge = defaultMaxAge
	}
	if !(QuotaLevels{th.Warn, th.Drain, th.Hold}).ordered() {
		l.logOnce("quota-order", map[string]any{"action": "quota-order", "warn": th.Warn, "drain": th.Drain, "hold": th.Hold,
			"reason": "want warn <= drain <= hold: the defaults are used for all three"})
		th.Warn, th.Drain, th.Hold, th.DrainSet = defaultWarnPct, defaultDrainPct, defaultHoldPct, false // as For reads broken defaults
	} else {
		l.forget("quota-order") // a later broken order is a new episode, logged again
	}
	ps := make([]string, 0, len(th.Providers))
	for p := range th.Providers {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	for _, p := range ps { // a provider's broken order: its own episode, logged once
		key := "quota-order:" + p
		if _, from := th.For(p); from == QuotaFromOrder {
			own := th.Providers[p]
			l.logOnce(key, map[string]any{"action": "quota-order", "provider": p, "warn": own.Warn, "drain": own.Drain, "hold": own.Hold,
				"reason": "want warn <= drain <= hold once resolved: " + p + " uses the defaults"})
		} else {
			l.forget(key)
		}
	}
	l.quota.th = th
}

// forget drops a logOnce key, so its next entry is logged.
func (l *loop) forget(key string) {
	l.logMu.Lock()
	delete(l.last, key)
	l.logMu.Unlock()
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
		l.refreshThresholds(q)
	}
	th := l.quota.th
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
	var work map[string]bool // agents holding work, read once when a window warns (run-budget §4)
	for p := range used {
		r := l.quota.readings[p]
		usable := r.Usable(now, th.MaxAge)
		lv, _ := th.For(p) // the provider's own thresholds, else the defaults
		for _, w := range usable {
			if w.UsedPct < lv.Warn {
				continue
			}
			if work == nil {
				work = l.agentsWithWork(ctx, snaps)
			}
			if err := l.quotaWarnAgents(ctx, p, w, snaps, work, marks); err != nil {
				return err
			}
			if p == coord && w.UsedPct >= lv.Drain { // start nothing new above (§2.1)
				if err := l.quotaDrainRun(ctx, p, w); err != nil {
					return err
				}
			}
			field := "warn:" + p + ":" + w.Name
			reset := strconv.FormatInt(w.ResetsAt, 10)
			if marked, _ := strconv.ParseInt(marks[field], 10, 64); quota.SameReset(marked, w.ResetsAt) {
				continue // the same window, its reset read a few seconds off: warned already
			}
			msg := fmt.Sprintf("Quota %s %.0f%% (%s, resets %s): finish what is in flight, start nothing new.",
				p, w.UsedPct, w.Name, q.resetStamp(w, now))
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
		heldOpen := heldKnown && !windowClear(usable, heldWin.Name, lv.Hold)
		switch {
		case ok && tight.UsedPct >= lv.Hold:
			if quota.SameReset(recorded, tight.ResetsAt) {
				tight.ResetsAt = recorded // the same window: its episode keeps the stored reset
			} else {
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
		case recorded != 0 && r.ServedSince(now, th.MaxAge) && (!heldKnown || servedPast(r, heldWin)):
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
		if !l.quota.held[p] && warnClear(usable, lv.Drain) { // a drained run runs again under the drain (§2.1)
			// the provider clear again: a run drained for its quota runs (§4)
			if _, err := l.b.FencedRunQuotaRelease(ctx, l.token, p); err != nil {
				return err
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
	// A quota drain's announcement, from the run's state alone: a failed
	// write is retried even when no reading warns any more (reviewer-codex).
	if err := l.quotaDrainTell(ctx, ""); err != nil {
		return err
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
	// A run drained for this quota runs again (run-budget §4), whatever
	// ended the hold: a reading, a call served, the reset passed.
	if _, err := l.b.FencedRunQuotaRelease(ctx, l.token, p); err != nil {
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
		if quota.SameReset(w.ResetsAt, recorded) { // its reset may be read a few seconds off
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

// heldBy reports whether agent is held: its provider this pass, or its own
// login (hold-login:<agent>, login-updates r9 §3.6).
func (l *loop) heldBy(agent string, snap *bus.AgentSnapshot) bool {
	return l.heldReason(agent, snap) != ""
}

// heldReason is bus.HeldReason's answer from this pass's view: quota, login or "".
func (l *loop) heldReason(agent string, snap *bus.AgentSnapshot) string {
	if snap != nil && snap.Provider != "" && l.quota.held[snap.Provider] {
		return bus.HeldQuota
	}
	if l.loginHeld[agent] {
		return bus.HeldLogin
	}
	return ""
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

// warnClear: a usable reading with every window under the warn threshold.
func warnClear(usable []quota.Window, warnPct float64) bool {
	if len(usable) == 0 {
		return false
	}
	for _, w := range usable {
		if w.UsedPct >= warnPct {
			return false
		}
	}
	return true
}

// agentsWithWork are the agents holding work (run-budget §4): an accepted
// task on the board, or declared working. A board that cannot be read leaves
// only the working ones.
func (l *loop) agentsWithWork(ctx context.Context, snaps map[string]bus.AgentSnapshot) map[string]bool {
	out := map[string]bool{}
	for name, s := range snaps {
		if s.State == "working" {
			out[name] = true
		}
	}
	board, err := l.b.Board(ctx)
	if err != nil {
		l.logError("", "quota-warn board", err)
		return out
	}
	for _, e := range board {
		if e.State == "accepted" && bus.ValidName(e.Owner) { // an accepted task; a plan claim is not work held
			out[e.Owner] = true
		}
	}
	return out
}

// quotaWarnAgents warns each agent on provider p holding work, besides
// master, once per window (run-budget §4): the warn's mark extended with the
// agent.
func (l *loop) quotaWarnAgents(ctx context.Context, p string, w quota.Window, snaps map[string]bus.AgentSnapshot, work map[string]bool, marks map[string]string) error {
	q := l.cfg.Quota.filled()
	names := make([]string, 0, len(snaps))
	for name := range snaps {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if snaps[name].Provider != p || name == l.cfg.EscalateTo || !work[name] {
			continue
		}
		field := "warn:" + p + ":" + w.Name + ":" + name
		if marked, _ := strconv.ParseInt(marks[field], 10, 64); quota.SameReset(marked, w.ResetsAt) {
			continue
		}
		msg := fmt.Sprintf("Quota %s %.0f%% (%s, resets %s): finish your step, commit work in progress on your branch, report what is left, then status done. Start nothing new.",
			p, w.UsedPct, w.Name, q.resetStamp(w, q.Now()))
		if _, err := l.b.FencedCmd(ctx, l.token, dispatchFrom, name, bus.CmdDirective, msg); err != nil {
			return err
		}
		if err := l.b.FencedQuotaSet(ctx, l.token, field, strconv.FormatInt(w.ResetsAt, 10)); err != nil {
			return err
		}
		l.log(map[string]any{"action": "quota-warn-agent", "provider": p, "window": w.Name, "agent": name})
	}
	return nil
}

// quotaDrainRun drains the run for master's provider p (run-budget §4),
// told once per drain to master and to the person, as a limit's drain is.
func (l *loop) quotaDrainRun(ctx context.Context, p string, w quota.Window) error {
	if _, _, err := l.b.FencedRunDrain(ctx, l.token, bus.RunWhyQuota(p)); err != nil {
		return err
	}
	q := l.cfg.Quota.filled()
	return l.quotaDrainTell(ctx, fmt.Sprintf("%.0f%%, %s, resets %s", w.UsedPct, w.Name, q.resetStamp(w, q.Now())))
}

// quotaDrainTell tells master and the person that the run drains for a
// quota, on each pass while it does, by the drain's once-marks: an
// announcement whose write failed is retried from the run's own state,
// whatever the provider reads now (reviewer-codex), and none is sent twice.
// detail is the reading's, "" when the pass has none.
func (l *loop) quotaDrainTell(ctx context.Context, detail string) error {
	run, err := l.b.Run(ctx)
	if err != nil || run == nil || run.State != bus.RunDraining {
		return nil
	}
	p, ok := strings.CutPrefix(run.Why, "quota:")
	if !ok {
		return nil
	}
	if detail != "" {
		detail = " (" + detail + ")"
	}
	ep := fmt.Sprintf("%d:%d", run.StartedAt, run.Drains)
	directive := fmt.Sprintf("Run draining for %s's quota%s: dispatch no new task; finish and deliver what is in flight; the run goes on at the quota's release.", p, detail)
	if _, err := l.b.FencedCmdOnce(ctx, l.token, "run-drain", ep, dispatchFrom, l.cfg.EscalateTo, bus.CmdDirective, directive, false); err != nil {
		return err
	}
	notice := fmt.Sprintf("%s: the run drains for %s's quota%s: no new task but yours; it goes on at the quota's release (agentbus run)", l.b.Project(), p, detail)
	_, err = l.b.FencedNotifyOnceForPerson(ctx, l.token, "run-drain-person", ep, dispatchFrom, notice)
	return err
}
