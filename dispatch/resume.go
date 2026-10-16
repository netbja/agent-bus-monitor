package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
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
