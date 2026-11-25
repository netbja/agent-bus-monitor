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
	return 34 // "no acceptance 40m since request" whole: the alert says what it measures (board-drift §7)
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
	noAccept, noReply time.Duration          // board.no_accept_after, board.no_reply_after
	showDone          bool                   // d: the done tasks, after the others
	masterDown        bool                   // no master to cancel or reassign (masterDown)
	between           bool                   // only the rows between agents: no request to or from self (Work's line)
	self              string                 // the person running busmon
	agents            map[string]*agentState // their wip facts: a queued request's waits: tag (wip-limit §3.5)
}

// boardView is the Board panel: its rows, column header, frame label and
// footer (views-redesign §3).
type boardView struct {
	items                 []ListItem
	header, label, footer Line
}

// boardAlert is a tracked request's alert and since when, "" when on time
// (views-redesign §3), each naming what it measures from (board-drift §7):
// its body no longer in the stream (body lost, at once, with the request's
// age); sent and not accepted past noAccept (no acceptance … since request);
// accepted, still accepted, with no reply past noReply (no reply … since
// accept): a request in review waits on its reviewers, a blocked one on
// someone else. A setting at 0 is off; a closed request has none.
func boardAlert(r request, now time.Time, o boardOpts) (string, time.Time) {
	if !r.Tracked || !r.waiting() {
		return "", time.Time{}
	}
	if r.Availability == "missing" {
		at := r.Created
		if at.IsZero() {
			at = r.Updated
		}
		return "body lost (request " + humanAge(now.Sub(at)) + ")", at
	}
	if r.Accepted.IsZero() {
		if r.State == "requested" && o.noAccept > 0 && !r.Created.IsZero() && now.Sub(r.Created) >= o.noAccept {
			return "no acceptance " + humanAge(now.Sub(r.Created)) + " since request", r.Created
		}
		return "", time.Time{}
	}
	if r.State == "accepted" && r.Responded.IsZero() && o.noReply > 0 && now.Sub(r.Accepted) >= o.noReply {
		return "no reply " + humanAge(now.Sub(r.Accepted)) + " since accept", r.Accepted
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
		if q := e.Request; o.between && q != nil && (q.From == o.self || q.Target == o.self) {
			continue
		}
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
		col("STATE", boardStateW, RoleDim), Seg{Text: "CHANGED", Role: RoleDim}), width) // the last transition (board-drift §7)
	v.label = Line{{Text: fmt.Sprintf("%d open %s %d done (d)", open, GlyphNone, done), Role: RoleMuted}}
	if o.between {
		v.label = append(Line{{Text: "between agents " + GlyphNone + " ", Role: RoleAccent}}, v.label...)
	}
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
		if w := waitsTag(r.r, o.agents); w != "" {
			l = append(l, Seg{Text: "  " + w, Role: RoleMuted})
		}
		if r.alert != "" && o.masterDown {
			l = append(l, Seg{Text: "  master is down; self-repair relaunches it", Role: RoleAttention})
		}
		v.items = append(v.items, ListItem{Key: r.task, Line: FitLine(l, width), Selectable: true})
	}
	return v
}
