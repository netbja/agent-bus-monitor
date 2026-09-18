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

// modal centres an overlay over the main layout. The sizes are proportional,
// never fixed: at 40 columns the overlay still leaves a frame of context around
// it instead of demanding a width the terminal does not have.
func modal(p tview.Primitive) tview.Primitive {
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(p, 0, 12, true).
			AddItem(nil, 0, 1, false), 0, 12, true).
		AddItem(nil, 0, 1, false)
}

// feedLine is one ACTIVITY entry: its TextView region id, the coloured line and
// the plain (tag-free) one for the clipboard, and the event itself. Keeping the
// event — rather than only the string that was printed — is what lets a
// selected line be opened in full, filtered on, or traced to its thread.
// A day separator has a zero Event (Kind ""), which is how it is told apart.
type feedLine struct {
	id   string
	line string // colour-tagged, as written to the view
	text string // tag-free, for the clipboard
	ev   bus.Event
	at   time.Time
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

// moveSelection steps the selection through the lines currently on screen and
// returns the newly selected id. It walks the VISIBLE slice, so ↑↓ never stop
// on a line the filter is hiding, and it clamps at both ends rather than
// wrapping — wrapping from the newest line to the oldest, in a feed that grows
// while you read it, loses your place.
//
// An empty view, or a selection that has scrolled out of the retained window,
// lands on the newest visible line.
func moveSelection(visible []feedLine, cur string, delta int) string {
	if len(visible) == 0 {
		return ""
	}
	i := selPos(visible, cur)
	if cur == "" || i < 0 {
		return visible[len(visible)-1].id
	}
	i += delta
	if i < 0 {
		i = 0
	}
	if i >= len(visible) {
		i = len(visible) - 1
	}
	return visible[i].id
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

// healthIndicator renders the monitor's own connection, always present so its
// absence is never mistaken for silence on the bus.
func healthIndicator(h health, now time.Time) string {
	if h.ok {
		return "[green]⇄ bus ok[-]"
	}
	age := ""
	if !h.since.IsZero() {
		age = " " + humanAge(now.Sub(h.since))
	}
	reason := ""
	if h.err != "" {
		reason = " (" + clip(h.err, 40) + ")"
	}
	return tag("red", "⇄ monitor cannot reach the bus"+age+reason)
}

// statusData is everything the top bar reports. Grouped in a struct because the
// bar answers several unrelated questions at once — who drives, what is
// waiting, what the account has left, and whether the monitor itself is
// connected — and each one arrives from a different place.
type statusData struct {
	project string
	driver  string
	budgets map[string]bus.BudgetSnapshot
	waiting string // "3 waiting · oldest 42m", empty when nothing waits
	health  health
	now     time.Time
}

// statusBar renders the top bar: the project, the master indicator derived from
// the pilot-lease driver (master == whoever holds the lease; empty = none),
// what is waiting on someone, the ACCOUNT budget per provider, and the
// monitor's own link to the broker.
//
// The budget belongs here and not on an agent chip: a session/weekly window is
// the shared subscription every agent draws on, so pinning it next to one agent
// would read as that agent's own number.
func statusBar(s statusData) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, " [white]%s[-]  ·  ", tview.Escape(s.project))
	if s.driver == "" {
		sb.WriteString("[yellow]autonomous (no master)[-]")
	} else {
		fmt.Fprintf(&sb, "[green]⬢ MASTER %s[-]", tview.Escape(s.driver))
	}
	if s.waiting != "" {
		sb.WriteString("  ·  " + s.waiting)
	}
	if b := budgetBar(s.budgets); b != "" {
		sb.WriteString("  ·  " + b)
	}
	sb.WriteString("  ·  " + healthIndicator(s.health, s.now))
	return sb.String()
}

// budgetBar renders "anthropic 25%/44%" per provider, coloured by the tighter of
// the two windows. Empty when nothing has been published — an unknown budget must
// look unknown, never like 0% used.
func budgetBar(budgets map[string]bus.BudgetSnapshot) string {
	names := make([]string, 0, len(budgets))
	for n := range budgets {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		s := budgets[n]
		worst := s.SessionPct
		if s.WeeklyPct > worst {
			worst = s.WeeklyPct
		}
		parts = append(parts, fmt.Sprintf("[%s]%s %.0f%%/%.0f%%[-]",
			budgetColor(worst), tview.Escape(n), s.SessionPct, s.WeeklyPct))
	}
	return strings.Join(parts, " · ")
}

// budgetColor warns before the wall, not at it: past 75% of a window there is
// time to finish and commit in-flight work; past 90% there is not.
func budgetColor(pct float64) string {
	switch {
	case pct >= 90:
		return "red"
	case pct >= 75:
		return "yellow"
	default:
		return "green"
	}
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

// boardPanel renders the BOARD pane: one line per task — task, owner, colored
// state, branch, age — newest activity first (the same order as
// `agentbus board`, so TUI and CLI tell the same story). Fields are clipped to
// the pane width; color tags never count toward it.
func boardPanel(m map[string]bus.BoardEntry, now time.Time, width int) string {
	if len(m) == 0 || width < 1 {
		return ""
	}
	tasks := make([]string, 0, len(m))
	ownerW := 0
	for t, e := range m {
		tasks = append(tasks, t)
		if w := len([]rune(e.Owner)); w > ownerW {
			ownerW = w
		}
	}
	sort.Slice(tasks, func(i, j int) bool {
		if m[tasks[i]].Updated != m[tasks[j]].Updated {
			return m[tasks[i]].Updated > m[tasks[j]].Updated
		}
		return tasks[i] < tasks[j]
	})
	if ownerW > 12 {
		ownerW = 12
	}
	// columns: task owner state branch age — state is padded inside its color
	// tag so the branch column lines up; task/branch split what is left once
	// owner, state, age (≤4: "45s"…"999d") and the 4 separators are paid for.
	// clip() adds its "…" on top of the runes it keeps, so each of the three
	// clippable columns can overshoot its budget by one — reserve 3 for that.
	const stateW = 8
	avail := width - ownerW - stateW - 4 - 4 - 3
	if avail < 2 {
		avail = 2
	}
	taskW := avail / 2
	branchW := avail - taskW
	var sb strings.Builder
	for _, t := range tasks {
		e := m[t]
		state := tag(stateColor(e.State), pad(e.State, stateW))
		fmt.Fprintf(&sb, "%s %s %s %s %s\n",
			pad(clip(t, taskW), taskW),
			pad(clip(e.Owner, ownerW), ownerW),
			state,
			tview.Escape(clip(e.Branch, branchW)),
			humanAge(now.Sub(time.Unix(e.Updated, 0))))
	}
	return sb.String()
}

// boardTitle carries the all-done signal: when every task is done the pane
// says so in green — the visual cue that an `agentbus shutdown` is pertinent.
func boardTitle(m map[string]bus.BoardEntry) string {
	done := 0
	for _, e := range m {
		if e.State == "done" {
			done++
		}
	}
	if len(m) > 0 && done == len(m) {
		return " BOARD  [green][✓ all done][-] "
	}
	return fmt.Sprintf(" BOARD  [gray][%d/%d done][-] ", done, len(m))
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

// usageBadge is the per-agent chip badge: that agent's own context fill, e.g.
// "608k ctx". Account-scope numbers (session/weekly windows) deliberately do NOT
// appear here — they are the same for every agent, so a chip is the one place
// they cannot mean anything; the status bar carries them instead.
//
// Falls back to the legacy Session/Reset strings so a snapshot written by the
// old status-line tee still renders something rather than vanishing.
func usageBadge(snap bus.UsageSnapshot) string {
	if snap.Ctx != "" {
		return snap.Ctx
	}
	parts := make([]string, 0, 2)
	if snap.Session != "" {
		parts = append(parts, snap.Session)
	}
	if snap.Reset != "" {
		parts = append(parts, snap.Reset)
	}
	return strings.Join(parts, "·")
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

// freshness reports how old the agent's own last word is — never what it means.
// The bus has no heartbeat, so silence is silence: it may be a busy agent, a
// closed pane or a dead host, and busmon is not entitled to choose between
// them. Under idleAfter the age is uninteresting and the last message takes the
// space instead; past it, the age replaces the message because "when did we
// last hear anything" is then the only question worth the width.
func freshness(last, now time.Time, message string) string {
	if last.IsZero() {
		return " " + tag("gray", "· no update seen")
	}
	switch age := now.Sub(last); {
	case age > staleAfter:
		return " " + tag("orange", "· no update "+humanAge(age))
	case age > idleAfter:
		return " " + tag("yellow", "· no update "+humanAge(age))
	}
	if message != "" {
		return " " + tview.Escape("("+clip(message, 32)+")")
	}
	return ""
}

// agentLabel renders one agent's AGENTS-pane chip. It keeps three facts apart
// that the old chip merged into one word:
//
//   - the state the agent DECLARED (coloured, always shown, never overwritten —
//     an agent that said "working" and then went quiet is still shown as
//     working, because that is the last thing it actually said);
//   - how old that declaration is ("· no update 18m" — a fact, unlike the
//     "offline" this replaces, which was a guess dressed as an observation);
//   - whether a subscriber is armed for it (👂), which is a lease, not a state.
//
// Then the badges: command backlog (⌛N — orange when nobody is listening), open
// 4-eyes challenges (🔒N), herdr pane (⧉), own context fill ([..]). When master
// is true, prepends a ⬢ marker.
func agentLabel(n string, a *agentState, now time.Time, master bool) string {
	var label string
	if a.state == "" {
		// Seen on the bus (a report, or an armed lease) but it has never said
		// what it is doing. That is not a state, and inventing one here is how
		// presence gets mistaken for progress.
		label = tag("gray", n+": no state declared")
		if !a.lastSeen.IsZero() {
			label += " " + tag("gray", "· last seen "+humanAge(now.Sub(a.lastSeen))+" ago")
		}
	} else {
		// The age is the age of the DECLARATION, not of the agent's last sign of
		// life: a report proves the process is alive, not that "working" is still
		// true. Reading the fresher stamp here would quietly re-validate a stale
		// claim, which is the conflation this chip exists to undo.
		label = tag(stateColor(a.state), n+": "+a.state) + freshness(a.stateAt, now, a.message)
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
	if a.usage != "" {
		label += " [gray][" + a.usage + "][-]"
	}
	if master {
		label = "[fuchsia]⬢[-] " + label
	}
	return label
}

// agentsOrNil drops a snapshot that came from a failed read, so a broker that
// could not answer never looks like a broker that answered "nobody".
func agentsOrNil(snaps map[string]bus.AgentSnapshot, err error) map[string]bus.AgentSnapshot {
	if err != nil {
		return nil
	}
	return snaps
}
