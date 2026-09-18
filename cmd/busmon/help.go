package main

import "strings"

// helpText is the legend: every badge, colour and counter on the screen, in
// words. The panes stay compact precisely because this exists — a chip can
// afford to be a symbol when one keypress spells it out.
//
// Kept as one string (not generated from the renderers) so it reads as prose
// to a human; helpCoversEveryBadge in the tests is what keeps it honest.
func helpText() string {
	return strings.Join([]string{
		tag("white", "KEYS"),
		"  Tab            move between the message feed and the input line",
		"  ↑ ↓ / j k      select a line in the feed (the feed stops following)",
		"  Enter          open the selected message: full text, who, when, thread",
		"  y              copy the selected line (OSC52 — works over SSH)",
		"  / then text    filter the feed: @agent, a thread id, or any words",
		"  Esc            leave the selection or the filter, and follow live again",
		"  r   F2         requests: what is waiting on someone",
		"  ?   F1         this legend",
		"  q   Ctrl-C     quit",
		"",
		tag("white", "IN AN OPEN MESSAGE"),
		"  y              copy the message text exactly as it was published",
		"  Y              copy the whole thread as plain text",
		"  ↑ ↓ / j k      scroll     Esc   back to the feed",
		"",
		tag("white", "AGENT CHIPS"),
		"  name: working  the state the agent DECLARED the last time it spoke.",
		"                 It is never overwritten by silence: an agent that said",
		"                 working and then went quiet still reads working.",
		"  · no update 18m",
		"                 how long ago that declaration was published. The bus has",
		"                 no heartbeat, so this is silence — not death, not idleness.",
		"                 Nothing is shown while the agent spoke within 2 minutes.",
		"  no state declared",
		"                 seen on the bus (a report, or an armed subscriber) but it",
		"                 has never published what it is doing.",
		"  [green]👂[-]             a subscriber is armed for this agent — its `agentbus",
		"                 subscribe` lease is live. A lease is not a state and not",
		"                 a promise that anything was read.",
		"  [yellow]⌛N[-]            N entries of the cmd stream this agent's consumer group",
		"                 has not read yet. Orange when no subscriber is armed: that",
		"                 pair (backlog, nobody listening) is the stopped-agent tell.",
		"  [red]🔒N[-]            N open 4-eyes challenges gate this agent; it must not",
		"                 proceed until a verdict closes them.",
		"  [blue]⧉[-]              the agent published a herdr pane id, so it runs in a pane.",
		"  [gray][120k ctx][-]      that agent's OWN context fill. Account-wide budget is in",
		"                 the status bar instead — it is the same number for everyone.",
		"  [fuchsia]⬢[-]              holds the pilot lease (the master).",
		"",
		tag("white", "STATUS BAR"),
		"  ⬢ MASTER x     x holds the pilot lease. `autonomous` means nobody does.",
		"  N waiting      tracked requests plus unanswered cmd threads; see r.",
		"  anthropic 25%/44%",
		"                 ACCOUNT budget: session window / weekly window, shared by",
		"                 every agent on that provider. Blank when nothing published it.",
		"  ⇄ bus ok       the MONITOR's own link to Redis. When it turns red, busmon",
		"                 is blind — the agents may be perfectly fine.",
		"",
		tag("white", "REQUESTS"),
		"  requested      published and recorded; nobody has accepted it.",
		"  accepted       the target agent explicitly accepted it.",
		"  blocked        the target agent explicitly reported it cannot proceed.",
		"  done           the target agent published a completion reply.",
		"  written to subscriber output",
		"                 the bytes reached a subscriber's output. It is NOT proof",
		"                 the agent read them, and never an acceptance.",
		"  no deadline set",
		"                 the request carries no expiry. busmon never invents one.",
		"  body no longer on the bus",
		"                 the request text has been trimmed from the stream. This",
		"                 says nothing about whether the work was done.",
		"",
		tag("white", "MESSAGE TEXT"),
		"  full text retained",
		"                 the whole text is stored and shown.",
		"  truncated at publish",
		"                 the writer cut it when publishing; the rest was never",
		"                 stored and cannot be recovered — ask the author to resend.",
		"  preview only   nothing beyond the one-line preview was retained.",
		"  completeness not marked",
		"                 published before the completeness marker existed, so",
		"                 busmon cannot promise the text is whole.",
		"  no longer on the bus",
		"                 the entry has aged out of the capped stream.",
	}, "\n")
}

func helpTitle() string {
	return " LEGEND  [gray][↑↓/jk scroll · Esc back][-] "
}
