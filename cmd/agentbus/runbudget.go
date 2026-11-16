package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/service"
)

// The run budget's drain and stop (spec docs/superpowers/specs/2026-11-15-run-budget-design.md
// §3, step 2), on the merge-queue pass right after the run's write: a limit
// reached drains the run once (master told, the person told, each by its own
// once-mark), and a run draining for a limit stops when nothing is in flight
// or run.drain_max after the drain began: the project pauses (kind run). A
// drain for a quota (§4) never stops here: the quota's own pause holds the
// project until its release, which returns the run to running.

// runLimits are the run's settings (the registry: 0 = no limit).
type runLimits struct {
	maxDuration, drainMax time.Duration
	maxDeliveries         int
}

func readRunLimits(getenv func(string) string) runLimits {
	s := config.Machine(getenv)
	return runLimits{maxDuration: s.Duration("run.max_duration"), drainMax: s.Duration("run.drain_max"),
		maxDeliveries: s.Int("run.max_deliveries")}
}

// runLimitReached is the limit run has reached at nowMs (Redis time), "" for none.
func runLimitReached(run bus.RunRecord, nowMs int64, l runLimits) string {
	switch {
	case l.maxDeliveries > 0 && run.Deliveries >= l.maxDeliveries:
		return bus.RunWhyDeliveries
	case l.maxDuration > 0 && run.Elapsed(nowMs) >= l.maxDuration:
		return bus.RunWhyDuration
	}
	return ""
}

// runTotals is the run's count for a person: "6 deliveries of 6, 3h10m of 3h".
func runTotals(run bus.RunRecord, nowMs int64, l runLimits) string {
	d := fmt.Sprintf("%d deliveries", run.Deliveries)
	if l.maxDeliveries > 0 {
		d += fmt.Sprintf(" of %d", l.maxDeliveries)
	}
	e := runDuration(run.Elapsed(nowMs))
	if l.maxDuration > 0 {
		e += " of " + runDuration(l.maxDuration)
	}
	return d + ", " + e
}

// runEpisode is the episode of a drain's once-marks (§3).
func runEpisode(run bus.RunRecord) string { return fmt.Sprintf("%d:%d", run.StartedAt, run.Drains) }

// runStep acts on the run after the pass's write, the queue's undelivered
// heads read.
func (c *mergeQueueChore) runStep(ctx context.Context, token string, run bus.RunRecord, undelivered []bus.MergeUndelivered) error {
	return c.runStepKnown(ctx, token, run, undelivered, true)
}

// runStepKnown is runStep; known is false when the pass could not read the
// queue: nothing is stopped then, since what is in flight is not known.
// Only the fenced writes' errors are returned (a lost lease); a failed read
// leaves the run as it is until the next pass.
func (c *mergeQueueChore) runStepKnown(ctx context.Context, token string, run bus.RunRecord, undelivered []bus.MergeUndelivered, known bool) error {
	if run.State == "" {
		return nil
	}
	l := readRunLimits(c.getenv)
	now := run.SeenAt // the pass's Redis time
	if run.State == bus.RunRunning || (run.State == bus.RunDraining && strings.HasPrefix(run.Why, "quota:")) {
		if why := runLimitReached(run, now, l); why != "" {
			if _, _, err := c.b.FencedRunDrain(ctx, token, why); err != nil {
				return err
			}
			if r, err := c.b.Run(ctx); err != nil || r == nil {
				return nil
			} else {
				run = *r
			}
		}
	}
	// Every message is told on each pass of its state, by its once-mark: a
	// transition committed whose message failed is told on a later pass, and
	// none twice (reviewer-codex). A quota drain is the quota pass's to tell.
	switch {
	case run.State == bus.RunStopped:
		inFlight, _ := c.runInFlight(ctx, undelivered)
		return c.announceStop(ctx, token, run, now, l, inFlight)
	case run.State != bus.RunDraining || strings.HasPrefix(run.Why, "quota:"):
		return nil
	}
	if err := c.announceDrain(ctx, token, runEpisode(run), run, now, l); err != nil {
		return err
	}
	if !known {
		return nil
	}
	inFlight, err := c.runInFlight(ctx, undelivered)
	if err != nil {
		return nil
	}
	cut := l.drainMax > 0 && now-run.Since >= l.drainMax.Milliseconds()
	if len(inFlight) > 0 && !cut {
		return nil
	}
	stopped, err := c.b.FencedRunStop(ctx, token, c.escalateTo())
	if err != nil || !stopped {
		return err
	}
	return c.announceStop(ctx, token, run, now, l, inFlight)
}

// announceStop tells the person the run stopped, once per drain episode,
// with what is still in flight (a drain cut short by run.drain_max).
func (c *mergeQueueChore) announceStop(ctx context.Context, token string, run bus.RunRecord, now int64, l runLimits, inFlight []string) error {
	text := fmt.Sprintf("%s: run stopped (%s; %s): the project is paused; agentbus run reset starts a new run", c.b.Project(), run.Why, runTotals(run, now, l))
	if len(inFlight) > 0 {
		text += fmt.Sprintf("; still in flight (run.drain_max %s passed): %s", runDuration(l.drainMax), strings.Join(inFlight, ", "))
	}
	_, err := c.b.FencedNotifyOnceForPerson(ctx, token, "run-stop-person", runEpisode(run), "dispatch", text)
	return err
}

// announceDrain tells master (a waking directive) and the person (a
// notice), each once per drain episode, by its own mark.
func (c *mergeQueueChore) announceDrain(ctx context.Context, token, episode string, run bus.RunRecord, now int64, l runLimits) error {
	totals := runTotals(run, now, l)
	directive := fmt.Sprintf("Run budget reached (%s): dispatch no new task; finish and deliver what is in flight; the project pauses when nothing is left.", totals)
	if _, err := c.b.FencedCmdOnce(ctx, token, "run-drain", episode, "dispatch", c.escalateTo(), bus.CmdDirective, directive, false); err != nil {
		return err
	}
	notice := fmt.Sprintf("%s: run budget reached (%s): the run drains; no new task but yours; the project pauses when nothing is in flight (agentbus run)", c.b.Project(), totals)
	_, err := c.b.FencedNotifyOnceForPerson(ctx, token, "run-drain-person", episode, "dispatch", notice)
	return err
}

// escalateTo is master: the services' escalation target, else "master".
func (c *mergeQueueChore) escalateTo() string {
	if v := c.getenv(service.EscalateEnv); bus.ValidName(v) {
		return v
	}
	return "master"
}

// runInFlight is what a drain waits for (§3): a board entry requested or
// accepted, an undelivered head, an open gate, an agent working; sorted.
func (c *mergeQueueChore) runInFlight(ctx context.Context, undelivered []bus.MergeUndelivered) ([]string, error) {
	var out []string
	board, err := c.b.Board(ctx)
	if err != nil {
		return nil, err
	}
	for task, e := range board {
		// §3: requested or accepted only; a plan claim (working) is not
		// in flight, or a stale one would hold every drain (foureyes)
		if e.State == "requested" || e.State == "accepted" {
			out = append(out, fmt.Sprintf("task %s (%s, %s)", task, e.State, e.Owner))
		}
	}
	for _, u := range undelivered {
		h := u.Head
		if len(h) > 8 {
			h = h[:8]
		}
		out = append(out, fmt.Sprintf("head %s undelivered (%s)", h, u.Subject))
	}
	snaps, err := c.b.Agents(ctx)
	if err != nil {
		return nil, err
	}
	for name, s := range snaps {
		if s.State == "working" {
			out = append(out, name+" working")
		}
	}
	// Every gate key: a reviewer offline, with no snapshot, still holds one
	// (reviewer-codex).
	gates, err := c.b.AllGates(ctx)
	if err != nil {
		return nil, err
	}
	for name, g := range gates {
		if len(g) > 0 {
			out = append(out, "gate on "+name)
		}
	}
	sort.Strings(out)
	return out, nil
}
