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
	"context"
	"flag"
	"fmt"
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

const (
	idleAfter  = 2 * time.Minute
	staleAfter = 10 * time.Minute

	feedCap = 500 // ACTIVITY lines retained for display + selection
)

type agentState struct {
	state    string
	message  string
	lastSeen time.Time
	gated    int // open 4-eyes challenges; >0 shows a lock badge
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
		a := agents[n]
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
		if a.gated > 0 {
			label += fmt.Sprintf(" [red]🔒%d[-]", a.gated)
		}
		parts = append(parts, label)
	}
	view.SetText(strings.Join(parts, "   "))
}

func main() {
	host := flag.String("host", "", "Redis host (overrides REDIS_HOST)")
	projectFlag := flag.String("project", "", "project namespace (or AGENT_BUS_PROJECT)")
	flag.Parse()

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

	// Tail the project's streams: "0" backfills retained history, then live.
	go func() {
		_ = b.Tail(ctx, "0", []string{"status", "report", "notify", "cmd"}, func(e bus.Event) {
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
		})
	}()

	// Poll pilot mode + per-agent gate counts off the UI thread; re-render so
	// chips age into idle/offline even with no new traffic.
	go func() {
		for range time.Tick(time.Second) {
			driver, _ := b.PilotDriver(ctx)
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
			for n, c := range gates {
				if a := agents[n]; a != nil {
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
