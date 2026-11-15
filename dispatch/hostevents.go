package dispatch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// What the dispatcher adds on a terminal host (herdr exit step 9, spec
// 2026-11-10-herdr-exit-design.md 4.2, 4.3a, 4.3b, 6.5): master told when an
// agent is blocked, the person when a line may be left in an input, and the
// terminal-lifecycle events. Only HostRuntime's facts lead here: herdr's
// Runtime carries no blocked look and returns none of termhost's errors, so
// the running dispatcher behaves as before.

// onHost: the loop runs on a terminal host, not herdr.
func (l *loop) onHost() bool {
	_, ok := l.rt.(*HostRuntime)
	return ok
}

// event records one fact in {p}:events, fenced; a failure is logged, never
// stops the pass (the events are a timeline, not a source of truth).
func (l *loop) event(ctx context.Context, kind, agent, detail string) {
	if _, err := l.b.FencedAppendEvent(ctx, l.token, bus.TermEvent{Kind: kind, Agent: agent, By: dispatchFrom, Detail: detail}); err != nil {
		l.logError(agent, "event "+kind, err)
	}
}

// blockedMark and unblockedMark are the {p}:quota once-marks of an episode's
// two reports: one enter and one leave, even across a restart within it.
func blockedMark(agent string) string   { return "blocked:" + agent }
func unblockedMark(agent string) string { return "unblocked:" + agent }

// episodeKey names one episode: its process and when it began.
func episodeKey(e agentstate.Episode) string {
	return fmt.Sprintf("%d-%d", e.PID, e.Since.UnixMilli())
}

// blockedLook steps agent's blocked episode with one look of the host (4.3a)
// and sends the report it makes: the enter to the escalation target (a
// waking directive; for the person when the target is the agent itself), the
// leave without waking anyone. Each with its event.
func (l *loop) blockedLook(ctx context.Context, agent, client string, o agentstate.Observation) error {
	if l.episodes == nil {
		l.episodes = map[string]agentstate.Episode{}
	}
	e, ok := l.episodes[agent]
	if !ok {
		e = agentstate.Episode{Agent: agent, Client: client}
	}
	var before agentstate.Episode
	if e.Open {
		before = e
	}
	next, ev := e.Step(o)
	if ev == nil {
		l.episodes[agent] = next
		return nil
	}
	to := l.cfg.EscalateTo
	line := ev.Line()
	var sent bool
	var err error
	switch {
	case ev.Kind == agentstate.Unblocked:
		ep := episodeKey(before)
		if agent == to {
			sent, err = l.b.FencedNotifyOnceForPerson(ctx, l.token, unblockedMark(agent), ep, dispatchFrom, line)
		} else {
			sent, err = l.b.FencedCmdOnce(ctx, l.token, unblockedMark(agent), ep, dispatchFrom, to, bus.CmdDirective, line, true)
		}
	case agent == to: // master itself is blocked: only the person can answer
		sent, err = l.b.FencedNotifyOnceForPerson(ctx, l.token, blockedMark(agent), episodeKey(next), dispatchFrom, line)
	default:
		sent, err = l.b.FencedCmdOnce(ctx, l.token, blockedMark(agent), episodeKey(next), dispatchFrom, to, bus.CmdDirective, line, false)
	}
	if err != nil {
		return err // the episode is not moved on: the next look reports again
	}
	l.episodes[agent] = next
	if !sent { // sent before this dispatcher's restart
		return nil
	}
	l.log(map[string]any{"agent": agent, "action": string(ev.Kind), "to": to})
	l.event(ctx, string(ev.Kind), agent, line)
	return nil
}

// promptOutcome records what a host made of a wake line: typed, refused
// with nothing typed, or not seen in the input (the person is told, as the
// line may be left there).
func (l *loop) promptOutcome(ctx context.Context, agent string, observed bool, err error) error {
	if !l.onHost() {
		return nil
	}
	switch {
	case err == nil && observed:
		l.event(ctx, bus.EventWakeTyped, agent, "")
	case errors.Is(err, termhost.ErrNotLanded):
		return l.notLanded(ctx, agent, "wake")
	case errors.Is(err, termhost.ErrPersonActive), errors.Is(err, termhost.ErrNotReady), errors.Is(err, termhost.ErrLeftBehind):
		l.event(ctx, bus.EventWakeRefused, agent, err.Error())
	}
	return nil
}

// NotLandedText is the person's notice when a line was pasted and not seen
// in agent's input (spec 4.2): no CR was sent, and the terminal takes no
// line until its input reads empty.
func NotLandedText(agent, kind string) string {
	if kind == "clear" {
		return fmt.Sprintf("/clear may be left unsent in %s's input — submitting it clears the conversation. Attach and erase it.", agent)
	}
	return fmt.Sprintf("wake line may be left unsent in %s's input. Attach and erase it, or submit it.", agent)
}

// notLanded tells the person (busmon) and records the event.
func (l *loop) notLanded(ctx context.Context, agent, kind string) error {
	text := NotLandedText(agent, kind)
	if _, err := l.b.FencedNotifyForPerson(ctx, l.token, dispatchFrom, text); err != nil {
		return err
	}
	l.log(map[string]any{"agent": agent, "action": "not-landed", "kind": kind})
	l.event(ctx, bus.EventWakeNotLanded, agent, text)
	return nil
}

// backgroundSkip records, once per change of what runs, that action skipped
// agent for its background work (4.3b).
func (l *loop) backgroundSkip(ctx context.Context, agent, action, why string) {
	if l.bgSkips == nil {
		l.bgSkips = map[string]string{}
	}
	key := agent + "|" + action
	if l.bgSkips[key] == why {
		return
	}
	l.bgSkips[key] = why
	l.event(ctx, bus.EventBackgroundSkip, agent, action+" skipped — "+why)
}

// blockedObserve feeds the observation pass's look at agent to its blocked
// episode: a host's look of the agent's own process, or its terminal gone
// (which ends an open episode). A failed look, another process in the
// terminal, or herdr's Runtime (no look) are no new fact.
func (l *loop) blockedObserve(ctx context.Context, agent string, snap *bus.AgentSnapshot, in Inspection) error {
	var o agentstate.Observation
	switch {
	case in.Gone:
		if _, open := l.episodes[agent]; !open {
			return nil
		}
		o = agentstate.Observation{At: time.Now()}
	case in.Blocked == nil || in.Err != nil || !identityOK(snap, in):
		return nil
	default:
		o = *in.Blocked
	}
	client := in.Client
	if client == "" {
		client = snap.Client
	}
	return l.blockedLook(ctx, agent, client, o)
}
