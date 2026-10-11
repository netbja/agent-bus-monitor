package main

import (
	"fmt"
	"strings"
)

// helpText is the legend (F1): KEYS generated from the key tables, then every
// state, flag, type and counter on the screen, in words. The panels stay
// compact precisely because this exists.
func helpText() string {
	var sb strings.Builder
	head := func(s string) { sb.WriteString(tg(RoleAccent, s) + "\n") }
	row := func(k, v string) { fmt.Fprintf(&sb, "  %s %s\n", tg(RoleText, Fit(k, 20)), v) }
	more := func(v string) { fmt.Fprintf(&sb, "  %s %s\n", Fit("", 20), v) }
	blank := func() { sb.WriteString("\n") }

	head("KEYS")
	for _, sec := range []struct {
		name string
		m    Mode
	}{{"panels", ModePanel}, {"writing a message", ModeInput}, {"editing the filter", ModeFilter}, {"an open detail", ModeOverlay}} {
		sb.WriteString("  " + tg(RoleMuted, sec.name) + "\n")
		for _, b := range bindings(sec.m) {
			if k, d, ok := helpRow(b); ok {
				row(k, d)
			}
		}
	}
	row("1 2 3 (4)", "medium and narrow screens: switch the visible panel")
	row("click / wheel", "click selects, click on the selected row opens it; the wheel scrolls")
	more("a click outside an open detail closes it, like esc")
	row("i : @", "write a message; @agent text sends a directive, plain text a notify")
	blank()

	head("STATES")
	row(GlyphWorking+" working", "the state the agent DECLARED the last time it published a status.")
	row(GlyphIdle+" idle", "Silence never changes it: an agent that said working and went quiet")
	row(GlyphBlocked+" blocked", "still reads working. Any other recorded value is shown as written.")
	row(GlyphDone+" done", "")
	row(GlyphNone+" no state declared", "seen on the bus (a report, a lease) but it never published a state.")
	row("updated", "bar and age of that declaration: the bar empties over 10 minutes.")
	more("It measures freshness, not liveness - the bus has no heartbeat.")
	row("ctx", "the agent's own context, in raw tokens. Never a percentage.")
	row("sort", "attention first: escalated, gated or blocked; then queued or being woken;")
	more("then working, idle, other states, done, no state declared. Names inside each.")
	blank()
	sb.WriteString("  " + tg(RoleMuted, "flags") + "\n")
	row("escalated", "the dispatcher could not wake this agent and told the escalation target.")
	row("escalating", "that escalation is being published.")
	row("gate N", "N open 4-eyes challenges gate this agent: it must wait for a verdict.")
	row("deferred", "a wake is due but the pane was not ready (busy, focused, typed input).")
	row("unobserved", "a wake line was typed but its nonce was never seen on screen.")
	row("waking", "the dispatcher reserved a wake and is typing the line.")
	row("woken", "the nonce was echoed on the agent's screen - not proof it read or accepted anything.")
	row("pilot", "holds the pilot lease (the master).")
	row("armed", "a receiver lease is live: something subscribes for this agent. A lease, not a state.")
	row("stale N", "N addressed entries older than the 24h horizon; they wake no one.")
	row("wake:<value>", "a wake state this busmon does not know, shown as stored.")
	blank()
	sb.WriteString("  " + tg(RoleMuted, "activity types") + "\n")
	row("status", "a state declaration (state - message).")
	row("report  auto", "an agent's report; auto = written by a hook. (+N): more text retained.")
	row("notify", "a broadcast note.")
	row("request", "a directive carrying a tracked request (agentbus request send).")
	row("directive  reply", "a cmd to an agent, and its answer.")
	row("challenge  verdict", "4-eyes: a review request and its decision.")
	row("escalation", "the dispatcher could not wake an agent.")
	row("blocked", "a request target reports it cannot proceed.")
	row("complete", "the conductor integrated every mission of a change revision.")
	row("board", "task, owner, state and age; working, requested, accepted, blocked, done.")
	blank()

	head("DELIVERY")
	row(GlyphWorking+" bus ok", "busmon's OWN link to Redis. "+GlyphBlocked+" bus down Ns: busmon is blind,")
	more("the agents may be fine; values on screen are the last ones read.")
	row(GlyphWorking+" dispatch up", "a dispatcher holds the lease: unarmed agents are woken in their panes.")
	row(GlyphIdle+" dispatch down", "none runs: nothing wakes an unarmed agent. ? dispatch: not measured yet.")
	row(GlyphWorking+" missions up", "a missions conductor runs; down: a change is loaded but no conductor runs (its work is stalled).")
	row("used", "account windows consumed per provider (session / week), shared by every agent.")
	row("queue", "messages that would wake this agent: addressed, not wake=no, within the horizon.")
	more(GlyphNone + " = measured zero, " + GlyphDash + " = not measured yet, N? = last read failed.")
	row("lag (detail)", "entries in the SHARED cmd stream this agent's group has not read: not a work queue.")
	row("gap (detail)", "entries may have been trimmed before this agent read them - possible, not proven.")
	blank()
	sb.WriteString("  " + tg(RoleMuted, "requests") + "\n")
	row("requested", "published and recorded; nobody has accepted it.")
	row("accepted", "the target explicitly accepted it.")
	row("written to output", "the bytes reached a subscriber's output - not proof the agent read them.")
	row("queued", "no delivery attempt is recorded - not proof none happened.")
	row("no deadline set", "the request carries no expiry; busmon never invents one.")
	row("body not retained", "the request text reads back empty; trimmed, deleted and never-existed look alike.")
	row("untracked threads", "cmd threads with no answer in the retained history; never shown as accepted.")
	blank()
	sb.WriteString("  " + tg(RoleMuted, "message text") + "\n")
	row("full text retained", "the whole text is stored and shown.")
	row("truncated at publish", "MARKED as cut when published; the rest was never stored. A trailing "+GlyphEllipsis+" proves nothing.")
	row("one-line preview", "nothing beyond the preview is stored for this entry.")
	row("completeness not marked", "published before the marker existed; busmon cannot promise it is whole.")
	row("not retained", "the entry reads back empty; the cause is unknown.")
	return sb.String()
}

func helpTitle() string { return "Help" }

// helpRow is a binding's KEYS line: its keys with the help-only alternates,
// and its footer hint or, failing that, its help text. A binding with neither
// is folded into another row (j under the arrows, 2-4 under "1 2 3 (4)").
func helpRow(b binding) (keys, desc string, ok bool) {
	desc = b.Hint
	if desc == "" {
		desc = b.Help
	}
	if desc == "" {
		return "", "", false
	}
	keys = b.Keys
	if b.Alt != "" {
		keys += " " + b.Alt
	}
	return keys, desc, true
}
