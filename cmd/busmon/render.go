package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rivo/tview"

	"github.com/netbja/agent-bus-monitor/bus"
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
		return fmt.Sprintf(" ACTIVITY  [yellow][↑ pause · %d below — End/G for live][-] ", below)
	}
	return " ACTIVITY  [green][live][-] "
}

// selectionTitle is the ACTIVITY header shown while a feed line is selected.
func selectionTitle(pos, total int) string {
	return fmt.Sprintf(" ACTIVITY  [aqua][● selection %d/%d — ↑↓/jk move · y/⏎ copy · Esc live][-] ", pos, total)
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

// statusBar renders the top bar: the project, then the master indicator derived
// from the pilot-lease driver (master == whoever holds the lease; empty = none).
func statusBar(project, driver string) string {
	if driver == "" {
		return fmt.Sprintf(" [white]%s[-]  ·  [yellow]autonomous (no master)[-]", tview.Escape(project))
	}
	return fmt.Sprintf(" [white]%s[-]  ·  [green]⬢ MASTER %s[-]", tview.Escape(project), tview.Escape(driver))
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

// chip is one agent's AGENTS-pane label plus its visible width (color tags
// excluded), so packChips can fit chips to the pane width without counting tags.
type chip struct {
	text string
	w    int
}

const chipSep = "  " // two spaces between chips on a row

// packChips greedily packs chips into rows no wider than width, keeping each chip
// intact. At most maxRows rows; if chips remain after maxRows, the last row gets a
// "[gray]+N[-]" marker counting the unplaced chips. Returns the rendered rows and
// their count (always >= 1). width<1 and maxRows<1 are clamped to 1.
func packChips(chips []chip, width, maxRows int) ([]string, int) {
	if width < 1 {
		width = 1
	}
	if maxRows < 1 {
		maxRows = 1
	}
	if len(chips) == 0 {
		return []string{""}, 1
	}
	var rows []string
	i := 0
	for i < len(chips) && len(rows) < maxRows {
		var cur strings.Builder
		curW := 0
		for i < len(chips) {
			c := chips[i]
			sep := 0
			if curW > 0 {
				sep = len(chipSep)
			}
			if curW > 0 && curW+sep+c.w > width {
				break // chip won't fit on this row
			}
			if curW > 0 {
				cur.WriteString(chipSep)
			}
			cur.WriteString(c.text)
			curW += sep + c.w
			i++
		}
		rows = append(rows, cur.String())
	}
	if i < len(chips) {
		rows[len(rows)-1] += fmt.Sprintf("%s[gray]+%d[-]", chipSep, len(chips)-i)
	}
	return rows, len(rows)
}

// parseDirected splits an "@<agent> <body>" line. directed is true only when the
// line starts with '@', the agent token is a valid name, and a non-empty body
// follows. Otherwise it returns ("", text, false) so the caller broadcasts the
// whole line to notify.
func parseDirected(text string) (target, body string, directed bool) {
	if !strings.HasPrefix(text, "@") {
		return "", text, false
	}
	rest := text[1:]
	sp := strings.IndexByte(rest, ' ')
	if sp < 0 {
		return "", text, false // "@agent" with no body
	}
	agent := rest[:sp]
	body = strings.TrimSpace(rest[sp+1:])
	if !bus.ValidName(agent) || body == "" {
		return "", text, false
	}
	return agent, body, true
}

// agentCompletions returns "@<name> " entries for the @-prefixed first token of
// currentText (no space yet) whose name matches the partial, sorted. Returns nil
// once a space (the body) has started, or when currentText is not @-prefixed.
func agentCompletions(currentText string, names []string) []string {
	if !strings.HasPrefix(currentText, "@") || strings.ContainsRune(currentText, ' ') {
		return nil
	}
	prefix := currentText[1:]
	var out []string
	for _, n := range names {
		if strings.HasPrefix(n, prefix) {
			out = append(out, "@"+n+" ")
		}
	}
	sort.Strings(out)
	return out
}

// agentLabel renders one agent's AGENTS-pane chip: the aged state, then badges
// for listening (👂), command backlog (⌛N — orange when nobody is listening),
// and open 4-eyes challenges (🔒N). When master is true, prepends a ⬢ marker.
func agentLabel(n string, a *agentState, now time.Time, master bool) string {
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
	if a.pane != "" {
		label += " [blue]⧉[-]"
	}
	if master {
		label = "[fuchsia]⬢[-] " + label
	}
	return label
}
