package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// wakeMeaning spells out a stored wake state; "woken" in particular is a
// screen observation, never a delivery or an acceptance.
func wakeMeaning(state string) string {
	switch state {
	case "prompting":
		return "waking - the dispatcher reserved a wake and is typing the line"
	case "prompted":
		return "woken - nonce echoed on the agent's screen; not read or accepted"
	case "unobserved":
		return "unobserved - a wake line was typed, its nonce was never seen"
	case "deferred":
		return "deferred - a wake is due but the pane was not ready"
	case "", "clear":
		return "clear - nothing to wake for"
	}
	return "unknown state " + Preview(state)
}

// agentDetail is the agent overlay (spec §3.6): the full status message first,
// then every fact busmon holds about the agent, each with what it means when
// absent. recent is the retained Activity window; up to five lines involving
// the agent are shown and labelled as coming from that window.
func agentDetail(name string, a *agentState, pilot bool, board map[string]bus.BoardEntry, recent []feedLine, now time.Time) (title, body string) {
	var sb strings.Builder
	row := func(k, v string) { fmt.Fprintf(&sb, "%s %s\n", tg(RoleMuted, Fit(k, 15)), v) }
	// rowWrap is a row whose long value wraps under the value column.
	rowWrap := func(k string, r Role, v string) {
		sb.WriteString(strings.Join(wrapSpans([]mdSpan{{text: v, role: r}}, mdWidth, Fit(k, 15)+" ", strings.Repeat(" ", 16)), "\n") + "\n")
	}
	ago := func(t time.Time) string { return humanAge(now.Sub(t)) + " ago" }
	text := func(s string) string { return tg(RoleText, s) }

	glyph, word, role, _ := stateLook(a)
	title = fmt.Sprintf("%s %s %s %s", glyph, Preview(name), GlyphNone, word)

	sb.WriteString(tg(RoleAccent, "Status message") + "\n")
	if a.message == "" {
		sb.WriteString(tg(RoleMuted, "no status message") + "\n")
	} else {
		sb.WriteString(renderMarkdown(a.message, mdWidth) + "\n")
		if a.forYou {
			sb.WriteString(tg(RoleMuted, "It names you but declared idle: no question is open.") + "\n")
		}
	}
	sb.WriteString("\n")
	// What the agent last wrote at length: for an agent waiting for a person,
	// that is the decision asked for, so it comes before every other fact.
	if rep, at, ok := lastReport(name, recent); ok {
		head := "Last report"
		asking := a.openAsk > 0 // the questions above say what it needs
		if a.state == "waiting" && !asking {
			head = "What " + Preview(name) + " needs from you"
		}
		sb.WriteString(tg(RoleAccent, head) + tg(RoleMuted, " "+GlyphNone+" "+ago(at)) + "\n")
		sb.WriteString(renderMarkdown(rep, mdWidth) + "\n")
		if a.state == "waiting" && !asking {
			sb.WriteString(tg(RoleMuted, "Answer: i (reply to @"+Preview(name)+")") + "\n")
		}
		sb.WriteString("\n")
	} else if a.state == "waiting" {
		sb.WriteString(tg(RoleMuted, "Its last report is older than the Activity window: agentbus reports") + "\n\n")
	}

	if a.stateAt.IsZero() {
		row("Declared", tg(RoleMuted, "never declared"))
	} else {
		declared := tg(role, word)
		if a.state != "" && word != Preview(a.state) { // a bus fact covers it in the row (quota, paused, a question): say what was declared
			declared = tg(RoleText, Preview(a.state)) + tg(RoleMuted, " (shown as "+word+")")
		}
		row("Declared", declared+text(fmt.Sprintf(" %s %s (%s)", GlyphNone, ago(a.stateAt), a.stateAt.Format("15:04:05"))))
	}
	switch {
	case word == "quota":
		rowWrap("Stopped", RoleAttention, "its provider ("+Preview(a.provider)+") is at its limit: the dispatcher wakes it again after the reset")
	case word == "paused":
		rowWrap("Stopped", RoleAttention, "the project is paused: nobody is woken until it resumes")
	}
	if a.lastSeen.IsZero() {
		row("Last seen", tg(RoleMuted, GlyphDash))
	} else {
		row("Last seen", text(ago(a.lastSeen)+" (any status or report)"))
	}
	switch {
	case promptWaiting(a):
		where := "its tab"
		if a.pane != "" {
			where += " (pane " + Preview(a.pane) + ")"
		}
		rowWrap("Observed", RoleAttention, "blocked "+GlyphNone+" since "+ago(a.obsSince)+": its client is asking something in "+where+". Answer it there.")
	case a.obsAt.IsZero():
		row("Observed", tg(RoleMuted, "pane not observed (no dispatch service, or no verified pane)"))
	case busyUndeclared(a, now):
		rowWrap("Observed", RoleOK, "busy "+GlyphNone+" "+ago(a.obsAt)+": the pane runs a turn, but the agent does not declare working")
	case now.Sub(a.obsAt) > observedFresh:
		row("Observed", tg(RoleMuted, "last observation "+ago(a.obsAt)+" (too old to count)"))
	case a.obsBusy:
		row("Observed", text("busy "+GlyphNone+" "+ago(a.obsAt)))
	default:
		row("Observed", text("not running a turn "+GlyphNone+" "+ago(a.obsAt)))
	}
	if clientOutdated(a, now) { // client reload spec: propose only
		u := a.clientUpdate
		ver := func(v string) string {
			if v == "" {
				return "?"
			}
			return v
		}
		rowWrap("Client", RoleAttention, ver(u.Running)+" running "+GlyphNone+" "+ver(u.Installed)+" installed "+GlyphNone+" reload when idle")
		rowWrap("", RoleMuted, "in its pane: /exit, then agentbus up <project> --resume (the dry run first)")
	}
	if f := a.fallback; f != nil {
		since := "?"
		if f.Since > 0 {
			since = time.UnixMilli(f.Since).Local().Format("Mon 15:04")
		}
		by := ""
		if f.By != "" {
			by = ", by " + Preview(f.By)
		}
		rowWrap("Fallback", RoleDispatch, fmt.Sprintf("on %s (%s) since %s%s %s own profile %s (%s); back with: agentbus swap %s --back",
			Preview(f.Profile), Preview(f.Provider), since, by, GlyphNone, Preview(f.OwnProfile), Preview(f.OwnProvider), Preview(name)))
	}
	if v := a.model; v != nil && v.PID == a.pid && a.pid != 0 {
		switch {
		case modelDrift(a):
			rowWrap("Model", RoleAttention, Preview(v.Why))
		case v.Launched != "":
			row("Model", text(Preview(v.Launched)+" "+GlyphNone+" as roles.toml configures"))
		}
		if v.Used != "" {
			row("Used", text(Preview(v.Used)+" (its transcript)"))
		}
	}
	if a.pane == "" && a.pid == 0 {
		row("Pane", tg(RoleMuted, "not recorded"))
	} else {
		pane := a.pane
		if pane == "" {
			pane = "no pane"
		}
		pid := "pid not recorded"
		if a.pid != 0 {
			pid = fmt.Sprintf("pid %d (start %d)", a.pid, a.pidStart)
		}
		row("Pane", text(Preview(pane)+" "+GlyphNone+" "+pid))
	}
	if a.armed {
		row("Receiver lease", text("armed - a subscriber holds a lease (not a state)"))
	} else {
		row("Receiver lease", tg(RoleMuted, "not armed"))
	}
	switch {
	case !a.queueKnown:
		row("Queue", tg(RoleMuted, "not measured yet"))
	case a.queueFailed:
		row("Queue", tg(RoleAttention, fmt.Sprintf("%d waiting (last value) %s read failed, last success %s", a.waiting, GlyphNone, ago(a.queueAt))))
	default:
		row("Queue", text(fmt.Sprintf("%d waiting %s %d stale %s measured %s", a.waiting, GlyphNone, a.stale, GlyphNone, ago(a.queueAt))))
	}
	rowWrap("", RoleMuted, fmt.Sprintf("lag %d: entries of the SHARED cmd stream this agent's group has not read - not a work queue", a.lag))
	if a.wake == nil {
		row("Wake", tg(RoleMuted, "no wake record"))
	} else {
		w := wakeMeaning(a.wake.State) + fmt.Sprintf(" %s tries %d", GlyphNone, a.wake.Tries)
		switch a.wake.Escalated {
		case "pending":
			w += " " + GlyphNone + " escalating"
		case "true":
			w += " " + GlyphNone + " escalated"
		}
		if a.wake.Reason != "" {
			w += " " + GlyphNone + " " + Preview(a.wake.Reason)
		}
		row("Wake", text(w))
	}
	if a.gap {
		rowWrap("Gap", RoleAttention, "entries may have been trimmed before this agent read them - possible, not proven")
	}
	if a.gated > 0 {
		row("Gate", tg(RoleError, fmt.Sprintf("%d open 4-eyes challenge(s): wait for a verdict", a.gated)))
	} else {
		row("Gate", tg(RoleMuted, "none"))
	}
	switch n, ok := parseCtx(a.ctxRaw); {
	case a.ctxRaw == "":
		row("Context", tg(RoleMuted, "not reported"))
	case ok:
		row("Context", text(fmt.Sprintf("%s tokens (raw %q)", formatTokens(n), a.ctxRaw)))
	default:
		row("Context", tg(RoleMuted, "not a token count: "+Preview(a.ctxRaw)))
	}
	if pilot {
		row("Pilot", text("holds the pilot lease (master)"))
	}
	var tasks []string
	for t, e := range board {
		if e.Owner == name {
			tasks = append(tasks, t)
		}
	}
	sort.Strings(tasks)
	if len(tasks) == 0 {
		row("Board", tg(RoleMuted, "none"))
	}
	for i, t := range tasks {
		e := board[t]
		k := ""
		if i == 0 {
			k = "Board"
		}
		branch := e.Branch
		if branch == "" {
			branch = GlyphDash
		}
		row(k, text(Preview(t))+" "+tg(boardStateRole(e.State), Preview(e.State))+text(" "+GlyphNone+" "+Preview(branch)))
	}

	sb.WriteString("\n" + tg(RoleAccent, "Recent activity") + tg(RoleMuted, " (in the retained window)") + "\n")
	var mine []feedLine
	for _, fl := range recent {
		if fl.sep {
			continue
		}
		e := fl.ev
		if e.Agent == name || e.From == name || e.Target == name {
			mine = append(mine, fl)
		}
	}
	if len(mine) > 5 {
		mine = mine[len(mine)-5:]
	}
	if len(mine) == 0 {
		sb.WriteString(tg(RoleMuted, "none in the retained window") + "\n")
	}
	for _, fl := range mine {
		look := lookEvent(fl.ev)
		to := ""
		if look.To != "" {
			to = " " + GlyphArrowR + " " + Preview(look.To)
		}
		sb.WriteString(activityEntry(fl.at.Format("15:04"), look.Type, look.Role, Preview(look.From)+to, Preview(look.Text), mdWidth) + "\n")
	}
	return title, sb.String()
}

// agentDetailPlain is what y copies from the agent detail: the status message
// exactly as retained.
func agentDetailPlain(a *agentState) string { return CopyText(a.message) }

// taskDetail is the board-task overlay; plain is the one-line copy.
func taskDetail(e bus.BoardEntry, task string, now time.Time) (title, body, plain string) {
	branch := e.Branch
	if branch == "" {
		branch = GlyphDash
	}
	var sb strings.Builder
	row := func(k, v string) { fmt.Fprintf(&sb, "%s %s\n", tg(RoleMuted, Fit(k, 10)), v) }
	row("Task", tg(RoleText, Preview(task)))
	row("Owner", tg(RoleText, Preview(e.Owner)))
	row("State", tg(boardStateRole(e.State), Preview(e.State)))
	row("Branch", tg(RoleText, Preview(branch)))
	if e.Updated > 0 {
		row("Updated", tg(RoleText, humanAge(now.Sub(time.Unix(e.Updated, 0)))+" ago"))
	} else {
		row("Updated", tg(RoleMuted, GlyphDash))
	}
	if e.Request != nil {
		row("Request", tg(RoleMuted, "tracked - open it from Requests (F2) for delivery and acceptance"))
	}
	return "Task " + GlyphNone + " " + Preview(task), sb.String(), strings.Join([]string{task, e.Owner, e.State, branch}, " ")
}

// requestDetail is one tracked request: the v0.8 request block, plus a plain
// one-line copy.
func requestDetail(r request, now time.Time) (title, body, plain string) {
	next, _ := nextAction(r, now)
	deadline, _ := deadlineWords(r, now)
	plain = strings.Join([]string{r.Task, target(r), r.State, next, deadline}, " | ")
	return "Request " + GlyphNone + " " + Preview(r.Task), requestBlock(r, now), plain
}

// lastReport is the agent's newest report in the Activity window, full text
// when it was retained, with its time.
func lastReport(name string, feed []feedLine) (string, time.Time, bool) {
	for i := len(feed) - 1; i >= 0; i-- {
		e := feed[i].ev
		if feed[i].sep || e.Kind != "report" || e.Agent != name {
			continue
		}
		if e.Full != "" {
			return e.Full, feed[i].at, true
		}
		return e.Message, feed[i].at, true
	}
	return "", time.Time{}, false
}

// recentLines is how many lines one Recent activity entry may take; the
// whole text is one Enter away in the Activity panel.
const recentLines = 3

// activityEntry is one Recent activity line of an agent detail: time and
// type, then who and the text wrapped under the text column, at most
// recentLines lines.
func activityEntry(at, typ string, r Role, who, msg string, width int) string {
	pad := strings.Repeat(" ", Width(at)+1+10)
	lines := wrapSpans([]mdSpan{{text: who, role: RoleText}, {text: " " + GlyphNone + " ", role: RoleMuted}, {text: msg, role: RoleText}}, width-2, pad, pad) // room for the ellipsis
	if len(lines) == 0 {
		return tg(RoleMuted, at+" ") + tg(r, Fit(typ, 10))
	}
	if len(lines) > recentLines {
		lines = lines[:recentLines]
		lines[recentLines-1] += tg(RoleMuted, " "+GlyphEllipsis)
	}
	lines[0] = tg(RoleMuted, at+" ") + tg(r, Fit(typ, 10)) + strings.TrimPrefix(lines[0], tg(RoleMuted, pad))
	return strings.Join(lines, "\n")
}
