package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

// The prompt notification (prompt-notify, the person, 2026-10-27 "Bureau"): an
// agent whose pane shows its client's own prompt (a permission or limit
// menu, read by the observe pass as the host's state blocked: busmon's `prompt`
// flag) earns the person one desktop notification, once per episode, after
// a grace. Only the person can answer it, in that pane: nothing is typed,
// no state changes and no bus entry is written; only the episode's mark.

// PromptConfig turns the notification on.
type PromptConfig struct {
	// Grace is how long a prompt must have been seen before it rings (0 = off):
	// a permission answered within it never rings.
	Grace time.Duration
	// Notify shows a desktop notification (nil = none: a logged note).
	Notify func(ctx context.Context, title, body string) error
	// Skip reports that notifications' own desktop target rings for the
	// prompt episode that began at since (notifications spec, the person's
	// answer 6): the episode is marked, the desktop is not rung a second time.
	// An episode older than that target's baseline stays the ring's: the
	// notifier baselined it and will never send it (reviewer-codex).
	Skip func(ctx context.Context, since int64) bool
	// FocusGrace is how long a wake waits on a focused pane before the
	// person gets a desktop notice (focus-notice; 0 = off).
	FocusGrace time.Duration
	// StuckAfter is dispatch.stuck_after (agent-screen-blocked §3): a
	// settled screen the client's signature does not recognise, unchanged
	// this long, rings once per episode with its own words (0 = off).
	StuckAfter time.Duration
}

// StuckAfter is dispatch.stuck_after: AGENT_BUS_STUCK_AFTER (whole seconds,
// 0 = off), else config.toml, else 3 minutes (the person's Q3).
func StuckAfter(getenv func(string) string) time.Duration {
	return config.Machine(getenv).Duration("dispatch.stuck_after")
}

// stuckMark is agent's {p}:quota field for a stuck screen's episode,
// pid:ScreenSince.
func stuckMark(agent string) string { return "stuck:" + agent }

// PromptGraceDefault: two observe passes past the first reading. A prompt is
// first seen up to ObserveEvery after it appears, so it rings 30-45 s after
// it opened; a permission answered while the person is at the screen does
// not ring, one left alone rings well before the turn is lost.
const PromptGraceDefault = 30 * time.Second

// PromptGrace is dispatch.prompt_grace: AGENT_BUS_PROMPT_GRACE (whole seconds
// 0-3600, 0 = off), else config.toml, else PromptGraceDefault.
func PromptGrace(getenv func(string) string) time.Duration {
	return config.Machine(getenv).Duration("dispatch.prompt_grace")
}

// promptMark is agent's {p}:quota field; its value is the episode, pid:since.
func promptMark(agent string) string { return "prompt:" + agent }

// promptNotices judges the last observation once; a lost lease is
// ErrNotHolder. It runs after the pause check, so a paused project rings
// nobody. The mark is set (fenced) before the notification: a dispatcher
// that lost its lease rings nothing, and an episode rings at most once.
func (l *loop) promptNotices(ctx context.Context, snaps map[string]bus.AgentSnapshot) error {
	if !l.obsFresh {
		return nil
	}
	l.obsFresh = false
	grace, stuck := l.cfg.Prompt.Grace, l.cfg.Prompt.StuckAfter
	if grace <= 0 && stuck <= 0 {
		return nil
	}
	marks, err := l.b.QuotaMarks(ctx)
	if err != nil {
		return err
	}
	// An agent gone from the observation (forgotten, its identity lost) ends
	// its episode too: its mark would otherwise stay forever.
	var gone []string
	for k := range marks {
		a, ok := strings.CutPrefix(k, "prompt:")
		if !ok {
			a, ok = strings.CutPrefix(k, "stuck:")
		}
		if ok {
			if _, seen := l.obs[a]; !seen {
				gone = append(gone, k)
			}
		}
	}
	if len(gone) > 0 {
		sort.Strings(gone)
		if err := l.b.FencedQuotaDel(ctx, l.token, gone...); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(l.obs))
	for a := range l.obs {
		names = append(names, a)
	}
	sort.Strings(names)
	if stuck > 0 {
		if err := l.stuckNotices(ctx, snaps, marks, names, stuck); err != nil {
			return err
		}
	}
	if grace <= 0 {
		return nil
	}
	var asking map[string]bool
	for _, a := range names {
		o, s := l.obs[a], snaps[a]
		mark := promptMark(a)
		if o.State != "blocked" || o.PID == 0 || o.PID != s.PID {
			if _, ok := marks[mark]; ok {
				if err := l.b.FencedQuotaDel(ctx, l.token, mark); err != nil {
					return err
				}
			}
			continue // no prompt, or another process's: the episode is over
		}
		episode := fmt.Sprintf("%d:%d", o.PID, o.Since)
		if marks[mark] == episode || o.At-o.Since < grace.Milliseconds() || s.State == "blocked" || s.State == "waiting" {
			continue
		}
		if asking == nil {
			asks, err := l.b.Asks(ctx)
			if err != nil {
				return err
			}
			asking = map[string]bool{}
			for _, k := range asks {
				if k.Open() > 0 {
					asking[k.From] = true
				}
			}
		}
		if asking[a] {
			continue // its own open ask is what the person answers
		}
		if swapping, err := l.b.Swapping(ctx, a); err != nil || swapping {
			if err != nil {
				l.logError(a, "prompt notify", err)
			}
			continue
		}
		if err := l.b.FencedQuotaSet(ctx, l.token, mark, episode); err != nil {
			if errors.Is(err, bus.ErrNotHolder) {
				return err
			}
			l.logError(a, "prompt notify", err)
			continue
		}
		l.log(map[string]any{"agent": a, "action": "prompt-notify", "pane": s.Pane, "since": o.Since})
		if skip := l.cfg.Prompt.Skip; skip != nil && skip(ctx, o.Since) {
			l.log(map[string]any{"agent": a, "action": "prompt-notify", "note": "notifications' desktop target rings it"})
			continue
		}
		n := l.cfg.Prompt.Notify
		if n == nil {
			l.log(map[string]any{"agent": a, "action": "prompt-notify", "note": "no notify-send on PATH: nothing shown"})
			continue
		}
		body := fmt.Sprintf("%s: %s waits on a prompt in its pane (%s)", l.b.Project(), a, s.Pane)
		if err := n(ctx, "agentbus "+l.b.Project(), body); err != nil {
			l.logError(a, "desktop notify", err)
		}
	}
	return nil
}

// stuckNotices rings once per episode for a stuck screen (agent-screen-blocked
// §3, the person's Q6): the observation reads the state unknown (settled, its
// client's signature reads neither ready, nor busy, nor a prompt), for the
// agent's own process, with the same screen hash for stuck_after. Its words
// never say prompt. A recognised state, another process or no hash ends the
// episode; a new hash is a new one. prompt-notify's suppressions hold.
func (l *loop) stuckNotices(ctx context.Context, snaps map[string]bus.AgentSnapshot, marks map[string]string, names []string, after time.Duration) error {
	var asking map[string]bool
	for _, a := range names {
		o, s := l.obs[a], snaps[a]
		mark := stuckMark(a)
		if o.State != "unknown" || o.ScreenHash == "" || o.ScreenSince == 0 || o.PID == 0 || o.PID != s.PID {
			if _, ok := marks[mark]; ok {
				if err := l.b.FencedQuotaDel(ctx, l.token, mark); err != nil {
					return err
				}
			}
			continue
		}
		episode := fmt.Sprintf("%d:%d", o.PID, o.ScreenSince)
		stood := time.Duration(o.At-o.ScreenSince) * time.Millisecond
		if marks[mark] == episode || stood < after || s.State == "blocked" || s.State == "waiting" {
			continue
		}
		if asking == nil {
			asks, err := l.b.Asks(ctx)
			if err != nil {
				return err
			}
			asking = map[string]bool{}
			for _, k := range asks {
				if k.Open() > 0 {
					asking[k.From] = true
				}
			}
		}
		if asking[a] {
			continue
		}
		if swapping, err := l.b.Swapping(ctx, a); err != nil || swapping {
			if err != nil {
				l.logError(a, "stuck notify", err)
			}
			continue
		}
		if err := l.b.FencedQuotaSet(ctx, l.token, mark, episode); err != nil {
			if errors.Is(err, bus.ErrNotHolder) {
				return err
			}
			l.logError(a, "stuck notify", err)
			continue
		}
		l.log(map[string]any{"agent": a, "action": "stuck-notify", "pane": s.Pane, "since": o.ScreenSince})
		n := l.cfg.Prompt.Notify
		if n == nil {
			l.log(map[string]any{"agent": a, "action": "stuck-notify", "note": "no notify-send on PATH: nothing shown"})
			continue
		}
		body := fmt.Sprintf("%s: %s's screen has not changed for %s and is not a prompt agentbus knows (%s)", l.b.Project(), a, stuckFor(stood), s.Pane)
		if err := n(ctx, "agentbus "+l.b.Project(), body); err != nil {
			l.logError(a, "desktop notify", err)
		}
	}
	return nil
}

// stuckFor is how long a screen stood, in whole minutes ("3m"), or seconds
// under a minute.
func stuckFor(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	return fmt.Sprintf("%dm", int(d/time.Minute))
}
