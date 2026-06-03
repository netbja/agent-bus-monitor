// busmon — live TUI dashboard for the Agent Bus over Redis Streams.
//
// Tails one project's streams ({project}:status|report|notify|cmd) and renders:
//
//	AGENTS   per-agent presence (state from status:, liveness also from report:),
//	         the pilot mode (piloted/autonomous), and a lock badge when an agent
//	         is gated by open 4-eyes challenges.
//	ACTIVITY scrolling feed of status/report/notify/cmd events. Tab focuses it;
//	         ↑↓/j/k select a line, y/Enter copies it to the clipboard (OSC52, so
//	         it works over SSH), Esc returns to the live tail.
//	INPUT    type a message + Enter to publish on {project}:notify; Esc/Ctrl-C quits.
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
// project's streams. It accepts y/yes/oui (case-insensitive); anything else —
// including EOF from a piped or non-TTY stdin — declines, so a purge is never
// triggered by an unattended pipe.
func confirmReset(project string, in io.Reader) bool {
	fmt.Printf("Purger l'historique des 4 streams du projet '%s' (status/report/notify/cmd) ? [y/N] ", project)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "oui":
		return true
	default:
		return false
	}
}

const (
	idleAfter  = 2 * time.Minute
	staleAfter = 10 * time.Minute

	feedCap = 500 // ACTIVITY lines retained for display + selection
)

type agentState struct {
	state    string
	message  string
	lastSeen time.Time
	gated    int   // open 4-eyes challenges; >0 shows a lock badge
	armed    bool  // a live subscribe lease exists → 👂 listening badge
	lag      int64 // unconsumed {p}:cmd entries for this agent → ⌛ backlog badge
}

func stateColor(state string) string {
	switch state {
	case "working":
		return "green"
	case "idle":
		return "yellow"
	case "blocked":
		return "red"
	case "done":
		return "blue"
	case "active": // report-only presence (no status: yet)
		return "teal"
	}
	return "white"
}

func tag(color, text string) string {
	return fmt.Sprintf("[%s]%s[-]", color, tview.Escape(text))
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

func activityTitle(total, topRow, height int) string {
	if below := total - topRow - height; below > 0 {
		return fmt.Sprintf(" ACTIVITY  [yellow][↑ pause · %d plus bas — Fin/G pour le direct][-] ", below)
	}
	return " ACTIVITY  [green][live][-] "
}

// selectionTitle is the ACTIVITY header shown while a feed line is selected.
func selectionTitle(pos, total int) string {
	return fmt.Sprintf(" ACTIVITY  [aqua][● sélection %d/%d — ↑↓/jk déplacer · y/⏎ copier · Échap direct][-] ", pos, total)
}

// feedLine pairs a TextView region id with the plain (tag-free) text of one
// ACTIVITY entry, so a selected line can be copied verbatim to the clipboard.
type feedLine struct {
	id   string
	text string
}

// selPos returns the index of id in feed, or -1 if it has scrolled out.
func selPos(feed []feedLine, id string) int {
	for i := range feed {
		if feed[i].id == id {
			return i
		}
	}
	return -1
}

// pilotLabel renders the AGENTS-pane pilot indicator from the lease driver.
func pilotLabel(driver string) string {
	if driver == "" {
		return "[yellow][autonome][-]"
	}
	return "[green][piloté par " + tview.Escape(driver) + "][-]"
}

// entryTime parses a Redis stream ID ("<ms>-<seq>") to wall-clock time so a
// backfilled entry ages correctly instead of looking freshly seen.
func entryTime(id string) time.Time {
	ms := id
	if i := strings.IndexByte(id, '-'); i >= 0 {
		ms = id[:i]
	}
	if n, err := strconv.ParseInt(ms, 10, 64); err == nil {
		return time.UnixMilli(n)
	}
	return time.Now()
}

// agentLabel renders one agent's AGENTS-pane chip: the aged state, then badges
// for listening (👂), command backlog (⌛N — orange when nobody is listening),
// and open 4-eyes challenges (🔒N).
func agentLabel(n string, a *agentState, now time.Time) string {
	var label string
	switch age := now.Sub(a.lastSeen); {
	case age > staleAfter:
		label = tag("gray", n+": offline")
	case age > idleAfter:
		label = tag("yellow", fmt.Sprintf("%s: idle %dm", n, int(age.Minutes())))
	default:
		label = tag(stateColor(a.state), n+": "+a.state)
		if a.message != "" {
			label += " " + tview.Escape("("+clip(a.message, 48)+")")
		}
	}
	if a.armed {
		label += " [green]👂[-]"
	}
	if a.lag > 0 {
		color := "yellow" // listening but behind — transient
		if !a.armed {
			color = "orange" // backlog with no listener — the "stopped re-arming" tell
		}
		label += fmt.Sprintf(" [%s]⌛%d[-]", color, a.lag)
	}
	if a.gated > 0 {
		label += fmt.Sprintf(" [red]🔒%d[-]", a.gated)
	}
	return label
}

func renderAgents(view *tview.TextView, agents map[string]*agentState, mu *sync.Mutex, pilot *string) {
	mu.Lock()
	defer mu.Unlock()
	view.SetTitle(" AGENTS  " + pilotLabel(*pilot) + " ")
	names := make([]string, 0, len(agents))
	for n := range agents {
		names = append(names, n)
	}
	sort.Strings(names)
	now := time.Now()
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, agentLabel(n, agents[n], now))
	}
	view.SetText(strings.Join(parts, "   "))
}

func main() {
	host := flag.String("host", "", "Redis host (overrides REDIS_HOST)")
	projectFlag := flag.String("project", "", "project namespace (or AGENT_BUS_PROJECT)")
	limitFlag := flag.Int("limit", 25, "ACTIVITY backfill: replay the last N lines on launch (0 = all history; or AGENT_BUS_BUSMON_LIMIT)")
	resetFlag := flag.Bool("reset", false, "purge the project's streams before launching (asks to confirm)")
	yesFlag := flag.Bool("yes", false, "skip the --reset confirmation prompt")
	flag.Parse()

	limitSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "limit" {
			limitSet = true
		}
	})
	limit := resolveLimit(limitSet, *limitFlag, os.Getenv("AGENT_BUS_BUSMON_LIMIT"))

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
			fmt.Println("Annulé.")
			os.Exit(0)
		}
		removed, err := b.Purge(ctx, streamKinds)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: purge failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Purgé: %d entrées effacées.\n", removed)
	}

	app := tview.NewApplication()

	agentsView := tview.NewTextView().SetDynamicColors(true)
	agentsView.SetBorder(true).SetTitle(" AGENTS ")

	activityView := tview.NewTextView()
	activityView.SetDynamicColors(true).SetRegions(true).SetMaxLines(feedCap).SetScrollable(true)
	activityView.SetBorder(true).SetTitle(activityTitle(0, 0, 0))
	activityView.ScrollToEnd()

	input := tview.NewInputField().SetLabel("> ")
	// Inherit the terminal's own palette instead of tview's default white-on-blue
	// field (ContrastBackgroundColor/PrimaryTextColor), which is hard to read.
	input.SetFieldBackgroundColor(tcell.ColorDefault).SetFieldTextColor(tcell.ColorDefault)
	input.SetBorder(true).SetTitle(" INPUT ")

	agents := make(map[string]*agentState)
	var mu sync.Mutex
	var pilot string // current pilot lease driver (guarded by mu)

	// ACTIVITY line-selection state. Everything here is touched only on the
	// tview event loop (input handlers + QueueUpdateDraw both run there), so it
	// needs no locking — unlike the agents map, which the tail goroutine writes.
	var feed []feedLine     // last feedCap entries, parallel to the view's regions
	var seq int             // monotonic region id source (ids are never reused)
	selID := ""             // selected region id; "" = live tail (no selection)
	var screen tcell.Screen // captured each draw, for clipboard (OSC52) writes

	refreshTitle := func() {
		if pos := selPos(feed, selID); selID != "" && pos >= 0 {
			activityView.SetTitle(selectionTitle(pos+1, len(feed)))
			return
		}
		row, _ := activityView.GetScrollOffset()
		_, _, _, height := activityView.GetInnerRect()
		activityView.SetTitle(activityTitle(activityView.GetWrappedLineCount(), row, height))
	}
	selectLine := func(id string) {
		selID = id
		activityView.Highlight(id).ScrollToHighlight()
		input.SetTitle(" INPUT ")
		refreshTitle()
	}
	enterSelect := func() {
		if len(feed) > 0 {
			selectLine(feed[len(feed)-1].id)
		}
	}
	moveSelect := func(delta int) {
		i := selPos(feed, selID)
		if selID == "" || i < 0 {
			enterSelect()
			return
		}
		i += delta
		if i < 0 {
			i = 0
		}
		if i >= len(feed) {
			i = len(feed) - 1
		}
		selectLine(feed[i].id)
	}
	exitSelect := func() {
		selID = ""
		activityView.Highlight()   // no args clears the highlight
		activityView.ScrollToEnd() // resume live tail
		input.SetTitle(" INPUT ")
		refreshTitle()
	}
	copySelect := func() {
		i := selPos(feed, selID)
		if i < 0 || screen == nil {
			return
		}
		screen.SetClipboard([]byte(feed[i].text)) // OSC52 — reaches the local clipboard even over SSH
		input.SetTitle(" INPUT  [green][✓ copié][-] ")
	}

	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			if text := strings.TrimSpace(input.GetText()); text != "" {
				b.Notify(ctx, self, text)
			}
			input.SetText("")
		}
	})

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(agentsView, 3, 0, false).
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
	app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyCtrlC:
			app.Stop()
			return nil
		case tcell.KeyTab, tcell.KeyBacktab:
			if activityView.HasFocus() {
				focusInput()
			} else {
				enterFeed()
			}
			return nil
		case tcell.KeyEscape:
			if activityView.HasFocus() {
				focusInput()
			} else {
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
				if len(feed) > 0 {
					selectLine(feed[0].id)
				}
				return nil
			case tcell.KeyEnd:
				enterSelect()
				return nil
			case tcell.KeyEnter:
				copySelect()
				return nil
			case tcell.KeyRune:
				switch ev.Rune() {
				case 'k':
					moveSelect(-1)
				case 'j':
					moveSelect(1)
				case 'g':
					if len(feed) > 0 {
						selectLine(feed[0].id)
					}
				case 'G':
					enterSelect()
				case 'y':
					copySelect()
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
	handle := func(e bus.Event) {
		ts := entryTime(e.ID).Format("15:04:05")
		var line, plain string // line = colored display; plain = tag-free, for the clipboard
		switch e.Kind {
		case "status":
			mu.Lock()
			a := agents[e.Agent]
			if a == nil {
				a = &agentState{}
				agents[e.Agent] = a
			}
			a.state, a.message, a.lastSeen = e.State, e.Message, entryTime(e.ID)
			mu.Unlock()
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
			mu.Lock()
			a := agents[e.Agent]
			if a == nil {
				a = &agentState{state: "active"}
				agents[e.Agent] = a
			}
			a.lastSeen = entryTime(e.ID)
			if e.Message != "" {
				a.message = e.Message
			}
			mu.Unlock()
			line = tag("gray", ts) + " " + tag("teal", "[report:"+e.RKind+"->"+e.Agent+"]") + " " + tview.Escape(e.Message)
			plain = ts + " [report:" + e.RKind + "->" + e.Agent + "] " + e.Message
		default:
			line = tag("gray", ts) + " " + tview.Escape(e.Message)
			plain = ts + " " + e.Message
		}
		app.QueueUpdateDraw(func() {
			seq++
			id := strconv.Itoa(seq)
			feed = append(feed, feedLine{id: id, text: plain})
			if len(feed) > feedCap {
				feed = feed[len(feed)-feedCap:]
			}
			// Wrap the line in a region so it can be selected/highlighted;
			// tview.Escape above already neutralised any ["..."] in messages.
			fmt.Fprintf(activityView, "[\"%s\"]%s[\"\"]\n", id, line)
			refreshTitle()
			renderAgents(agentsView, agents, &mu, &pilot)
		})
	}

	// Backfill, then live-tail. With a limit, fetch the last N merged entries up
	// front (fatal on error, while the terminal is still ours) and resume the
	// live tail from the per-stream cursors Recent returns — no replay, no "$"
	// gap. Rendering runs in the goroutine so it drains through QueueUpdateDraw
	// concurrently with app.Run(); a large backfill must not block on the queue
	// before the loop starts. limit <= 0 keeps the original behavior: replay all
	// retained history.
	var backfill []bus.Event
	var cursors map[string]string
	if limit > 0 {
		var err error
		backfill, cursors, err = b.Recent(ctx, streamKinds, limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: backfill failed: %v\n", err)
			os.Exit(1)
		}
	}
	go func() {
		for _, e := range backfill {
			handle(e)
		}
		if limit > 0 {
			_ = b.TailFrom(ctx, cursors, streamKinds, handle)
		} else {
			_ = b.Tail(ctx, "0", streamKinds, handle)
		}
	}()

	// Poll pilot mode + per-agent gate counts + armed leases + cmd backlog off
	// the UI thread; re-render so chips age and badges update with no new traffic.
	go func() {
		for range time.Tick(time.Second) {
			driver, _ := b.PilotDriver(ctx)
			armed, _ := b.ArmedAgents(ctx)
			lag, _ := b.CmdLag(ctx)
			mu.Lock()
			names := make([]string, 0, len(agents))
			for n := range agents {
				names = append(names, n)
			}
			mu.Unlock()
			gates := make(map[string]int, len(names))
			for _, n := range names {
				if m, err := b.OpenChallenges(ctx, n); err == nil {
					gates[n] = len(m)
				}
			}
			mu.Lock()
			pilot = driver
			// Surface agents known only via a live armed lease (subscribed but no
			// status published yet). Armed keys are TTL'd, so this never leaks a
			// ghost. Lag-only groups are NOT synthesized — consumer groups persist
			// after an agent is gone, so a stale group must not conjure a chip.
			for n := range armed {
				if agents[n] == nil {
					agents[n] = &agentState{state: "active", lastSeen: time.Now()}
				}
			}
			for n, a := range agents {
				_, a.armed = armed[n]
				a.lag = lag[n]
				if c, ok := gates[n]; ok {
					a.gated = c
				}
			}
			mu.Unlock()
			app.QueueUpdateDraw(func() {
				renderAgents(agentsView, agents, &mu, &pilot)
				refreshTitle()
			})
		}
	}()

	if err := app.SetRoot(layout, true).EnableMouse(true).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
