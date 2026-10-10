package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// daySeparator renders the day-boundary marker inserted into the ACTIVITY
// feed when an event falls on a different day than the one before it. The
// feed shows times only, so without this marker a multi-day history reads as
// one shuffled morning. Returns the colored display line and the tag-free
// text (for the clipboard), like handle's line/plain pair.
func daySeparator(t time.Time) (line, plain string) {
	d := t.Format("Mon 2006-01-02")
	return tag("gray", "── "+d+" ──"), "── " + d + " ──"
}

// reportMarker returns a compact " (+N)" breadcrumb (N = full rune length) when a
// report retained a full text, so the operator knows `agentbus reports <id>` shows
// more; empty when there is nothing extra.
func reportMarker(full string) string {
	if full == "" {
		return ""
	}
	return fmt.Sprintf(" (+%d)", len([]rune(full)))
}

func stateColor(state string) string {
	switch state {
	case "working":
		return "green"
	case "idle", "review":
		return "gray" // a normal idle is not a warning
	case "blocked":
		return "red"
	case "done":
		return "blue"
	case "active": // report-only presence (no status: yet)
		return "teal"
	}
	return "white"
}

// tag colours text for an overlay (TextView) body. The colour names are the
// v0.8 vocabulary; they resolve to theme roles, so the light theme and
// NO_COLOR apply to every overlay too.
func tag(color, text string) string { return th.Tag(colorRole(color), text) }

func colorRole(color string) Role {
	switch color {
	case "gray", "grey", "silver":
		return RoleMuted
	case "green":
		return RoleOK
	case "yellow", "orange", "olive":
		return RoleAttention
	case "red", "maroon":
		return RoleError
	case "aqua", "teal", "cyan":
		return RoleAccent
	case "blue":
		return RoleDone
	case "fuchsia", "purple":
		return RoleDispatch
	}
	return RoleText
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

// feedLine is one ACTIVITY entry: its TextView region id, the coloured line and
// the plain (tag-free) one for the clipboard, and the event itself. Keeping the
// event — rather than only the string that was printed — is what lets a
// selected line be opened in full, filtered on, or traced to its thread.
// A day separator has a zero Event (Kind ""), which is how it is told apart.
type feedLine struct {
	id   string
	seq  int    // monotonic arrival number (separators included)
	sep  bool   // a day separator, not an event
	line string // colour-tagged, as written to the view
	text string // tag-free, for the clipboard
	ev   bus.Event
	at   time.Time
}

// health is what the monitor knows about its OWN link to the broker. It is
// deliberately separate from anything an agent publishes: "busmon cannot reach
// Redis" and "this agent has gone quiet" look identical on screen otherwise,
// and they call for opposite reactions.
type health struct {
	ok    bool
	since time.Time // when the current condition started
	err   string    // last connection error, when not ok
}

// projectTable renders `busmon --list`: one row per project on the broker, in
// the order given (bus.Projects already sorts newest first). Plain text with no
// tview colour tags — this goes to stdout, not the TUI, where a "[green]" would
// print literally. Empty input renders "" so the caller can say "nothing here"
// on stderr and leave stdout clean for `| wc -l`.
func projectTable(list []bus.ProjectSummary, now time.Time) string {
	if len(list) == 0 {
		return ""
	}
	nameW, masterW := len("PROJECT"), len("MASTER")
	for _, p := range list {
		if len(p.Project) > nameW {
			nameW = len(p.Project)
		}
		if len(p.Master) > masterW {
			masterW = len(p.Master)
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-*s  %6s  %-*s  %s\n",
		nameW, "PROJECT", "AGENTS", masterW, "MASTER", "LAST ACTIVITY")
	for _, p := range list {
		master := p.Master
		if master == "" {
			master = "-"
		}
		fmt.Fprintf(&sb, "%-*s  %6d  %-*s  %s\n",
			nameW, p.Project, p.Agents, masterW, master, lastSeen(p.LastTS, now))
	}
	return sb.String()
}

// lastSeen renders a project's last-activity stamp. A project with nothing dated
// reads "never" rather than a bogus 1970 age.
func lastSeen(ts int64, now time.Time) string {
	if ts <= 0 {
		return "never"
	}
	return humanAge(now.Sub(time.UnixMilli(ts))) + " ago"
}

// humanAge renders a duration as one coarse unit ("45s", "2m", "3h", "6d").
// Precision past the leading unit is noise: the question a project row answers is
// "minutes or days ago", not "how many minutes".
func humanAge(d time.Duration) string {
	if d < 0 {
		d = 0 // a stamp from the future (clock skew) is not a negative age
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// pad right-pads s with spaces to n runes (no-op when already wider).
func pad(s string, n int) string {
	if w := len([]rune(s)); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
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

const chipSep = "  " // two spaces between chips on a row

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
