package main

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The Elm shell (busmon Charm spec, step b): one Model, Update drives every
// change, View paints the model and does nothing else. main.go runs it (step c).

// pollMsg is one tick's snapshot, taken by pollCmd at `at`.
type pollMsg struct {
	snap     snapshot
	at       time.Time
	swaps    []bus.SwapEvent // the swap ledger's entries new since the last pollMsg
	swapLost bool            // entries were trimmed before they were read
}

// tickMsg schedules the next poll.
type tickMsg time.Time

// tailMsg is a batch of events from the tail goroutine (Program.Send).
type tailMsg struct {
	events   []bus.Event
	backfill bool // the start's backfill: history, never a notice
}

// fadeMsg is a fade frame: only the waiting words change (busmon Charm spec,
// "Caching"). It moves the clock and bumps no data; scheduleFade sends it
// only while an agent waits.
type fadeMsg time.Time

// shellConfig is what the shell is started with.
type shellConfig struct {
	ctx           context.Context
	reader        busReader
	w             busWriter // sends, answers, thread reads, removals, resume
	st            *shared
	project, self string
	settings      busmonSettings // bell, fade, reminder and recap timings (config.toml, busmon.toml)
	cache         *busmonCache   // the local cache: snoozes, last visit
	cachePath     string         // where it is saved ("" never saves)
	priorVisit    time.Time      // the last visit before this one (the recap's since)
	tour          *tourMarker    // the machine's tour marker; nil = the tour never opens by itself
	// docker runs the F3 pages' docker reads; nil = none at all (tests).
	docker svcRunner
	// brokerHost is --host, which overrides REDIS_HOST (bus.Connect): the
	// Redis page states container facts only for the broker it reads.
	brokerHost string
	// liveFrom is busmon's start: a tail event older than it is history,
	// never a notice (the --limit 0 replay comes through the live tail).
	liveFrom time.Time
	// histDir is the history archive on this host ("" = none: live streams only).
	histDir string
}

// uiState is the person's place on the screen.
type uiState struct {
	focus          PanelID
	tab            PanelID // the tabbed panel's tab
	view           PanelID // the narrow tier's view
	showAll        bool    // dormant agents listed (a)
	agentsTouched  bool    // the person moved in Agents: stop following the top row
	filter         feedFilter
	line           lineKind // what the input line edits (tea_input.go)
	edit           string   // its text
	tail           int      // its cursor, as the bytes after it (0: at the end; lineedit.go)
	pick           int      // the highlighted "@" completion
	msgDraft       string   // the message line's draft, kept by Esc
	pause          pauseCursor
	flash          string
	flashErr       bool
	expanded       string            // the row expanded in place (its list key), "" none
	expandIn       PanelID           // the list it is in: Activity or For you
	planExpanded   bool              // the Plan tab shows its done tasks (A11)
	reqOpen        reqOpen           // the Requests view's expanded groups and folded section
	compose        *composeState     // the compose window, when open (tea_compose.go)
	msgCompose     composeDraft      // the window's message draft while it is closed
	askDrafts      map[string]string // free answers being written, by ask|question
	askDraft       string            // a directive held once: its recipient has an open ask
	broadcastDraft string            // a broadcast warned about once: the next send sends it
	askChoose      *askChoice        // several recipients hold questions: the next digit picks one
	sending        bool              // a send is in flight
	pauseAck       string            // the send the person chose to make despite the pause
	pausePopup     *pauseDialog      // the pause popup, when open
	confirm        *pendingRemoval   // a d waiting for its y
	reminder       *reminderDialog   // the reminder, when open
	summary        bool              // the recap's summary confirmation is open
	summarySent    bool              // the summary request is in flight: its success closes the recap
	tour           *tourState        // the first-launch tour, when open (tour.go)
}

// shellLists are the shell's lists. They live behind a pointer: the Model is
// copied by value through Update, the lists' selection rules are listState's.
type shellLists struct {
	forYou, agents, activity, board, requests, plan listState
	reqView                                         reqView // the Requests view refresh computed: the layout, fillLists and the header read it
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
	case PanelPlan:
		return &ls.plan
	}
	return nil
}

// Model is the shell. snap is a copy of shared (never shared.mu in View);
// now comes from the last poll or tick, so View never reads the clock.
type Model struct {
	cfg     shellConfig
	histCap int64 // the History page's read cap; 0 = histReadCap (tests lower it)
	snap    snapshot
	feed    *activityFeed
	forYou  []attentionItem
	ui      uiState
	lists   *shellLists
	layout  shellLayout
	width   int
	height  int
	now     time.Time
	tick    int
	gen     int // data generation: bumped by pollMsg and tailMsg only
	paint   *painter
	rd      *reader             // the reader's pages
	cache   *viewCache          // rendered markdown
	sig     *signalState        // bells, fade, recap, reminder (tea_signals.go)
	bars    map[string]barSlide // the account gauges' slides (bars.go)
	notice  *noticeState        // the quiet line for automatic actions (notice.go)
}

// newShell is the shell before its first poll and size.
func newShell(cfg shellConfig) Model {
	m := Model{cfg: cfg, feed: &activityFeed{unread: newUnreadState()}, lists: &shellLists{}, paint: newPainter(th),
		ui: uiState{focus: PanelForYou, tab: PanelActivity, view: PanelForYou, askDrafts: map[string]string{}},
		rd: newReader(), cache: newViewCache(), sig: newSignals(cfg.cache), notice: &noticeState{}}
	if c := cfg.cache; c != nil { // the person's read marks; nothing older than the last visit is marked
		for _, id := range c.Read {
			m.feed.unread.read[id] = true
		}
		m.feed.unread.floor = c.LastVisit
	}
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
	tokOpen := m.tokensOpen() // read on the update path: the command must not touch the model
	return func() tea.Msg {
		if cfg.st != nil {
			cfg.st.mu.Lock()
			cfg.st.tokOpen = tokOpen
			cfg.st.mu.Unlock()
		}
		pollOnce(cfg.ctx, cfg.reader, cfg.st, tick)
		now := time.Now()
		swaps, lost := takeSwapNews(cfg.st)
		return pollMsg{snap: takeSnapshot(cfg.st, now), at: now, swaps: swaps, swapLost: lost}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.update(msg)
	return m, tea.Batch(cmd, m.afterUpdate())
}

func (m *Model) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.refresh()
		m.noteCut()
		m.layoutCompose()
	case pollMsg:
		m.applySnapshot(msg.snap, msg.at)
		m.notice.fromPoll(msg.snap, m.cfg.self, msg.swaps, msg.swapLost)
		if m.sig.started.IsZero() {
			m.sig.started = msg.at
		}
		return tea.Batch(tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }), m.afterPoll())
	case tickMsg:
		m.tick++
		return m.pollCmd(m.tick + 1)
	case fadeMsg:
		m.sig.fading = false
		sliding := m.barView().moving() // its last frame draws the bar at rest
		m.now = time.Time(msg)
		if top := m.rd.top(); top != nil && top.kind == ovTokens && (sliding || m.tokensSkeleton()) { // the shimmer and the bars are in the page body
			m.rd.top().dirty = true
		}
		m.refresh()
		return m.scheduleFade()
	case threadMsg:
		m.gotThread(msg)
	case historyMsg:
		m.gotHistory(msg)
	case servicesMsg:
		return m.gotServices(msg)
	case svcTickMsg:
		return m.svcRefresh(msg.serial)
	case serviceLogMsg:
		return m.push(page{kind: ovServiceLog, title: msg.kind + " log", body: msg.body, fixed: true})
	case redisPageMsg:
		return m.gotRedisPage(msg)
	case answerMsg:
		m.gotAnswer(msg)
	case sendResultMsg:
		m.gotSend(msg)
		m.refresh()
	case resumeMsg:
		cmd := m.gotResume(msg)
		m.refresh()
		return cmd
	case removeMsg:
		m.gotRemove(msg)
		m.refresh()
	case recapMsg:
		m.gotRecap(msg)
	case cacheSavedMsg:
		if msg.err != nil && m.cfg.st != nil {
			m.cfg.st.mu.Lock()
			m.cfg.st.configNote = joinNote(m.cfg.st.configNote, msg.note)
			m.cfg.st.mu.Unlock()
		}
	case tea.KeyPressMsg:
		return m.key(msg)
	case tea.PasteMsg:
		switch {
		case m.ui.compose != nil:
			m.pasteCompose(msg.Content)
		case m.ui.line != lineNone:
			m.pasteLine(msg.Content)
		}
		m.refresh()
	case tea.MouseClickMsg:
		return m.click(msg.Mouse())
	case tea.MouseWheelMsg:
		m.wheel(msg.Mouse())
	case tailMsg:
		for _, e := range msg.events {
			if m.cfg.st != nil {
				ingestEvent(m.cfg.st, e)
			}
			m.feed.add(e, m.cfg.self)
			if n, ok := tailNotice(e); ok && !msg.backfill && !eventTimestamp(e.ID).Before(m.cfg.liveFrom) {
				m.notice.push(n)
			}
		}
		if m.cfg.st != nil {
			m.snap = takeSnapshot(m.cfg.st, m.now)
		}
		m.gen++
		m.refresh()
	}
	return nil
}

// applySnapshot takes a poll's snapshot as the model's data.
func (m *Model) applySnapshot(s snapshot, at time.Time) {
	m.snap, m.now = s, at
	m.gen++
	m.bars = updateSlides(m.bars, barTargets(s.budgets), at, m.cfg.settings.Fade && th.Animated())
	// lookEvent reads the asks the last fill saw (activity.go): set here, on
	// the update path, never in View. Step c removes the global.
	currentAsks = map[string]bus.Ask{}
	for _, a := range s.asks {
		currentAsks[a.ID] = a
	}
	m.feed.classifyWaits(waitingNames(s.agents))
	m.forgetAnswers()
	m.refresh()
}

// barView is the gauges' slides at the Model's now.
func (m Model) barView() barView { return barView{slides: m.bars, now: m.now} }

// counts is what the layout needs from the content.
func (m *Model) counts() shellCounts {
	names, _ := visibleAgents(m.snap.agents, m.snap.pilot, m.ui.showAll, m.now)
	w := 0
	for _, it := range m.forYou {
		w = max(w, forYouNatural(it))
	}
	fy := len(m.forYou)
	if fy == 0 { // the empty state's lines, shown whole, as wide as they need (capped by the layout)
		empty := forYouEmpty(m.snap, m.lists.reqView.count(secOwed))
		fy = len(empty)
		for _, l := range empty {
			w = max(w, l.Width())
		}
	}
	return shellCounts{ForYou: fy, Agents: len(names), ForYouWidth: w + listGutter + 2, Composing: m.ui.line != lineNone}
}

// refresh recomputes the marks, the For you list, the layout and every list's
// rows from the model. Update calls it after every change; View only draws.
func (m *Model) refresh() {
	m.keepExpansion()
	marks := m.feed.unread.marks(m.feed.feed, currentAsks)
	for i := range m.feed.feed {
		m.feed.feed[i].mark = marks[m.feed.feed[i].ev.ID]
	}
	m.forYou = forYou(m.snap, m.feed.feed)
	if m.width == 0 {
		return
	}
	// One Requests view per refresh, before the layout: For you's empty state
	// is sized and filled from the same owed count (reviewer-codex).
	m.lists.reqView = requestGroups(m.requestInput())
	m.layout = shellLayoutFor(m.width, m.height, m.counts(), m.ui.view, m.ui.tab)
	m.fillLists()
	for m.rd.open() && !m.buildPage() { // an ask answered elsewhere: its page goes
		m.rd.pages = m.rd.pages[:len(m.rd.pages)-1]
		if p := m.rd.top(); p != nil {
			p.dirty = true
		}
	}
}
