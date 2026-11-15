package dispatch

import (
	"context"
	"fmt"
	"time"

	"github.com/netbja/agent-bus-monitor/loginexpiry"
)

// Login expiry (spec docs/superpowers/specs/2026-11-15-login-updates-design.md
// 2): an agent whose login has expired reads as a stuck agent; the person is
// told once a day per client, nothing else changes (no hold, no retry, nothing
// typed). Claude Code's expiry comes from its transcripts (quota.ClaudeLoginExpired),
// account-wide: every Claude agent shares the login.

// LoginConfig is the login-expiry pass. A nil source is not read.
type LoginConfig struct {
	// ClaudeExpired: when Claude Code's login expired, if it has.
	ClaudeExpired func(now time.Time) (time.Time, bool)
	Every         time.Duration  // how often the sources are read; 0: a minute
	Zone          *time.Location // the host's zone, for the day and the time shown; nil: time.Local
	// Credentials reads one Claude config directory's login expiry ("" the
	// default directory), loginexpiry.Read (r9 §3); nil: not read.
	Credentials func(configDir string, now time.Time) (time.Time, loginexpiry.Reason)
	// Checked: the client has a fixed login expiry (its profile's [login]
	// source); its agents are read.
	Checked func(client string) bool
	// Warn is dispatch.login_expiry_warn, read at each pass (0: no warning);
	// nil: DefaultLoginExpiryWarn.
	Warn func() time.Duration
}

func (c LoginConfig) on() bool { return c.ClaudeExpired != nil || c.Credentials != nil }

// loginExpiredMark is the {p}:quota once-mark of a client's expiry notice;
// its episode is the host's local date: one notice per client per day.
func loginExpiredMark(client string) string { return "login-expired:" + client }

// ClaudeLoginExpiredText is the person's notice (2.4). No agent is named: the
// transcripts are the account's, mapped to no agent.
func ClaudeLoginExpiredText(at time.Time) string {
	return fmt.Sprintf("Claude Code login expired (seen %s in a Claude session; the login is the account's, so every Claude agent is affected): run /login in one Claude pane; restart a pane that still fails after it.",
		at.Format("15:04"))
}

// loginPass reads the login sources at most once per Every and tells the
// person of an expiry, once per client per day (Redis TIME, in the host's
// zone).
func (l *loop) loginPass(ctx context.Context) error {
	c := l.cfg.Login
	if !c.on() {
		return nil
	}
	every := c.Every
	if every <= 0 {
		every = time.Minute
	}
	if !l.loginAt.IsZero() && time.Since(l.loginAt) < every {
		return nil
	}
	l.loginAt = time.Now()
	zone := c.Zone
	if zone == nil {
		zone = time.Local
	}
	now, err := l.b.ServerTime(ctx)
	if err != nil {
		return err
	}
	if c.Credentials != nil {
		if err := l.credentialsPass(ctx, now, zone); err != nil {
			return err
		}
	}
	if c.ClaudeExpired == nil {
		return nil
	}
	at, expired := c.ClaudeExpired(now)
	if !expired {
		return nil
	}
	day := now.In(zone).Format("2006-01-02")
	sent, err := l.b.FencedNotifyOnceForPerson(ctx, l.token, loginExpiredMark("claude-code"), day, dispatchFrom,
		ClaudeLoginExpiredText(at.In(zone)))
	if err != nil {
		return err
	}
	if sent {
		l.log(map[string]any{"action": "login-expired", "client": "claude-code", "seen": at.UnixMilli()})
	}
	return nil
}
