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
	snap snapshot
	at   time.Time
}

// tickMsg schedules the next poll.
type tickMsg time.Time

// tailMsg is a batch of events from the tail goroutine (Program.Send).
type tailMsg struct{ events []bus.Event }

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
	settings      busmonSettings // bell, fade, reminder and recap timings (busmon.toml)
	cache         *busmonCache   // the local cache: snoozes, last visit
	cachePath     string         // where it is saved ("" never saves)
	priorVisit    time.Time      // the last visit before this one (the recap's since)
	tour          *tourMarker    // the machine's tour marker; nil = the tour never opens by itself
	// docker runs the F3 pages' docker reads; nil = none at all (tests).
	docker svcRunner
	// brokerHost is --host, which overrides REDIS_HOST (bus.Connect): the
	// Redis page states container facts only for the broker it reads.
	brokerHost string
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
	rd     *reader      // the reader's pages
	cache  *viewCache   // rendered markdown
	sig    *signalState // bells, fade, recap, reminder (tea_signals.go)
}

// newShell is the shell before its first poll and size.
func newShell(cfg shellConfig) Model {
	m := Model{cfg: cfg, feed: &activityFeed{unread: newUnreadState()}, lists: &shellLists{}, paint: newPainter(th),
		ui: uiState{focus: PanelForYou, tab: PanelActivity, view: PanelForYou, askDrafts: map[string]string{}},
		rd: newReader(), cache: newViewCache(), sig: newSignals(cfg.cache)}
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
		return pollMsg{snap: takeSnapshot(cfg.st, now), at: now}
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
		if m.sig.started.IsZero() {
			m.sig.started = msg.at
		}
		return tea.Batch(tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }), m.afterPoll())
	case tickMsg:
		m.tick++
		return m.pollCmd(m.tick + 1)
	case fadeMsg:
		m.sig.fading = false
		m.now = time.Time(msg)
		if m.tokensSkeleton() && m.rd.top().kind == ovTokens { // the shimmer is in the page body
			m.rd.top().dirty = true
		}
		m.refresh()
		return m.scheduleFade()
	case threadMsg:
		m.gotThread(msg)
	case servicesMsg:
		f := msg.facts
		return m.push(page{kind: ovServices, title: "Services", svc: &f})
	case serviceLogMsg:
		return m.push(page{kind: ovServiceLog, title: msg.kind + " log", body: msg.body, fixed: true})
	case redisPageMsg:
		return m.push(page{kind: ovRedis, title: "Redis", body: msg.body, fixed: true})
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

// counts is what the layout needs from the content.
func (m *Model) counts() shellCounts {
	names, _ := visibleAgents(m.snap.agents, m.snap.pilot, m.ui.showAll, m.now)
	w := 0
	for _, it := range m.forYou {
		w = max(w, forYouNatural(it))
	}
	return shellCounts{ForYou: len(m.forYou), Agents: len(names), ForYouWidth: w + listGutter + 2, Composing: m.ui.line != lineNone}
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
	m.layout = shellLayoutFor(m.width, m.height, m.counts(), m.ui.view, m.ui.tab)
	m.fillLists()
	for m.rd.open() && !m.buildPage() { // an ask answered elsewhere: its page goes
		m.rd.pages = m.rd.pages[:len(m.rd.pages)-1]
		if p := m.rd.top(); p != nil {
			p.dirty = true
		}
	}
}
