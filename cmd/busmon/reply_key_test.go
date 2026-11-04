package main

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// message-reply-key: i on a message, report or note page opens the compose
// window to its sender, prefilled with "re: " and the text's first line, the
// cursor after it; offered only where the sender is a bus agent.

func TestReplyKeyOpensComposeToTheSender(t *testing.T) {
	cases := []struct {
		name string
		ev   bus.Event
		to   string
		body string
	}{
		{"a directive", bus.Event{Kind: "cmd", Type: "directive", From: "master", Target: "coder", ID: "9-0",
			Message: "\n  Rebase feat/x on main\nthen run the suite"}, "master", "re: Rebase feat/x on main\n"},
		{"a report, its full text", bus.Event{Kind: "report", Agent: "coder", ID: "9-1", Message: "preview",
			Full: "Done: the parser\n\n## What remains"}, "coder", "re: Done: the parser\n"},
		{"a note", bus.Event{Kind: "notify", From: "sentinel", ID: "9-2", Message: "health ok again"}, "sentinel", "re: health ok again\n"},
		{"a status", bus.Event{Kind: "status", Agent: "qa", State: "blocked", ID: "9-3", Message: "out of quota"}, "qa", "re: out of quota\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := fixtureShell(t, 140, 40)
			m.pushMessage(c.ev, shellNow)
			if !m.pageOffers(ActComposeOpen) {
				t.Fatal("the page does not offer the reply")
			}
			m = keys(t, m, 'i')
			cm := m.ui.compose
			if cm == nil || cm.recipient() != c.to || cm.area.value() != c.body {
				t.Fatalf("i opened %+v", cm)
			}
			if cm.area.cur != len([]rune(c.body)) || cm.onTo {
				t.Fatalf("cursor %d on the recipient %v, want after the prefill", cm.area.cur, cm.onTo)
			}
			if m.rd.open() {
				t.Fatal("the reader stayed open over the compose window")
			}
			// Esc keeps it as the draft, as always.
			if m = keys(t, m, kEsc); m.ui.msgCompose.body != c.body || m.ui.msgCompose.to != "@"+c.to {
				t.Fatalf("draft after esc %+v", m.ui.msgCompose)
			}
		})
	}
}

// The first line is cut: the person recalls the message, the sender has it.
func TestReplyKeyCutsTheFirstLine(t *testing.T) {
	got := replyPrefill(bus.Event{Kind: "cmd", Message: strings.Repeat("long words ", 40)})
	line := strings.TrimSuffix(strings.TrimPrefix(got, "re: "), "\n")
	if len([]rune(line)) > replyFirstMax+1 || !strings.HasSuffix(line, GlyphEllipsis) || !strings.HasSuffix(got, "\n") {
		t.Fatalf("prefill %q", got)
	}
	if got := replyPrefill(bus.Event{Kind: "cmd", Message: "a\x1b[31m red‮ line"}); got != "re: a[31m red line\n" {
		t.Fatalf("controls kept: %q", got)
	}
	if got := replyPrefill(bus.Event{Kind: "cmd", Message: " \n\t\n"}); got != "" {
		t.Fatalf("an empty text: %q", got)
	}
}

// Not offered where nobody on the bus would read a reply; i says why and
// opens nothing.
func TestReplyKeyNotOffered(t *testing.T) {
	cases := []struct {
		name string
		ev   bus.Event
		why  string
	}{
		{"a dispatcher notice", bus.Event{Kind: "cmd", Type: "directive", From: "dispatch", Target: "charlie", NoWake: true, ID: "9-0",
			Message: "report from coder"}, "the dispatcher wrote this notice"},
		{"a dispatcher note", bus.Event{Kind: "notify", From: "dispatch", For: bus.ForPerson, ID: "9-1", Message: "stalled"}, "the dispatcher wrote this notice"},
		{"the person's own", bus.Event{Kind: "cmd", Type: "directive", From: "charlie", Target: "coder", Human: true, ID: "9-2",
			Message: "do it"}, "you wrote this"},
		{"not an agent", bus.Event{Kind: "cmd", Type: "complete", From: "conductor", Target: "master", ID: "9-3",
			Message: "change done"}, "conductor is not an agent on the bus"},
		{"no sender", bus.Event{Kind: "notify", ID: "9-4", Message: "orphan"}, "no sender to reply to"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := fixtureShell(t, 140, 40)
			m.pushMessage(c.ev, shellNow)
			if m.pageOffers(ActComposeOpen) {
				t.Fatal("offered")
			}
			m = keys(t, m, 'i')
			if m.ui.compose != nil || !m.rd.open() {
				t.Fatalf("i opened the window: %+v", m.ui.compose)
			}
			if !strings.Contains(m.ui.flash, c.why) {
				t.Fatalf("flash %q, want %q", m.ui.flash, c.why)
			}
		})
	}
	// A page with no event at all (a fixed body: a verdict, a template).
	m := fixtureShell(t, 140, 40)
	m.push(page{kind: ovMessage, title: "Verdict", fixed: true})
	if m.pageOffers(ActComposeOpen) {
		t.Fatal("a fixed page offers the reply")
	}
}

// A draft is never overwritten: i opens it as it was and says so.
func TestReplyKeyKeepsTheDraft(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	m.ui.msgCompose = composeDraft{to: "@foureyes", body: "my own words"}
	m.pushMessage(bus.Event{Kind: "cmd", Type: "directive", From: "master", Target: "coder", ID: "9-0", Message: "rebase"}, shellNow)
	m = keys(t, m, 'i')
	if c := m.ui.compose; c == nil || c.area.value() != "my own words" || c.recipient() != "foureyes" {
		t.Fatalf("the draft was replaced: %+v", c)
	}
	if !strings.Contains(m.ui.flash, "your unsent draft is open") {
		t.Fatalf("flash %q", m.ui.flash)
	}
}

// The footer advertises the reply only where it is offered.
func TestReplyKeyFooter(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	m.pushMessage(bus.Event{Kind: "cmd", Type: "directive", From: "master", Target: "coder", ID: "9-0", Message: "rebase"}, shellNow)
	if !hintsHave(m, "reply") {
		t.Fatal("no reply hint on a message from an agent")
	}
	m = fixtureShell(t, 140, 40)
	m.pushMessage(bus.Event{Kind: "notify", From: "dispatch", ID: "9-1", Message: "x"}, shellNow)
	if hintsHave(m, "reply") {
		t.Fatal("a reply hint on a dispatcher notice")
	}
}

func hintsHave(m Model, hint string) bool {
	var b strings.Builder
	for _, s := range footerHintsOn(m.mode(), m.ui.focus, m.layout.Tier, 400, m.pageOffers, m.pageHint) {
		b.WriteString(s.Text)
	}
	return strings.Contains(b.String(), " "+hint)
}

// A report notice (from its author) replies to the author with the report's
// own first line, never its envelope: the retained report's full text, else
// the notice's subject when the report is gone (foureyes).
func TestReplyKeyOnAReportNotice(t *testing.T) {
	notice := bus.Event{Kind: "cmd", Type: "directive", From: "coder", Target: "charlie", ID: "9-5",
		Message: reportPrefix("8-0") + "Parser done, one question"}
	report := bus.Event{Kind: "report", Agent: "coder", ID: "8-0", Message: "Parser done, one question",
		Full: "\nParser done: the grammar is in\n\n## What remains for you"}
	for _, c := range []struct {
		name     string
		retained bool
		body     string
	}{
		{"its report retained", true, "re: Parser done: the grammar is in\n"},
		{"its report gone", false, "re: Parser done, one question\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := fixtureShell(t, 140, 40)
			if c.retained {
				m.feed.feed = append(m.feed.feed, feedLine{ev: report, at: shellNow})
			}
			m.pushMessage(notice, shellNow)
			m = keys(t, m, 'i')
			cm := m.ui.compose
			if cm == nil || cm.recipient() != "coder" || cm.area.value() != c.body {
				t.Fatalf("i opened %+v", cm)
			}
			if strings.Contains(cm.area.value(), "for you (full text") {
				t.Fatalf("the envelope was quoted: %q", cm.area.value())
			}
		})
	}
}
