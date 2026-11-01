package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	guardian "github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/plan"
)

const recapHistoryLimit = 1000

type recapReader interface {
	Agents(context.Context) (map[string]bus.AgentSnapshot, error)
	Budgets(context.Context) (map[string]bus.BudgetSnapshot, error)
	Board(context.Context) (map[string]bus.BoardEntry, error)
	Recent(context.Context, []string, int) ([]bus.Event, map[string]string, error)
	ServiceStates(context.Context) (*bus.ServicesInstall, map[string]bus.ServiceRecord, error)
	QuotaMarks(context.Context) (map[string]string, error)
	Pause(context.Context) (*bus.PauseRecord, error)
	Asks(context.Context) ([]bus.Ask, error)
	Verdicts(context.Context, string) ([]bus.Verdict, error)
	SwapLog(context.Context, string, int) ([]bus.SwapEvent, error)
}

type healthTransitionReader interface {
	HealthTransitions(context.Context, string, int64) ([]guardian.Transition, string, error)
}

func readRecap(ctx context.Context, r recapReader, st *shared, since time.Time, human string, unread []bus.Event) recapModel {
	now := time.Now()
	input := recapInput{Now: now, Since: since, Human: human, Unread: unread,
		Errors: map[string]error{}}
	st.mu.Lock()
	input.Agents = copyAgentStates(st.agents)
	input.Board = copyBoard(st.board)
	input.Asks = append([]bus.Ask(nil), st.asks...)
	if st.plan.loaded {
		p := st.plan
		p.rows = append([]plan.Row(nil), st.plan.rows...)
		input.Plan = &p
	}
	input.Services = copyServices(st.svcRecs)
	input.Pause = st.pause
	input.Holds = copyHolds(st.holds)
	st.mu.Unlock()
	if snaps, err := r.Agents(ctx); err != nil {
		input.Errors["Agent snapshot"] = err
	} else {
		input.Agents = recapAgents(snaps, input.Agents)
	}
	if board, err := r.Board(ctx); err != nil {
		input.Errors["Board"] = err
	} else {
		input.Board = board
	}
	if asks, err := r.Asks(ctx); err != nil {
		input.Errors["Open asks"] = err
	} else {
		input.Asks = asks
	}
	if pause, err := r.Pause(ctx); err != nil {
		input.Errors["Pause"] = err
	} else {
		input.Pause = pause
	}
	if _, services, err := r.ServiceStates(ctx); err != nil {
		input.Errors["Services"] = err
	} else {
		input.Services = services
	}
	if marks, err := r.QuotaMarks(ctx); err != nil {
		input.Errors["Quota"] = err
	} else {
		input.Holds = quotaHolds(marks)
	}
	for _, ask := range openAsks(input.Asks) {
		a := input.Agents[ask.From]
		if a != nil {
			a.openAsk, a.askID = ask.Open(), ask.ID
		}
	}
	for _, a := range input.Agents {
		a.paused = input.Pause != nil
		_, a.held = held(input.Holds, a.provider, time.Now())
	}
	if events, _, err := r.Recent(ctx, []string{"cmd"}, recapHistoryLimit); err != nil {
		input.Errors["cmd"] = err
	} else {
		input.Commands = events
	}
	if events, _, err := r.Recent(ctx, []string{"report"}, recapHistoryLimit); err != nil {
		input.Errors["report"] = err
	} else {
		input.Reports = events
	}
	if verdicts, err := r.Verdicts(ctx, ""); err != nil {
		input.Errors["verdict"] = err
	} else {
		input.Verdicts = verdicts
	}
	if swaps, err := r.SwapLog(ctx, "", recapHistoryLimit); err != nil {
		input.Errors["swap"] = err
	} else {
		input.Swaps = swaps
	}
	if hr, ok := r.(healthTransitionReader); ok {
		after := ""
		if !since.IsZero() {
			after = fmt.Sprintf("%d-0", since.UnixMilli()-1)
		}
		if rows, floor, err := hr.HealthTransitions(ctx, after, recapHistoryLimit); err != nil {
			input.Errors["Health"] = err
		} else {
			input.Health, input.HealthFloor = rows, floor
		}
	}
	return buildRecap(input)
}

func copyAgentStates(src map[string]*agentState) map[string]*agentState {
	out := make(map[string]*agentState, len(src))
	for name, a := range src {
		if a != nil {
			copy := *a
			out[name] = &copy
		}
	}
	return out
}

func copyBoard(src map[string]bus.BoardEntry) map[string]bus.BoardEntry {
	out := make(map[string]bus.BoardEntry, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func copyServices(src map[string]bus.ServiceRecord) map[string]bus.ServiceRecord {
	out := make(map[string]bus.ServiceRecord, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func copyHolds(src map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func recapAgents(snaps map[string]bus.AgentSnapshot, previous map[string]*agentState) map[string]*agentState {
	out := make(map[string]*agentState, len(snaps))
	for name, snap := range snaps {
		a := &agentState{state: snap.State, message: snap.Message, registered: true, provider: bus.ProviderFor(snap.Client, snap.Provider)}
		if snap.TS > 0 {
			a.stateAt = time.UnixMilli(snap.TS)
		}
		if old := previous[name]; old != nil && old.state == a.state && old.stateAt.Equal(a.stateAt) {
			// The snapshot has declared state, while answeredAt is derived from retained
			// human commands by the poller. Keep that display-only correlation for the
			// same declaration; never carry it across a newer declaration.
			a.answeredAt = old.answeredAt
		}
		out[name] = a
	}
	return out
}

func firstRecapSelection(rows []recapRow) int {
	for i, row := range rows {
		if row.Selectable {
			return i
		}
	}
	return 0
}

// recapKeepCursor is the cursor on rows after a re-filter: the row it was on
// (by key), else the first selectable row from where it was, else the first.
func recapKeepCursor(old []recapRow, sel int, rows []recapRow) int {
	if sel >= 0 && sel < len(old) {
		if k := old[sel].Key; k != "" {
			for i, row := range rows {
				if row.Key == k {
					return i
				}
			}
		}
		for i := min(sel, len(rows)); i < len(rows); i++ {
			if rows[i].Selectable {
				return i
			}
		}
	}
	return firstRecapSelection(rows)
}

// recapFold folds the For you items older than a day behind one '+ N older'
// line (recap-foryou), unless showOlder (o): a folded row is gone, so the
// cursor never lands on it.
func recapFold(r recapModel, showOlder bool) recapModel {
	if showOlder {
		return r
	}
	out := r
	out.Rows = nil
	n, last := 0, -1
	for _, row := range r.Rows {
		if row.Older {
			n++
			continue
		}
		out.Rows = append(out.Rows, row)
		if row.Section == recapForYou {
			last = len(out.Rows) - 1
		}
	}
	if n > 0 && last >= 0 {
		fold := recapRow{Key: "older", Section: recapForYou, Title: fmt.Sprintf("+ %d older  (o shows them)", n)}
		out.Rows = append(out.Rows[:last+1], append([]recapRow{fold}, out.Rows[last+1:]...)...)
	}
	return out
}

// recapHasOlder reports whether any row folds behind '+ N older'.
func recapHasOlder(r recapModel) bool {
	for _, row := range r.Rows {
		if row.Older {
			return true
		}
	}
	return false
}

// recapItemLine is a For you item in the list format: its unread mark, the
// sender in its colour, its text with (N) when merged, the age right-aligned
// and muted.
func recapItemLine(row recapRow, width int) Line {
	const nameW = 12
	textW := max(8, width-2-2-nameW-2-2-listAgeW)
	text := Preview(row.Text)
	if row.Count > 1 {
		text += fmt.Sprintf(" (%d)", row.Count)
	}
	age := Truncate(row.Age, listAgeW)
	m := markNone
	if row.Unread {
		m = markUnread
	}
	return Line{markCell(m), S(row.NameRole, Fit(Preview(row.Name), nameW)), S(RoleText, "  "+Fit(text, textW)+"  "),
		S(RoleText, strings.Repeat(" ", listAgeW-Width(age))), S(RoleMuted, age)}
}

// recapBody is the recap's text in width cells with the cursor on row cursor,
// and the line that row is on (its reader page draws it).
func recapBody(rows []recapRow, cursor, width int) (lines []Line, cursorLine int) {
	var b body
	lastSection := ""
	for i, row := range rows {
		if row.Section != lastSection {
			if len(b.Lines()) > 0 {
				b.text("\n")
			}
			b.seg(RoleAccent, glyphHorizontal+glyphHorizontal+" "+row.Section+" "+glyphHorizontal+glyphHorizontal).text("\n")
			lastSection = row.Section
		}
		if row.Key == "" && row.Title == row.Section {
			continue
		}
		mark := S(RoleText, "  ")
		if i == cursor && row.Selectable {
			cursorLine = b.at()
			mark = S(RoleAccent, "> ")
		}
		role := RoleText
		if !row.Selectable {
			role = RoleMuted
		}
		if row.Name != "" { // a For you item: one line (recap-foryou)
			b.segs(mark).segs(recapItemLine(row, width)...).text("\n")
			continue
		}
		b.segs(mark).seg(role, Preview(row.Title)).text("\n")
		if row.Detail != "" {
			b.text("    ").seg(RoleMuted, Preview(row.Detail)).text("\n")
		}
	}
	return trimEnd(b.Lines()), cursorLine
}

// recapForScreen is the recap as its page shows it: the For you section only
// when the For you panel is not on screen (the person's answer, busmon pages);
// shown and empty, it says what For you says when nothing is expected.
func recapForScreen(r recapModel, forYouShown bool, empty []Line) recapModel {
	out := r
	out.Rows = nil
	items := 0
	for _, row := range r.Rows {
		if row.Section != recapForYou {
			out.Rows = append(out.Rows, row)
			continue
		}
		if forYouShown && recapInPanel(row) { // only what the panel itself lists (foureyes)
			continue
		}
		if row.Key != "" || row.Title != row.Section {
			items++
		}
		out.Rows = append(out.Rows, row)
	}
	if forYouShown { // a heading left alone, its rows all in the panel, goes too
		kept := 0
		for _, row := range out.Rows {
			if row.Section == recapForYou && (row.Key != "" || row.Title != row.Section) {
				kept++
			}
		}
		if kept == 0 {
			var rows []recapRow
			for _, row := range out.Rows {
				if row.Section != recapForYou {
					rows = append(rows, row)
				}
			}
			out.Rows = rows
		}
	}
	if !forYouShown && items == 0 { // the empty state, right under the section's heading
		var rows []recapRow
		for _, row := range out.Rows {
			rows = append(rows, row)
			if row.Section == recapForYou && row.Title == row.Section {
				for _, l := range empty {
					rows = append(rows, recapRow{Section: recapForYou, Title: l.text()})
				}
			}
		}
		out.Rows = rows
	}
	return out
}

// recapInPanel: a For-you row the For you panel also lists (its unread lines,
// asks and waits); directives owed, blocked agents, plan tasks and read
// errors are only in the recap and always stay.
func recapInPanel(row recapRow) bool {
	for _, p := range []string{"unread:", "ask:", "wait:"} {
		if strings.HasPrefix(row.Key, p) {
			return true
		}
	}
	return false
}
