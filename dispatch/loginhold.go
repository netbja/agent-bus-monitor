package dispatch

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

// An expired login holds its agent (login-updates r9 §3.6): per agent, not
// per provider, so other Claude agents with another directory still work.
//   - fail: the fenced field hold-login:<agent> in {p}:quota, its value the
//     expiry (ms, the episode); what the agent had in {p}:held (provider
//     "login"), when it had work; one notice for the person and one
//     directive to master per episode. While the field exists the agent gets
//     no wake (heldBy).
//   - release: a later expiry still ahead. The resume directive, then the
//     {p}:held field, then the hold field last: a failed write leaves the
//     hold, and the next pass redoes what is missing.
// Nothing else releases it: an unknown read, the same expiry or an older one
// keeps the hold.

// loginProvider is the {p}:held provider and the resume mark's of a login hold.
const loginProvider = "login"

// runHeldMark is master's directive's once-mark (run-budget §4's).
func runHeldMark(agent string) string { return "run-held:" + agent }

// loginExpiredAgentMark is the person's fail notice's once-mark.
func loginExpiredAgentMark(agent string) string { return "login-expired:" + agent }

// loginHolds holds the agents whose login failed and releases those a later
// expiry renewed. The hold fields are then the loop's view of who is held.
func (l *loop) loginHolds(ctx context.Context, results []LoginExpiry, now time.Time, zone *time.Location) error {
	marks, err := l.b.QuotaMarks(ctx)
	if err != nil {
		return err
	}
	l.loginHeld = bus.LoginHeldAgents(marks)
	var work *agentWork
	for _, r := range results {
		field := bus.LoginHoldField(r.Agent)
		recorded, _ := strconv.ParseInt(marks[field], 10, 64)
		switch {
		case r.Status == health.Fail:
			if work == nil {
				if work, err = l.readAgentWork(ctx); err != nil {
					return err
				}
			}
			if err := l.loginHold(ctx, r, recorded, work, now, zone); err != nil {
				return err
			}
			l.loginHeld[r.Agent] = true
		// A renewed login: ok or warn is an expiry still ahead, so later than
		// the held one, which had passed; unknown and fail keep the hold.
		case marks[field] != "" && (r.Status == health.OK || r.Status == health.Warn):
			if work == nil {
				if work, err = l.readAgentWork(ctx); err != nil {
					return err
				}
			}
			done, err := l.loginRelease(ctx, r.Agent, marks[field], work)
			if err != nil {
				return err
			}
			if done {
				delete(l.loginHeld, r.Agent)
			}
		}
	}
	return l.loginHoldsOfOtherClients(ctx)
}

// loginHoldsOfOtherClients releases a login hold whose agent now runs a
// client with no login expiry (master swapped it, foureyes on 3a): its
// credentials are no longer read, so no later expiry would ever release it.
// No resume: the swap resumes the agent itself; while it runs, the hold
// waits. An agent with no snapshot keeps its hold (retire --forget clears it).
func (l *loop) loginHoldsOfOtherClients(ctx context.Context) error {
	names := make([]string, 0, len(l.loginHeld))
	for a := range l.loginHeld {
		names = append(names, a)
	}
	sort.Strings(names)
	for _, a := range names {
		snap, ok := l.snaps[a]
		if !ok || l.cfg.Login.Checked != nil && l.cfg.Login.Checked(snap.Client) { // still read: released by its expiry
			continue
		}
		if swapping, err := l.b.Swapping(ctx, a); err != nil || swapping {
			if err != nil {
				return err
			}
			continue
		}
		if err := l.b.FencedDelHeld(ctx, l.token, a); err != nil {
			return err
		}
		if err := l.b.FencedQuotaDel(ctx, l.token, bus.LoginHoldField(a)); err != nil {
			return err
		}
		delete(l.loginHeld, a)
		l.log(map[string]any{"action": "login-release", "agent": a, "reason": "client " + snap.Client + " has no login expiry"})
	}
	return nil
}

// loginHold writes one agent's hold, idempotent per episode (the expiry).
func (l *loop) loginHold(ctx context.Context, r LoginExpiry, recorded int64, work *agentWork, now time.Time, zone *time.Location) error {
	episode := strconv.FormatInt(r.Expiry.UnixMilli(), 10)
	if recorded != r.Expiry.UnixMilli() {
		if err := l.b.FencedQuotaSet(ctx, l.token, bus.LoginHoldField(r.Agent), episode); err != nil {
			return err
		}
		l.log(map[string]any{"action": "login-hold", "agent": r.Agent, "expiry": r.Expiry.UnixMilli()})
	}
	tasks, branches := work.of(r.Agent)
	if len(tasks) > 0 || l.snaps[r.Agent].State == "working" {
		if _, has := work.held[r.Agent]; !has {
			rec := bus.HeldRecord{Provider: loginProvider, Since: now.UnixMilli(), Tasks: tasks, Branches: branches, Report: work.report[r.Agent]}
			if err := l.b.FencedPutHeld(ctx, l.token, r.Agent, rec); err != nil {
				return err
			}
			work.held[r.Agent] = rec
		}
	}
	at := r.Expiry.In(zone).Format("Mon 15:04")
	notice := fmt.Sprintf("%s's Claude login expired %s: run /login in its pane. Its work is held (master told).", r.Agent, at)
	if r.Agent == l.cfg.EscalateTo {
		notice = fmt.Sprintf("%s's Claude login expired %s: run /login in its pane. Its work is held.", r.Agent, at)
	}
	if _, err := l.b.FencedNotifyOnceForPerson(ctx, l.token, loginExpiredAgentMark(r.Agent), episode, dispatchFrom, notice); err != nil {
		return err
	}
	if r.Agent == l.cfg.EscalateTo {
		return nil // master gets no wakes; the person's notice names it
	}
	_, err := l.b.FencedCmdOnce(ctx, l.token, runHeldMark(r.Agent), episode, dispatchFrom, l.cfg.EscalateTo, bus.CmdDirective,
		loginHeldText(r.Agent, at, tasks, branches), false)
	return err
}

// loginRelease resumes agent after a /login: the resume directive (when it
// had work), its {p}:held field, then its hold field last. done: all written.
func (l *loop) loginRelease(ctx context.Context, agent, episode string, work *agentWork) (bool, error) {
	tasks, _ := work.of(agent)
	if rec, has := work.held[agent]; has && len(rec.Tasks) > 0 {
		tasks = rec.Tasks
	}
	_, had := work.held[agent]
	if had || len(tasks) > 0 || l.snaps[agent].State == "working" {
		if swapping, err := l.b.Swapping(ctx, agent); err != nil || swapping {
			return false, err // a swap sends its own resume: the hold waits for it
		}
		if _, err := l.b.FencedCmdOnce(ctx, l.token, resumeMark(loginProvider, agent), episode, dispatchFrom, agent, bus.CmdDirective,
			loginResumeText(tasks), false); err != nil {
			return false, err
		}
	}
	if err := l.b.FencedDelHeld(ctx, l.token, agent); err != nil {
		return false, err
	}
	if err := l.b.FencedQuotaDel(ctx, l.token, bus.LoginHoldField(agent)); err != nil {
		return false, err
	}
	l.log(map[string]any{"action": "login-release", "agent": agent})
	return true, nil
}

// loginHeldText is master's directive.
func loginHeldText(agent, at string, tasks, branches []string) string {
	head := fmt.Sprintf("%s is held (Claude login expired %s, released after /login): ", agent, at)
	if len(tasks) == 0 {
		return truncRunes(head+"nothing accepted.", bus.CmdMaxRunes)
	}
	shown := make([]string, len(tasks))
	for i, t := range tasks {
		shown[i] = t
		if branches[i] != "" {
			shown[i] += " (" + branches[i] + ")"
		}
	}
	return truncRunes(head+"accepted "+strings.Join(shown, ", ")+". Reassign it, or wait.", bus.CmdMaxRunes)
}

// loginResumeText is the resume directive after a /login, worded as the
// quota release's (resumeText).
func loginResumeText(tasks []string) string {
	t := resumeText(loginProvider, tasks)
	return truncRunes(strings.Replace(t, "Your provider's limit (login) has reset.", "Your Claude login is renewed.", 1), bus.CmdMaxRunes)
}

// agentWork is what the agents hold now: their accepted tasks and
// branches (board), their last report, and the {p}:held records.
type agentWork struct {
	tasks  map[string][]string
	branch map[string]string // task → branch
	report map[string]string // agent → its last report id
	held   map[string]bus.HeldRecord
}

// agentWorkReports is how many recent reports are searched for an agent's last.
const agentWorkReports = 500

func (l *loop) readAgentWork(ctx context.Context) (*agentWork, error) {
	w := &agentWork{tasks: map[string][]string{}, branch: map[string]string{}, report: map[string]string{}}
	board, err := l.b.Board(ctx)
	if err != nil {
		return nil, err
	}
	for task, e := range board {
		if e.State == "accepted" && e.Owner != "" {
			w.tasks[e.Owner] = append(w.tasks[e.Owner], task)
			w.branch[task] = e.Branch
		}
	}
	reports, err := l.b.Reports(ctx, agentWorkReports)
	if err != nil {
		return nil, err
	}
	for _, e := range reports { // oldest first: the last one wins
		w.report[e.Agent] = e.ID
	}
	if w.held, err = l.b.Held(ctx); err != nil {
		return nil, err
	}
	return w, nil
}

// of is agent's accepted tasks, sorted, and their branches ("" none).
func (w *agentWork) of(agent string) (tasks, branches []string) {
	tasks = append([]string(nil), w.tasks[agent]...)
	sort.Strings(tasks)
	branches = make([]string, len(tasks))
	for i, t := range tasks {
		branches[i] = w.branch[t]
	}
	return tasks, branches
}
