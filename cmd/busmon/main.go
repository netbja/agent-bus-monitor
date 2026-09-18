// busmon — live TUI dashboard for the Agent Bus over Redis Streams.
//
// Tails one project's streams ({project}:status|report|notify|cmd) and renders:
//
//	STATUS   project name + pilot-lease driver as "⬢ MASTER <driver>", or
//	         "autonomous (no master)" when no lease is held; what is waiting on
//	         someone; the ACCOUNT budget per provider ("anthropic 25%/44%" =
//	         session/weekly window); and the MONITOR's own link to the broker.
//	AGENTS   one chip per agent, keeping three facts apart: the state the agent
//	         DECLARED (never overwritten by silence), how old that declaration
//	         is ("· no update 18m"), and whether a subscriber is armed (👂).
//	         Chips wrap to fit; the master's shows a ⬢ marker, and badges carry
//	         cmd backlog (⌛), open 4-eyes challenges (🔒), pane (⧉), context.
//	BOARD    the shared task board ({project}:board): task → owner/state/branch/age,
//	         refreshed by the same 1s ticker; the title turns "✓ all done" green
//	         when every task is done. Hidden while the board is empty.
//	ACTIVITY scrolling feed of status/report/notify/cmd events, with a dim
//	         "── Mon 2006-01-02 ──" separator at each day boundary (lines
//	         show times only). Tab focuses it; ↑↓/j/k select a line, Enter opens
//	         it in full, y copies it (OSC52, so it works over SSH), / filters by
//	         @agent, thread or words, Esc walks back out to the live tail.
//	INPUT    type a message + Enter to publish on {project}:notify; Esc/Ctrl-C quits.
//
// Three overlays sit over that layout, one keypress away so the panes can stay
// compact: the selected message in full with its thread (Enter), what is
// waiting on someone (r / F2), and the legend for every badge (? / F1).
//
// Project is required: -project or AGENT_BUS_PROJECT. -host overrides REDIS_HOST.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/netbja/agent-bus-monitor/bus"
)

// streamKinds are the four streams busmon tails into the ACTIVITY feed and that
// --reset purges.
var streamKinds = []string{"status", "report", "notify", "cmd"}

// resolveLimit picks the ACTIVITY backfill size: an explicit --limit wins, else
// AGENT_BUS_BUSMON_LIMIT, else 25. A non-numeric env value falls through to 25.
// The result is the number of most-recent merged lines to replay on launch;
// 0 (or negative) means "replay all retained history" (the pre-limit behavior).
func resolveLimit(flagSet bool, flagVal int, env string) int {
	if flagSet {
		return flagVal
	}
	if n, err := strconv.Atoi(strings.TrimSpace(env)); env != "" && err == nil {
		return n
	}
	return 25
}

// confirmReset asks for interactive confirmation before --reset purges the
// project's streams. It accepts y/yes (case-insensitive); anything else —
// including EOF from a piped or non-TTY stdin — declines, so a purge is never
// triggered by an unattended pipe.
func confirmReset(project string, in io.Reader) bool {
	fmt.Printf("Purge the 4 stream histories for project '%s' (status/report/notify/cmd)? [y/N] ", project)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// confirmDelete asks before --delete erases a project. It shows what is about to
// go — key count, agent count, how recently the project was active — because the
// slug alone is a poor thing to judge on: "demo-2574" and "demo" look equally
// disposable until you see one of them was busy a minute ago.
//
// Same accept rule as confirmReset: y/yes, and EOF from a pipe declines.
// The prompt goes to an injectable writer (confirmReset predates this and prints
// straight to stdout): what it says IS the safety feature here, so it is worth a
// test.
func confirmDelete(s bus.ProjectSummary, keys int, now time.Time, out io.Writer, in io.Reader) bool {
	fmt.Fprintf(out, "Delete ALL %d %s for project '%s' (%d agents, last activity %s)?\n",
		keys, plural(keys, "key"), s.Project, s.Agents, lastSeen(s.LastTS, now))
	fmt.Fprint(out, "This is a DEL, not --reset: streams, consumer groups, agents/usage/budget/verdicts, "+
		"pilot and gate leases all go. [y/N] ")
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// deleteProject implements `busmon --delete <project>`. A project with no keys is
// a no-op success: that is the typo signal (you asked to delete 31 keys and were
// told there are none) without making a repeated cleanup script fail.
func deleteProject(host, project string, yes bool, in io.Reader, now time.Time) error {
	client, err := bus.Connect(host)
	if err != nil {
		return fmt.Errorf("Redis connection failed: %w", err)
	}
	defer client.Close()
	ctx := context.Background()

	keys, err := bus.ProjectKeys(ctx, client, project)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		fmt.Fprintf(os.Stderr, "no keys for project '%s' — nothing to delete\n", project)
		return nil
	}

	summary, err := bus.ProjectInfo(ctx, client, project)
	if err != nil {
		return err
	}
	if !yes && !confirmDelete(summary, len(keys), now, os.Stdout, in) {
		fmt.Println("Cancelled.")
		return nil
	}

	n, err := bus.DeleteProject(ctx, client, project)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted %d %s for project '%s'.\n", n, plural(int(n), "key"), project)
	return nil
}

// plural is the usual English -s. Only ever fed regular nouns here.
func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// listProjects writes the `busmon --list` table to out. A broker with no
// projects reports on stderr and leaves out untouched, so `busmon --list | wc -l`
// counts projects and nothing else.
func listProjects(host string, out io.Writer, now time.Time) error {
	client, err := bus.Connect(host)
	if err != nil {
		return fmt.Errorf("Redis connection failed: %w", err)
	}
	defer client.Close()

	list, err := bus.Projects(context.Background(), client)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(os.Stderr, "no projects on this bus yet")
		return nil
	}
	_, err = io.WriteString(out, projectTable(list, now))
	return err
}

const (
	idleAfter  = 2 * time.Minute
	staleAfter = 10 * time.Minute

	feedCap      = 500 // ACTIVITY lines retained for display + selection
	maxAgentRows = 5   // AGENTS content rows before the "+N" overflow marker

	pollBudget     = 3 * time.Second // deadline for ALL of one tick's reads
	tailRetry      = 2 * time.Second // pause before resuming the feed after a broker error
	requestPoll    = 5               // ticks between follow-up view refreshes
	openThreadScan = 200             // cmd entries scanned for unanswered exchanges

	// The input line advertises the two keys that are otherwise invisible from
	// a focused text field.
	inputTitle = " INPUT  [gray][Tab feed · F1 legend · F2 requests][-] "
)

// agentState is what busmon knows about one agent, with the three facts the old
// chip conflated kept apart:
//
//	state/message/stateAt — the last state the agent DECLARED, and when.
//	lastSeen              — the last time it published anything at all. A report
//	                        proves the process is alive; it does not refresh the
//	                        declared state, so the two stamps differ on purpose.
//	armed                 — a live subscribe lease. A lease, not a state.
type agentState struct {
	state    string
	message  string
	stateAt  time.Time // when the state above was declared
	lastSeen time.Time // last activity of any kind on the bus
	gated    int       // open 4-eyes challenges; >0 shows a lock badge
	armed    bool      // a live subscribe lease exists → 👂 listening badge
	lag      int64     // unconsumed {p}:cmd entries for this agent → ⌛ backlog badge
	pane     string    // HERDR_PANE_ID from the agents hash → ⧉ herdr-attached badge
	usage    string    // this agent's own context fill from the usage hash → [..] badge
}

// shared is every piece of bus state the polling goroutines publish to the UI.
// One mutex covers all of it: the writers are two background goroutines, the
// readers are the render functions on the tview loop, and the whole snapshot is
// small enough that finer locking would only buy bugs.
type shared struct {
	mu       sync.Mutex
	agents   map[string]*agentState
	pilot    string                        // pilot-lease driver, "" = autonomous
	budgets  map[string]bus.BudgetSnapshot // account scope, per provider
	board    map[string]bus.BoardEntry
	requests []request    // tracked requests read from the board
	threads  []openThread // cmd exchanges with no answer in retained history
	health   health       // the MONITOR's own link to the broker
}

func renderAgents(layout *tview.Flex, row *tview.Flex, view *tview.TextView, st *shared) {
	st.mu.Lock()
	defer st.mu.Unlock()
	names := make([]string, 0, len(st.agents))
	for n := range st.agents {
		names = append(names, n)
	}
	sort.Strings(names)
	now := time.Now()
	_, _, w, _ := view.GetInnerRect()
	if w < 1 {
		w = 80 // before the first layout pass; corrected on the next render
	}
	chips := make([]chip, 0, len(names))
	for _, n := range names {
		lbl := agentLabel(n, st.agents[n], now, n == st.pilot)
		chips = append(chips, chip{lbl, tview.TaggedStringWidth(lbl)})
	}
	rows, used := packChips(chips, w, maxAgentRows)
	view.SetText(strings.Join(rows, "\n"))
	layout.ResizeItem(row, used+2, 0) // +2 borders; grow to fit, capped by maxAgentRows
}

// renderBoard updates the BOARD pane from the snapshot the 1s ticker refreshes.
// An empty board hides the pane entirely (ResizeItem 0,0) so AGENTS keeps the
// full row width.
func renderBoard(row *tview.Flex, view *tview.TextView, st *shared) {
	st.mu.Lock()
	m := st.board
	st.mu.Unlock()
	if len(m) == 0 {
		row.ResizeItem(view, 0, 0)
		return
	}
	row.ResizeItem(view, 0, 1)
	_, _, w, _ := view.GetInnerRect()
	if w < 1 {
		w = 40 // before the first layout pass; corrected on the next render
	}
	view.SetText(boardPanel(m, time.Now(), w))
	view.SetTitle(boardTitle(m))
}

// renderStatus updates the top status bar: project, master, what is waiting,
// account budget, and the monitor's own connection.
func renderStatus(view *tview.TextView, project string, st *shared) {
	now := time.Now()
	st.mu.Lock()
	d := statusData{
		project: project,
		driver:  st.pilot,
		budgets: st.budgets,
		waiting: waitingLine(st.requests, st.threads, now),
		health:  st.health,
		now:     now,
	}
	st.mu.Unlock()
	view.SetText(statusBar(d))
}

// renderRequests fills the follow-up overlay from the same snapshot.
func renderRequests(view *tview.TextView, st *shared) {
	now := time.Now()
	st.mu.Lock()
	reqs, threads := st.requests, st.threads
	st.mu.Unlock()
	_, _, w, _ := view.GetInnerRect()
	if w < 1 {
		w = 80
	}
	waiting := len(threads)
	for _, r := range trackedOnly(reqs) {
		if r.waiting() {
			waiting++
		}
	}
	view.SetTitle(requestsTitle(waiting))
	view.SetText(requestsPanel(reqs, threads, now, w))
}

func main() {
	host := flag.String("host", "", "Redis host (overrides REDIS_HOST)")
	projectFlag := flag.String("project", "", "project namespace (or AGENT_BUS_PROJECT)")
	limitFlag := flag.Int("limit", 25, "ACTIVITY backfill: replay the last N lines on launch (0 = all history; or AGENT_BUS_BUSMON_LIMIT)")
	resetFlag := flag.Bool("reset", false, "purge the project's streams before launching (asks to confirm)")
	yesFlag := flag.Bool("yes", false, "skip the --reset confirmation prompt")
	listFlag := flag.Bool("list", false, "list the projects on this bus and exit")
	deleteFlag := flag.String("delete", "", "delete every key of a project (DEL, not --reset's XTRIM) and exit")
	flag.Parse()

	limitSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "limit" {
			limitSet = true
		}
	})
	limit := resolveLimit(limitSet, *limitFlag, os.Getenv("AGENT_BUS_BUSMON_LIMIT"))

	// --list answers "which projects are on this bus?", so it MUST run before the
	// project-required exit below — that error is the very thing it exists to
	// resolve. It also wins over --reset: a read-only query never purges.
	if *listFlag {
		if err := listProjects(*host, os.Stdout, time.Now()); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// --delete names its own target, so like --list it must not require --project.
	// It also sits above --reset: purging a project's history on the way to
	// deleting the project would be work done twice and a confirmation asked twice.
	if *deleteFlag != "" {
		if err := deleteProject(*host, *deleteFlag, *yesFlag, os.Stdin, time.Now()); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	project := *projectFlag
	if project == "" {
		project = os.Getenv("AGENT_BUS_PROJECT")
	}
	if project == "" {
		fmt.Fprintln(os.Stderr, "error: project required: -project <p> or AGENT_BUS_PROJECT")
		os.Exit(1)
	}
	self := os.Getenv("AGENT_BUS_AGENT")
	if self == "" {
		self = "hermes"
	}

	client, err := bus.Connect(*host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: Redis connection failed: %v\n", err)
		os.Exit(1)
	}
	b, err := bus.Open(client, project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	// --reset purges the project's streams before the TUI starts (terminal still
	// in normal mode for the confirmation prompt). XTRIM clears history but keeps
	// consumer groups + armed/pilot/gate leases.
	if *resetFlag {
		if !*yesFlag && !confirmReset(project, os.Stdin) {
			fmt.Println("Cancelled.")
			os.Exit(0)
		}
		removed, err := b.Purge(ctx, streamKinds)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: purge failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Purged: %d entries removed.\n", removed)
	}

	app := tview.NewApplication()

	statusView := tview.NewTextView().SetDynamicColors(true)

	agentsView := tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	agentsView.SetBorder(true).SetTitle(" AGENTS ")

	boardView := tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	boardView.SetBorder(true).SetTitle(" BOARD ")

	activityView := tview.NewTextView()
	activityView.SetDynamicColors(true).SetRegions(true).SetMaxLines(feedCap).SetScrollable(true)
	activityView.SetBorder(true).SetTitle(activityTitle(0, 0, 0))
	activityView.ScrollToEnd()

	input := tview.NewInputField().SetLabel("> ")
	// Inherit the terminal's own palette instead of tview's default white-on-blue
	// field (ContrastBackgroundColor/PrimaryTextColor), which is hard to read.
	input.SetFieldBackgroundColor(tcell.ColorDefault).SetFieldTextColor(tcell.ColorDefault)
	input.SetBorder(true).SetTitle(inputTitle)

	// The three overlays. They are pages over the main layout rather than panes
	// in it: the brief is a readable screen with the detail one keypress away,
	// and a fourth permanent pane would cost the ACTIVITY feed its height on
	// exactly the narrow terminals where it is already tight.
	detailView := tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetWordWrap(true)
	detailView.SetScrollable(true).SetBorder(true)

	helpView := tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	helpView.SetScrollable(true).SetBorder(true)
	helpView.SetTitle(helpTitle())
	helpView.SetText(helpText())

	requestsView := tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	requestsView.SetScrollable(true).SetBorder(true)
	requestsView.SetTitle(requestsTitle(0))

	// Everything the background goroutines publish to the UI. Empty until the
	// first tick: an unknown budget stays blank rather than implying 0% used,
	// and the board pane stays hidden while there is nothing to show. The
	// monitor's link starts healthy because bus.Connect just pinged it.
	st := &shared{
		agents:  map[string]*agentState{},
		budgets: map[string]bus.BudgetSnapshot{},
		board:   map[string]bus.BoardEntry{},
		health:  health{ok: true, since: time.Now()},
	}

	// ACTIVITY line-selection state. Everything here is touched only on the
	// tview event loop (input handlers + QueueUpdateDraw both run there), so it
	// needs no locking — unlike the agents map, which the tail goroutine writes.
	var feed []feedLine     // last feedCap entries, parallel to the view's regions
	var seq int             // monotonic region id source (ids are never reused)
	selID := ""             // selected region id; "" = live tail (no selection)
	var screen tcell.Screen // captured each draw, for clipboard (OSC52) writes
	lastDay := ""           // day of the last feed entry; a change inserts a day separator
	filter := feedFilter{}  // display filter; the retained feed is never narrowed
	shown := 0              // feed lines currently admitted by the filter
	filtering := false      // the INPUT line is collecting a filter, not a message

	pages := tview.NewPages()
	// What to focus when an overlay closes, so Esc lands back where the
	// operator was rather than always at the input line.
	var overlayReturn tview.Primitive = input

	refreshTitle := func() {
		if pos := selPos(feed, selID); selID != "" && pos >= 0 {
			activityView.SetTitle(selectionTitle(pos+1, len(feed)))
			return
		}
		if !filter.empty() {
			activityView.SetTitle(filterTitle(filter, shown, len(feed)))
			return
		}
		row, _ := activityView.GetScrollOffset()
		_, _, _, height := activityView.GetInnerRect()
		activityView.SetTitle(activityTitle(activityView.GetWrappedLineCount(), row, height))
	}
	// appendFeed retains one line and shows it if the filter admits it. The feed
	// keeps every line either way: a filter hides, it never forgets.
	appendFeed := func(fl feedLine) {
		feed = append(feed, fl)
		if filter.match(fl) {
			shown++
			fmt.Fprintf(activityView, "[\"%s\"]%s[\"\"]\n", fl.id, fl.line)
		}
		if len(feed) > feedCap {
			feed = feed[len(feed)-feedCap:]
		}
	}
	// redrawFeed rebuilds the view from the retained lines — used whenever the
	// filter changes, including when it is cleared and the whole journal returns.
	redrawFeed := func() {
		activityView.Clear()
		shown = 0
		for _, fl := range feed {
			if !filter.match(fl) {
				continue
			}
			shown++
			fmt.Fprintf(activityView, "[\"%s\"]%s[\"\"]\n", fl.id, fl.line)
		}
		if selID != "" {
			activityView.Highlight(selID).ScrollToHighlight()
		} else {
			activityView.ScrollToEnd()
		}
		refreshTitle()
	}
	selectLine := func(id string) {
		selID = id
		activityView.Highlight(id).ScrollToHighlight()
		input.SetTitle(inputTitle)
		refreshTitle()
	}
	// visibleAt finds the nth visible line, so ↑↓ walk what is on screen rather
	// than stepping through lines the filter is hiding.
	visible := func() []feedLine {
		if filter.empty() {
			return feed
		}
		out := make([]feedLine, 0, shown)
		for _, fl := range feed {
			if filter.match(fl) {
				out = append(out, fl)
			}
		}
		return out
	}
	enterSelect := func() {
		if v := visible(); len(v) > 0 {
			selectLine(v[len(v)-1].id)
		}
	}
	moveSelect := func(delta int) {
		if id := moveSelection(visible(), selID, delta); id != "" {
			selectLine(id)
		}
	}
	exitSelect := func() {
		selID = ""
		activityView.Highlight()   // no args clears the highlight
		activityView.ScrollToEnd() // resume live tail
		input.SetTitle(inputTitle)
		refreshTitle()
	}
	copyText := func(s, what string) {
		if screen == nil || s == "" {
			return
		}
		screen.SetClipboard([]byte(s)) // OSC52 — reaches the local clipboard even over SSH
		input.SetTitle(fmt.Sprintf(" INPUT  [green][✓ %s copied][-] ", what))
	}
	copySelect := func() {
		i := selPos(feed, selID)
		if i < 0 {
			return
		}
		copyText(feed[i].text, "line")
	}

	closeOverlay := func() {
		for _, name := range []string{"detail", "help", "requests"} {
			pages.HidePage(name)
		}
		app.SetFocus(overlayReturn)
	}
	showOverlay := func(name string, p tview.Primitive) {
		if activityView.HasFocus() {
			overlayReturn = activityView
		} else {
			overlayReturn = input
		}
		pages.ShowPage(name)
		app.SetFocus(p)
	}

	// Detail state. detailGen discards a thread read whose overlay has already
	// been replaced — otherwise a slow read would overwrite a newer message.
	var detailEvent bus.Event
	var detailThread threadState
	detailGen := 0

	openDetail := func() {
		i := selPos(feed, selID)
		if i < 0 || feed[i].ev.Kind == "" { // nothing selected, or a day separator
			return
		}
		fl := feed[i]
		detailEvent = fl.ev
		detailThread = threadState{ID: threadID(fl.ev)}
		detailGen++
		gen := detailGen
		if detailThread.ID == "" {
			detailThread.Loaded = true
		} else {
			go func(e bus.Event, want int) {
				entries, err := b.Thread(ctx, threadID(e))
				app.QueueUpdateDraw(func() {
					if want != detailGen {
						return // the operator opened something else meanwhile
					}
					detailThread = threadState{ID: threadID(e), Entries: entries, Loaded: true, Err: err}
					detailView.SetText(detailText(e, entryTime(e.ID), time.Now(), detailThread))
				})
			}(fl.ev, gen)
		}
		detailView.SetTitle(detailTitle(fl.ev))
		detailView.SetText(detailText(fl.ev, fl.at, time.Now(), detailThread))
		detailView.ScrollToBeginning()
		showOverlay("detail", detailView)
	}

	startFilter := func() {
		filtering = true
		input.SetLabel("filter> ").SetText(filter.raw)
		input.SetTitle(" FILTER  [gray][@agent · thread id · words · Enter applies · Esc cancels][-] ")
		app.SetFocus(input)
	}
	endFilter := func(apply bool) {
		if apply {
			filter = parseFilter(input.GetText())
			selID = ""
			activityView.Highlight()
		}
		filtering = false
		input.SetLabel("> ").SetText("")
		input.SetTitle(inputTitle)
		redrawFeed()
		app.SetFocus(activityView)
	}
	clearFilter := func() {
		filter = feedFilter{}
		redrawFeed()
	}

	input.SetAutocompleteFunc(func(currentText string) []string {
		st.mu.Lock()
		names := make([]string, 0, len(st.agents))
		for n := range st.agents {
			names = append(names, n)
		}
		st.mu.Unlock()
		return agentCompletions(currentText, names)
	})

	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			if filtering {
				endFilter(true)
				return
			}
			if text := strings.TrimSpace(input.GetText()); text != "" {
				if tgt, body, directed := parseDirected(text); directed {
					b.Cmd(ctx, self, tgt, bus.CmdDirective, "", body)
				} else {
					b.Notify(ctx, self, text)
				}
			}
			input.SetText("")
		}
	})

	agentsRow := tview.NewFlex().
		AddItem(agentsView, 0, 1, false).
		AddItem(boardView, 0, 0, false) // hidden until the board has a task

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(statusView, 1, 0, false).
		AddItem(agentsRow, 3, 0, false).
		AddItem(activityView, 0, 1, false).
		AddItem(input, 3, 0, true)

	focusInput := func() {
		exitSelect()
		app.SetFocus(input)
	}
	enterFeed := func() {
		app.SetFocus(activityView)
		enterSelect()
	}
	selectFirstVisible := func() {
		if v := visible(); len(v) > 0 {
			selectLine(v[0].id)
		}
	}
	openRequests := func() {
		renderRequests(requestsView, st)
		requestsView.ScrollToBeginning()
		showOverlay("requests", requestsView)
	}
	openHelp := func() {
		helpView.ScrollToBeginning()
		showOverlay("help", helpView)
	}

	app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyCtrlC {
			app.Stop()
			return nil
		}
		// While an overlay is up it owns the keyboard: its own capture handles
		// Esc and the copy keys, and tview's TextView already scrolls with
		// arrows, j/k, g/G and PageUp/Down.
		if front, _ := pages.GetFrontPage(); front != "main" {
			return ev
		}
		switch ev.Key() {
		case tcell.KeyF1:
			openHelp()
			return nil
		case tcell.KeyF2:
			openRequests()
			return nil
		case tcell.KeyTab, tcell.KeyBacktab:
			if filtering {
				return nil // Tab would leave a half-typed filter behind
			}
			if activityView.HasFocus() {
				focusInput()
			} else {
				enterFeed()
			}
			return nil
		case tcell.KeyEscape:
			switch {
			case filtering:
				endFilter(false)
			case activityView.HasFocus() && selID != "":
				exitSelect() // step one: drop the selection, keep the filter
			case activityView.HasFocus() && !filter.empty():
				clearFilter() // step two: the whole journal is back
			case activityView.HasFocus():
				focusInput()
			default:
				app.Stop()
			}
			return nil
		}
		// Line-selection keys, active only while the ACTIVITY feed is focused.
		// (When INPUT is focused these fall through so the field gets them.)
		if activityView.HasFocus() {
			switch ev.Key() {
			case tcell.KeyUp:
				moveSelect(-1)
				return nil
			case tcell.KeyDown:
				moveSelect(1)
				return nil
			case tcell.KeyHome:
				selectFirstVisible()
				return nil
			case tcell.KeyEnd:
				enterSelect()
				return nil
			case tcell.KeyEnter:
				openDetail()
				return nil
			case tcell.KeyRune:
				switch ev.Rune() {
				case 'k':
					moveSelect(-1)
				case 'j':
					moveSelect(1)
				case 'g':
					selectFirstVisible()
				case 'G':
					enterSelect()
				case 'y':
					copySelect()
				case 'v':
					openDetail()
				case '/':
					startFilter()
				case 'r':
					openRequests()
				case '?':
					openHelp()
				case 'q':
					app.Stop()
				default:
					return ev // let PageUp/Down, mouse wheel, etc. reach tview
				}
				return nil
			}
		}
		return ev
	})

	// Overlay keys. Esc/q close; y and Y copy what the overlay is showing —
	// the message exactly as published, or the whole thread as plain text.
	overlayKeys := func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEscape || ev.Rune() == 'q' {
			closeOverlay()
			return nil
		}
		return ev
	}
	helpView.SetInputCapture(overlayKeys)
	requestsView.SetInputCapture(overlayKeys)
	detailView.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case 'y':
			copyText(detailPlain(detailEvent), "message")
			return nil
		case 'Y':
			copyText(threadPlain(detailEvent, detailThread), "thread")
			return nil
		}
		return overlayKeys(ev)
	})
	// Capture the live screen each draw so copySelect can reach the clipboard;
	// returning false keeps the normal draw.
	app.SetBeforeDrawFunc(func(s tcell.Screen) bool {
		screen = s
		return false
	})

	// handle renders one stream event into the ACTIVITY feed and updates the
	// agents map. It runs on the tail goroutine; the feed mutation is deferred to
	// the tview loop via QueueUpdateDraw. Shared by the startup backfill and the
	// live tail.
	// Per-stream cursor of the last entry handled, so the tail can resume from
	// exactly here after a broker outage instead of replaying or skipping.
	// Written only on the tail goroutine, which is the only caller of handle.
	cursors := map[string]string{}

	handle := func(e bus.Event) {
		et := entryTime(e.ID)
		ts := et.Format("15:04:05")
		cursors[bus.StreamKey(project, e.Kind)] = e.ID
		var line, plain string // line = colored display; plain = tag-free, for the clipboard
		switch e.Kind {
		case "status":
			st.mu.Lock()
			a := st.agents[e.Agent]
			if a == nil {
				a = &agentState{}
				st.agents[e.Agent] = a
			}
			a.state, a.message, a.stateAt = e.State, e.Message, et
			a.lastSeen = et
			st.mu.Unlock()
			line = tag("gray", ts) + " " + tag(stateColor(e.State), "["+e.Agent+"]") + " " + tview.Escape(e.State)
			plain = ts + " [" + e.Agent + "] " + e.State
			if e.Message != "" {
				line += " | " + tview.Escape(e.Message)
				plain += " | " + e.Message
			}
		case "notify":
			line = tag("gray", ts) + " " + tag("aqua", "[notify]") + " " + tview.Escape(e.Message)
			plain = ts + " [notify] " + e.Message
		case "cmd":
			label := "[" + e.Type + " " + e.From + "->" + e.Target
			if e.Ref != "" {
				label += " " + e.Ref
			}
			label += "]"
			line = tag("gray", ts) + " " + tag("fuchsia", label) + " " + tview.Escape(e.Message)
			plain = ts + " " + label + " " + e.Message
		case "report":
			// A report proves the process is alive. It says nothing about what
			// the agent's state is now, so it moves lastSeen and leaves the
			// declared state — and the message shown beside it — untouched.
			// Filling either in from a report is how presence starts passing
			// for progress.
			st.mu.Lock()
			a := st.agents[e.Agent]
			if a == nil {
				a = &agentState{}
				st.agents[e.Agent] = a
			}
			a.lastSeen = et
			st.mu.Unlock()
			marker := reportMarker(e.Full)
			line = tag("gray", ts) + " " + tag("teal", "[report:"+e.RKind+"->"+e.Agent+"]") + " " + tview.Escape(e.Message) + marker
			plain = ts + " [report:" + e.RKind + "->" + e.Agent + "] " + e.Message + marker
		default:
			line = tag("gray", ts) + " " + tview.Escape(e.Message)
			plain = ts + " " + e.Message
		}
		app.QueueUpdateDraw(func() {
			// A day boundary inserts a dim separator before the event, so a
			// multi-day feed stays readable even though lines show times only.
			// Backfill and the live tail share this path, and lastDay starts
			// empty, so the very first line of a launch is always anchored.
			if day := et.Format("2006-01-02"); day != lastDay {
				lastDay = day
				seq++
				sepLine, sepPlain := daySeparator(et)
				appendFeed(feedLine{id: strconv.Itoa(seq), line: sepLine, text: sepPlain, at: et})
			}
			seq++
			// The line is wrapped in a region so it can be selected/highlighted;
			// tview.Escape above already neutralised any ["..."] in messages.
			appendFeed(feedLine{id: strconv.Itoa(seq), line: line, text: plain, ev: e, at: et})
			refreshTitle()
			renderAgents(layout, agentsRow, agentsView, st)
			renderStatus(statusView, project, st)
		})
	}

	// Backfill, then live-tail. With a limit, fetch the last N merged entries up
	// front (fatal on error, while the terminal is still ours) and resume the
	// live tail from the per-stream cursors Recent returns — no replay, no "$"
	// gap. Rendering runs in the goroutine so it drains through QueueUpdateDraw
	// concurrently with app.Run(); a large backfill must not block on the queue
	// before the loop starts. limit <= 0 keeps the original behavior: replay all
	// retained history.
	// setHealth records the monitor's OWN link to the broker. A transition
	// stamps `since`, so the bar can say how long it has been down — and an
	// unreachable broker never gets rendered as agents falling silent.
	setHealth := func(err error) {
		st.mu.Lock()
		defer st.mu.Unlock()
		if err != nil {
			if st.health.ok {
				st.health.since = time.Now()
			}
			st.health.ok, st.health.err = false, err.Error()
			return
		}
		if !st.health.ok {
			st.health.since = time.Now()
		}
		st.health.ok, st.health.err = true, ""
	}

	var backfill []bus.Event
	if limit > 0 {
		recent, cur, err := b.Recent(ctx, streamKinds, limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: backfill failed: %v\n", err)
			os.Exit(1)
		}
		backfill = recent
		for k, v := range cur {
			cursors[k] = v
		}
	}
	go func() {
		for _, e := range backfill {
			handle(e)
		}
		replayAll := limit <= 0 // no --limit: replay everything once…
		for {
			var err error
			if replayAll {
				replayAll = false
				err = b.Tail(ctx, "0", streamKinds, handle)
			} else {
				err = b.TailFrom(ctx, cursors, streamKinds, handle) // …then always from where we stopped
			}
			if ctx.Err() != nil {
				return
			}
			// Without this retry the feed is the one thing that dies quietly
			// when the broker goes away: the ticker keeps drawing, the chips
			// keep aging, and no new line ever arrives again. handle has kept
			// `cursors` current, so the resumed tail neither replays nor skips.
			if err != nil {
				setHealth(err)
			}
			time.Sleep(tailRetry)
		}
	}()

	// Poll pilot mode + per-agent gate counts + armed leases + cmd backlog +
	// the shared task board off the UI thread; re-render so chips age and
	// badges update with no new traffic.
	go func() {
		tick := 0
		for range time.Tick(time.Second) {
			tick++
			// PilotDriver is the canary for the monitor's own link: a plain GET,
			// run every tick, whose failure means busmon is blind — not that the
			// agents have gone quiet.
			//
			// It gets its own short deadline. Left to the client's dial timeout
			// and retries, a stopped broker takes ~25s to surface as an error,
			// and the bar cheerfully reports "bus ok" throughout — the precise
			// lie this indicator exists to prevent. A GET that has not answered
			// in healthCanary seconds is not a healthy link, tunnel or not.
			// The whole tick shares one deadline. Bounding only the canary is
			// not enough: every later call would still sit on its own dial
			// timeout, so the redraw carrying the bad news arrives half a
			// minute after the news itself.
			//
			// The deadline is only worth this much because bus.Connect sets
			// ContextTimeoutEnabled on the client. Left at go-redis's default
			// it governs dialing and waiting for a free connection but not I/O
			// on one already open, so a black-holed link — packets dropped, the
			// socket still up — would sail past it and the bar would keep
			// saying "bus ok". If that option ever goes away, so does this
			// guarantee.
			pollCtx, cancelPoll := context.WithTimeout(ctx, pollBudget)
			driver, perr := b.PilotDriver(pollCtx)
			setHealth(perr)
			if perr != nil {
				// The link is down. Draw that immediately and keep every
				// previous snapshot: the remaining reads would each sit on
				// their own timeout, and their empty results would blank the
				// budget and the board — which reads as "nothing published"
				// rather than "nobody could ask". A stale number under a red
				// link is honest; a blank one is not.
				cancelPoll()
				app.QueueUpdateDraw(func() {
					renderAgents(layout, agentsRow, agentsView, st)
					renderStatus(statusView, project, st)
					refreshTitle()
				})
				continue
			}
			armed, armedErr := b.ArmedAgents(pollCtx)
			lag, lagErr := b.CmdLag(pollCtx)
			snaps, snapsErr := b.Agents(pollCtx)
			usageSnaps, usageErr := b.Usage(pollCtx)
			budgetSnaps, budgetErr := b.Budgets(pollCtx)
			boardSnap, boardErr := b.Board(pollCtx)

			// The follow-up view costs a hash read plus a slice of the cmd
			// stream, and it changes at human speed, so it refreshes every few
			// seconds rather than every tick. A failed read leaves the previous
			// answer in place instead of blanking the view.
			var reqs []request
			var haveReqs bool
			var threads []openThread
			var haveThreads bool
			// The board is already in hand, and the typed reader derived
			// Availability with it, so the requests view costs nothing extra.
			// Only the cmd-stream scan behind the untracked-threads list is
			// throttled: that one is an XREVRANGE over hundreds of entries.
			if boardErr == nil {
				reqs, haveReqs = boardRequests(boardSnap), true
			}
			if tick%requestPoll == 1 {
				if cmds, _, err := b.Recent(pollCtx, []string{"cmd"}, openThreadScan); err == nil {
					st.mu.Lock()
					known := st.requests
					st.mu.Unlock()
					if haveReqs {
						known = reqs
					}
					threads, haveThreads = openThreads(cmds, known), true
				}
			}

			st.mu.Lock()
			names := make([]string, 0, len(st.agents))
			for n := range st.agents {
				names = append(names, n)
			}
			st.mu.Unlock()
			gates := make(map[string]int, len(names))
			for _, n := range names {
				if m, err := b.OpenChallenges(pollCtx, n); err == nil {
					gates[n] = len(m)
				}
			}

			st.mu.Lock()
			st.pilot = driver
			// Each snapshot survives its own failed read, for the same reason:
			// an unanswered question must not be rendered as an answer.
			if budgetErr == nil {
				st.budgets = budgetSnaps
			}
			if boardErr == nil {
				st.board = boardSnap
			}
			if haveReqs {
				st.requests = reqs
			}
			if haveThreads {
				st.threads = threads
			}
			// The agents hash is the authoritative roster: it holds every agent
			// that has ever published a status, so discovery no longer depends
			// on whether that status happened to fall inside the --limit window
			// of the ACTIVITY backfill.
			for n, s := range agentsOrNil(snaps, snapsErr) {
				a := st.agents[n]
				if a == nil {
					a = &agentState{}
					st.agents[n] = a
				}
				if ts := time.UnixMilli(s.TS); ts.After(a.stateAt) {
					a.state, a.message, a.stateAt = s.State, s.Message, ts
					if ts.After(a.lastSeen) {
						a.lastSeen = ts
					}
				}
			}
			// Surface agents known only via a live armed lease (subscribed but no
			// status published yet). Armed keys are TTL'd, so this never leaks a
			// ghost. They get no state: a lease says a subscriber is listening,
			// not what the agent is doing. Lag-only groups are NOT synthesized —
			// consumer groups persist after an agent is gone, so a stale group
			// must not conjure a chip.
			for n := range armed {
				if st.agents[n] == nil {
					st.agents[n] = &agentState{}
				}
			}
			for n, a := range st.agents {
				if armedErr == nil {
					_, a.armed = armed[n]
				}
				if lagErr == nil {
					a.lag = lag[n]
				}
				if snapsErr == nil {
					a.pane = snaps[n].Pane
				}
				if usageErr == nil {
					a.usage = usageBadge(usageSnaps[n])
				}
				if c, ok := gates[n]; ok {
					a.gated = c
				}
			}
			st.mu.Unlock()

			cancelPoll()

			app.QueueUpdateDraw(func() {
				renderAgents(layout, agentsRow, agentsView, st)
				renderBoard(agentsRow, boardView, st)
				renderStatus(statusView, project, st)
				if front, _ := pages.GetFrontPage(); front == "requests" {
					renderRequests(requestsView, st)
				}
				refreshTitle()
			})
		}
	}()

	pages.AddPage("main", layout, true, true)
	pages.AddPage("requests", modal(requestsView), true, false)
	pages.AddPage("help", modal(helpView), true, false)
	pages.AddPage("detail", modal(detailView), true, false)

	if err := app.SetRoot(pages, true).EnableMouse(true).SetFocus(input).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
