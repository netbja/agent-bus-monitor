package main

// agentbus check holder-restart: the holder's live check (holder hardening
// spec §5; the person's answer to Q5: a scripted live check, run on the
// person's machine before each release). On a disposable project of its own
// (never the person's team), it crashes the holder as a crash does (SIGKILL
// of the holder's own process; its service manager, systemd, launchd or the
// detached supervisor, restarts it) and times what the in-process test
// cannot prove: the manager's restart, self-repair's relaunches on the real
// services, each agent resumed on its recorded session, master first at the
// project's pace, each woken again, health back to ok.
//
// --clients=fake runs the team on agentbus fake-client (no model, no quota:
// the base of a later soak run); --clients=real on the role's real client.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/selfrepair"
)

// liveAgent is one role as the bus says it: its proved process (0: none),
// its recorded resume session and when that record was written (a new
// process's SessionStart rewrites it: a relaunch resumed its session when
// the record is newer than the crash and names the same session).
type liveAgent struct {
	PID     int
	Session string
	At      time.Time
}

// liveWorld is the disposable project the check acts on and reads
// (liveProject in production; a scripted fake in tests).
type liveWorld interface {
	Up(ctx context.Context) error
	Down(ctx context.Context) error
	Roles() []string // the team, master first
	Agents(ctx context.Context) (map[string]liveAgent, error)
	Holder(ctx context.Context) (dispatch.ProcID, error) // the current build's holder process
	Crash(ctx context.Context, p dispatch.ProcID) error  // SIGKILL p, only while it is that process
	SwapMark(ctx context.Context, role string) (string, error)
	SwapsSince(ctx context.Context, role, mark string) ([]bus.SwapEvent, error)
	Wake(ctx context.Context, role string) (string, error) // a directive; its entry id
	Drained(ctx context.Context, role, id string) (bool, error)
	HolderHealth(ctx context.Context) (health.Status, string, error)
	// Resumed: the role's new process says it resumed session (the fake's
	// screen); a real client cannot say, true.
	Resumed(ctx context.Context, role, session string) (bool, error)
}

// liveDeadlines bound each step.
type liveDeadlines struct {
	Ready    time.Duration // the team started and proved after up
	Holder   time.Duration // a new holder process answers after the crash
	Warn     time.Duration // health holder warns that it restarted
	Relaunch time.Duration // the first relaunch after the crash
	Pace     time.Duration // each later one (self-repair's project pause, and its read)
	Drain    time.Duration // a wake drained
	Healthy  time.Duration // health holder ok again after the relaunches
}

// liveDefaults are the deadlines on a real machine: self-repair's grace
// (three reads, about a minute) and boot for the first relaunch, its pause
// between relaunches; a real client boots slower than the fake.
func liveDefaults(real bool) liveDeadlines {
	d := liveDeadlines{Ready: 3 * time.Minute, Holder: time.Minute, Warn: 3 * time.Minute, Relaunch: 5 * time.Minute,
		Pace: selfrepair.ProjectPause + 30*time.Second, Drain: 2 * time.Minute, Healthy: 3 * time.Minute}
	if real {
		d.Ready, d.Relaunch, d.Drain = 10*time.Minute, 10*time.Minute, 5*time.Minute
	}
	return d
}

// liveOpts is one run's options.
type liveOpts struct {
	Project   string // the disposable project's name, for the messages
	Clients   string // fake or real, for the messages
	Rounds    int
	Keep      bool
	Poll      time.Duration
	Deadlines liveDeadlines
}

// errLiveFail is a step that failed; its message is printed.
type errLiveFail struct{ msg string }

func (e errLiveFail) Error() string { return e.msg }

func liveFailf(format string, a ...any) error { return errLiveFail{fmt.Sprintf(format, a...)} }

// runLiveCheck runs the check on w: 0 passed, 1 failed (the step named).
func runLiveCheck(ctx context.Context, w liveWorld, o liveOpts, out io.Writer) int {
	if o.Rounds < 1 {
		o.Rounds = 1
	}
	if o.Poll <= 0 {
		o.Poll = 2 * time.Second
	}
	err := liveRun(ctx, w, o, out)
	switch {
	case o.Keep:
		fmt.Fprintf(out, "kept: %s (remove it: agentbus --project %s service remove; its broker keys: busmon --delete %s)\n", o.Project, o.Project, o.Project)
	default:
		if derr := w.Down(context.WithoutCancel(ctx)); derr != nil {
			fmt.Fprintf(out, "warning: removing the test project %s: %v\n", o.Project, derr)
		} else {
			fmt.Fprintf(out, "removed: the test project %s\n", o.Project)
		}
	}
	if err != nil {
		fmt.Fprintln(out, "FAIL "+err.Error())
		return 1
	}
	fmt.Fprintf(out, "holder live check passed: %d round(s), clients %s\n", o.Rounds, o.Clients)
	return 0
}

// liveWait polls cond every o.Poll until it holds, for at most d; the last
// thing cond said (why it does not hold yet, or an error) names a timeout.
func liveWait(ctx context.Context, o liveOpts, d time.Duration, step string, cond func() (bool, string, error)) error {
	deadline := time.Now().Add(d)
	last := ""
	for {
		ok, why, err := cond()
		var fail errLiveFail
		switch {
		case err != nil && errors.As(err, &fail):
			return liveFailf("%s: %s", step, fail.msg)
		case err != nil:
			last = err.Error()
		case ok:
			return nil
		default:
			last = why
		}
		if !time.Now().Before(deadline) {
			if last != "" {
				return liveFailf("%s: not within %s (%s)", step, d.Round(time.Second), last)
			}
			return liveFailf("%s: not within %s", step, d.Round(time.Second))
		}
		select {
		case <-ctx.Done():
			return liveFailf("%s: %v", step, ctx.Err())
		case <-time.After(o.Poll):
		}
	}
}

// liveRun is the check's steps; each one passed is printed with its time.
func liveRun(ctx context.Context, w liveWorld, o liveOpts, out io.Writer) error {
	d := o.Deadlines
	ok := func(step string, from time.Time) {
		fmt.Fprintf(out, "ok   %-46s %s\n", step, time.Since(from).Round(100*time.Millisecond))
	}
	t := time.Now()
	if err := w.Up(ctx); err != nil {
		return liveFailf("up: %v", err)
	}
	ok("up", t)
	roles := w.Roles()
	t = time.Now()
	if err := liveWait(ctx, o, d.Ready, "team ready", func() (bool, string, error) {
		as, err := w.Agents(ctx)
		if err != nil {
			return false, "", err
		}
		for _, r := range roles {
			if as[r].PID == 0 || as[r].Session == "" {
				return false, r + " not proved with a recorded session", nil
			}
		}
		return true, "", nil
	}); err != nil {
		return err
	}
	ok("team ready", t)
	if err := liveHealthy(ctx, w, o, d.Healthy, "holder health ok", out); err != nil {
		return err
	}
	for _, r := range roles {
		if err := liveWake(ctx, w, o, r, out); err != nil {
			return err
		}
	}
	for round := 1; round <= o.Rounds; round++ {
		if o.Rounds > 1 {
			fmt.Fprintf(out, "round %d of %d\n", round, o.Rounds)
		}
		if err := liveRound(ctx, w, o, roles, out); err != nil {
			return err
		}
	}
	return nil
}

// liveRound is one crash and its recovery.
func liveRound(ctx context.Context, w liveWorld, o liveOpts, roles []string, out io.Writer) error {
	d := o.Deadlines
	ok := func(step string, from time.Time) {
		fmt.Fprintf(out, "ok   %-46s %s\n", step, time.Since(from).Round(100*time.Millisecond))
	}
	before, err := w.Agents(ctx)
	if err != nil {
		return liveFailf("before the crash: %v", err)
	}
	marks := map[string]string{}
	for _, r := range roles {
		if marks[r], err = w.SwapMark(ctx, r); err != nil {
			return liveFailf("before the crash: %s's swap ledger: %v", r, err)
		}
	}
	hp, err := w.Holder(ctx)
	if err != nil || hp.PID <= 0 {
		return liveFailf("crash: the holder's process is not known (%v): nothing killed", err)
	}
	t0 := time.Now()
	if err := w.Crash(ctx, hp); err != nil {
		return liveFailf("crash: %v", err)
	}
	ok(fmt.Sprintf("crash: holder %d killed", hp.PID), t0)
	var np dispatch.ProcID
	if err := liveWait(ctx, o, d.Holder, "holder answers again", func() (bool, string, error) {
		p, err := w.Holder(ctx)
		if err != nil {
			return false, "", err
		}
		np = p
		return p.PID > 0 && p != hp, "the old process still answers", nil
	}); err != nil {
		return err
	}
	ok(fmt.Sprintf("holder answers again (pid %d)", np.PID), t0)
	if err := liveWait(ctx, o, d.Warn, "health holder warns: restarted", func() (bool, string, error) {
		st, detail, err := w.HolderHealth(ctx)
		if err != nil {
			return false, "", err
		}
		return st == health.Warn && strings.Contains(detail, "restarted"), fmt.Sprintf("health holder: %s, %s", st, detail), nil
	}); err != nil {
		return err
	}
	ok("health holder warns: restarted", t0)
	relaunched := map[string]string{} // role: its relaunch's ledger id
	for i, r := range roles {
		limit := d.Relaunch + time.Duration(i)*d.Pace
		step := "relaunch " + r
		if err := liveWait(ctx, o, time.Until(t0.Add(limit)), step, func() (bool, string, error) {
			evs, err := w.SwapsSince(ctx, r, marks[r])
			if err != nil {
				return false, "", err
			}
			id := ""
			for _, e := range evs {
				switch e.Event {
				case bus.SwapFailed:
					return false, "", liveFailf("self-repair failed: %s", e.Reason)
				case bus.SwapRelaunched:
					id = e.ID
				}
			}
			if id == "" {
				return false, "no relaunch in its swap ledger yet", nil
			}
			as, err := w.Agents(ctx)
			if err != nil {
				return false, "", err
			}
			a := as[r]
			switch {
			case a.PID == 0 || a.PID == before[r].PID:
				return false, "its new process not proved yet", nil
			case a.At.Before(t0):
				return false, "its new process has not recorded its session yet", nil
			case a.Session != before[r].Session:
				return false, "", liveFailf("on session %s, not its recorded %s", a.Session, before[r].Session)
			}
			if res, err := w.Resumed(ctx, r, a.Session); err != nil || !res {
				return false, "its screen does not say it resumed", err
			}
			relaunched[r] = id
			return true, "", nil
		}); err != nil {
			return err
		}
		ok(fmt.Sprintf("relaunch %s (resumed %s)", r, before[r].Session), t0)
	}
	for i := 1; i < len(roles); i++ {
		if bus.IDLess(relaunched[roles[i]], relaunched[roles[i-1]]) {
			return liveFailf("relaunch order: %s before %s", roles[i], roles[i-1])
		}
	}
	for _, r := range roles {
		if err := liveWake(ctx, w, o, r, out); err != nil {
			return err
		}
	}
	return liveHealthy(ctx, w, o, d.Healthy, "health holder ok again", out)
}

// liveHealthy waits for health holder ok.
func liveHealthy(ctx context.Context, w liveWorld, o liveOpts, d time.Duration, step string, out io.Writer) error {
	t := time.Now()
	if err := liveWait(ctx, o, d, step, func() (bool, string, error) {
		st, detail, err := w.HolderHealth(ctx)
		if err != nil {
			return false, "", err
		}
		return st == health.OK, fmt.Sprintf("health holder: %s, %s", st, detail), nil
	}); err != nil {
		return err
	}
	fmt.Fprintf(out, "ok   %-46s %s\n", step, time.Since(t).Round(100*time.Millisecond))
	return nil
}

// liveWake sends role a directive and waits for its drain.
func liveWake(ctx context.Context, w liveWorld, o liveOpts, role string, out io.Writer) error {
	t := time.Now()
	id, err := w.Wake(ctx, role)
	if err != nil {
		return liveFailf("wake %s: %v", role, err)
	}
	if err := liveWait(ctx, o, o.Deadlines.Drain, "wake "+role, func() (bool, string, error) {
		done, err := w.Drained(ctx, role, id)
		return done, "its message " + id + " not drained", err
	}); err != nil {
		return err
	}
	fmt.Fprintf(out, "ok   %-46s %s\n", "wake "+role, time.Since(t).Round(100*time.Millisecond))
	return nil
}
