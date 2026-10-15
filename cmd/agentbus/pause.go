package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

const pauseUsage = "usage: pause --for <2h> | --until <HH:MM> | --until-reset [--to <agent>] [reason...] | pause --status | resume [message...]"

// pauseDeps is what pause and resume read and write, injected for tests.
type pauseDeps struct {
	Set     func(ctx context.Context, by, notify, reason string, until time.Time) (bus.PauseRecord, error)
	Get     func(ctx context.Context) (*bus.PauseRecord, error)
	Resume  func(ctx context.Context, by, message string) (bool, error)
	Budgets func(ctx context.Context) (map[string]bus.BudgetSnapshot, error)
	Now     func() time.Time
	Env     func(string) string
}

// resetMargin: the quota window's reset is read from a cache; resuming a
// little after it avoids waking a team into a window that has not reopened.
const resetMargin = 2 * time.Minute

// runPause: pause the project until a deadline, show it, or (resume) end it.
func runPause(ctx context.Context, d pauseDeps, self string, args []string, out, errw io.Writer) int {
	args, status := extractBool(args, "--status")
	if status {
		p, err := d.Get(ctx)
		if err != nil {
			fmt.Fprintf(errw, "pause: %v\n", err)
			return 1
		}
		if p == nil {
			fmt.Fprintln(out, "not paused")
			return 1
		}
		fmt.Fprintf(out, "paused until %s (in %s) by %s%s; resume goes to %s\n", p.UntilTime().Format("15:04"),
			roundDur(p.UntilTime().Sub(d.Now())), p.By, reasonSuffix(p.Reason), p.Notify)
		return 0
	}
	args, forArg := extractFlag(args, "--for")
	args, untilArg := extractFlag(args, "--until")
	args, untilReset := extractBool(args, "--until-reset")
	args, to := extractFlag(args, "--to")
	usage := func(msg string) int { fmt.Fprintf(errw, "pause: %s\n%s\n", msg, pauseUsage); return 2 }
	n := 0
	for _, set := range []bool{forArg != "", untilArg != "", untilReset} {
		if set {
			n++
		}
	}
	if n != 1 {
		return usage("give exactly one of --for, --until, --until-reset (a pause always ends)")
	}
	now := d.Now()
	var until time.Time
	switch {
	case forArg != "":
		dur, err := time.ParseDuration(forArg)
		if err != nil || dur <= 0 {
			return usage(fmt.Sprintf("--for %q: want a duration like 90m or 2h", forArg))
		}
		until = now.Add(dur)
	case untilArg != "":
		t, err := time.ParseInLocation("15:04", untilArg, now.Location())
		if err != nil {
			return usage(fmt.Sprintf("--until %q: want HH:MM (local time)", untilArg))
		}
		until = time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
		if !until.After(now) {
			until = until.Add(24 * time.Hour)
		}
	default:
		bs, err := d.Budgets(ctx)
		if err != nil {
			fmt.Fprintf(errw, "pause: reading the quota window: %v\n", err)
			return 1
		}
		reset, err := time.Parse(time.RFC3339, bs["anthropic"].SessionReset)
		if err != nil || !reset.After(now) {
			fmt.Fprintln(errw, "pause: no upcoming session reset is known (agentbus refresh updates it); use --for or --until")
			return 1
		}
		until = reset.Add(resetMargin)
	}
	if to == "" {
		to = d.Env("AGENT_BUS_ESCALATE_TO")
	}
	if to == "" {
		to = "master"
	}
	if !bus.ValidName(to) {
		return usage(fmt.Sprintf("--to %q: invalid agent name", to))
	}
	p, err := d.Set(ctx, self, to, strings.Join(args, " "), until)
	if err != nil {
		fmt.Fprintf(errw, "pause: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "paused until %s (in %s)%s: nobody is woken until then; %s then gets the resume directive\n",
		p.UntilTime().Format("15:04"), roundDur(p.UntilTime().Sub(now)), reasonSuffix(p.Reason), p.Notify)
	return 0
}

func runResume(ctx context.Context, d pauseDeps, self string, args []string, out, errw io.Writer) int {
	ok, err := d.Resume(ctx, self, strings.Join(args, " "))
	if err != nil {
		fmt.Fprintf(errw, "resume: %v\n", err)
		return 1
	}
	if !ok {
		fmt.Fprintln(out, "not paused")
		return 0
	}
	fmt.Fprintln(out, "resumed: the resume directive is sent")
	return 0
}

func reasonSuffix(r string) string {
	if r == "" {
		return ""
	}
	return " (" + r + ")"
}

// roundDur renders a remaining time as 1h52m or 7m, never seconds.
func roundDur(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Minute {
		return "<1m"
	}
	h, m := int(d.Hours()), int(d.Minutes())%60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh%02dm", h, m)
}
