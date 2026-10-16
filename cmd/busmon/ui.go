package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/netbja/agent-bus-monitor/bus"
)

// busWriter is what the UI sends and reads on demand. *bus.Bus satisfies it.
type busWriter interface {
	HumanCmd(ctx context.Context, from, target, command string) (string, error)
	HumanNotify(ctx context.Context, from, message string) (string, error)
	HumanClose(ctx context.Context, from, target, ref, message string) (string, error)
	Answer(ctx context.Context, askID, qid, by, choice, text string) (string, int, error)
	Asks(ctx context.Context) ([]bus.Ask, error)
	ForgetAgent(ctx context.Context, agent string) (bool, error)
	Thread(ctx context.Context, threadID string) ([]bus.Event, error)
}

type overlayKind int

const (
	ovNone overlayKind = iota
	ovMessage
	ovAgent
	ovTask
	ovRequest
	ovRequests
	ovServices
	ovHelp
	ovAsk
)

var panelOrder = []PanelID{PanelAgents, PanelActivity, PanelBoard, PanelRequests}

// ui owns every primitive (created once, never recreated) and every piece of
// UI state. All fields are touched only on the tview event loop: handlers,
// QueueUpdateDraw callbacks and Draw.
type ui struct {
	app            *tview.Application
	st             *shared
	ctx            context.Context
	w              busWriter
	project, self  string
	broadcastDraft string // a draft with no @ already warned about: the next Enter sends it

	pages   *tview.Pages
	screen  *screenView
	modal   *modalView
	overlay *tview.TextView
	input   *tview.InputField
	lists   [4]*ListView

	layout        Layout
	width, height int // size of the last relayout
	scr           tcell.Screen
	focus         PanelID // logical focus (never a widget address)
	lowerTab      PanelID // medium: the panel in the lower frame
	composing     Mode    // ModePanel = not composing; ModeInput / ModeFilter
	msgDraft      string
	ov            overlayKind
	ovPlain       string
	ovThreadPlain string
	ovAgent       string // the agent an ovAgent overlay shows
	flash         string
	flashErr      bool // the flash reports a failure (RoleError)
	agentsTouched bool // the operator moved in Agents: stop following the top row
	showAll       bool // the Agents panel also lists dormant agents (a)
	confirm       *pendingRemoval
	ask           askPanel          // the ask panel's cursor
	rung          map[string]string // agent -> the waiting declaration already rung for
	rungAsks      map[string]bool   // ask ids already rung for
	bell          func()            // rings the terminal bell; nil = the screen's Beep
	animate       atomic.Bool       // an agent is waiting: the fade ticker redraws ~10 times a second

	feed         []feedLine
	seq          int
	lastDay      string
	filter       feedFilter
	pause        pauseCursor
	detailGen    int
	detailEvent  bus.Event
	detailThread threadState
	tabSpans     []tabSpan // where each tab label was drawn, for clicks
}

// onTick runs on the event loop after each poll (never from a draw): it rings
// the terminal bell once per new waiting declaration, so herdr marks the
// busmon tab even when it is hidden.
func (u *ui) onTick() {
	u.st.mu.Lock()
	ring := false
	seen := map[string]bool{}
	for _, n := range asksFor(u.st.agents) {
		seen[n] = true
		a := u.st.agents[n]
		if u.rung == nil {
			u.rung, u.rungAsks = map[string]string{}, map[string]bool{}
		}
		key := "status:" + a.stateAt.String()
		switch {
		case a.askID != "": // one ring per new ask; its waiting declaration is covered
			if !u.rungAsks[a.askID] {
				u.rungAsks[a.askID], ring = true, true
			}
			u.rung[n] = key
		case u.rung[n] != key: // a new waiting declaration without an ask
			u.rung[n], ring = key, true
		}
	}
	for n := range u.rung {
		if !seen[n] {
			delete(u.rung, n)
		}
	}
	u.animate.Store(len(seen) > 0 && th.Animated())
	u.st.mu.Unlock()
	switch {
	case !ring:
	case u.bell != nil:
		u.bell()
	case u.scr != nil:
		_ = u.scr.Beep()
	}
}

// pendingRemoval is a d that waits for its y: the next key confirms or drops it.
type pendingRemoval struct {
	key   string // "agent:<name>", "thr:<id>"
	label string
}

type tabSpan struct {
	x0, x1, y int
	panel     PanelID
}

func newUI(app *tview.Application, st *shared, ctx context.Context, w busWriter, project, self string) *ui {
	setRoundedBorders()
	u := &ui{app: app, st: st, ctx: ctx, w: w, project: project, self: self,
		focus: PanelAgents, lowerTab: PanelActivity, composing: ModePanel}
	for _, p := range panelOrder {
		p := p
		l := NewListView()
		l.SetHue(panelHue[p])
		l.SetFocusFunc(func() { u.onPanelFocus(p) })
		u.lists[p] = l
	}
	u.lists[PanelActivity].SetTail(true)
	u.lists[PanelActivity].SetOnChange(func(_ string, live bool) {
		if live {
			u.pause.Resume()
		} else {
			u.pause.Pause(u.seq)
		}
	})
	u.lists[PanelAgents].SetOnOpen(u.openAgent)
	u.lists[PanelActivity].SetOnOpen(u.openFeedLine)
	u.lists[PanelBoard].SetOnOpen(u.openTask)
	u.lists[PanelRequests].SetOnOpen(u.openRequestItem)

	u.input = tview.NewInputField().SetLabel(GlyphPrompt + " ")
	th.StyleInput(u.input)
	u.input.SetPlaceholder("i or : to write " + GlyphNone + " @agent or @all text wakes them " + GlyphNone + " plain text: a broadcast nobody reads")
	u.input.SetFocusFunc(func() {
		if u.composing == ModePanel { // reached by a click
			u.composing = ModeInput
			u.input.SetLabel(GlyphPrompt + " ").SetText(u.msgDraft)
		}
	})
	u.input.SetAutocompleteFunc(func(currentText string) []string {
		if u.composing != ModeInput {
			return nil
		}
		u.st.mu.Lock()
		names := make([]string, 0, len(u.st.agents))
		for n := range u.st.agents {
			names = append(names, n)
		}
		u.st.mu.Unlock()
		return agentCompletions(currentText, names)
	})
	u.input.SetDoneFunc(u.inputDone)

	u.overlay = tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetWordWrap(true)
	u.overlay.SetScrollable(true).SetBorder(true)
	u.overlay.SetBorderPadding(0, 0, 1, 1)
	th.StyleTextView(u.overlay)

	u.screen = &screenView{Box: tview.NewBox(), u: u}
	u.modal = &modalView{Box: tview.NewBox(), u: u}
	u.pages = tview.NewPages().
		AddPage("main", u.screen, true, true).
		AddPage("overlay", u.modal, true, false)

	app.SetBeforeDrawFunc(func(s tcell.Screen) bool {
		u.scr = s // for OSC52 clipboard writes
		if w, h := s.Size(); w != u.width || h != u.height {
			u.width, u.height = w, h
			// Never SetFocus from inside a draw: tview holds its lock here.
			go app.QueueUpdateDraw(u.relayout)
		}
		return false
	})
	app.SetInputCapture(u.handleKey)
	return u
}

// mode is who owns the keyboard right now.
func (u *ui) mode() Mode {
	if u.ov == ovAsk {
		return ModeAsk
	}
	if u.ov != ovNone {
		return ModeOverlay
	}
	return u.composing
}

func (u *ui) tier() Tier { return u.layout.Tier }

func (u *ui) counts() Counts {
	u.st.mu.Lock()
	defer u.st.mu.Unlock()
	shown, _ := visibleAgents(u.st.agents, u.st.pilot, u.showAll, time.Now())
	return Counts{Agents: len(shown), Board: len(u.st.board), Composing: u.composing != ModePanel}
}

func (u *ui) computeLayout(w, h int) Layout {
	return computeLayout(w, h, u.counts(), u.focus, u.lowerTab)
}

// relayout runs on the UI goroutine after a size change: it recomputes the
// layout and restores the logical focus (outside any draw).
func (u *ui) relayout() {
	mdWidth = max(30, overlayInnerWidth(u.width))
	u.layout = u.computeLayout(u.width, u.height)
	u.app.SetFocus(u.focusPrimitive())
}

func (u *ui) focusPrimitive() tview.Primitive {
	switch {
	case u.ov != ovNone:
		return u.overlay
	case u.composing != ModePanel:
		return u.input
	}
	return u.lists[u.focus]
}

func (u *ui) onPanelFocus(p PanelID) {
	u.focus = p
	if p != PanelAgents {
		u.lowerTab = p
	}
	if u.composing != ModePanel {
		u.leaveInput(true)
	}
}

func (u *ui) setFocusPanel(p PanelID) {
	u.focus = p
	if p != PanelAgents {
		u.lowerTab = p
	}
	u.layout = u.computeLayout(u.width, u.height)
	u.app.SetFocus(u.focusPrimitive())
}

// handleKey is the application input capture: it maps the key through the
// mode's table and performs the action. A key it does not consume goes on to
// the focused primitive (text for the input field, scrolling for an overlay).
func (u *ui) handleKey(ev *tcell.EventKey) *tcell.EventKey {
	m := u.mode()
	act := lookup(m, u.focus, u.tier(), ev)
	if act == ActQuit {
		u.app.Stop()
		return nil
	}
	if c := u.confirm; c != nil && m == ModePanel {
		u.confirm = nil
		if ev.Key() == tcell.KeyRune && ev.Rune() == 'y' {
			u.remove(c.key)
		} else {
			u.flash, u.flashErr = "not removed", false
		}
		return nil
	}
	if m == ModeAsk {
		u.flash, u.flashErr = "", false
		return u.askKey(ev)
	}
	if act != ActNone {
		u.flash, u.flashErr = "", false
	}
	switch m {
	case ModeOverlay:
		switch act {
		case ActClose:
			u.closeOverlay()
		case ActCopy:
			u.copyText(u.ovPlain, "text")
		case ActCopyThread:
			if u.ovThreadPlain != "" {
				u.copyText(u.ovThreadPlain, "thread")
			}
		case ActAsk:
			if u.ov == ovAgent && u.agentHasAsk(u.ovAgent) {
				u.openAskPanel(u.ovAgent)
			} else if ev.Key() != tcell.KeyEnter {
				u.openAskPanel("")
			}
		case ActUp, ActDown, ActPageUp, ActPageDown, ActFirst, ActLast:
			return ev // the TextView scrolls itself
		}
		return nil
	case ModeInput, ModeFilter:
		return ev // Enter/Esc/Tab reach the field: autocomplete first, then inputDone
	}
	l := u.lists[u.focus]
	if u.focus == PanelAgents && act != ActNone {
		u.agentsTouched = true
	}
	switch act {
	case ActNone:
		return nil
	case ActNextPanel, ActPrevPanel:
		u.cyclePanel(act == ActNextPanel)
	case ActTab1, ActTab2, ActTab3, ActTab4:
		u.switchTab(int(act - ActTab1))
	case ActUp:
		l.Move(-1)
	case ActDown:
		l.Move(1)
	case ActPageUp:
		l.Move(-max(1, l.rows()))
	case ActPageDown:
		l.Move(max(1, l.rows()))
	case ActFirst:
		l.First()
	case ActLast:
		l.Last()
	case ActLive:
		u.lists[PanelActivity].Resume()
	case ActOpen:
		l.Open()
	case ActCopy:
		u.copySelected()
	case ActDelete:
		u.askRemove(l.Selected())
	case ActAsk:
		agent := ""
		if u.focus == PanelAgents && u.agentHasAsk(u.lists[PanelAgents].Selected()) {
			agent = u.lists[PanelAgents].Selected()
		}
		u.openAskPanel(agent)
	case ActFollow:
		if act := u.lists[PanelActivity]; act.Live() {
			act.Pause()
			u.flash = "auto-scroll off: f or end resumes"
		} else {
			act.Resume()
			u.flash = "auto-scroll on"
		}
	case ActShowAll:
		u.showAll = !u.showAll
		u.relayout()
	case ActFilter:
		u.startFilter()
	case ActCompose:
		u.startCompose("")
	case ActComposeAt:
		u.startCompose("@")
	case ActHelp:
		u.showOverlay(ovHelp, "Help", helpText(), "", "")
	case ActRequests:
		u.openRequestsOverlay()
	case ActServices:
		u.openServicesOverlay()
	case ActEscape:
		u.escape()
	}
	return nil
}

func (u *ui) cyclePanel(forward bool) {
	order := u.layout.Visible
	if len(order) <= 1 {
		order = panelOrder // narrow: Tab walks the views
	}
	i := 0
	for j, p := range order {
		if p == u.focus {
			i = j
		}
	}
	step := 1
	if !forward {
		step = len(order) - 1
	}
	u.setFocusPanel(order[(i+step)%len(order)])
}

// switchTab: medium tabs are Activity, Board, Requests; narrow adds Agents first.
func (u *ui) switchTab(n int) {
	tabs := []PanelID{PanelActivity, PanelBoard, PanelRequests}
	if u.tier() == TierNarrow {
		tabs = panelOrder
	}
	if n < len(tabs) {
		u.setFocusPanel(tabs[n])
	}
}

// escape: clear the focused selection, then the Activity pause, then the
// filter. Esc never quits.
func (u *ui) escape() {
	act := u.lists[PanelActivity]
	switch {
	case u.lists[u.focus].Selected() != "":
		u.lists[u.focus].ClearSelection()
	case !act.Live():
		act.Resume()
	case !u.filter.empty():
		u.filter = feedFilter{}
	}
}

func (u *ui) startCompose(prefill string) {
	u.composing = ModeInput
	text := u.msgDraft
	if prefill != "" {
		text = prefill
	}
	u.input.SetLabel(GlyphPrompt + " ").SetText(text)
	u.layout = u.computeLayout(u.width, u.height)
	u.app.SetFocus(u.input)
}

func (u *ui) startFilter() {
	if u.composing == ModeInput {
		u.msgDraft = u.input.GetText()
	}
	u.composing = ModeFilter
	u.focus = PanelActivity
	u.lowerTab = PanelActivity
	u.input.SetLabel("filter" + GlyphPrompt + " ").SetText(u.filter.raw)
	u.layout = u.computeLayout(u.width, u.height)
	u.app.SetFocus(u.input)
}

// leaveInput returns from the input line to the panels; keep saves the
// message draft (a filter edit is simply dropped).
func (u *ui) leaveInput(keep bool) {
	u.broadcastDraft = "" // a warning holds for the next Enter only, never across an Esc
	if u.composing == ModeInput && keep {
		u.msgDraft = u.input.GetText()
	}
	u.composing = ModePanel
	u.input.SetLabel(GlyphPrompt + " ").SetText("")
	u.layout = u.computeLayout(u.width, u.height)
}

func (u *ui) inputDone(key tcell.Key) {
	switch key {
	case tcell.KeyEnter:
		text := strings.TrimSpace(u.input.GetText())
		if u.composing == ModeFilter {
			u.filter = parseFilter(text)
			u.leaveInput(false)
			u.setFocusPanel(PanelActivity)
			return
		}
		if text == "" {
			u.broadcastDraft = ""
		}
		if text != "" {
			tgt, body, directed := parseDirected(text)
			if !directed && u.broadcastDraft != text {
				// Nobody reads a notify and nothing wakes for it: say so once.
				u.broadcastDraft = text
				u.flash, u.flashErr = "nobody reads a broadcast: start with @agent or @all; Enter again to broadcast", true
				return
			}
			u.broadcastDraft = ""
			ctx, cancel := context.WithTimeout(u.ctx, pollBudget)
			var err error
			switch {
			case directed && tgt == "all":
				targets := u.agentNames()
				if len(targets) == 0 {
					cancel()
					u.flash, u.flashErr = "no agent to send to", true
					return
				}
				// Every agent is tried. If some were served, the draft is cleared:
				// a retry of @all would send them the directive twice.
				var failed []string
				var lastErr error
				for _, a := range targets {
					if _, e := u.w.HumanCmd(ctx, u.self, a, body); e != nil {
						failed, lastErr = append(failed, a), e
					}
				}
				u.flash = fmt.Sprintf("directive sent to %d agents", len(targets))
				switch {
				case len(failed) == len(targets):
					err = lastErr // nothing sent: keep the draft, a retry is safe
				case len(failed) > 0:
					cancel()
					u.flash = fmt.Sprintf("sent to %d of %d agents; failed: %s (%s)", len(targets)-len(failed), len(targets),
						strings.Join(failed, ", "), Preview(lastErr.Error()))
					u.flashErr = true
					u.input.SetText("")
					u.msgDraft = ""
					return
				}
			case directed:
				_, err = u.w.HumanCmd(ctx, u.self, tgt, body)
				u.flash = "directive sent to " + tgt
			default:
				_, err = u.w.HumanNotify(ctx, u.self, text)
				u.flash = "broadcast sent"
			}
			cancel()
			u.flashErr = err != nil
			if err != nil {
				// keep the draft: the operator retries once the bus is back
				u.flash = "send failed: " + Preview(err.Error())
				return
			}
		}
		u.input.SetText("")
		u.msgDraft = ""
	case tcell.KeyEscape:
		u.leaveInput(true)
		u.app.SetFocus(u.focusPrimitive())
	}
}

func (u *ui) copyText(s, what string) {
	if u.scr == nil || s == "" {
		return
	}
	u.scr.SetClipboard([]byte(s)) // OSC52: reaches the local clipboard even over SSH
	u.flash, u.flashErr = GlyphDone+" "+what+" copied", false
}

func (u *ui) feedIndex(id string) int {
	for i := range u.feed {
		if u.feed[i].id == id {
			return i
		}
	}
	return -1
}

func (u *ui) copySelected() {
	key := u.lists[u.focus].Selected()
	if key == "" {
		return
	}
	now := time.Now()
	u.st.mu.Lock()
	defer u.st.mu.Unlock()
	switch u.focus {
	case PanelAgents:
		if a := u.st.agents[key]; a != nil {
			u.copyText(agentDetailPlain(a), "status message")
		}
	case PanelActivity:
		if i := u.feedIndex(key); i >= 0 {
			u.copyText(u.feed[i].text, "line")
		}
	case PanelBoard:
		if e, ok := u.st.board[key]; ok {
			_, _, plain := taskDetail(e, key, now)
			u.copyText(plain, "task")
		}
	case PanelRequests:
		if r, ok := u.findRequest(key); ok {
			_, _, plain := requestDetail(r, now)
			u.copyText(plain, "request")
		} else if t, ok := u.findThread(key); ok {
			u.copyText(t.Message, "message")
		}
	}
}

// findRequest / findThread resolve a Requests key; the caller holds st.mu.
func (u *ui) findRequest(key string) (request, bool) {
	task, ok := strings.CutPrefix(key, "req:")
	if !ok {
		return request{}, false
	}
	for _, r := range u.st.requests {
		if r.Task == task {
			return r, true
		}
	}
	return request{}, false
}

func (u *ui) findThread(key string) (openThread, bool) {
	id, ok := strings.CutPrefix(key, "thr:")
	if !ok {
		return openThread{}, false
	}
	for _, t := range u.st.threads {
		if t.ID == id {
			return t, true
		}
	}
	return openThread{}, false
}

func (u *ui) showOverlay(kind overlayKind, title, body, plain, threadPlain string) {
	u.ov, u.ovPlain, u.ovThreadPlain = kind, plain, threadPlain
	hints := "esc close"
	if plain != "" {
		hints = "y copy " + GlyphNone + " " + hints
	}
	if threadPlain != "" {
		hints = "Y copy thread " + GlyphNone + " " + hints
	}
	u.overlay.SetTitle(" " + tg(RoleText, title) + "  " + tg(RoleMuted, hints) + " ")
	u.overlay.SetText(body)
	u.overlay.ScrollToBeginning()
	u.pages.ShowPage("overlay")
	u.app.SetFocus(u.overlay)
}

func (u *ui) closeOverlay() {
	u.ov = ovNone
	u.detailGen++ // a thread read still in flight must not reopen anything
	u.pages.HidePage("overlay")
	u.app.SetFocus(u.focusPrimitive())
}

func (u *ui) openAgent(name string) {
	u.st.mu.Lock()
	a := u.st.agents[name]
	if a == nil {
		u.st.mu.Unlock()
		return
	}
	now := time.Now()
	title, body := agentDetail(name, a, name == u.st.pilot, u.st.board, u.feed, now)
	snap, has := u.st.budgets[a.provider]
	until, isHeld := held(u.st.holds, a.provider, now)
	body += quotaDetail(a.provider, snap, has, until, isHeld, now)
	for _, ask := range openAsks(u.st.asks) {
		if ask.From == name {
			body = askSummary(ask) + body
		}
	}
	plain := agentDetailPlain(a)
	u.st.mu.Unlock()
	u.showOverlay(ovAgent, title, body, plain, "")
	u.ovAgent = name
}

func (u *ui) openTask(task string) {
	u.st.mu.Lock()
	e, ok := u.st.board[task]
	u.st.mu.Unlock()
	if !ok {
		return
	}
	title, body, plain := taskDetail(e, task, time.Now())
	u.showOverlay(ovTask, title, body, plain, "")
}

func (u *ui) openRequestItem(key string) {
	u.st.mu.Lock()
	r, isReq := u.findRequest(key)
	t, isThr := u.findThread(key)
	u.st.mu.Unlock()
	switch {
	case isReq:
		title, body, plain := requestDetail(r, time.Now())
		u.showOverlay(ovRequest, title, body, plain, "")
	case isThr:
		e := bus.Event{ID: t.ID, Kind: "cmd", Type: t.Type, From: t.From, Target: t.Target, Message: t.Message}
		u.openMessage(e, t.At)
	}
}

func (u *ui) openRequestsOverlay() {
	u.st.mu.Lock()
	body := requestsPanel(u.st.requests, u.st.threads, time.Now(), max(20, overlayInnerWidth(u.width)))
	label := requestsLabel(u.st.requests, u.st.threads, u.self, openQuestionCount(u.st.asks)).text()
	u.st.mu.Unlock()
	u.showOverlay(ovRequests, fmt.Sprintf("Requests %s %s", GlyphNone, label), body, "", "")
}

// openServicesOverlay shows the project's services. It reads the log tail
// from disk once, on the key press, never from a draw.
func (u *ui) openServicesOverlay() {
	u.st.mu.Lock()
	inst, recs := u.st.svcInst, u.st.svcRecs
	u.st.mu.Unlock()
	host, _ := os.Hostname()
	u.showOverlay(ovServices, "Services", servicesPanel(inst, recs, host, time.Now(), max(20, overlayInnerWidth(u.width))), "", "")
}

func (u *ui) openFeedLine(id string) {
	if i := u.feedIndex(id); i >= 0 && !u.feed[i].sep {
		u.openMessage(u.feed[i].ev, u.feed[i].at)
	}
}

// openMessage shows a message detail; a cmd's thread is read asynchronously
// and detailGen discards a read whose overlay has since been replaced.
func (u *ui) openMessage(e bus.Event, at time.Time) {
	u.detailEvent = e
	u.detailThread = threadState{ID: threadID(e)}
	u.detailGen++
	gen := u.detailGen
	if u.detailThread.ID == "" {
		u.detailThread.Loaded = true
	} else {
		go func() {
			entries, err := u.w.Thread(u.ctx, threadID(e))
			u.app.QueueUpdateDraw(func() {
				if gen != u.detailGen || u.ov != ovMessage {
					return // the operator opened something else meanwhile
				}
				u.detailThread = threadState{ID: threadID(e), Entries: entries, Loaded: true, Err: err}
				u.overlay.SetText(detailText(e, at, time.Now(), u.detailThread))
				u.ovThreadPlain = threadPlain(e, u.detailThread)
			})
		}()
	}
	u.showOverlay(ovMessage, detailTitle(e), detailText(e, at, time.Now(), u.detailThread),
		detailPlain(e), threadPlain(e, u.detailThread))
}

// ingest folds one stream event into the shared model. It runs on the tail
// goroutine; appendEvent (UI goroutine) adds it to the feed.
func (u *ui) ingest(e bus.Event) {
	if e.Kind != "status" && e.Kind != "report" {
		return
	}
	et := entryTime(e.ID)
	u.st.mu.Lock()
	defer u.st.mu.Unlock()
	a := u.st.agents[e.Agent]
	if a == nil {
		a = &agentState{}
		u.st.agents[e.Agent] = a
	}
	// A report proves the process is alive; it never refreshes the declared
	// state or the message beside it.
	if e.Kind == "status" {
		a.state, a.message, a.stateAt = e.State, e.Message, et
	}
	a.lastSeen = et
}

// appendEvent adds one event to the feed (with a day separator on a date
// change), keeping the last feedCap lines.
func (u *ui) appendEvent(e bus.Event) {
	et := entryTime(e.ID)
	if day := et.Format("2006-01-02"); day != u.lastDay {
		u.lastDay = day
		u.seq++
		_, plain := daySeparator(et)
		u.feed = append(u.feed, feedLine{id: strconv.Itoa(u.seq), seq: u.seq, sep: true, text: plain, at: et})
	}
	if last := len(u.feed) - 1; last >= 0 && sameFanOut(u.feed[last], e, et) {
		fl := &u.feed[last]
		if len(fl.to) == 0 {
			fl.to = []string{fl.ev.Target}
		}
		fl.to = append(fl.to, e.Target)
		fl.text = plainLine(fl.ev, fl.at) + " (+" + strconv.Itoa(len(fl.to)-1) + " more)"
		return
	}
	u.seq++
	fl := feedLine{id: strconv.Itoa(u.seq), seq: u.seq, text: plainLine(e, et), ev: e, at: et}
	u.feed = append(u.feed, fl)
	if len(u.feed) > feedCap {
		u.feed = u.feed[len(u.feed)-feedCap:]
	}
}

// plainLine is what y copies from an Activity line: the v0.8 plain format,
// unchanged (spec §3.4).
func plainLine(e bus.Event, et time.Time) string {
	ts := et.Format("15:04:05")
	switch e.Kind {
	case "status":
		s := ts + " [" + e.Agent + "] " + e.State
		if e.Message != "" {
			s += " | " + e.Message
		}
		return s
	case "notify":
		return ts + " [notify] " + e.Message
	case "cmd":
		label := "[" + e.Type + " " + e.From + "->" + e.Target
		if e.Ref != "" {
			label += " " + e.Ref
		}
		return ts + " " + label + "] " + e.Message
	case "report":
		return ts + " [report:" + e.RKind + "->" + e.Agent + "] " + e.Message + reportMarker(e.Full)
	}
	return ts + " " + e.Message
}

// screenView is the main page. Draw recomputes the geometry from the pure
// layout function and refreshes every panel's rows from the model; it never
// changes focus and never does I/O.
type screenView struct {
	*tview.Box
	u *ui
}

func (s *screenView) Focus(delegate func(p tview.Primitive)) { delegate(s.u.focusPrimitive()) }

func (s *screenView) HasFocus() bool {
	if s.u.input.HasFocus() {
		return true
	}
	for _, l := range s.u.lists {
		if l.HasFocus() {
			return true
		}
	}
	return false
}

// InputHandler forwards a key the capture let through to the focused child:
// tview delivers keys to the root, not to the focused widget.
func (s *screenView) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return s.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		if s.u.input.HasFocus() {
			s.u.input.InputHandler()(event, setFocus)
			return
		}
		for _, l := range s.u.lists {
			if l.HasFocus() {
				l.InputHandler()(event, setFocus)
				return
			}
		}
	})
}

func (s *screenView) Draw(screen tcell.Screen) {
	u := s.u
	x0, y0, w, h := s.GetRect()
	l := u.computeLayout(w, h)
	u.layout = l // geometry only: focus is restored by relayout, never here
	if l.Tier == TierTooSmall {
		msg := fmt.Sprintf("terminal too small (%dx%d)", w, h)
		for y := 0; y < h; y++ {
			DrawLine(screen, x0, y0+y, w, nil, false)
		}
		DrawLine(screen, x0, y0+h/2, w, Line{{Text: strings.Repeat(" ", max(0, (w-Width(msg))/2)) + msg, Role: RoleAttention}}, false)
		return
	}
	now := time.Now()
	u.st.mu.Lock()
	mineReqs, mineThreads := forPerson(u.st.requests, u.st.threads, u.self)
	hd := headerData{project: u.project, health: u.st.health, dispatch: u.st.dispatch, missions: u.st.missions, pilot: u.st.pilot, self: u.self, update: u.st.update,
		waiting: waitingText(mineReqs, mineThreads, now), asks: waitingQuestions(u.st.agents), pause: u.st.pause, holds: u.st.holds, pace: u.st.pace.last, budgets: u.st.budgets, now: now}
	u.fillPanels(l, now)
	u.st.mu.Unlock()

	lines := headerLines(hd, w, l.Tier)
	for i := 0; i < l.Header.H && i < len(lines); i++ {
		DrawLine(screen, x0, y0+l.Header.Y+i, w, lines[i], false)
	}
	u.tabSpans = nil
	if l.Tabs.H > 0 {
		DrawLine(screen, x0, y0+l.Tabs.Y, w, u.tabsLine(panelOrder, l.Visible[0], x0, y0+l.Tabs.Y), false)
	}
	for _, p := range l.Visible {
		r := l.panelRect(p)
		if l.Tier == TierMedium && p != PanelAgents {
			// The lower frame's title is the tab bar; Activity keeps its
			// live/paused indicator after it.
			lst := u.lists[p]
			title := u.tabsLine([]PanelID{PanelActivity, PanelBoard, PanelRequests}, p, x0+r.X+3, y0+r.Y)
			if len(lst.title) > 1 {
				title = append(title, lst.title[1:]...)
			}
			lst.SetFrame(title, lst.label)
		}
		u.lists[p].SetRect(x0+r.X, y0+r.Y, r.W, r.H)
		u.lists[p].Draw(screen)
	}
	if l.Input.H > 0 {
		u.input.SetRect(x0+l.Input.X, y0+l.Input.Y, l.Input.W, 1)
		u.input.Draw(screen)
	}
	var footer Line
	if u.flash != "" {
		role := RoleOK
		if u.flashErr {
			role = RoleError
		}
		footer = FitLine(Line{{Text: " " + u.flash, Role: role}}, w)
	} else {
		footer = footerHints(u.mode(), u.focus, l.Tier, w)
	}
	DrawLine(screen, x0, y0+l.Footer.Y, w, footer, false)
}

var panelNames = map[PanelID]string{PanelAgents: "Agents", PanelActivity: "Activity", PanelBoard: "Board", PanelRequests: "Requests"}

// tabsLine renders "1 Agents  2 Activity ..." with the active tab
// highlighted, recording each tab's cells for mouse clicks.
func (u *ui) tabsLine(tabs []PanelID, active PanelID, x, y int) Line {
	var l Line
	pos := x
	for i, p := range tabs {
		name := panelNames[p]
		if len(tabs) == 4 && p == PanelRequests {
			name = "Req" // narrow: four tabs must fit 30 columns
		}
		label := fmt.Sprintf(" %d %s ", i+1, name)
		role := RoleMuted
		if p == active {
			role = panelHue[p]
		}
		l = append(l, Seg{Text: label, Role: role, Bold: p == active, Reverse: p == active && th.Palette() == PaletteNone})
		u.tabSpans = append(u.tabSpans, tabSpan{x0: pos, x1: pos + Width(label), y: y, panel: p})
		pos += Width(label)
	}
	return l
}

// fillPanels refreshes rows, headers and frames from the model. The caller
// holds st.mu.
func (u *ui) fillPanels(l Layout, now time.Time) {
	currentAsks = map[string]bus.Ask{}
	for _, a := range u.st.asks {
		currentAsks[a.ID] = a
	}
	rowW := func(p PanelID) int { return max(0, l.panelRect(p).W-2-listGutter) }
	st := u.st

	agents := u.lists[PanelAgents]
	names, hidden := visibleAgents(st.agents, st.pilot, u.showAll, now)
	items := make([]ListItem, 0, len(names))
	working, blocked := 0, 0
	for _, n := range names {
		a := st.agents[n]
		items = append(items, ListItem{Key: n, Line: agentRow(n, a, n == st.pilot, l.Tier, rowW(PanelAgents), now), Selectable: true})
		switch _, word, _, _ := stateLook(a); word { // as shown: a paused agent is not working
		case "working":
			working++
		case "blocked":
			blocked++
		}
	}
	if len(items) == 0 {
		items = []ListItem{{Line: Line{{Text: "no agent has published yet", Role: RoleMuted}}}}
	}
	agents.SetHeader(agentHeader(l.Tier, rowW(PanelAgents)))
	agents.SetItems(items)
	if !u.agentsTouched && len(names) > 0 {
		agents.First() // the top (most urgent) agent, until the operator moves
	}
	label := fmt.Sprintf("%d agents %s %d working %s %d blocked", len(names), GlyphNone, working, GlyphNone, blocked)
	if hidden > 0 {
		label += fmt.Sprintf(" %s %d dormant (a shows)", GlyphNone, hidden)
	} else if u.showAll {
		label += fmt.Sprintf(" %s all (a hides dormant)", GlyphNone)
	}
	if sc := agents.Scroll(); sc.Total > sc.Visible && sc.Visible > 0 {
		label += fmt.Sprintf(" %s %d-%d of %d", GlyphNone, sc.Top+1, min(sc.Total, sc.Top+sc.Visible), sc.Total)
	}
	agents.SetFrame(Line{{Text: "Agents", Role: RoleText}}, Line{{Text: label, Role: RoleMuted}})

	act := u.lists[PanelActivity]
	var feedItems []ListItem
	admitted, events := admittedEvents(u.feed, u.filter)
	shown := len(admitted)
	for _, fl := range u.feed {
		if !u.filter.match(fl) {
			continue
		}
		feedItems = append(feedItems, ListItem{Key: fl.id, Line: activityRow(fl, l.Tier, rowW(PanelActivity)), Selectable: !fl.sep})
	}
	act.SetHeader(activityHeader(l.Tier, rowW(PanelActivity)))
	act.SetItems(feedItems)
	u.pause.Recount(admitted)
	at, al := activityFrame(&u.pause, shown, events, u.filter)
	act.SetFrame(at, al)

	board := u.lists[PanelBoard]
	board.SetHeader(boardHeader(rowW(PanelBoard)))
	board.SetItems(boardItems(st.board, now, rowW(PanelBoard)))
	bt, bl := boardFrame(st.board)
	board.SetFrame(bt, bl)

	reqs := u.lists[PanelRequests]
	reqs.SetItems(requestItems(st.requests, st.threads, now, rowW(PanelRequests)))
	reqs.SetFrame(Line{{Text: "Requests", Role: RoleText}}, requestsLabel(st.requests, st.threads, u.self, openQuestionCount(st.asks)))
}

// MouseHandler routes clicks and the wheel to the panel under the pointer,
// and tab clicks to the tab switch.
func (s *screenView) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return s.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
		u := s.u
		mx, my := event.Position()
		if action == tview.MouseLeftClick {
			for _, t := range u.tabSpans {
				if my == t.y && mx >= t.x0 && mx < t.x1 {
					u.setFocusPanel(t.panel)
					return true, nil
				}
			}
		}
		for _, p := range u.layout.Visible {
			if l := u.lists[p]; l.InRect(mx, my) {
				if p == PanelAgents && action != tview.MouseMove {
					// a click or the wheel: the operator has taken over the
					// selection, so stop pinning it to the top row
					u.agentsTouched = true
				}
				return l.MouseHandler()(action, event, setFocus)
			}
		}
		if u.input.InRect(mx, my) && u.layout.Input.H > 0 {
			return u.input.MouseHandler()(action, event, setFocus)
		}
		return false, nil
	})
}

// overlayInnerWidth is the text width inside the overlay frame that
// modalView.Draw places on a screen w cells wide (frame plus one-cell padding).
func overlayInnerWidth(w int) int {
	if w < mediumMinWidth {
		return w - 4
	}
	return min(100, w-4) - 4
}

// modalView is the overlay layer: it covers the whole screen and consumes
// every mouse event. A click outside the detail box closes it, like Esc, and
// never reaches the panels behind it.
type modalView struct {
	*tview.Box
	u *ui
}

func (m *modalView) Focus(delegate func(p tview.Primitive)) { delegate(m.u.overlay) }
func (m *modalView) HasFocus() bool                         { return m.u.overlay.HasFocus() }

func (m *modalView) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		m.u.overlay.InputHandler()(event, setFocus)
	})
}

func (m *modalView) Draw(screen tcell.Screen) {
	x, y, w, h := m.GetRect()
	if w < minWidth || h < minHeight {
		return // the main page already says "too small"
	}
	if w < mediumMinWidth {
		// Narrow (keep overlayInnerWidth in step): the whole width, below the header, so bus health and the
		// dispatcher stay visible while a detail is open.
		top := min(m.u.layout.Header.H, h-minHeight+1)
		m.u.overlay.SetRect(x, y+top, w, h-top)
	} else {
		bw, bh := min(100, w-4), h-4
		m.u.overlay.SetRect(x+(w-bw)/2, y+(h-bh)/2, bw, bh)
	}
	m.u.overlay.Draw(screen)
}

func (m *modalView) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return m.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
		if mx, my := event.Position(); m.u.overlay.InRect(mx, my) {
			if consumed, capture := m.u.overlay.MouseHandler()(action, event, setFocus); consumed {
				return true, capture
			}
		}
		if action == tview.MouseLeftClick {
			m.u.closeOverlay()
		}
		return true, nil // outside the box: swallowed, never passed to the panels
	})
}
