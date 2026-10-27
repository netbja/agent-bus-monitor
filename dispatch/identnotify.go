package dispatch

import (
	"context"
	"errors"
	"fmt"

	"github.com/netbja/agent-bus-monitor/bus"
)

// identMark is agent's {p}:quota field while it is deferred for an identity
// mismatch; its value is the episode, the registered pid:pid_start.
func identMark(agent string) string { return "ident:" + agent }

// identityNotice (A8b): an agent deferred for "identity mismatch" while
// messages wait for it earns the person one desktop notification per
// episode, through the prompt notification's runner. The escalation of such
// an agent goes to master, which helps nobody when master is the agent whose
// pane lost its identity. The mark is set (fenced) before the notification,
// and deleted once the agent's decision is no longer a mismatch. Nothing is
// written to a stream and nothing is typed.
func (l *loop) identityNotice(ctx context.Context, s *step, d Decision) error {
	mark := identMark(s.agent)
	if d.Reason != "identity mismatch" {
		if s.w.Reason == "identity mismatch" {
			return l.b.FencedQuotaDel(ctx, l.token, mark)
		}
		return nil
	}
	if s.snap == nil || s.o.Size() == 0 {
		return nil
	}
	episode := fmt.Sprintf("%d:%d", s.snap.PID, s.snap.PIDStart)
	marks, err := l.b.QuotaMarks(ctx)
	if err != nil {
		return err
	}
	if marks[mark] == episode {
		return nil
	}
	if err := l.b.FencedQuotaSet(ctx, l.token, mark, episode); err != nil {
		if errors.Is(err, bus.ErrNotHolder) {
			return err
		}
		l.logError(s.agent, "identity notify", err)
		return nil
	}
	l.log(map[string]any{"agent": s.agent, "action": "identity-notify", "pane": s.snap.Pane, "waiting": s.o.Size()})
	n := l.cfg.Prompt.Notify
	if n == nil {
		l.log(map[string]any{"agent": s.agent, "action": "identity-notify", "note": "no notify-send on PATH: nothing shown"})
		return nil
	}
	p := l.b.Project()
	body := fmt.Sprintf("%s: %s has %d message(s) waiting, but the process in its pane (%s) is not the one it registered: "+
		"relaunch it (agentbus up %s --resume)", p, s.agent, s.o.Size(), s.snap.Pane, p)
	if err := n(ctx, "agentbus "+p, body); err != nil {
		l.logError(s.agent, "desktop notify", err)
	}
	return nil
}
