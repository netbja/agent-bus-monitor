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
	// memoryLocal answers where the Memory page reads (tests); nil: the rule
	// on this machine (memoryLocalOf).
	memoryLocal func() (bool, string)
	// badge makes the project's name a badge for its first projectBadgeFor
	// (busmon-project-badge); main sets it, and lastProject when busmon chose
	// the project itself (no -p, no AGENT_BUS_PROJECT): the bottom line says so.
	badge, lastProject bool
	// holders reads the project's holders on this host for the Services page
	// (agent-screen-blocked §4); nil reads none.
	holders       func(context.Context) ([]holderRow, error)
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
	// about is where About reads the versions it cannot know itself.
	about aboutSources
	// brokerHost is --host, which overrides REDIS_HOST (bus.Connect): the
	// Redis page states container facts only for the broker it reads.
	brokerHost string
	// liveFrom is busmon's start: a tail event older than it is history,
	// never a notice (the --limit 0 replay comes through the live tail).
	liveFrom time.Time
	// histDir is the history archive on this host ("" = none: live streams only).
	histDir string
	// clip reads the clipboard's image for the compose window's ^V (zero =
	// the machine's tools); attachDir is where a pasted image is saved ("" =
	// pasting says so and saves nothing).
	clip      clipTools
	attachDir string
	// The settings page (C): the environment its config.toml is found and
	// resolved through (nil = none: a test reads no file), busmon's flags
	// (read-only rows), and how a theme applies at once (nil = next start).
	getenv func(string) string
	// ancestor names the registered agent this busmon descends from, for the
	// person-only acts (X on a service card); nil = no check.
	ancestor func(context.Context) (string, error)
	flags    settingsFlags
	retheme  func(name string) (*Theme, error)
	// Z (sleep.go): the runner of agentbus suspend/resume, the project's
	// suspend.log, suspend.wait; nil = Z says there is no agentbus to run.
	sleepRunner func(args []string, log string) (<-chan error, error)
	sleepLog    func(project string) string
	sleepWait   func() time.Duration
	// terms reads the project's holder terminals for an agent page's preview
	// (termpreview.go); nil = no preview. agentbusBin runs attach (t).
	terms       termReader
	agentbusBin string
	// admin is the Projects page's sources (M); nil = every part unknown.
	admin *adminSource
	// The project switch (project-switch note): the target's keys on this
	// broker, and the broker's projects (/project's completions).
	projectHasKeys func(ctx context.Context, project string) (bool, error)
	listProjects   func(ctx context.Context) ([]string, error)
	// from is the project a switch left (--from): B goes back to it; ""
	// when busmon did not start from a switch (project-switch-ux item 4).
	from string
	// files is where the compose window's @ lists from; nil = no checkout
	// and no home on this machine (compose-at-file).
	files *atSource
}

// uiState is the person's place on the screen.
type uiState struct {
	focus          PanelID
	tab            PanelID   // the tabbed panel's tab
	view           PanelID   // the narrow tier's view
	showAll        bool      // dormant agents listed (a)
	agentsTouched  bool      // the person moved in Agents: stop following the top row
	boardDone      bool      // d on the Board: its done tasks shown (views-redesign §3)
	planDone       bool      // d on the Plan tab: DONE shown (views-redesign §4)
	planLater      bool      // Enter on the Plan tab's folded LATER (below the wide tier): open
	boardBetween   bool      // the Board filtered to the rows between agents (Work's line; views-redesign §3, (a))
	reqFrom        PanelID   // the lower tab the Requests sub-view was opened from: Esc, the tab bar and the arrows read it, never d/o/+/-
	workLevel      workLevel // where the focus is on Work's Kanban: its tab, its column selector, a column's cards
	workCol        int       // the Kanban's column the selector is on
	filter         feedFilter
	line           lineKind // what the input line edits (tea_input.go)
	edit           string   // its text
	tail           int      // its cursor, as the bytes after it (0: at the end; lineedit.go)
	pick           int      // the highlighted "@" completion
	msgDraft       string   // the message line's draft, kept by Esc
	pause          pauseCursor
	flash          string
	flashErr       bool
	expanded       string                  // the row expanded in place (its list key), "" none
	expandIn       PanelID                 // the list it is in: Activity or For you
	walk           *walk                   // the triage walk in progress (T, triage.go), nil when none
	reqOpen        reqOpen                 // the Requests view's expanded groups and folded section
	prioBusy       bool                    // a priority write is in flight: + and - wait for its answer
	prioSeen       map[string]prioOverride // task -> the level this busmon wrote, until a board read begun after it
	compose        *composeState           // the compose window, when open (tea_compose.go)
	msgCompose     composeDraft            // the window's message draft while it is closed
	heldDrafts     []composeDraft          // message drafts kept after msgCompose, each opening in turn
	askDrafts      map[string]string       // free answers being written, by ask|question
	ideaDraft      string                  // a new idea's text while its window is closed (a on F6)
	ideaAdding     bool                    // an add is on its way: another waits for its result
	memoryDraft    string                  // the Memory page's a: its text while the window is closed
	ideaLast       *ideaChange             // this busmon's last state change, for u (ideas-triage)
	ideaReq        string                  // the request id of the text last sent (ideaReqText): a retry of it reuses the id
	ideaReqText    string
	askDraft       string            // a directive held once: its recipient has an open ask
	broadcastDraft string            // a broadcast warned about once: the next send sends it
	askChoose      *askChoice        // several recipients hold questions: the next digit picks one
	sending        bool              // a send is in flight
	pauseAck       string            // the send the person chose to make despite the pause
	pausePopup     *pauseDialog      // the pause popup, when open
	confirm        *pendingRemoval   // a d waiting for its y
	palSel         int               // the palette's chosen row (a leading / in the line)
	palMoved       bool              // ↑↓ moved it since the last edit: Enter runs that row
	palHidden      bool              // Esc hid the list; the next edit shows it again
	sleepAsk       *sleepAsk         // a Z waiting for its y
	quitAsk        bool              // q's popup: y quits (busmon-quit-confirm)
	sleepRun       *sleepRun         // agentbus suspend or resume running
	sleepSeq       int               // the last run's id
	reminder       *reminderDialog   // the reminder, when open
	summary        bool              // the recap's summary confirmation is open
	summarySent    bool              // the summary request is in flight: its success closes the recap
	tour           *tourState        // the first-launch tour, when open (tour.go)
	pastedImages   map[string]string // an image pasted in compose: its hash -> the file saved (never saved twice)
	atRecent       []string          // the @ popup's picks this session, most recent first (compose-at-file §4.2)
	del            *deletePanel      // the Projects page's delete confirmation (projects_delete.go)
	// The idle view (idleview.go): shown after display.idle_view_after with
	// no input; lastInput is the last input's poll time (the first poll's
	// until then), idleSince when the view was entered.
	idle                 bool
	idleBlurred          bool // the window was left while the view is shown: the next focus wakes it
	lastInput, idleSince time.Time
	// activitySeen is the feed's seq when Activity was last shown: what came
	// after is "N new" on its tab (plan-tab-r3 item 1).
	activitySeen int
	// releases is V's choice on the Plan tab's Releases column: 0 the width
	// decides (releasesMinWidth), 1 shown, -1 hidden (plan-tab-r3 item 5).
	releases int
	// badgeUntil is when the launch's project badge ends (busmon-project-badge).
	badgeUntil time.Time
}

// projectBadgeFor is how long the project's name stays a badge at launch.
const projectBadgeFor = 3 * time.Second

// newLaunchShell is the shell main starts: cfg with the launch's badge on,
// and lastProject when neither -p nor AGENT_BUS_PROJECT named the project
// (busmon-project-badge).
func newLaunchShell(opts busmonOpts, getenv func(string) string, cfg shellConfig) Model {
	cfg.badge, cfg.lastProject = true, projectFromLastUsed(opts.project, getenv("AGENT_BUS_PROJECT"))
	return newShell(cfg)
}

// projectFromLastUsed: busmon chose the project itself, the last one opened
// on this broker, since neither -p nor AGENT_BUS_PROJECT named one.
func projectFromLastUsed(flag, env string) bool { return flag == "" && env == "" }

// shellLists are the shell's lists. They live behind a pointer: the Model is
// copied by value through Update, the lists' selection rules are listState's.
type shellLists struct {
	forYou, agents, activity, board, requests, work, plan listState
	releases                                              listState // the Plan tab's Releases column (plan-tab-r3 item 5)
	reqView                                               reqView   // the Requests view refresh computed: the layout, fillLists and the header read it
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
	case PanelWork:
		return &ls.work
	case PanelPlan:
		return &ls.plan
	case PanelReleases:
		return &ls.releases
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
	work    workModel // who owes what: the one counter (work_model.go)
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
	// exitNote is printed once busmon has left the screen (the open
	// project deleted, projects-admin §5.2.3).
	exitNote string
	// switchTo is the project busmon ends to re-execute on (main finishes
	// the switch); knownProjects the broker's, for /project's completions.
	switchTo string
	// ro is the read-only guard's state (readonly.go), shared with the
	// guarded writer the commands call.
	ro *readOnlyFlag
	// others is the other projects' counts, by project (others.go).
	others        map[string]otherRead
	knownProjects []string
}

// newShell is the shell before its first poll and size.
func newShell(cfg shellConfig) Model {
	cfg.from = backTarget(cfg.from, cfg.project)
	ro := &readOnlyFlag{}
	cfg.w = guardWriter(cfg.w, ro) // a stopped project reached by a switch writes nothing (readonly.go)
	m := Model{cfg: cfg, feed: &activityFeed{unread: newUnreadState()}, lists: &shellLists{}, paint: newPainter(th),
		ui: uiState{focus: PanelForYou, tab: PanelActivity, view: PanelForYou, askDrafts: map[string]string{}},
		rd: newReader(), cache: newViewCache(), sig: newSignals(cfg.cache), notice: &noticeState{}, ro: ro}
	if c := cfg.cache; c != nil { // the person's read marks; nothing older than the last visit is marked
		for _, id := range c.Read {
			m.feed.unread.read[id] = true
		}
		for _, id := range c.Answered {
			m.feed.unread.answered[id] = true
		}
		m.feed.unread.floor = c.LastVisit
	}
	m.lists.activity.SetTail(true)
	m.lists.activity.SetFollow(!cfg.settings.NoFollow) // display.follow (palette spec §8.3)
	for _, p := range shellPanels {
		m.lists.of(p).SetHue(panelHue[p])
	}
	return m
}

func (m Model) Init() tea.Cmd { return tea.Batch(m.pollCmd(1), m.projectNamesCmd(), m.othersCmd()) }

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
	if m.ui.idle && m.idleFocus(msg) {
		return nil
	}
	if isInput(msg) {
		if m.ui.idle { // the waking input does nothing else (idle view §3)
			m.leaveIdle()
			return nil
		}
		m.ui.lastInput = m.now
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.refresh()
		m.noteCut()
		m.layoutCompose()
	case othersTickMsg:
		return m.othersCmd()
	case othersMsg:
		return m.gotOthers(msg)
	case pollMsg:
		m.applySnapshot(msg.snap, msg.at)
		if m.cfg.badge && m.ui.badgeUntil.IsZero() { // the launch's badge, from the first poll (busmon-project-badge)
			m.ui.badgeUntil = msg.at.Add(projectBadgeFor)
			if m.cfg.lastProject {
				m.notice.push(notice{text: "last project opened: " + m.cfg.project + " (no -p) " + GlyphNone + " M to switch", level: noticeInfo})
			}
		}
		if m.ui.lastInput.IsZero() { // a busmon left alone counts from its first poll (reviewer-codex)
			m.ui.lastInput = msg.at
		}
		m.enterIdle()
		m.endPriorityOverrides(msg.snap.requestsReadAt) // only a successful board read begun after the write
		m.notice.fromPoll(msg.snap, m.cfg.self, msg.swaps, msg.swapLost)
		if m.sig.started.IsZero() {
			m.sig.started = msg.at
		}
		return tea.Batch(tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }), m.afterPoll())
	case tickMsg:
		m.tick++
		return m.pollCmd(m.tick + 1)
	case marqueeMsg:
		return m.marqueeStep(msg)
	case fadeMsg:
		m.sig.fading = false
		sliding := m.barView().moving() // its last frame draws the bar at rest
		m.now = time.Time(msg)
		m.aboutFrame(m.now)
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
	case termTickMsg:
		return m.termRead(msg.serial)
	case termViewMsg:
		return m.gotTermView(msg)
	case attachDoneMsg:
		if f := attachFlash(msg.agent, msg.err, msg.stderr); f != "" {
			m.ui.flash = f
		}
		m.refresh()
	case serviceLogMsg:
		v := msg.view
		return m.push(page{kind: ovServiceLog, title: msg.kind + " log", slog: &v})
	case redisPageMsg:
		return m.gotRedisPage(msg)
	case aboutVersionsMsg:
		m.gotAboutVersions(msg)
	case answerMsg:
		return m.gotAnswer(msg)
	case sendResultMsg:
		cmd := m.gotSend(msg)
		m.refresh()
		return cmd
	case sleepAskMsg:
		m.gotSleepAsk(msg)
	case sleepStartedMsg:
		return m.gotSleepStarted(msg)
	case sleepLineMsg:
		return m.gotSleepLine(msg)
	case sleepDoneMsg:
		return m.gotSleepDone(msg)
	case pastedImageMsg:
		m.gotPastedImage(msg)
	case resumeMsg:
		cmd := m.gotResume(msg)
		m.refresh()
		return cmd
	case removeMsg:
		m.gotRemove(msg)
		m.refresh()
	case priorityMsg:
		m.gotPriority(msg)
	case ideaMsg:
		m.gotIdea(msg)
		m.refresh()
	case recapMsg:
		m.gotRecap(msg)
	case settingsMsg:
		m.gotSettings(msg)
	case followMsg:
		m.gotFollow(msg)
	case switchCheckedMsg:
		return m.gotSwitchChecked(msg)
	case projectNamesMsg:
		m.knownProjects = msg.names
	case projectMemoryMsg:
		m.gotMemory(msg)
	case personMemoryMsg:
		m.gotPersonMemory(msg)
	case memoryEditMsg:
		return m.gotMemoryEdit(msg)
	case projectsMsg:
		return m.gotProjects(msg)
	case deleteCheckMsg:
		m.gotDeleteCheck(msg)
		m.refresh()
	case deleteDoneMsg:
		cmd := m.gotDeleteDone(msg)
		m.refresh()
		return cmd
	case notifyAssistMsg:
		if m.settingsPageOpen() != nil { // the page it was asked from is still open
			return m.push(notifyAssistPage(msg.kind, msg.f))
		}
	case cacheSavedMsg:
		if msg.err != nil && m.cfg.st != nil {
			m.cfg.st.mu.Lock()
			m.cfg.st.configNote = joinNote(m.cfg.st.configNote, msg.note)
			m.cfg.st.mu.Unlock()
		}
	case tea.KeyPressMsg:
		return m.key(msg)
	case notifySetupMsg:
		if m.settingsPageOpen() != nil { // the page it was asked from is still open
			return m.openNotifySetup(msg)
		}
	case notifySetupDone:
		m.notifySetupResult(msg)
		m.refresh()
		return m.settingsCmd(func() error { return nil })
	case atListMsg:
		cmd := m.atListed(msg)
		m.refresh()
		return cmd
	case tea.PasteMsg:
		switch {
		case m.rd.open() && m.rd.top().kind == ovNotifySetup:
			m.notifyPaste(msg.Content)
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
	items := forYouPanelItems(m.forYou)
	for _, it := range items {
		w = max(w, forYouNatural(it))
	}
	fy, extra := len(items), 0
	if fy == 0 { // the empty state's lines (the pointer among them), shown whole, as wide as they need (capped by the layout)
		empty := forYouEmpty(m.snap, m.lists.reqView.count(secOwed), m.work.decideCount())
		fy = len(empty)
		for _, l := range empty {
			w = max(w, l.Width())
		}
	} else if p, ok := planPointer(m.snap); ok { // the pointer under the decisions, past any "+N more"
		extra = 1
		w = max(w, p.line.Width())
	}
	return shellCounts{ForYou: fy, ForYouExtra: extra, Agents: len(names), ForYouWidth: w + listGutter + 2, Composing: m.ui.line != lineNone}
}

// refresh recomputes the marks, the For you list, the layout and every list's
// rows from the model. Update calls it after every change; View only draws.
func (m *Model) refresh() {
	m.keepExpansion()
	marks := m.feed.unread.marks(m.feed.feed, currentAsks)
	for i := range m.feed.feed {
		m.feed.feed[i].mark = marks[m.feed.feed[i].ev.ID]
		m.feed.feed[i].answered = m.feed.unread.answered[lineReportID(m.feed.feed[i])]
	}
	m.forYou = forYou(m.snap, m.feed.feed)
	if m.width == 0 {
		return
	}
	// One Requests view per refresh, before the layout: For you's empty state
	// is sized and filled from the same owed count (reviewer-codex).
	m.lists.reqView = requestGroups(m.requestInput())
	m.work = workOf(m.workInput())
	c := m.counts()
	m.ro.set(m.readOnly())
	if m.stoppedBanner() {
		m.layout = withBanner(shellLayoutFor(m.width, m.height-1, c, m.ui.view, m.ui.tab))
	} else {
		m.layout = shellLayoutFor(m.width, m.height, c, m.ui.view, m.ui.tab)
	}
	if m.rd.open() && m.rd.pages[0].drawer { // the plan drawer under a For you of its own rows (views-redesign §4.1)
		m.layout = shellDrawer(m.layout, c)
	}
	if m.releasesShown() { // the Plan tab's Releases column (plan-tab-r3 item 5)
		m.layout = withReleases(m.layout)
	}
	if m.ui.focus == PanelReleases && m.layout.Side.W == 0 { // the column hidden under the focus: back to the Plan
		m.ui.focus = PanelPlan
	}
	m.fillLists()
	for m.rd.open() && !m.buildPage() { // an ask answered elsewhere: its page goes
		if w := m.ui.walk; w != nil && !w.moving && m.rd.top().serial == w.serial {
			w.lost = true // the walk's own: afterUpdate moves the walk on (foureyes)
		}
		m.rd.pages = m.rd.pages[:len(m.rd.pages)-1]
		if p := m.rd.top(); p != nil {
			p.dirty = true
		}
	}
	if m.layout.Tier == TierWide && m.rd.open() && m.rd.pages[0].drawer { // the drawer to its content, the active tab under it (views-redesign §4.2)
		need := 0
		if p := m.rd.top(); p.kind == ovPlan {
			need = len(p.body) + len(p.detail) + 2
		}
		m.layout = fitDrawer(m.layout, need)
		m.placeLists()
		if p := m.rd.top(); p != nil {
			m.clampPage(p)
		}
	}
	if m.activityOnScreen() { // drawn: what it holds is seen (plan-tab-r3 item 1)
		m.ui.activitySeen = m.feed.seq
	}
}

// activityOnScreen says whether Activity is drawn: its tab (or the narrow
// tier's view) active, the dashboard shown (not the idle view) and no page
// over it; the wide drawer sized to its content leaves the tab under it
// (reviewer-codex).
func (m *Model) activityOnScreen() bool {
	l := m.layout
	if m.ui.idle || l.Active != PanelActivity || l.Tier == TierTooSmall {
		return false
	}
	if !m.rd.open() {
		return true
	}
	return l.Tier == TierWide && m.rd.pages[0].drawer && l.Lower.H > 0
}

// placeLists gives every list its frame again, after the layout moved
// (fitDrawer, fitPlanTab): only places change, never the rows.
func (m *Model) placeLists() {
	for _, p := range shellPanels {
		m.lists.of(p).rect = m.layout.rect(p)
	}
}
