package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Board columns: owner, state and age are fixed; the task fits its longest
// name plus a gap, never more than what the others leave (A2: on a wide
// screen a full-width task column pushed them to the far right).
const (
	boardOwnerW = 10
	boardStateW = 11
	boardAgeW   = 5
)

func boardTaskW(m map[string]bus.BoardEntry, width int) int {
	need := Width("TASK")
	for t := range m {
		need = max(need, Width(Preview(t)))
	}
	return max(1, min(need+2, width-boardOwnerW-boardStateW-boardAgeW))
}

// boardAlertW is the alert column's width when one row has an alert: the
// longest alert, "⚠ no acceptance 59m", and its gap (21 cells; the mock-up's
// 19 showed only "no reply"), 12 below 100 columns (views-redesign §3,
// "⚠ no reply…").
func boardAlertW(width int) int {
	if width < 100 {
		return 12
	}
	return 21
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

// boardOpts are what the Board's rows read besides the board: the two alert
// settings (0 off), whether d shows the done tasks, and master's liveness.
type boardOpts struct {
	noAccept, noReply time.Duration // board.no_accept_after, board.no_reply_after
	showDone          bool          // d: the done tasks, after the others
	masterDown        bool          // no master to cancel or reassign (masterDown)
}

// boardView is the Board panel: its rows, column header, frame label and
// footer (views-redesign §3).
type boardView struct {
	items                 []ListItem
	header, label, footer Line
}

// boardAlert is a tracked request's alert and since when, "" when on time
// (views-redesign §3): its body no longer in the stream (body lost, at once);
// sent and not accepted past noAccept (no acceptance); accepted with no
// reply past noReply (no reply). A setting at 0 is off; a closed request has
// none.
func boardAlert(r request, now time.Time, o boardOpts) (string, time.Time) {
	if !r.Tracked || !r.waiting() {
		return "", time.Time{}
	}
	if r.Availability == "missing" {
		at := r.Created
		if at.IsZero() {
			at = r.Updated
		}
		return "body lost", at
	}
	if r.Accepted.IsZero() {
		if r.State == "requested" && o.noAccept > 0 && !r.Created.IsZero() && now.Sub(r.Created) >= o.noAccept {
			return "no acceptance " + humanAge(now.Sub(r.Created)), r.Created
		}
		return "", time.Time{}
	}
	if r.Responded.IsZero() && o.noReply > 0 && now.Sub(r.Accepted) >= o.noReply {
		return "no reply " + humanAge(now.Sub(r.Accepted)), r.Accepted
	}
	return "", time.Time{}
}

// masterDown says the pilot can act on no request: no entry, or its terminal
// gone from the holder (bus.ShownState's no terminal). Self-repair relaunches
// it; busmon asks nothing of anyone (views-redesign §3, Q8).
func masterDown(agents map[string]*agentState, pilot string) bool {
	a := agents[pilot]
	return a == nil || bus.ShownState(a.state, a.pane, a.host.termsRead, a.host.live) == bus.StateNoTerminal
}

// boardViewOf lists the tasks, one line each: the alerts first, oldest alert
// first, then the open tasks most recently updated first, then (d) the done
// ones. The alert column exists only while a row shown has an alert, never a
// column of blanks (the person's Q1). The branch lives in the task detail.
func boardViewOf(m map[string]bus.BoardEntry, now time.Time, width int, o boardOpts) boardView {
	type row struct {
		task    string
		e       bus.BoardEntry
		r       request
		alert   string
		alertAt time.Time
	}
	reqOf := map[string]request{}
	for _, r := range boardRequests(m) {
		reqOf[r.Task] = r
	}
	var rows []row
	open, done := 0, 0
	for t, e := range m {
		if e.State == "done" {
			done++
			if !o.showDone {
				continue
			}
		} else {
			open++
		}
		r := reqOf[t]
		a, at := boardAlert(r, now, o)
		rows = append(rows, row{task: t, e: e, r: r, alert: a, alertAt: at})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if (a.alert != "") != (b.alert != "") {
			return a.alert != ""
		}
		if a.alert != "" && !a.alertAt.Equal(b.alertAt) {
			return a.alertAt.Before(b.alertAt)
		}
		if (a.e.State == "done") != (b.e.State == "done") {
			return b.e.State == "done"
		}
		if a.e.Updated != b.e.Updated {
			return a.e.Updated > b.e.Updated
		}
		return a.task < b.task
	})
	alerts := 0
	shown := map[string]bus.BoardEntry{}
	for _, r := range rows {
		shown[r.task] = r.e
		if r.alert != "" {
			alerts++
		}
	}
	alertW := 0
	if alerts > 0 {
		alertW = boardAlertW(width)
	}
	taskW := max(1, boardTaskW(shown, width-alertW))
	var v boardView
	head := Line{}
	if alertW > 0 {
		head = append(head, col("ALERT", alertW, RoleDim))
	}
	v.header = FitLine(append(head, col("TASK", taskW, RoleDim), col("OWNER", boardOwnerW, RoleDim),
		col("STATE", boardStateW, RoleDim), Seg{Text: "AGE", Role: RoleDim}), width)
	v.label = Line{{Text: fmt.Sprintf("%d open %s %d done (d)", open, GlyphNone, done), Role: RoleMuted}}
	foot := []string{"enter diagnostics", "d done"}
	if alerts > 0 {
		foot = append(foot, "resend (dispatch)")
		if !o.masterDown {
			foot = append(foot, "cancel/reassign (master)")
		}
	}
	v.footer = Line{{Text: strings.Join(foot, " "+GlyphNone+" "), Role: RoleMuted}}
	if len(rows) == 0 {
		word := "no task on the board"
		if done > 0 {
			word = fmt.Sprintf("no open task (d shows %d done)", done)
		}
		v.items = []ListItem{{Line: FitLine(Line{{Text: word, Role: RoleMuted}}, width)}}
		return v
	}
	for _, r := range rows {
		state, role, why := r.e.State, boardStateRole(r.e.State), ""
		if r.e.Request != nil && r.e.Request.ClosedUnaccepted != "" { // done, but nothing delivered (request-lost-body)
			state, role, why = "closed", RoleAttention, "  "+r.e.Request.ClosedUnaccepted+", nothing delivered"
		}
		l := Line{}
		if alertW > 0 {
			text := ""
			if r.alert != "" {
				text = GlyphWarn + " " + r.alert
			}
			l = append(l, col(text, alertW, RoleAttention))
		}
		l = append(l, col(Preview(r.task), taskW, RoleText), col(Preview(r.e.Owner), boardOwnerW, RoleMuted),
			col(Preview(state), boardStateW, role),
			Seg{Text: humanAge(now.Sub(time.Unix(r.e.Updated, 0))), Role: RoleMuted}, Seg{Text: Preview(why), Role: RoleAttention})
		if r.alert != "" && o.masterDown {
			l = append(l, Seg{Text: "  master is down; self-repair relaunches it", Role: RoleAttention})
		}
		v.items = append(v.items, ListItem{Key: r.task, Line: FitLine(l, width), Selectable: true})
	}
	return v
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
