package main

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The Elm shell (busmon Charm spec, step b): one Model, Update drives every
// change, View paints the model and does nothing else. Not wired: main.go
// still runs the tview screen until step c; only the tests run this.

// pollMsg is one tick's snapshot, taken by pollCmd at `at`.
type pollMsg struct {
	snap snapshot
	at   time.Time
}

// tickMsg schedules the next poll.
type tickMsg time.Time

// tailMsg is a batch of events from the tail goroutine (Program.Send).
type tailMsg struct{ events []bus.Event }

// shellConfig is what the shell is started with.
type shellConfig struct {
	ctx           context.Context
	reader        busReader
	st            *shared
	project, self string
}

// uiState is the person's place on the screen.
type uiState struct {
	focus         PanelID
	tab           PanelID // the tabbed panel's tab
	view          PanelID // the narrow tier's view
	showAll       bool    // dormant agents listed (a)
	agentsTouched bool    // the person moved in Agents: stop following the top row
	filter        feedFilter
	pause         pauseCursor
	flash         string
	flashErr      bool
}

// shellLists are the shell's lists. They live behind a pointer: the Model is
// copied by value through Update, the lists' selection rules are listState's.
type shellLists struct {
	forYou, agents, activity, board, requests listState
}

func (ls *shellLists) of(p PanelID) *listState {
	switch p {
	case PanelForYou:
		return &ls.forYou
	case PanelAgents:
		return &ls.agents
	case PanelActivity:
		return &ls.activity
	case PanelBoard:
		return &ls.board
	case PanelRequests:
		return &ls.requests
	}
	return nil
}

// Model is the shell. snap is a copy of shared (never shared.mu in View);
// now comes from the last poll or tick, so View never reads the clock.
type Model struct {
	cfg    shellConfig
	snap   snapshot
	feed   *activityFeed
	forYou []attentionItem
	ui     uiState
	lists  *shellLists
	layout shellLayout
	width  int
	height int
	now    time.Time
	tick   int
	gen    int // data generation: bumped by pollMsg and tailMsg only
	paint  *painter
}

// newShell is the shell before its first poll and size.
func newShell(cfg shellConfig) Model {
	m := Model{cfg: cfg, feed: &activityFeed{unread: newUnreadState()}, lists: &shellLists{}, paint: newPainter(th),
		ui: uiState{focus: PanelForYou, tab: PanelActivity, view: PanelForYou}}
	m.lists.activity.SetTail(true)
	for _, p := range narrowPanels {
		m.lists.of(p).SetHue(panelHue[p])
	}
	return m
}

func (m Model) Init() tea.Cmd { return m.pollCmd(1) }

// pollCmd runs today's pollOnce against the reader and returns its snapshot.
func (m Model) pollCmd(tick int) tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		pollOnce(cfg.ctx, cfg.reader, cfg.st, tick)
		now := time.Now()
		return pollMsg{snap: takeSnapshot(cfg.st, now), at: now}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.refresh()
		return m, nil
	case pollMsg:
		m.applySnapshot(msg.snap, msg.at)
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
	case tickMsg:
		m.tick++
		return m, m.pollCmd(m.tick + 1)
	case tailMsg:
		for _, e := range msg.events {
			if m.cfg.st != nil {
				ingestEvent(m.cfg.st, e)
			}
			m.feed.add(e, m.cfg.self)
		}
		if m.cfg.st != nil {
			m.snap = takeSnapshot(m.cfg.st, m.now)
		}
		m.gen++
		m.refresh()
		return m, nil
	}
	return m, nil
}

// applySnapshot takes a poll's snapshot as the model's data.
func (m *Model) applySnapshot(s snapshot, at time.Time) {
	m.snap, m.now = s, at
	m.gen++
	// lookEvent reads the asks the last fill saw (activity.go): set here, on
	// the update path, never in View. Step c removes the global.
	currentAsks = map[string]bus.Ask{}
	for _, a := range s.asks {
		currentAsks[a.ID] = a
	}
	m.feed.classifyWaits(waitingNames(s.agents))
	m.refresh()
}

// counts is what the layout needs from the content.
func (m *Model) counts() shellCounts {
	names, _ := visibleAgents(m.snap.agents, m.snap.pilot, m.ui.showAll, m.now)
	w := 0
	for _, it := range m.forYou {
		w = max(w, forYouNatural(it))
	}
	return shellCounts{ForYou: len(m.forYou), Agents: len(names), ForYouWidth: w + listGutter + 2}
}

// refresh recomputes the marks, the For you list, the layout and every list's
// rows from the model. Update calls it after every change; View only draws.
func (m *Model) refresh() {
	marks := m.feed.unread.marks(m.feed.feed, currentAsks)
	for i := range m.feed.feed {
		m.feed.feed[i].mark = marks[m.feed.feed[i].ev.ID]
	}
	m.forYou = forYou(m.snap, m.feed.feed)
	if m.width == 0 {
		return
	}
	m.layout = shellLayoutFor(m.width, m.height, m.counts(), m.ui.view, m.ui.tab)
	m.fillLists()
}
