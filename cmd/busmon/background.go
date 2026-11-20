package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// backgroundReader reads agents' background work (progress-line §4): the
// bus has it; a reader without it shows none.
type backgroundReader interface {
	Backgrounds(ctx context.Context, agents []string) (map[string]bus.Background, error)
}

// bgTasksReader reads what the agents' own hooks said of their background
// tasks (background-work-visible §4, step 3); a reader without it shows none.
type bgTasksReader interface {
	BgTasksOf(ctx context.Context, agents []string) (map[string]bus.BgTasks, error)
}

// liveTasks is the agent's hooks record when it is of its current process
// and within its hour (background-work-visible §3: stale entries are not
// shown); nil otherwise.
func liveTasks(a *agentState, now time.Time) *bus.BgTasks {
	r := a.bgTasks
	if r == nil || a.pid == 0 || r.PID != a.pid || now.Sub(time.UnixMilli(r.At)) > bus.BgTasksTTL {
		return nil
	}
	return r
}

// observedProcs is the dispatcher's count of the agent's active descendants
// and the oldest one's first sight, from a fresh observation of its current
// process; 0 otherwise.
func observedProcs(a *agentState, now time.Time) (int, time.Time) {
	if !a.obsOwnPID || a.obsAt.IsZero() || now.Sub(a.obsAt) > observedFresh || a.obsBgProcs <= 0 {
		return 0, time.Time{}
	}
	return a.obsBgProcs, a.obsBgOldest
}

// oldestTask is the first sight of the record's oldest task.
func oldestTask(r *bus.BgTasks) time.Time {
	var at int64
	for _, t := range r.Tasks {
		if t.FirstSeen > 0 && (at == 0 || t.FirstSeen < at) {
			at = t.FirstSeen
		}
	}
	if at == 0 {
		return time.Time{}
	}
	return time.UnixMilli(at)
}

// withAge is s and the age since at, when known.
func withAge(s string, at, now time.Time) string {
	if at.IsZero() {
		return s
	}
	return s + " " + humanAge(now.Sub(at))
}

// observedMark is the mark of work observed but not declared: the hooks'
// tasks counted together ("3 bg tasks 14m"), else the process source's lower
// bound ("bg ≥1? 9m"); "" when neither sees any.
func observedMark(a *agentState, now time.Time) string {
	if r := liveTasks(a, now); r != nil && len(r.Tasks) > 0 {
		word := "bg tasks"
		if len(r.Tasks) == 1 {
			word = "bg task"
		}
		return withAge(strconv.Itoa(len(r.Tasks))+" "+word, oldestTask(r), now)
	}
	if n, oldest := observedProcs(a, now); n > 0 {
		return withAge("bg "+GlyphAtLeast+"1?", oldest, now)
	}
	return ""
}

// backgroundText is an agent's background mark as its row shows it, one per
// agent (background-work-visible §3): its declared text ("… 2 checks running
// 4m"), else the work observed ("… 3 bg tasks 14m", "… bg ≥1? 9m"), then ◷
// when it has scheduled wakeups; "… ?" when its latest read failed; "" when
// it has none.
func backgroundText(a *agentState, now time.Time) string {
	if a != nil && a.bgFailed {
		return GlyphEllipsis + " ?"
	}
	if a == nil {
		return ""
	}
	t := ""
	switch {
	case a.bg != nil:
		t = GlyphEllipsis + " " + withAge(Preview(a.bg.Text), a.bg.Since, now)
	default:
		if m := observedMark(a, now); m != "" {
			t = GlyphEllipsis + " " + m
		}
	}
	if r := liveTasks(a, now); r != nil && len(r.Crons) > 0 {
		if t != "" {
			t += " "
		}
		t += GlyphClock
	}
	return t
}

// hasBackground: the agent has running work to show, declared or observed
// (a cron alone is not running work).
func hasBackground(a *agentState, now time.Time) bool {
	return a != nil && !a.bgFailed && (a.bg != nil || observedMark(a, now) != "")
}

// backgroundChip is the header's chip: "… master: 2 checks running +1", the
// pilot's work first, then the declared before the observed, then by name;
// an agent with observed work alone reads "bg"; "" when no agent has any.
func backgroundChip(agents map[string]*agentState, pilot string, now time.Time) string {
	var names []string
	for n, a := range agents {
		if hasBackground(a, now) {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == pilot) != (names[j] == pilot) {
			return names[i] == pilot
		}
		if di, dj := agents[names[i]].bg != nil, agents[names[j]].bg != nil; di != dj {
			return di
		}
		return names[i] < names[j]
	})
	text := "bg"
	if b := agents[names[0]].bg; b != nil {
		text = b.Text
	}
	chip := GlyphEllipsis + " " + names[0] + ": " + text
	if len(names) > 1 {
		chip += " +" + strconv.Itoa(len(names)-1)
	}
	return chip
}

// liveNames is the program names read live from the holder, once each,
// sorted, each through Preview: never stored (background-work-visible Q3).
func liveNames(procs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range procs {
		n := Preview(p)
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// backgroundSection is the agent page's Background section
// (background-work-visible §3): the declared text, the tasks observed, their
// source, the programs running under the agent now (v, the page's live read
// of its terminal; nil before the first), the scheduled wakeups. Nothing when
// there is none of these.
func backgroundSection(a *agentState, v *termView, now time.Time) []Line {
	r := liveTasks(a, now)
	n, oldest := observedProcs(a, now)
	var names []string
	if v != nil && v.found {
		names = liveNames(v.procs)
	}
	hasTasks := r != nil && len(r.Tasks) > 0
	if a.bg == nil && !hasTasks && n == 0 && (r == nil || len(r.Crons) == 0) {
		return nil
	}
	var b body
	b.seg(RoleAccent, "Background").seg(RoleMuted, " "+GlyphNone+" the state stays what the agent declared").text("\n")
	if d := a.bg; d != nil {
		by := ""
		if d.By != "" {
			by = ", set by " + Preview(d.By)
		}
		b.seg(RoleText, Preview(d.Text)).seg(RoleMuted, " "+GlyphNone+" declared "+humanAge(now.Sub(d.Since))+" ago"+by).text("\n")
	}
	switch {
	case hasTasks:
		for _, t := range r.Tasks {
			what := Preview(t.Type)
			if t.AgentType != "" {
				what += " " + Preview(t.AgentType)
			}
			line := what + " " + GlyphNone + " " + Preview(t.Status)
			if t.FirstSeen > 0 {
				line += " " + GlyphNone + " " + humanAge(now.Sub(time.UnixMilli(t.FirstSeen)))
			}
			b.seg(RoleText, line).text("\n")
		}
		b.seg(RoleMuted, "from Claude Code's hooks, last report "+humanAge(now.Sub(time.UnixMilli(r.At)))+" ago ("+Preview(r.Event)+")").text("\n")
	case n > 0:
		b.seg(RoleText, withAge(fmt.Sprintf("at least 1 process running (%d active)", n), oldest, now)).text("\n")
		b.seg(RoleMuted, "from the process tree: a count of processes, not of tasks").text("\n")
	}
	if len(names) > 0 {
		line := "running under it now: "
		for i, nm := range names {
			if i > 0 {
				line += ", "
			}
			line += nm
		}
		b.seg(RoleText, line).seg(RoleMuted, " (read live, never stored)").text("\n")
	}
	if r != nil {
		for _, c := range r.Crons {
			kind := "once"
			if c.Recurring {
				kind = "recurring"
			}
			b.seg(RoleText, GlyphClock+" "+Preview(c.Schedule)).seg(RoleMuted, " "+GlyphNone+" "+kind+" (a scheduled wakeup, not running work)").text("\n")
		}
	}
	if hasTasks || n > 0 {
		b.seg(RoleMuted, "ages are counted from when agentbus first saw the work, not from its start").text("\n")
	}
	b.text("\n")
	return b.Lines()
}
