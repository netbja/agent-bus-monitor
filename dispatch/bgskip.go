package dispatch

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/bus"
)

// A skip that lasts (background-work-visible §4a): background work makes the
// dispatcher skip an agent's auto-clear or pop-retire; a forgotten task would
// hold that skip forever, silently. Each action's skips of one agent are an
// episode, from the first pass that skipped to the first that did not (the
// action ran, it is no longer pending, the work ended) or a new process; its
// start is a fenced mark, and once it has lasted BackgroundNoticeAfter the
// escalation target is told once (FencedCmdOnce on the episode's mark), so a
// dispatcher restart repeats nothing.

// bgSkipFact is one skip of this pass: the judgement that held it, the
// agent's process.
type bgSkipFact struct {
	source, what string
	pid          int
}

// bgSkipped notes action's skip of agent in this pass.
func (l *loop) bgSkipped(action, agent string, j *agentstate.Judgement, pid int) {
	if l.bgPass == nil {
		l.bgPass = map[string]map[string]bgSkipFact{}
	}
	if l.bgPass[action] == nil {
		l.bgPass[action] = map[string]bgSkipFact{}
	}
	f := bgSkipFact{source: "unknown", pid: pid}
	if j != nil {
		f.source, f.what = j.Source(), j.What()
	}
	l.bgPass[action][agent] = f
}

// bgPassStart forgets action's skips before its pass.
func (l *loop) bgPassStart(action string) {
	if l.bgPass != nil {
		delete(l.bgPass, action)
	}
}

// bgEpisodes ends the episodes of action this pass did not skip, opens the new
// ones, and tells the escalation target of those that lasted. marks is
// {p}:quota as the pass read it, nowMS the pass's Redis TIME.
func (l *loop) bgEpisodes(ctx context.Context, action string, marks map[string]string, nowMS int64) error {
	skipped := l.bgPass[action]
	suffix := ":" + action
	for field := range marks { // the episodes that ended
		rest, ok := strings.CutPrefix(field, "bgskip:")
		if !ok || !strings.HasSuffix(rest, suffix) {
			continue
		}
		agent := strings.TrimSuffix(rest, suffix)
		if _, still := skipped[agent]; !still {
			if err := l.b.FencedQuotaDel(ctx, l.token, field, bus.BgSkipNoticeMark(agent, action)); err != nil {
				return err
			}
		}
	}
	for agent, f := range skipped {
		mark, pidStr := bus.BgSkipMark(agent, action), strconv.Itoa(f.pid)
		start, pid, ok := strings.Cut(marks[mark], ":")
		if !ok || pid != pidStr { // a new episode: none open, or another process
			start = strconv.FormatInt(nowMS, 10)
			if err := l.b.FencedQuotaDel(ctx, l.token, bus.BgSkipNoticeMark(agent, action)); err != nil {
				return err
			}
			if err := l.b.FencedQuotaSet(ctx, l.token, mark, start+":"+pidStr); err != nil {
				return err
			}
		}
		after := l.cfg.BackgroundNoticeAfter
		startMS, err := strconv.ParseInt(start, 10, 64)
		if after <= 0 || err != nil || nowMS-startMS < after.Milliseconds() {
			continue
		}
		to := l.cfg.EscalateTo
		if !bus.ValidName(to) {
			continue
		}
		text := BackgroundNoticeText(agent, action, time.Duration(nowMS-startMS)*time.Millisecond, f.source, f.what)
		if _, err := l.b.FencedCmdOnce(ctx, l.token, bus.BgSkipNoticeMark(agent, action), start+":"+pidStr,
			dispatchFrom, to, bus.CmdDirective, text, to == agent); err != nil {
			return err
		}
	}
	return nil
}

// BackgroundNoticeText is the notice to the escalation target (§4a): the
// agent, the action, how long, and the source of the judgement with what it
// said; never a command or its arguments.
func BackgroundNoticeText(agent, action string, held time.Duration, source, what string) string {
	long := fmt.Sprintf("%d min", int(held/time.Minute))
	if held >= time.Hour {
		long = fmt.Sprintf("%d h", int(held/time.Hour))
	}
	if what == "" {
		what = "no authoritative source read"
	}
	return fmt.Sprintf("background: %s's %s has been skipped for %s — %s: %s. If that work was forgotten (a dev server?), "+
		"stop it or tell the agent; the dispatcher acts once it ends. See its page in busmon, or agentbus background %s.",
		agent, action, long, source, what, agent)
}
