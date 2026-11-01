package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

const healthPassBudget = 30 * time.Second

// HealthCheck is one deterministic check. Run must honor ctx and return only
// bounded, non-secret detail; an error is stored as unknown without its text.
type HealthCheck struct {
	Definition health.Definition
	Run        func(context.Context) (health.Outcome, error)
}

type HealthConfig struct {
	Checks        []HealthCheck
	PassBudget    time.Duration
	Tick          time.Duration
	DailyWake     bool
	UsageRefresh  func(context.Context, string) error
	ModelsRefresh func(context.Context) error
	RedisRefresh  func(context.Context) error // redis-watch: the daily latest-release check
	// StallNotify runs right after each stored team-stall record, with the
	// lease token: the stall guard's fenced notices for the person (a check's
	// Run gets no token and never writes).
	StallNotify func(ctx context.Context, token string, rec health.Record) error
	// StallWake runs right after each stored clear-stall or quiet-agent
	// record, with the lease token and the check's id: the stall guard's
	// fenced waking directives (chain-reliability: a warning alone woke nobody).
	StallWake func(ctx context.Context, token, check string) error
}

func (c HealthConfig) filled() HealthConfig {
	if c.PassBudget <= 0 {
		c.PassBudget = healthPassBudget
	}
	if c.Tick <= 0 {
		c.Tick = 5 * time.Second
	}
	return c
}

// healthLoop is deliberately separate from reconcile: slow health reads can
// never delay command delivery, and the single loop guarantees one instance of
// each check at a time. Every result is written through the dispatch fence.
func (l *loop) healthLoop(ctx context.Context) error {
	cfg := l.cfg.Health.filled()
	next := make(map[string]time.Time, len(cfg.Checks))
	nextUsage, nextModels, nextRedis := time.Time{}, time.Time{}, time.Time{}
	lastDaily := time.Time{}
	ticker := time.NewTicker(cfg.Tick)
	defer ticker.Stop()
	for {
		passCtx, cancel := context.WithTimeout(ctx, cfg.PassBudget)
		passEnd := time.Now().Add(cfg.PassBudget)
		if cfg.UsageRefresh != nil && !time.Now().Before(nextUsage) && time.Until(passEnd) > 0 {
			cctx, ccancel := context.WithTimeout(passCtx, 10*time.Second)
			err := cfg.UsageRefresh(cctx, l.token)
			ccancel()
			nextUsage = time.Now().Add(15 * time.Minute)
			if errors.Is(err, bus.ErrNotHolder) {
				cancel()
				return ErrLeaseLost
			}
			if errors.Is(err, bus.ErrProjectNotInstalled) {
				cancel()
				return nil
			}
			if err != nil && ctx.Err() == nil {
				l.logError("", "health usage refresh", err)
			}
		}
		if cfg.ModelsRefresh != nil && !time.Now().Before(nextModels) && time.Until(passEnd) > 0 {
			cctx, ccancel := context.WithTimeout(passCtx, 30*time.Second)
			err := cfg.ModelsRefresh(cctx)
			ccancel()
			nextModels = time.Now().Add(24 * time.Hour)
			if err != nil && ctx.Err() == nil {
				l.logError("", "health model refresh", err)
			}
		}
		if cfg.RedisRefresh != nil && !time.Now().Before(nextRedis) && time.Until(passEnd) > 0 {
			cctx, ccancel := context.WithTimeout(passCtx, 10*time.Second)
			err := cfg.RedisRefresh(cctx)
			ccancel()
			nextRedis = time.Now().Add(24 * time.Hour)
			if err != nil && ctx.Err() == nil {
				l.logError("", "health redis release check", err)
			}
		}
		for _, check := range cfg.Checks {
			if check.Run == nil || check.Definition.ID == "" || time.Now().Before(next[check.Definition.ID]) {
				continue
			}
			if time.Until(passEnd) <= 0 {
				break
			}
			deadline := check.Definition.Deadline
			if remaining := time.Until(passEnd); deadline <= 0 || deadline > remaining {
				deadline = remaining
			}
			checkCtx, checkCancel := context.WithTimeout(passCtx, deadline)
			out, err := check.Run(checkCtx)
			status, detail := out.Status, out.Detail
			checkCancel()
			if ctx.Err() != nil {
				cancel()
				return ctx.Err()
			}
			if errors.Is(err, bus.ErrNotHolder) {
				cancel()
				return ErrLeaseLost
			}
			if err != nil || status != health.OK && status != health.Warn && status != health.Fail && status != health.Unknown {
				status, detail = health.Unknown, "check could not be completed"
			}
			if !health.SafeText(detail) {
				status, detail = health.Unknown, "unsafe check detail suppressed"
			}
			// The reason only for the evaluator's own warn or fail, and only a
			// code the check declares: an error or an unsafe detail, read as
			// unknown above, drops it.
			reason := ""
			if _, ok := check.Definition.Reasons[out.Reason]; ok && status == out.Status && detail == out.Detail && (status == health.Warn || status == health.Fail) {
				reason = out.Reason
			}
			rec := health.Record{ID: check.Definition.ID, Status: status, Detail: detail, Reason: reason}
			rec.Fingerprint = healthFingerprint(status, detail)
			wctx, wcancel := context.WithTimeout(passCtx, 2*time.Second)
			err = l.b.FencedHealthPut(wctx, l.token, l.cfg.EscalateTo, rec)
			wcancel()
			if errors.Is(err, bus.ErrNotHolder) {
				cancel()
				return ErrLeaseLost
			}
			if errors.Is(err, bus.ErrProjectNotInstalled) {
				cancel()
				return nil
			}
			if err != nil {
				l.logError("", "health "+check.Definition.ID, err)
			} else {
				next[check.Definition.ID] = time.Now().Add(check.Definition.Interval)
				if cfg.StallNotify != nil && check.Definition.ID == "team-stall" {
					hctx, hcancel := context.WithTimeout(passCtx, 2*time.Second)
					err := cfg.StallNotify(hctx, l.token, rec)
					hcancel()
					if errors.Is(err, bus.ErrNotHolder) {
						cancel()
						return ErrLeaseLost
					}
					if err != nil {
						l.logError("", "health team-stall notice", err)
					}
				}
				if id := check.Definition.ID; cfg.StallWake != nil && (id == "clear-stall" || id == "quiet-agent") {
					hctx, hcancel := context.WithTimeout(passCtx, 2*time.Second)
					err := cfg.StallWake(hctx, l.token, id)
					hcancel()
					if errors.Is(err, bus.ErrNotHolder) {
						cancel()
						return ErrLeaseLost
					}
					if err != nil {
						l.logError("", "health "+id+" wake", err)
					}
				}
			}
		}
		if cfg.DailyWake && time.Since(lastDaily) >= 24*time.Hour && passCtx.Err() == nil {
			wctx, wcancel := context.WithTimeout(passCtx, 2*time.Second)
			_, err := l.b.FencedHealthDailyWake(wctx, l.token, l.cfg.EscalateTo)
			wcancel()
			switch {
			case errors.Is(err, bus.ErrNotHolder):
				cancel()
				return ErrLeaseLost
			case errors.Is(err, bus.ErrProjectNotInstalled):
				cancel()
				return nil
			case err != nil:
				l.logError("", "health daily wake", err)
			default:
				lastDaily = time.Now()
			}
		}
		cancel()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func healthFingerprint(status health.Status, detail string) string {
	sum := sha256.Sum256([]byte(string(status) + "\x00" + detail))
	return hex.EncodeToString(sum[:12])
}

func healthTask(id string, run func(context.Context) (health.Status, string, error)) (HealthCheck, error) {
	d, ok := health.ByID(id)
	if !ok {
		return HealthCheck{}, fmt.Errorf("unknown health check %q", id)
	}
	return HealthCheck{Definition: d, Run: func(ctx context.Context) (health.Outcome, error) {
		status, detail, err := run(ctx)
		return health.Outcome{Status: status, Detail: detail}, err
	}}, nil
}
