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
)

// The prompt notification (prompt-notify, the person, 2026-10-27 "Bureau"): an
// agent whose pane shows its client's own prompt (a permission or limit
// menu, read by the observe pass as herdr state blocked: busmon's `prompt`
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
}

// PromptGraceDefault: two observe passes past the first reading. A prompt is
// first seen up to ObserveEvery after it appears, so it rings 30-45 s after
// it opened; a permission answered while the person is at the screen does
// not ring, one left alone rings well before the turn is lost.
const PromptGraceDefault = 30 * time.Second

// PromptGrace reads AGENT_BUS_PROMPT_GRACE (seconds, 0 = off), else the default.
func PromptGrace(getenv func(string) string) time.Duration {
	v := getenv("AGENT_BUS_PROMPT_GRACE")
	if v == "" {
		return PromptGraceDefault
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 3600 {
		return PromptGraceDefault
	}
	return time.Duration(n) * time.Second
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
	grace := l.cfg.Prompt.Grace
	if grace <= 0 {
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
		if a, ok := strings.CutPrefix(k, "prompt:"); ok {
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
