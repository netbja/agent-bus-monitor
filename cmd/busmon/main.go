// busmon — live TUI dashboard for the Agent Bus (Redis pub/sub).
//
// Subscribes to status:* and hermes:* and renders three panes:
//
//	AGENTS   per-agent last state + idle/offline derived from time since last status
//	ACTIVITY scrolling feed of status changes, notifications, and commands
//	INPUT    type a message + Enter to publish to hermes:notify; Esc or Ctrl-C quits
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
	}
	return "white"
}

func tag(color, text string) string {
	return fmt.Sprintf("[%s]%s[-]", color, tview.Escape(text))
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
				label += " " + tview.Escape("("+a.message+")")
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
	activityView.SetBorder(true).SetTitle(" ACTIVITY ")

	input := tview.NewInputField().SetLabel("> ")
	input.SetBorder(true).SetTitle(" INPUT ")

	agents := make(map[string]*agentState)
	var mu sync.Mutex

	input.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyEnter:
			if text := strings.TrimSpace(input.GetText()); text != "" {
				bus.Notify(ctx, client, text)
			}
			input.SetText("")
		case tcell.KeyEscape:
			app.Stop()
		}
	})

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(agentsView, 3, 0, false).
		AddItem(activityView, 0, 1, false).
		AddItem(input, 3, 0, true)

	app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyCtrlC {
			app.Stop()
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
				line = tag("gray", ts) + " " + tag("teal", "[report:"+state+"->"+agent+"]") + " " + tview.Escape(message)
			default:
				line = tag("gray", ts) + " " + tview.Escape(message)
			}
			app.QueueUpdateDraw(func() {
				fmt.Fprintln(activityView, line)
				activityView.ScrollToEnd()
				renderAgents(agentsView, agents, &mu)
			})
		}
	}()

	go func() {
		for range time.Tick(time.Second) {
			app.QueueUpdateDraw(func() { renderAgents(agentsView, agents, &mu) })
		}
	}()

	if err := app.SetRoot(layout, true).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
