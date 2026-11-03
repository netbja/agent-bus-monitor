package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/notify"
)

// The notifications chore (notifications spec, "Where it runs"): one pass
// every 30 s in the project's dispatch service, under its lease. A pass reads
// every source (any failed read: no baseline, no send, the source recorded
// for health), decides with notify.Decide, reserves the hour's sends under
// the host lock, sends outside it, records each target's success in
// {p}:notified (fenced) and every outcome in notify-last.json.

type notifier struct {
	b        *bus.Bus
	reader   *notifyReader
	getenv   func(string) string
	now      func() time.Time
	stateDir string                            // <state>: notify-last.json and notify.lock
	sender   func(notify.Target) notify.Sender // a target's sender
	failRead map[string]string                 // a test's failed reads
}

// serviceNotify is the dispatch service's notifications chore.
func serviceNotify(b *bus.Bus, getenv func(string) string) dispatch.NotifyConfig {
	run := desktopRun(exec.LookPath, runQuiet)
	n := &notifier{b: b, reader: &notifyReader{b: b, grace: dispatch.PromptGrace(getenv)}, getenv: getenv, now: time.Now,
		stateDir: notifyStateDir(getenv), sender: func(t notify.Target) notify.Sender { return t.Sender(run) }}
	return dispatch.NotifyConfig{Pass: n.pass}
}

// desktopRingsPrompts: notifications are on, a listed desktop target is
// valid and prompts are among the kinds sent, so the dispatcher's own prompt
// ring stays silent (the person's answer 6: never rung twice).
func desktopRingsPrompts(getenv func(string) string) bool {
	r := config.Machine(getenv)
	if !r.Bool("notify.on") || !slices.Contains(config.SplitList(r.String("notify.events")), "prompt") {
		return false
	}
	f := notify.Load(notify.Path(getenv))
	for _, name := range config.SplitList(r.String("notify.targets")) {
		if t, ok := f.Target(name); ok && t.Kind == "desktop" {
			return true
		}
	}
	return false
}

type sendOutcome struct {
	target  string
	at      time.Time
	err     error
	counted bool
	sent    bool // false: not attempted (the lease was lost first)
}

func (n *notifier) pass(ctx context.Context, token string) error {
	r := config.Machine(n.getenv)
	if !r.Bool("notify.on") {
		rec, err := n.b.Notified(ctx)
		if err != nil || len(rec) == 0 {
			return err
		}
		return n.b.FencedNotifiedClear(ctx, token)
	}
	if n.stateDir == "" {
		return errors.New("no state folder (HOME and XDG_STATE_HOME unset): nothing sent")
	}
	file := notify.Load(notify.Path(n.getenv))
	if file.Refused != "" {
		return errors.New("notify.toml refused: " + file.Refused) // health says it
	}
	var targets []notify.Target
	for _, name := range config.SplitList(r.String("notify.targets")) {
		if t, ok := file.Target(name); ok {
			targets = append(targets, t)
		}
	}
	now := n.now()
	facts, failed := n.reader.read(ctx, now)
	for k, v := range n.failRead {
		failed[k] = v
	}
	lastPath := notify.LastPath(n.stateDir)
	sources := map[string]time.Time{}
	for s := range failed {
		sources[s] = now
	}
	if err := notify.UpdateLast(lastPath, func(l *notify.Last) error {
		l.SetSources(n.b.Project(), sources, now)
		return nil
	}); err != nil {
		return err
	}
	if len(failed) > 0 {
		names := make([]string, 0, len(failed))
		for s := range failed {
			names = append(names, s)
		}
		sort.Strings(names)
		return fmt.Errorf("could not read %s: nothing sent", strings.Join(names, ", "))
	}
	events := notifyEvents(facts, n.reader.seen(facts.PlanWaiting, now), now)
	rec, err := n.b.Notified(ctx)
	if err != nil {
		return err
	}
	var plan notify.Plan
	if err := notify.UpdateLast(lastPath, func(l *notify.Last) error {
		plan = notify.Decide(notify.Pass{Project: n.b.Project(), Now: now, Events: events, Paused: facts.Paused,
			Targets: targets, Records: rec, SentInHour: func(t string) int { return l.SentInHour(t, now) }}, notify.SettingsFrom(r))
		for _, tp := range plan.Targets {
			if tp.Message != nil && tp.Counted {
				l.Reserve(tp.Target.Name, now)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := n.b.FencedNotifiedDel(ctx, token, plan.Delete...); err != nil {
		return n.giveBack(lastPath, plan, nil, now, err)
	}
	var outcomes []sendOutcome
	var stop error
	for _, tp := range plan.Targets {
		if stop != nil {
			break
		}
		if tp.TakeBaseline {
			stop = n.b.FencedNotifiedBaseline(ctx, token, tp.Target.Name, tp.Baseline...)
			continue
		}
		if tp.Message == nil {
			continue
		}
		// The lease is checked before each target: a pass that lost it stops.
		if held, err := n.b.HoldsDispatch(ctx, token); err != nil || !held {
			stop = err
			if stop == nil {
				stop = bus.ErrNotHolder
			}
			break
		}
		o := sendOutcome{target: tp.Target.Name, at: now, counted: tp.Counted, sent: true}
		o.err = n.sender(tp.Target).Send(ctx, *tp.Message)
		outcomes = append(outcomes, o)
		if o.err == nil {
			if err := n.b.FencedNotifiedSent(ctx, token, tp.Fields...); err != nil {
				stop = err // sent, not recorded: the one accepted repeat
			}
		}
	}
	return n.giveBack(lastPath, plan, outcomes, now, stop)
}

// giveBack records the outcomes and returns every reservation not used by a
// successful send (a failed send, or one never made), then returns err.
func (n *notifier) giveBack(lastPath string, plan notify.Plan, outcomes []sendOutcome, now time.Time, err error) error {
	done := map[string]sendOutcome{}
	for _, o := range outcomes {
		done[o.target] = o
	}
	werr := notify.UpdateLast(lastPath, func(l *notify.Last) error {
		for _, tp := range plan.Targets {
			if tp.Message == nil {
				continue
			}
			o, tried := done[tp.Target.Name]
			if tried {
				out := notify.Outcome{Project: n.b.Project(), At: o.at, OK: o.err == nil}
				if o.err != nil {
					out.Err = o.err.Error()
				}
				l.Add(tp.Target.Name, out, false)
			}
			if tp.Counted && (!tried || o.err != nil) {
				l.Unreserve(tp.Target.Name, now)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if werr != nil {
		return fmt.Errorf("the outcomes were not recorded: %w", werr)
	}
	return nil
}
