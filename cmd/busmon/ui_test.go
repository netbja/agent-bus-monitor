package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/netbja/agent-bus-monitor/bus"
)

// fakeWriter records sends; Thread blocks until release is closed (when set).
type fakeWriter struct {
	mu      sync.Mutex
	sent    []string
	threads int
	release chan struct{}
}

func (f *fakeWriter) Cmd(_ context.Context, from, target, typ, ref, command string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, "cmd "+target+" "+command)
	return "1-0", nil
}

func (f *fakeWriter) Notify(_ context.Context, from, message string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, "notify "+message)
	return "1-0", nil
}

func (f *fakeWriter) Thread(_ context.Context, id string) ([]bus.Event, error) {
	f.mu.Lock()
	f.threads++
	rel := f.release
	f.mu.Unlock()
	if rel != nil {
		<-rel
	}
	return []bus.Event{{ID: id, Kind: "cmd", Type: "directive", From: "master", Target: "codex", Message: "THREADROOT"}}, nil
}

func demoShared(now time.Time) *shared {
	return &shared{
		agents: map[string]*agentState{
			"coder": {state: "working", message: "refactoring the parser",
				stateAt: now.Add(-18 * time.Minute), lastSeen: now.Add(-18 * time.Minute)},
			"foureyes": {state: "blocked", message: "waiting for the API key", stateAt: now.Add(-30 * time.Second), gated: 1},
			"sentinel": {lastSeen: now.Add(-90 * time.Second)},
		},
		budgets: map[string]bus.BudgetSnapshot{"anthropic": {SessionPct: 25, WeeklyPct: 44}},
		board:   map[string]bus.BoardEntry{"ui-theme": {Owner: "coder", State: "working", Updated: now.Unix()}},
		health:  health{ok: true, since: now},
		pilot:   "master",
	}
}

// harness drives a ui without an event loop: Draw is called directly and keys
// go through handleKey then, if not consumed, to the focused primitive — the
// same path tview's loop takes.
type harness struct {
	t   *testing.T
	sim tcell.SimulationScreen
	app *tview.Application
	u   *ui
	w   *fakeWriter
}

func newHarness(t *testing.T, st *shared) *harness {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	app := tview.NewApplication().SetScreen(sim)
	w := &fakeWriter{}
	u := newUI(app, st, context.Background(), w, "demo", "hermes")
	app.SetRoot(u.pages, true).SetFocus(u.lists[PanelAgents])
	h := &harness{t: t, sim: sim, app: app, u: u, w: w}
	t.Cleanup(sim.Fini)
	return h
}

// draw renders at w×h, applying the relayout a real resize would trigger.
func (h *harness) draw(w, hh int) string {
	h.sim.SetSize(w, hh)
	if w != h.u.width || hh != h.u.height {
		h.u.width, h.u.height = w, hh
		h.u.relayout()
	}
	h.u.scr = h.sim
	h.u.pages.SetRect(0, 0, w, hh)
	h.u.pages.Draw(h.sim)
	h.sim.Show()
	return screenText(h.sim)
}

func screenText(sim tcell.SimulationScreen) string {
	cells, cw, ch := sim.GetContents()
	var sb strings.Builder
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			if c := cells[y*cw+x]; len(c.Runes) > 0 {
				sb.WriteString(string(c.Runes))
			} else {
				sb.WriteRune(' ')
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func (h *harness) key(ev *tcell.EventKey) {
	if out := h.u.handleKey(ev); out != nil {
		if p := h.app.GetFocus(); p != nil {
			p.InputHandler()(out, func(p tview.Primitive) { h.app.SetFocus(p) })
		}
	}
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.key(runeKey(r))
	}
}

func (h *harness) feed(events ...bus.Event) {
	for _, e := range events {
		h.u.ingest(e)
		h.u.appendEvent(e)
	}
}

func feedEvents() []bus.Event {
	return []bus.Event{
		{ID: "1791649424000-0", Kind: "status", Agent: "coder", State: "working", Message: "parser"},
		{ID: "1791649425000-0", Kind: "cmd", Type: "directive", From: "master", Target: "codex", Message: "review render.go"},
		{ID: "1791649426000-0", Kind: "report", Agent: "kimi", RKind: "note", Message: "tests green"},
	}
}

// A silent agent keeps the state it declared; silence is shown as an age.
func TestScreenSilentAgentKeepsItsDeclaredState(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	screen := h.draw(160, 40)
	for _, want := range []string{"coder", "working", "18m", "foureyes", "blocked", "gate 1", "sentinel"} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "offline") {
		t.Errorf("silence rendered as a guess:\n%s", screen)
	}
	// A report-only agent declared nothing: "—", never an invented state.
	for _, line := range strings.Split(screen, "\n") {
		if strings.Contains(line, "sentinel") && !strings.Contains(line, GlyphDash) {
			t.Errorf("sentinel row should show no declared state: %q", line)
		}
	}
}

func TestScreenReportsTheMonitorsOwnConnection(t *testing.T) {
	now := time.Now()
	st := demoShared(now)
	st.health = health{ok: false, since: now.Add(-12 * time.Second), err: "connection refused"}
	h := newHarness(t, st)
	if screen := h.draw(160, 40); !strings.Contains(screen, GlyphBlocked+" bus down 12s") {
		t.Errorf("a broken monitor link must be named:\n%s", screen)
	}
	if screen := h.draw(64, 22); !strings.Contains(screen, GlyphBlocked+" bus 12s") {
		t.Errorf("narrow header must keep health:\n%s", screen)
	}
}

func TestTooSmallRoundTrip(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 45)
	h.key(key(tcell.KeyF1))
	if h.u.ov != ovHelp {
		t.Fatal("help did not open")
	}
	if s := h.draw(20, 5); !strings.Contains(s, "too small") {
		t.Fatalf("too small screen:\n%s", s)
	}
	s := h.draw(160, 45)
	if h.u.ov != ovHelp || !strings.Contains(s, "KEYS") || h.app.GetFocus() != h.u.overlay {
		t.Fatalf("overlay lost after too-small round trip:\n%s", s)
	}
}

func TestFocusSurvivesTierChange(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 45)
	h.key(key(tcell.KeyTab))
	h.key(key(tcell.KeyTab)) // Agents → Activity → Board
	if h.u.focus != PanelBoard {
		t.Fatalf("focus %v, want Board", h.u.focus)
	}
	h.draw(100, 30)
	if h.u.layout.Tier != TierMedium || h.u.layout.Board != h.u.layout.Lower || h.app.GetFocus() != h.u.lists[PanelBoard] {
		t.Fatalf("medium: board not in the lower tab or not focused: %+v", h.u.layout)
	}
	h.draw(64, 22)
	if h.u.layout.Tier != TierNarrow || h.u.layout.Visible[0] != PanelBoard {
		t.Fatalf("narrow shows %v", h.u.layout.Visible)
	}
	h.draw(160, 45)
	if h.app.GetFocus() != h.u.lists[PanelBoard] {
		t.Fatal("focus lost back in wide")
	}
}

func TestTabNeverHiddenPanel(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(100, 30) // medium: Agents + lower tab (Activity)
	seen := map[PanelID]bool{}
	for i := 0; i < 6; i++ {
		h.key(key(tcell.KeyTab))
		h.draw(100, 30)
		seen[h.u.focus] = true
		if !inPanels(h.u.layout.Visible, h.u.focus) {
			t.Fatalf("Tab focused hidden panel %v (visible %v)", h.u.focus, h.u.layout.Visible)
		}
	}
	if seen[PanelBoard] || seen[PanelRequests] {
		t.Fatalf("medium Tab reached a hidden tab: %v", seen)
	}
	h.key(runeKey('3'))
	h.draw(100, 30)
	if h.u.focus != PanelRequests || h.u.layout.Requests != h.u.layout.Lower {
		t.Fatalf("tab 3: focus %v", h.u.focus)
	}
	h.draw(64, 22)
	for i := 0; i < 4; i++ {
		h.key(key(tcell.KeyTab))
		h.draw(64, 22)
		if len(h.u.layout.Visible) != 1 || h.u.layout.Visible[0] != h.u.focus {
			t.Fatalf("narrow shows %v while focus is %v", h.u.layout.Visible, h.u.focus)
		}
	}
}

func TestEscLadder(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.feed(feedEvents()...)
	h.draw(160, 45)
	h.u.filter = parseFilter("tests")
	h.key(key(tcell.KeyTab)) // Activity
	h.key(key(tcell.KeyUp))  // select newest, leaves live
	act := h.u.lists[PanelActivity]
	if act.Selected() == "" || act.Live() {
		t.Fatal("Up did not select")
	}
	h.key(key(tcell.KeyEscape))
	if act.Selected() != "" || act.Live() {
		t.Fatalf("Esc 1: sel %q live %v, want no selection, still paused", act.Selected(), act.Live())
	}
	h.key(key(tcell.KeyEscape))
	if !act.Live() || h.u.filter.empty() {
		t.Fatalf("Esc 2: live %v filter %q", act.Live(), h.u.filter.raw)
	}
	h.key(key(tcell.KeyEscape))
	if !h.u.filter.empty() {
		t.Fatal("Esc 3 kept the filter")
	}
	h.key(key(tcell.KeyEscape)) // nothing left: never quits
}

func TestDraftsSeparate(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 45)
	h.key(runeKey('i'))
	h.typeText("hello q1")
	if h.u.input.GetText() != "hello q1" || h.u.mode() != ModeInput {
		t.Fatalf("typing: %q mode %v", h.u.input.GetText(), h.u.mode())
	}
	h.key(key(tcell.KeyEscape))
	if h.u.mode() != ModePanel || h.u.msgDraft != "hello q1" {
		t.Fatalf("Esc: mode %v draft %q", h.u.mode(), h.u.msgDraft)
	}
	h.key(runeKey('/'))
	h.typeText("xyz")
	h.key(key(tcell.KeyEscape)) // cancel the filter edit
	if !h.u.filter.empty() || h.u.focus != PanelActivity {
		t.Fatalf("filter cancel: %q focus %v", h.u.filter.raw, h.u.focus)
	}
	h.key(runeKey('i'))
	if h.u.input.GetText() != "hello q1" {
		t.Fatalf("message draft lost: %q", h.u.input.GetText())
	}
	h.key(key(tcell.KeyEnter))
	if fmt.Sprint(h.w.sent) != "[notify hello q1]" {
		t.Fatalf("sent %v", h.w.sent)
	}
	h.typeText("@coder go")
	h.key(key(tcell.KeyEnter))
	if len(h.w.sent) != 2 || h.w.sent[1] != "cmd coder go" {
		t.Fatalf("directive: %v", h.w.sent)
	}
}

func TestModalBlocksOutsideClick(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 45)
	sel := h.u.lists[PanelAgents].Selected()
	h.key(key(tcell.KeyF1))
	h.draw(160, 45)
	consumed, _ := h.u.pages.MouseHandler()(tview.MouseLeftClick, tcell.NewEventMouse(1, 5, tcell.ButtonPrimary, 0), func(p tview.Primitive) { h.app.SetFocus(p) })
	if !consumed || h.u.ov != ovHelp || h.u.lists[PanelAgents].Selected() != sel {
		t.Fatalf("outside click leaked: consumed %v ov %v sel %q", consumed, h.u.ov, h.u.lists[PanelAgents].Selected())
	}
	h.key(runeKey('1'))
	h.key(key(tcell.KeyTab))
	if h.u.ov != ovHelp || h.u.focus != PanelAgents {
		t.Fatal("keys reached the panels under the overlay")
	}
	h.key(runeKey('q'))
	if h.u.ov != ovNone {
		t.Fatal("q did not close the overlay")
	}
}

func TestEnterOpensAgentDetailAndCopies(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 45)
	first := h.u.lists[PanelAgents].Selected()
	if first != "foureyes" {
		t.Fatalf("attention sort: first agent %q, want the blocked foureyes", first)
	}
	h.key(key(tcell.KeyEnter))
	s := h.draw(160, 45)
	if h.u.ov != ovAgent || !strings.Contains(s, "waiting for the API key") {
		t.Fatalf("agent detail:\n%s", s)
	}
	h.key(runeKey('y'))
	if h.u.flash == "" {
		t.Fatal("y did not copy")
	}
}

// fakeReader counts every read; fail makes PilotDriver fail (broker down).
type fakeReader struct {
	calls map[string]int
	fail  bool
}

func (f *fakeReader) n(name string) { f.calls[name]++ }
func (f *fakeReader) PilotDriver(context.Context) (string, error) {
	f.n("PilotDriver")
	if f.fail {
		return "", errors.New("connection refused")
	}
	return "master", nil
}
func (f *fakeReader) ArmedAgents(context.Context) (map[string]string, error) {
	f.n("ArmedAgents")
	return map[string]string{}, nil
}
func (f *fakeReader) CmdLag(context.Context) (map[string]int64, error) {
	f.n("CmdLag")
	return nil, nil
}
func (f *fakeReader) Agents(context.Context) (map[string]bus.AgentSnapshot, error) {
	f.n("Agents")
	return map[string]bus.AgentSnapshot{"coder": {State: "working", TS: 1, PID: 42}, "kimi": {State: "idle", TS: 1}}, nil
}
func (f *fakeReader) Usage(context.Context) (map[string]bus.UsageSnapshot, error) {
	f.n("Usage")
	return map[string]bus.UsageSnapshot{"coder": {Ctx: "120k ctx"}}, nil
}
func (f *fakeReader) Budgets(context.Context) (map[string]bus.BudgetSnapshot, error) {
	f.n("Budgets")
	return map[string]bus.BudgetSnapshot{"anthropic": {SessionPct: 25}}, nil
}
func (f *fakeReader) Board(context.Context) (map[string]bus.BoardEntry, error) {
	f.n("Board")
	return map[string]bus.BoardEntry{"t": {Owner: "coder", State: "working"}}, nil
}
func (f *fakeReader) Recent(context.Context, []string, int) ([]bus.Event, map[string]string, error) {
	f.n("Recent")
	return nil, nil, nil
}
func (f *fakeReader) DispatchAlive(context.Context) (bool, error) {
	f.n("DispatchAlive")
	return true, nil
}
func (f *fakeReader) Obligation(context.Context, string, time.Duration) (bus.Obligation, error) {
	f.n("Obligation")
	return bus.Obligation{Unread: 2}, nil
}
func (f *fakeReader) Wake(context.Context, string) (bus.WakeRecord, bool, error) {
	f.n("Wake")
	return bus.WakeRecord{}, false, nil
}
func (f *fakeReader) OpenChallenges(context.Context, string) (map[string]string, error) {
	f.n("OpenChallenges")
	return nil, nil
}

func TestNoExtraReadsPerTick(t *testing.T) {
	f := &fakeReader{calls: map[string]int{}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	pollOnce(context.Background(), f, st, 1) // discovers coder + kimi
	f.calls = map[string]int{}
	pollOnce(context.Background(), f, st, 6) // 6 % 5 == 1: a full tick
	full := map[string]int{"PilotDriver": 1, "ArmedAgents": 1, "CmdLag": 1, "Agents": 1, "Usage": 1, "Budgets": 1,
		"Board": 1, "Recent": 1, "DispatchAlive": 1, "Obligation": 2, "Wake": 2, "OpenChallenges": 2}
	if fmt.Sprint(f.calls) != fmt.Sprint(full) {
		t.Fatalf("full tick reads %v\nwant %v", f.calls, full)
	}
	f.calls = map[string]int{}
	pollOnce(context.Background(), f, st, 7)
	light := map[string]int{"PilotDriver": 1, "ArmedAgents": 1, "CmdLag": 1, "Agents": 1, "Usage": 1, "Budgets": 1,
		"Board": 1, "OpenChallenges": 2}
	if fmt.Sprint(f.calls) != fmt.Sprint(light) {
		t.Fatalf("light tick reads %v\nwant %v", f.calls, light)
	}
	// Drawing and opening details read nothing.
	h := newHarness(t, st)
	h.draw(160, 45)
	h.key(key(tcell.KeyEnter))
	h.draw(160, 45)
	if h.w.threads != 0 {
		t.Fatalf("agent detail read a thread")
	}
	if a := st.agents["coder"]; a.pid != 42 || a.ctxRaw != "120k ctx" || !a.queueKnown || a.waiting != 2 {
		t.Fatalf("tick did not fill the model: %+v", a)
	}
}

func TestBrokerDownKeepsValues(t *testing.T) {
	f := &fakeReader{calls: map[string]int{}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	pollOnce(context.Background(), f, st, 1)
	pollOnce(context.Background(), f, st, 6)
	f.fail = true
	pollOnce(context.Background(), f, st, 7)
	if st.health.ok || len(st.budgets) == 0 || len(st.board) == 0 || st.pilot != "master" {
		t.Fatalf("broker down blanked the model: %+v", st)
	}
	if c := queueCell(st.agents["coder"], TierWide); c.Text != "2?" {
		t.Fatalf("queue after failure %q, want 2?", c.Text)
	}
	h := newHarness(t, st)
	if s := h.draw(160, 45); !strings.Contains(s, "bus down") || !strings.Contains(s, "2?") {
		t.Fatalf("screen:\n%s", s)
	}
}

// The real event loop: open a message whose thread read blocks, resize while
// it is in flight, then let the read return. Nothing may deadlock and the
// update must land in the open overlay.
func TestResizeDuringDetailLoadNoDeadlock(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(160, 45)
	app := tview.NewApplication().SetScreen(sim)
	w := &fakeWriter{release: make(chan struct{})}
	st := demoShared(time.Now())
	u := newUI(app, st, context.Background(), w, "demo", "hermes")
	app.SetRoot(u.pages, true).SetFocus(u.lists[PanelAgents])
	done := make(chan error, 1)
	go func() { done <- app.Run() }()
	defer app.Stop()

	onUI := func(f func()) {
		ch := make(chan struct{})
		app.QueueUpdateDraw(func() { f(); close(ch) })
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("UI loop stuck")
		}
	}
	onUI(func() {
		for _, e := range feedEvents() {
			u.ingest(e)
			u.appendEvent(e)
		}
	})
	onUI(func() {
		u.setFocusPanel(PanelActivity)
		u.lists[PanelActivity].Select("3") // the directive (seq 1 = day separator)
		u.lists[PanelActivity].Open()
	})
	var ov overlayKind
	onUI(func() { ov = u.ov })
	if ov != ovMessage {
		t.Fatalf("overlay %v, want message", ov)
	}
	sim.SetSize(80, 24)
	app.QueueEvent(tcell.NewEventResize(80, 24))
	onUI(func() {})
	close(w.release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var text string
		onUI(func() { text = u.overlay.GetText(true); ov = u.ov })
		if strings.Contains(text, "THREADROOT") && ov == ovMessage {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("thread update never landed (ov %v):\n%s", ov, text)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var tier Tier
	onUI(func() { tier = u.layout.Tier })
	if tier != TierMedium {
		t.Fatalf("after resize tier %v", tier)
	}
}
