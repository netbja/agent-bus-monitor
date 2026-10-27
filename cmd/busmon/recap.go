package main

import (
	"context"
	"fmt"
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

// recapBody is the recap's text with the cursor on row cursor, and the line
// that row is on (its reader page draws it).
func recapBody(rows []recapRow, cursor int) (lines []Line, cursorLine int) {
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
		b.segs(mark).seg(role, Preview(row.Title)).text("\n")
		if row.Detail != "" {
			b.text("    ").seg(RoleMuted, Preview(row.Detail)).text("\n")
		}
	}
	return trimEnd(b.Lines()), cursorLine
}
