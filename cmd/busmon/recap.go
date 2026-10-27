package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
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

func (u *ui) maybeAutoRecap() {
	if u.recapAutoDone {
		return
	}
	u.st.mu.Lock()
	ready := !u.st.lastPollAt.IsZero()
	u.st.mu.Unlock()
	if !ready {
		return
	}
	u.recapAutoDone = true
	now := time.Now()
	auto := !u.priorVisit.IsZero() && u.settings.AutoAfter > 0 && now.Sub(u.priorVisit) >= u.settings.AutoAfter
	if u.cache != nil {
		u.cache.LastVisit = now.UnixMilli()
		if err := u.cache.save(u.cachePath); err != nil {
			u.st.mu.Lock()
			u.st.configNote = joinNote(u.st.configNote, "last visit not saved")
			u.st.mu.Unlock()
		}
	}
	if auto {
		u.requestRecap(true)
	}
}

func (u *ui) requestRecap(auto bool) {
	if u.recapLoading {
		return
	}
	r, ok := u.w.(recapReader)
	if !ok {
		u.recap = buildRecap(recapInput{Now: time.Now(), Since: u.priorVisit, Human: u.self,
			Errors: map[string]error{"Bus": fmt.Errorf("recap reader unavailable")}})
		u.recapPending = true
		u.maybeOpenRecap()
		return
	}
	u.recapLoading = true
	since, human := u.priorVisit, u.self
	unread := u.unreadEvents() // the UI goroutine's state, read before the async read
	if auto {                  // already on the event loop (key or tick): no queued update
		u.flash, u.flashErr = "building project recap"+GlyphEllipsis, false
	}
	go func() {
		ctx, cancel := context.WithTimeout(u.ctx, 7*time.Second)
		defer cancel()
		model := readRecap(ctx, r, u.st, since, human, unread)
		u.app.QueueUpdateDraw(func() {
			u.recap, u.recapLoading, u.recapPending = model, false, true
			u.recapCursor = firstRecapSelection(model.Rows)
			u.maybeOpenRecap()
		})
	}()
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

func (u *ui) uiBusyForModal() bool {
	return u.comp.open || u.ov != ovNone || u.composing != ModePanel || strings.TrimSpace(u.msgDraft) != "" ||
		strings.TrimSpace(u.input.GetText()) != "" || u.askChoose != nil
}

func (u *ui) maybeOpenRecap() {
	if !u.recapPending || u.uiBusyForModal() {
		return
	}
	u.recapPending = false
	u.showOverlay(ovRecap, "", nil, "", "")
	u.renderRecap()
}

func (u *ui) recapKey(ev *tcell.EventKey) *tcell.EventKey {
	switch {
	case ev.Key() == tcell.KeyEscape:
		u.closeOverlay()
	case ev.Key() == tcell.KeyUp || ev.Key() == tcell.KeyBacktab || ev.Key() == tcell.KeyRune && ev.Rune() == 'k':
		u.moveRecap(-1)
	case ev.Key() == tcell.KeyDown || ev.Key() == tcell.KeyTab || ev.Key() == tcell.KeyRune && ev.Rune() == 'j':
		u.moveRecap(1)
	case ev.Key() == tcell.KeyEnter:
		u.activateRecapRow()
	}
	return nil
}

func (u *ui) moveRecap(step int) {
	if len(u.recap.Rows) == 0 {
		return
	}
	i := u.recapCursor
	for n := 0; n < len(u.recap.Rows); n++ {
		i = (i + step + len(u.recap.Rows)) % len(u.recap.Rows)
		if u.recap.Rows[i].Selectable {
			u.recapCursor = i
			u.renderRecap()
			return
		}
	}
}

func (u *ui) activateRecapRow() {
	if u.recapCursor < 0 || u.recapCursor >= len(u.recap.Rows) {
		return
	}
	r := u.recap.Rows[u.recapCursor]
	if !r.Selectable {
		return
	}
	if r.Kind == "summary" {
		u.openSummaryConfirmation()
		return
	}
	u.closeOverlayQuiet()
	switch r.Kind {
	case "ask":
		u.openAskPanel(r.Agent)
	case "agent":
		u.openAgent(r.Agent)
	case "board":
		u.openTask(r.Task)
	case "thread":
		u.openMessage(r.Event, eventTimestamp(r.Event.ID))
	case "verdict":
		if r.Verdict != nil {
			v := r.Verdict
			var b body
			b.seg(RoleAttention, strings.ToUpper(v.Decision)).text("\n").seg(RoleMuted, v.Reviewer+" reviewed "+v.Author).text("\n\n").text(Preview(v.Message))
			u.showOverlay(ovMessage, "Verdict "+GlyphNone+" "+v.Subject, b.Lines(), "", "")
		}
	case "swap":
		if r.Swap != nil {
			u.showOverlay(ovMessage, "Swap "+GlyphNone+" "+r.Swap.Role, []Line{{S(RoleText, Preview(swapDescription(*r.Swap)))}}, "", "")
		}
	case "services":
		u.openServicesOverlay()
	case "pause":
		u.showOverlay(ovMessage, "Project pause", []Line{{S(RoleText, Preview(r.Detail))}}, "", "")
	}
}

func (u *ui) renderRecap() {
	lines, cursorLine := recapBody(u.recap.Rows, u.recapCursor)
	u.overlay.SetTitle(overlayTitle("Where was I?", GlyphUp+GlyphDownArrow+" select "+GlyphNone+" Enter open "+GlyphNone+" Esc close"))
	u.overlay.SetText(linesToTview(lines))
	u.overlay.ScrollTo(max(0, cursorLine-3), 0) // keep the selected row in view
}

// recapBody is the recap's text with the cursor on row cursor, and the line
// that row is on (tview's overlay and the shell's reader page draw it).
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

func (u *ui) openSummaryConfirmation() {
	if u.recap.SummaryDisabled != "" {
		return
	}
	u.showOverlay(ovSummaryConfirm, "", nil, "", "")
	u.overlay.SetTitle(lineToTview(Line{S(RoleText, " "), S(RoleAttention, "Send summary request?"), S(RoleText, " ")}))
	var b body
	b.text("This sends a message to "+u.recap.Target+" and uses its tokens.\n").seg(RoleMuted, "y confirm "+GlyphNone+" n or Esc cancel")
	u.overlay.SetText(linesToTview(b.Lines()))
}

func (u *ui) summaryConfirmKey(ev *tcell.EventKey) *tcell.EventKey {
	switch {
	case ev.Key() == tcell.KeyEscape || ev.Key() == tcell.KeyRune && (ev.Rune() == 'n' || ev.Rune() == 'N'):
		u.closeOverlayQuiet()
		u.recapPending = true
		u.maybeOpenRecap()
	case ev.Key() == tcell.KeyRune && (ev.Rune() == 'y' || ev.Rune() == 'Y'):
		u.sendRecapSummary()
	}
	return nil
}

func (u *ui) sendRecapSummary() {
	if u.recap.SummaryDisabled != "" {
		return
	}
	u.closeOverlayQuiet()
	since := "the most recent available history"
	if !u.priorVisit.IsZero() {
		since = u.priorVisit.Local().Format(time.RFC3339)
	}
	body := "Summarise where the project stands for me: what was done since " + since +
		", what is in progress, what you expect from me. Answer on the bus with agentbus reply."
	result := u.send(u.recap.Target, body, true, false)
	if result != sendSent {
		u.recapPending = true
		u.maybeOpenRecap()
		return
	}
}
