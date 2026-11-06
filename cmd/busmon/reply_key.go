package main

import (
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// message-reply-key: i on a message, report or note page writes to its
// sender, in the compose window prefilled with "re: <first line>". Offered
// only where the sender is a registered bus agent: a dispatcher notice, the
// person's own entry or a sender no agent reads is refused with why.

// replyFirstMax caps the quoted first line: the person recalls the message,
// the sender has it whole.
const replyFirstMax = 72

// replySender is the author a page's event names: the agent of a status or
// a report, the sender of a cmd or a note.
func replySender(ev bus.Event) string {
	if ev.Kind == "status" || ev.Kind == "report" {
		return ev.Agent
	}
	return ev.From
}

// replyTarget is whom i writes to from a page showing ev: its sender when
// that is a registered agent, else "" and why nobody would read a reply.
func replyTarget(ev bus.Event, agents map[string]*agentState, self string) (to, why string) {
	from := replySender(ev)
	switch {
	case from == "":
		return "", "no sender to reply to"
	case from == "dispatch":
		return "", "the dispatcher wrote this notice: no agent reads a reply to it"
	case from == self:
		return "", "you wrote this: nothing to reply to"
	}
	if a := agents[from]; a == nil || !a.registered {
		return "", from + " is not an agent on the bus: nobody would read a reply"
	}
	return from, ""
}

// replyPrefill is "re: " and the first non-empty line of ev's text (a
// report's full text when it has one), display controls removed and cut to
// replyFirstMax runes, then a line break for the answer; "" for no text.
func replyPrefill(ev bus.Event) string {
	text := ev.Message
	if ev.Full != "" {
		text = ev.Full
	}
	if h := bus.ReportHeadline(text); h != "" { // report-one-row §4: never a heading
		text = h
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(Preview(line))
		if line == "" {
			continue
		}
		if r := []rune(line); len(r) > replyFirstMax {
			line = strings.TrimSpace(string(r[:replyFirstMax])) + GlyphEllipsis
		}
		return "re: " + line + "\n"
	}
	return ""
}

// replyFromPage is i on a message page: the compose window to its sender,
// prefilled; a draft is the person's and is opened as it was; a page with
// no agent to answer says why and opens nothing.
func (m *Model) replyFromPage(p *page) {
	to, why := replyTarget(p.ev, m.snap.agents, m.cfg.self)
	if why != "" {
		m.ui.flash, m.ui.flashErr = why, false
		return
	}
	m.closeReader()
	if d := m.ui.msgCompose; d.to != "" || d.body != "" {
		m.openMessageCompose("", "")
		m.ui.flash, m.ui.flashErr = "your unsent draft is open: the reply was not put in", false
		return
	}
	m.openMessageCompose("", to)
	if c := m.ui.compose; c != nil {
		c.area.set(replyPrefill(m.replySource(p.ev)))
		c.onTo = false
		m.layoutCompose()
	}
}

// replySource is the event whose text the reply quotes: a report notice
// (report --to, from its author) quotes its report, never its envelope: the
// retained report's own text, else the notice's subject when the report is
// gone (foureyes). Any other event quotes itself.
func (m *Model) replySource(ev bus.Event) bus.Event {
	subject, id, ok := reportSubject(ev.Message)
	if !ok || ev.Kind != "cmd" {
		return ev
	}
	for i := len(m.feed.feed) - 1; i >= 0; i-- {
		if r := m.feed.feed[i].ev; r.Kind == "report" && r.ID == id {
			return r
		}
	}
	src := ev
	src.Message, src.Full = subject, ""
	return src
}
