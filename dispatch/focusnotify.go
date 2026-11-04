package dispatch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

// focusMark is agent's {p}:quota field while a focus notice stands; its value
// is the episode, the oldest waiting message's id.
func focusMark(agent string) string { return "focus:" + agent }

// FocusNoticeDefault is dispatch.focus_notice's default: a wake that waits
// half a minute on the pane the person is looking at.
const FocusNoticeDefault = 30 * time.Second

// FocusNotice is dispatch.focus_notice: AGENT_BUS_FOCUS_NOTICE, else
// config.toml, else FocusNoticeDefault; 0 = off.
func FocusNotice(getenv func(string) string) time.Duration {
	return config.Machine(getenv).Duration("dispatch.focus_notice")
}

// focusNotice (focus-notice, the person's decision): rule 8 never types into
// the pane that has the focus, so a wake can wait there unseen while the
// person reads another part of it. Once the oldest waiting message is
// FocusGrace old, the person gets one desktop notice per oldest message,
// through the prompt notification's runner; leaving the pane lets the next
// pass wake the agent. The mark is set (fenced) before the notice and
// deleted once the decision is no longer the focus. Nothing is written to a
// stream and nothing is typed.
func (l *loop) focusNotice(ctx context.Context, s *step, d Decision) error {
	mark := focusMark(s.agent)
	if !strings.HasPrefix(d.Reason, "pane focused") {
		if strings.HasPrefix(s.w.Reason, "pane focused") {
			return l.b.FencedQuotaDel(ctx, l.token, mark)
		}
		return nil
	}
	grace := l.cfg.Prompt.FocusGrace
	if grace <= 0 || s.o.Size() == 0 || s.o.OldestID == "" || s.x.OldestAge < grace {
		return nil
	}
	marks, err := l.b.QuotaMarks(ctx)
	if err != nil {
		return err
	}
	if marks[mark] == s.o.OldestID {
		return nil
	}
	if err := l.b.FencedQuotaSet(ctx, l.token, mark, s.o.OldestID); err != nil {
		if errors.Is(err, bus.ErrNotHolder) {
			return err
		}
		l.logError(s.agent, "focus notify", err)
		return nil
	}
	pane := ""
	if s.snap != nil {
		pane = s.snap.Pane
	}
	l.log(map[string]any{"agent": s.agent, "action": "focus-notify", "pane": pane, "waiting": s.o.Size()})
	n := l.cfg.Prompt.Notify
	if n == nil {
		l.log(map[string]any{"agent": s.agent, "action": "focus-notify", "note": "no notify-send on PATH: nothing shown"})
		return nil
	}
	p := l.b.Project()
	body := fmt.Sprintf("%s: %s has %d message(s) waiting, but its pane (%s) has the focus, so it is not woken: "+
		"leave the pane and it is woken at once", p, s.agent, s.o.Size(), pane)
	if err := n(ctx, "agentbus "+p, body); err != nil {
		l.logError(s.agent, "desktop notify", err)
	}
	return nil
}
