package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Board columns: the task takes what owner, state and age leave.
const (
	boardOwnerW = 10
	boardStateW = 11
	boardAgeW   = 5
)

func boardTaskW(width int) int { return max(1, width-boardOwnerW-boardStateW-boardAgeW) }

// boardHeader is the Board column header, exactly width cells.
func boardHeader(width int) Line {
	return FitLine(Line{col("task", boardTaskW(width), RoleDim), col("owner", boardOwnerW, RoleDim),
		col("state", boardStateW, RoleDim), {Text: "age", Role: RoleDim}}, width)
}

// boardStateRole colours a board state (spec §3.5); the word is always shown too.
func boardStateRole(state string) Role {
	switch state {
	case "working":
		return RoleOK
	case "requested":
		return RoleAttention
	case "accepted":
		return RoleAccent
	case "blocked":
		return RoleError
	case "done":
		return RoleDone
	}
	return RoleMuted
}

// boardItems lists the tasks, most recently updated first (as the v0.8 pane
// did). The branch lives in the task detail, not in the row.
func boardItems(m map[string]bus.BoardEntry, now time.Time, width int) []ListItem {
	if len(m) == 0 {
		return []ListItem{{Line: FitLine(Line{{Text: "no task on the board", Role: RoleMuted}}, width)}}
	}
	tasks := make([]string, 0, len(m))
	for t := range m {
		tasks = append(tasks, t)
	}
	sort.Slice(tasks, func(i, j int) bool {
		if m[tasks[i]].Updated != m[tasks[j]].Updated {
			return m[tasks[i]].Updated > m[tasks[j]].Updated
		}
		return tasks[i] < tasks[j]
	})
	out := make([]ListItem, 0, len(tasks))
	for _, t := range tasks {
		e := m[t]
		l := Line{col(Preview(t), boardTaskW(width), RoleText), col(Preview(e.Owner), boardOwnerW, RoleMuted),
			col(Preview(e.State), boardStateW, boardStateRole(e.State)),
			{Text: humanAge(now.Sub(time.Unix(e.Updated, 0))), Role: RoleMuted}}
		out = append(out, ListItem{Key: t, Line: FitLine(l, width), Selectable: true})
	}
	return out
}

// boardFrame is the Board frame title and its "D/N done" label.
func boardFrame(m map[string]bus.BoardEntry) (title, label Line) {
	done := 0
	for _, e := range m {
		if e.State == "done" {
			done++
		}
	}
	return Line{{Text: "Board", Role: RoleText}}, Line{{Text: fmt.Sprintf("%d/%d done", done, len(m)), Role: RoleMuted}}
}

// forPerson keeps what is addressed to the person running busmon: tracked
// requests and untracked threads whose target is self. Threads between agents
// stay in the Requests list but are theirs to answer, not the person's.
func forPerson(reqs []request, threads []openThread, self string) ([]request, []openThread) {
	var rs []request
	for _, r := range reqs {
		if r.Target == self {
			rs = append(rs, r)
		}
	}
	var ts []openThread
	for _, t := range threads {
		if t.Target == self {
			ts = append(ts, t)
		}
	}
	return rs, ts
}

// requestsLabel is the Requests frame label: what waits for you first, the
// agents' own open threads muted.
func requestsLabel(reqs []request, threads []openThread, self string, questions int) Line {
	base := waitingCount(forPerson(reqs, threads, self))
	others := waitingCount(reqs, threads) - base
	mine := base + questions
	l := Line{}
	if mine > 0 {
		l = append(l, Seg{Text: fmt.Sprintf("%d for you", mine), Role: RoleAttention})
	}
	if others > 0 {
		if len(l) > 0 {
			l = append(l, Seg{Text: " " + GlyphNone + " ", Role: RoleMuted})
		}
		l = append(l, Seg{Text: fmt.Sprintf("%d between agents", others), Role: RoleMuted})
	}
	if len(l) == 0 {
		l = Line{{Text: "nothing waiting", Role: RoleMuted}}
	}
	return l
}

// waitingCount is what the header and the Requests title count: tracked
// requests still waiting plus untracked threads with no answer (openThreads
// already leaves out the threads a tracked request accounts for).
func waitingCount(reqs []request, threads []openThread) int {
	n := len(threads)
	for _, r := range trackedOnly(reqs) {
		if r.waiting() {
			n++
		}
	}
	return n
}

// requestItems is the Requests panel: tracked requests still waiting, then the
// untracked threads, each under a labelled section header. Every entry is two
// items: a selectable head and a non-selectable fact line.
func requestItems(reqs []request, threads []openThread, now time.Time, width int) []ListItem {
	var tracked []request
	for _, r := range trackedOnly(reqs) {
		if r.waiting() {
			tracked = append(tracked, r)
		}
	}
	sort.SliceStable(tracked, func(i, j int) bool { return requestLess(tracked[i], tracked[j]) })
	if len(tracked) == 0 && len(threads) == 0 {
		return []ListItem{{Line: FitLine(Line{{Text: "nothing waiting", Role: RoleMuted}}, width)}}
	}
	fit := func(l Line) Line { return FitLine(l, width) }
	var out []ListItem
	if len(tracked) > 0 {
		out = append(out, ListItem{Line: fit(Line{{Text: fmt.Sprintf("tracked requests %s %d waiting", GlyphNone, len(tracked)), Role: RoleDim}})})
	}
	for _, r := range tracked {
		head := Line{{Text: Preview(target(r)), Role: RoleAccent}, {Text: " " + GlyphArrowL + " ", Role: RoleMuted},
			{Text: Preview(r.From), Role: RoleText}, {Text: "  " + Preview(r.Task), Role: RoleText}}
		since := r.Created
		if since.IsZero() {
			since = r.Updated
		}
		deadline, _ := deadlineWords(r, now)
		facts := Line{{Text: "  ", Role: RoleMuted}, {Text: Preview(r.State), Role: boardStateRole(r.State)},
			{Text: fmt.Sprintf(" %s %s %s %s", GlyphNone, humanAge(now.Sub(since)), GlyphNone, deadline), Role: RoleMuted}}
		if r.State == "blocked" && r.BlockedReason != "" {
			facts = append(facts, Seg{Text: " " + GlyphNone + " ", Role: RoleMuted}, Seg{Text: Preview(r.BlockedReason), Role: RoleError})
		}
		out = append(out, ListItem{Key: "req:" + r.Task, Line: fit(head), Selectable: true}, ListItem{Line: fit(facts)})
	}
	if len(threads) > 0 {
		out = append(out, ListItem{Line: fit(Line{{Text: fmt.Sprintf("untracked threads, no answer in retained history %s %d", GlyphNone, len(threads)), Role: RoleDim}})})
	}
	for _, t := range threads {
		head := Line{{Text: Preview(t.Type) + "  ", Role: RoleAttention}, {Text: Preview(t.From), Role: RoleText},
			{Text: " " + GlyphArrowR + " ", Role: RoleMuted}, {Text: Preview(t.Target), Role: RoleAccent},
			{Text: "  " + Preview(t.Message), Role: RoleText}}
		facts := Line{{Text: "  asked " + humanAge(now.Sub(t.At)) + " ago", Role: RoleMuted}}
		out = append(out, ListItem{Key: "thr:" + t.ID, Line: fit(head), Selectable: true}, ListItem{Line: fit(facts)})
	}
	return out
}
