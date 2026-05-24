// busmon — live TUI dashboard for the Agent Bus (Redis pub/sub).
//
// Subscribes to status:* and hermes:* and renders three panes:
//
//	AGENTS   per-agent presence: state from status:, liveness also from report:
//	ACTIVITY scrolling feed of status changes, notifications, commands, reports
//	INPUT    type a message + Enter to publish to hermes:notify; Esc or Ctrl-C quits
//
// The feed live-tails by default. Tab moves focus to ACTIVITY to scroll back
// (arrows/PgUp/PgDn/mouse wheel; g/G for top/bottom); the title shows [live] or
// [↑ pause · N plus bas]. Tab/Esc returns to the input and resumes the tail.
//
// Connection conventions match agent_bus.py (see package bus): REDIS_URL, or
// REDIS_HOST/PORT/PASSWORD; --host overrides REDIS_HOST.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
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
)

type agentState struct {
	state    string
	message  string
	lastSeen time.Time
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
	case "active": // report-only presence (no status: yet) — matches the report tag
		return "teal"
	}
	return "white"
}

func tag(color, text string) string {
	return fmt.Sprintf("[%s]%s[-]", color, tview.Escape(text))
}

// clip shortens s to at most n runes, appending an ellipsis when truncated, so
// an agent chip stays compact even when its last message/report is long.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

// activityTitle renders the ACTIVITY pane title from scroll geometry: the total
// wrapped line count, the topmost visible row, and the visible height. When the
// user has scrolled up (lines hidden below the viewport) it reports how far and
// how to resume the live tail; otherwise it shows the feed is following.
func activityTitle(total, topRow, height int) string {
	if below := total - topRow - height; below > 0 {
		return fmt.Sprintf(" ACTIVITY  [yellow][↑ pause · %d plus bas — Fin/G pour le direct][-] ", below)
	}
	return " ACTIVITY  [green][live][-] "
}

// refreshActivityTitle recomputes the ACTIVITY title from the view's current
// scroll geometry. Safe to call from the UI goroutine (QueueUpdateDraw).
func refreshActivityTitle(v *tview.TextView) {
	row, _ := v.GetScrollOffset()
	_, _, _, height := v.GetInnerRect()
	v.SetTitle(activityTitle(v.GetWrappedLineCount(), row, height))
}

func renderAgents(view *tview.TextView, agents map[string]*agentState, mu *sync.Mutex) {
	mu.Lock()
	defer mu.Unlock()
	names := make([]string, 0, len(agents))
	for n := range agents {
		names = append(names, n)
	}
	sort.Strings(names)
	now := time.Now()
	parts := make([]string, 0, len(names))
	for _, n := range names {
		a := agents[n]
		switch age := now.Sub(a.lastSeen); {
		case age > staleAfter:
			parts = append(parts, tag("gray", n+": offline"))
		case age > idleAfter:
			parts = append(parts, tag("yellow", fmt.Sprintf("%s: idle %dm", n, int(age.Minutes()))))
		default:
			label := tag(stateColor(a.state), n+": "+a.state)
			if a.message != "" {
				label += " " + tview.Escape("("+clip(a.message, 48)+")")
			}
			parts = append(parts, label)
		}
	}
	view.SetText(strings.Join(parts, "   "))
}

func main() {
	host := flag.String("host", "", "Redis host (overrides REDIS_HOST)")
	flag.Parse()

	client, err := bus.Connect(*host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: Redis connection failed: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	app := tview.NewApplication()

	agentsView := tview.NewTextView().SetDynamicColors(true)
	agentsView.SetBorder(true).SetTitle(" AGENTS ")

	activityView := tview.NewTextView()
	activityView.SetDynamicColors(true).SetMaxLines(500).SetScrollable(true)
	activityView.SetBorder(true).SetTitle(activityTitle(0, 0, 0))
	activityView.ScrollToEnd() // follow the tail until the user scrolls up

	input := tview.NewInputField().SetLabel("> ")
	input.SetBorder(true).SetTitle(" INPUT ")

	agents := make(map[string]*agentState)
	var mu sync.Mutex

	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			if text := strings.TrimSpace(input.GetText()); text != "" {
				bus.Notify(ctx, client, text)
			}
			input.SetText("")
		}
	})

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(agentsView, 3, 0, false).
		AddItem(activityView, 0, 1, false).
		AddItem(input, 3, 0, true)

	// Global keys: Ctrl-C quits; Tab toggles focus between the input and the
	// scrollable ACTIVITY feed; Esc quits from the input but returns to the
	// input (resuming the live tail) when the feed is focused.
	focusInput := func() {
		activityView.ScrollToEnd() // resume following the tail
		app.SetFocus(input)
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
				app.SetFocus(activityView)
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
		return ev
	})

	pubsub := client.PSubscribe(ctx, "status:*", "hermes:*")
	go func() {
		for msg := range pubsub.Channel() {
			agent, kind, state, message := bus.Parse(msg.Channel, msg.Payload)
			ts := time.Now().Format("15:04:05")
			var line string
			switch kind {
			case "status":
				mu.Lock()
				agents[agent] = &agentState{state: state, message: message, lastSeen: time.Now()}
				mu.Unlock()
				line = tag("gray", ts) + " " + tag(stateColor(state), "["+agent+"]") + " " + tview.Escape(state)
				if message != "" {
					line += " | " + tview.Escape(message)
				}
			case "notify":
				line = tag("gray", ts) + " " + tag("aqua", "[notify]") + " " + tview.Escape(message)
			case "cmd":
				line = tag("gray", ts) + " " + tag("fuchsia", "[cmd->"+agent+"]") + " " + tview.Escape(message)
			case "report":
				// A report proves the agent is alive: surface it in AGENTS even
				// if it never published a status. Keep its real state if known
				// ("working"/…), otherwise mark it "active" (report-only presence).
				mu.Lock()
				a := agents[agent]
				if a == nil {
					a = &agentState{state: "active"}
					agents[agent] = a
				}
				a.lastSeen = time.Now()
				if message != "" {
					a.message = message
				}
				mu.Unlock()
				line = tag("gray", ts) + " " + tag("teal", "[report:"+state+"->"+agent+"]") + " " + tview.Escape(message)
			default:
				line = tag("gray", ts) + " " + tview.Escape(message)
			}
			app.QueueUpdateDraw(func() {
				// No forced ScrollToEnd here: tview's trackEnd follows the tail
				// on its own and stays put once the user scrolls up, so history
				// browsing isn't yanked back to the bottom by incoming traffic.
				fmt.Fprintln(activityView, line)
				refreshActivityTitle(activityView)
				renderAgents(agentsView, agents, &mu)
			})
		}
	}()

	go func() {
		for range time.Tick(time.Second) {
			app.QueueUpdateDraw(func() {
				renderAgents(agentsView, agents, &mu)
				refreshActivityTitle(activityView)
			})
		}
	}()

	if err := app.SetRoot(layout, true).EnableMouse(true).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
