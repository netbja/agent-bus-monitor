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
	"github.com/netbja/agent-bus-monitor/quota"
)

// Resume after a hold (spec 2026-10-16-quota-resume-and-fallback-design.md
// §3): the dispatcher wakes an agent for unread messages only, so a turn cut
// by a provider's limit is invisible to it. When the hold of provider p ends,
// every agent of p with accepted work on the board or a declared `working` is
// told to resume, once per episode (the hold's reset R), and the escalation
// target gets one wake=no notice naming them.

// resumeTasksShown bounds the tasks named in one directive.
const resumeTasksShown = 5

// resumeMark is the {p}:quota field of one recipient's resume in an episode.
func resumeMark(provider, agent string) string { return "resumed:" + provider + ":" + agent }

// resumeNoticeMark is the field of the escalation target's notice.
func resumeNoticeMark(provider string) string { return "resumed:" + provider + ":@notice" }

// resumeText is the directive to one agent; tasks are cut to
// resumeTasksShown, the whole to CmdMaxRunes.
func resumeText(provider string, tasks []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Your provider's limit (%s) has reset. ", provider)
	switch {
	case len(tasks) == 0:
		sb.WriteString("You declared working when it stopped you.")
	default:
		shown := tasks
		if len(shown) > resumeTasksShown {
			shown = shown[:resumeTasksShown]
		}
		sb.WriteString("Work interrupted by it: " + strings.Join(shown, ", "))
		if n := len(tasks) - len(shown); n > 0 {
			fmt.Fprintf(&sb, " and %d more", n)
		}
		sb.WriteString(".")
	}
	sb.WriteString(" Resume it where you stopped, then report as usual.")
	return truncRunes(sb.String(), bus.CmdMaxRunes)
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}

// resumeAfterHold sends the resume directives of provider p's episode R. One
// recipient's failure is logged and does not stop the others, but makes the
// release incomplete (complete false): the caller keeps the hold mark so the
// next pass retries. A lost lease ends the pass.
func (l *loop) resumeAfterHold(ctx context.Context, p string, R int64, snaps map[string]bus.AgentSnapshot) (complete bool, err error) {
	if R == 0 {
		return true, nil
	}
	board, err := l.b.Board(ctx)
	if err != nil {
		if errors.Is(err, bus.ErrNotHolder) {
			return false, err
		}
		l.logError("", "resume", err)
		return false, nil
	}
	complete = true
	accepted := map[string][]string{}
	for task, e := range board {
		if e.State == "accepted" && e.Owner != "" {
			accepted[e.Owner] = append(accepted[e.Owner], task)
		}
	}
	coord := snaps[l.cfg.EscalateTo].Provider
	episode := strconv.FormatInt(R, 10)
	names := make([]string, 0, len(snaps))
	for a := range snaps {
		names = append(names, a)
	}
	sort.Strings(names)
	var told []string
	for _, a := range names {
		s := snaps[a]
		if s.Provider != p || !bus.ValidName(a) {
			continue
		}
		if a == l.cfg.EscalateTo && p == coord {
			continue // the coordinator's quota pause sends its own resume directive
		}
		tasks := accepted[a]
		sort.Strings(tasks)
		if len(tasks) == 0 && s.State != "working" {
			continue
		}
		// Under a swap intent the swap sends this resume itself, after its
		// proof, through the same mark (§3.3): no mark here.
		// The hold mark stays until the swap is over (complete false): if it
		// fails, the next pass resumes the agent it leaves.
		if swapping, serr := l.b.Swapping(ctx, a); serr != nil || swapping {
			if serr != nil {
				l.logError(a, "resume", serr)
			}
			complete = false
			continue
		}
		var sent bool
		if l.h.resumeErr != nil {
			err = l.h.resumeErr(a)
		}
		if err == nil {
			sent, err = l.b.FencedCmdOnce(ctx, l.token, resumeMark(p, a), episode, dispatchFrom, a,
				bus.CmdDirective, resumeText(p, tasks), false)
		}
		if errors.Is(err, bus.ErrNotHolder) {
			return false, err
		}
		if err != nil {
			l.logError(a, "resume", err)
			complete = false
			continue
		}
		told = append(told, a)
		if sent {
			l.log(map[string]any{"agent": a, "action": "quota-resume", "provider": p, "tasks": len(tasks)})
		}
	}
	if len(told) == 0 || !bus.ValidName(l.cfg.EscalateTo) {
		return complete, nil
	}
	note := fmt.Sprintf("%s's limit has reset: told to resume their interrupted work: %s.", p, strings.Join(told, ", "))
	_, err = l.b.FencedCmdOnce(ctx, l.token, resumeNoticeMark(p), episode, dispatchFrom, l.cfg.EscalateTo,
		bus.CmdDirective, truncRunes(note, bus.CmdMaxRunes), true)
	if errors.Is(err, bus.ErrNotHolder) {
		return false, err
	}
	if err != nil {
		l.logError(l.cfg.EscalateTo, "resume notice", err)
		return false, nil
	}
	return complete, nil
}

// clearEpisodeMarks drops provider p's marks of other episodes when a hold
// with reset R is recorded; a hold that flaps back to the same reset keeps
// them, so it sends nothing again.
func (l *loop) clearEpisodeMarks(ctx context.Context, p string, R int64, marks map[string]string) error {
	episode := strconv.FormatInt(R, 10)
	var fields []string
	for f, v := range marks {
		if (strings.HasPrefix(f, "resumed:"+p+":") || f == "longhold:"+p) && v != episode {
			fields = append(fields, f)
		}
	}
	if len(fields) == 0 {
		return nil
	}
	sort.Strings(fields)
	return l.b.FencedQuotaDel(ctx, l.token, fields...)
}

// longHoldMark is the {p}:quota field of a long hold's one message.
func longHoldMark(provider string) string { return "longhold:" + provider }

// inFlight lists provider p's agents with accepted work or a declared working,
// each with its accepted tasks, sorted by name.
func inFlight(p string, snaps map[string]bus.AgentSnapshot, board map[string]bus.BoardEntry) []string {
	accepted := map[string][]string{}
	for task, e := range board {
		if e.State == "accepted" && e.Owner != "" {
			accepted[e.Owner] = append(accepted[e.Owner], task)
		}
	}
	var out []string
	for a, s := range snaps {
		if s.Provider != p || !bus.ValidName(a) {
			continue
		}
		tasks := accepted[a]
		sort.Strings(tasks)
		switch {
		case len(tasks) > 0:
			if len(tasks) > resumeTasksShown {
				tasks = append(tasks[:resumeTasksShown], fmt.Sprintf("%d more", len(accepted[a])-resumeTasksShown))
			}
			out = append(out, a+" ("+strings.Join(tasks, ", ")+")")
		case s.State == "working":
			out = append(out, a+" (working)")
		}
	}
	sort.Strings(out)
	return out
}

// longHold (v0.15 §5.1): a hold whose reset is further away than LongHold is
// for a person to decide. For another provider than the coordinator's,
// master gets one directive (it asks whether to swap); for the coordinator's
// own provider master is held and cannot ask, so the person gets one notify.
// Once per episode (mark longhold:<p> = the reset), entry and mark together.
func (l *loop) longHold(ctx context.Context, p string, w quota.Window, now time.Time, coord string,
	snaps map[string]bus.AgentSnapshot) error {
	q := l.cfg.Quota
	if q.LongHold < 0 || w.ResetsAt == 0 || w.Reset().Sub(now) <= q.LongHold || !bus.ValidName(l.cfg.EscalateTo) {
		return nil
	}
	episode := strconv.FormatInt(w.ResetsAt, 10)
	marks, err := l.b.QuotaMarks(ctx)
	if err != nil {
		return err
	}
	if marks[longHoldMark(p)] == episode {
		return nil
	}
	until := w.Reset().Local().Format("Mon 02/01 15:04")
	wait := fmt.Sprintf("%dh", int(w.Reset().Sub(now).Round(time.Hour).Hours()))
	var sent bool
	if p == coord {
		msg := fmt.Sprintf("The project is paused until %s (%s, %s window, %s). To continue before, swap %s to "+
			"another provider from a terminal: agentbus roles fallbacks %s, then agentbus --project %s swap %s --profile <fallback>.",
			until, p, w.Name, wait, l.cfg.EscalateTo, l.cfg.EscalateTo, l.b.Project(), l.cfg.EscalateTo)
		sent, err = l.b.FencedNotifyOnce(ctx, l.token, longHoldMark(p), episode, dispatchFrom, truncRunes(msg, bus.CmdMaxRunes))
	} else {
		board, berr := l.b.Board(ctx)
		if berr != nil {
			return berr
		}
		held := inFlight(p, snaps, board)
		who := "none right now"
		if len(held) > 0 {
			who = strings.Join(held, ", ")
		}
		msg := fmt.Sprintf("%s is held until %s (%s, %s window). Held agents with work in flight: %s. "+
			"If their roles have fallback profiles (agentbus roles fallbacks <role>), ask the person whether "+
			"to swap them until the reset.", p, until, wait, w.Name, who)
		sent, err = l.b.FencedCmdOnce(ctx, l.token, longHoldMark(p), episode, dispatchFrom, l.cfg.EscalateTo,
			bus.CmdDirective, truncRunes(msg, bus.CmdMaxRunes), false)
	}
	if err != nil {
		return err
	}
	if sent {
		l.log(map[string]any{"action": "quota-long-hold", "provider": p, "until": w.Reset().UTC().Format(time.RFC3339)})
	}
	return nil
}
