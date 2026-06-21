package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rivo/tview"
)

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
