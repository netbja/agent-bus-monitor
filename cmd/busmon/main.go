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

	feedCap = 500 // ACTIVITY lines retained for display + selection

	pollBudget     = 3 * time.Second // deadline for ALL of one tick's reads
	tailRetry      = 2 * time.Second // pause before resuming the feed after a broker error
	requestPoll    = 5               // ticks between follow-up view refreshes
	wakePoll       = 5               // ticks between dispatcher-badge reads (Obligation/Wake/DispatchAlive)
	openThreadScan = 200             // cmd entries scanned for unanswered exchanges
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
	stateAt  time.Time       // when the state above was declared
	lastSeen time.Time       // last activity of any kind on the bus
	gated    int             // open 4-eyes challenges (flag "gate N")
	armed    bool            // a live subscribe lease exists (flag "armed")
	lag      int64           // unconsumed {p}:cmd entries of the agent's group (detail only)
	pane     string          // HERDR_PANE_ID from the agents hash (detail only)
	waiting  int64           // wake-relevant messages (pending + unread): the queue column
	stale    int             // addressed entries older than the dispatch horizon (flag "stale N")
	gap      bool            // entries may have been trimmed unread (detail only)
	wake     *bus.WakeRecord // the dispatcher's wake record, nil when none

	pid         int       // AgentSnapshot.PID (0 = not recorded)
	pidStart    uint64    // AgentSnapshot.PIDStart
	queueKnown  bool      // an Obligation read has succeeded at least once
	queueFailed bool      // the latest attempted Obligation read failed (or the broker is down)
	queueAt     time.Time // time of the last successful Obligation read
	ctxRaw      string    // UsageSnapshot.Ctx exactly as read
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
	dispatch string                        // "", "up" or "down"
	board    map[string]bus.BoardEntry
	requests []request    // tracked requests read from the board
	threads  []openThread // cmd exchanges with no answer in retained history
	health   health       // the MONITOR's own link to the broker
}

func main() {
	host := flag.String("host", "", "Redis host (overrides REDIS_HOST)")
	projectFlag := flag.String("project", "", "project namespace (or AGENT_BUS_PROJECT)")
	limitFlag := flag.Int("limit", 25, "ACTIVITY backfill: replay the last N lines on launch (0 = all history; or AGENT_BUS_BUSMON_LIMIT)")
	resetFlag := flag.Bool("reset", false, "purge the project's streams before launching (asks to confirm)")
	yesFlag := flag.Bool("yes", false, "skip the --reset confirmation prompt")
	listFlag := flag.Bool("list", false, "list the projects on this bus and exit")
	deleteFlag := flag.String("delete", "", "delete every key of a project (DEL, not --reset's XTRIM) and exit")
	themeFlag := flag.String("theme", "", "colour theme: auto, dark or light (or AGENT_BUS_BUSMON_THEME; default auto)")
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

	themeVal := *themeFlag
	// tview initialises the screen it is given (exactly once); the colour
	// depth is read from that same screen before anything is drawn.
	scr, err := tcell.NewScreen()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: terminal: %v\n", err)
		os.Exit(1)
	}
	app := tview.NewApplication().SetScreen(scr)
	_, noColor := os.LookupEnv("NO_COLOR")
	t, err := ResolveTheme(themeVal, os.Getenv("AGENT_BUS_BUSMON_THEME"), os.Getenv("COLORFGBG"), noColor, scr.Colors())
	if err != nil {
		scr.Fini()
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	th = t
	setRoundedBorders()

	// Everything the background goroutines publish to the UI. Empty until the
	// first tick: an unknown budget stays blank rather than implying 0% used.
	// The monitor's link starts healthy because bus.Connect just pinged it.
	st := &shared{
		agents:  map[string]*agentState{},
		budgets: map[string]bus.BudgetSnapshot{},
		board:   map[string]bus.BoardEntry{},
		health:  health{ok: true, since: time.Now()},
	}
	u := newUI(app, st, ctx, b, project, self)

	cursors := map[string]string{}
	handle := func(e bus.Event) {
		cursors[bus.StreamKey(project, e.Kind)] = e.ID
		u.ingest(e)
		app.QueueUpdateDraw(func() { u.appendEvent(e) })
	}

	// Backfill, then live-tail. With a limit, fetch the last N merged entries up
	// front (fatal on error, while the terminal is still ours) and resume the
	// live tail from the per-stream cursors Recent returns — no replay, no "$"
	// gap. limit <= 0 keeps the original behavior: replay all retained history.
	var backfill []bus.Event
	if limit > 0 {
		recent, cur, err := b.Recent(ctx, streamKinds, limit)
		if err != nil {
			scr.Fini()
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
			// Without this retry the feed dies quietly when the broker goes
			// away; handle keeps `cursors` current, so the resumed tail neither
			// replays nor skips.
			if err != nil {
				st.setHealth(err)
			}
			time.Sleep(tailRetry)
		}
	}()

	// The poller: one pollOnce per second, then a redraw so ages and flags
	// move with no new traffic.
	go func() {
		tick := 0
		for range time.Tick(time.Second) {
			tick++
			pollOnce(ctx, b, st, tick)
			app.QueueUpdateDraw(func() {})
		}
	}()

	if err := app.SetRoot(u.pages, true).EnableMouse(true).SetFocus(u.lists[PanelAgents]).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// setRoundedBorders makes tview's own frames (the overlay TextView) match the
// rounded frames busmon draws itself. It is the only global tview state set.
func setRoundedBorders() {
	r := func(s string) rune { return []rune(s)[0] }
	tview.Borders.TopLeft, tview.Borders.TopRight = r(glyphTopLeft), r(glyphTopRight)
	tview.Borders.BottomLeft, tview.Borders.BottomRight = r(glyphBottomLeft), r(glyphBottomRight)
	tview.Borders.Horizontal, tview.Borders.Vertical = r(glyphHorizontal), r(glyphVertical)
	tview.Borders.TopLeftFocus, tview.Borders.TopRightFocus = r(glyphTopLeft), r(glyphTopRight)
	tview.Borders.BottomLeftFocus, tview.Borders.BottomRightFocus = r(glyphBottomLeft), r(glyphBottomRight)
	tview.Borders.HorizontalFocus, tview.Borders.VerticalFocus = r(glyphHorizontal), r(glyphVertical)
}
