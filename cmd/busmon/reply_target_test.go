package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// busmon-reply-target (2026-11-16, the person): r on a report master sent
// with --to <the person> opened a compose window "to: reviewer-codex", the one
// agent its text names, and the report's page said "To: (broadcast)". A reply
// goes to the message's sender, whatever agents its text names, and the
// recipient line names the report's addressees.
func TestReplyGoesToTheSenderNotAnAgentNamed(t *testing.T) {
	text := "Review ready: reviewer-codex approved build-id-untracked\n\nreviewer-codex found nothing blocking."
	rid := fmt.Sprintf("%d-0", shellNow.UnixMilli()-2000)
	report := bus.Event{ID: rid, Kind: "report", Agent: "master", Message: "Review ready: reviewer-codex approved build-id-untracked",
		Full: text, To: "charlie"}
	notice := bus.Event{ID: fmt.Sprintf("%d-0", shellNow.UnixMilli()-1000), Kind: "cmd", Type: bus.CmdDirective, From: "master", Target: "charlie",
		Ref: rid, Message: reportPrefix(rid) + "Review ready: reviewer-codex approved build-id-untracked"}
	for _, open := range []string{rid, notice.ID} {
		m := fixtureShell(t, 140, 40)
		m.snap.agents["reviewer-codex"] = &agentState{state: "idle", registered: true}
		m = step(t, m, tailMsg{events: []bus.Event{report, notice}})
		m.openFeedLine(open)
		if top := m.rd.top(); top == nil {
			t.Fatalf("%s: no page opened", open)
		}
		if got := detailTo(m.rd.top().ev); !strings.Contains(got, "charlie") || strings.Contains(got, "broadcast") {
			t.Errorf("%s: the recipient line reads %q", open, got)
		}
		m = keys(t, m, 'r')
		if cm := m.ui.compose; cm == nil || cm.recipient() != "master" {
			t.Errorf("%s: r opened %+v, not a reply to master", open, cm)
		}
	}
}

// A draft to another agent is the person's and is kept, but never takes the
// reply's place (what the person saw: r on master's report opened "to:
// reviewer-codex", an unsent draft's recipient): the reply opens to the
// sender, the draft is held and opens again once the reply is sent.
func TestReplyHoldsADraftToAnotherAgent(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	m.ui.msgCompose = composeDraft{to: "@reviewer-codex", body: "my own words"}
	m.pushMessage(bus.Event{Kind: "report", Agent: "master", ID: "9-0", Message: "Review ready"}, shellNow)
	m = keys(t, m, 'r')
	if c := m.ui.compose; c == nil || c.recipient() != "master" || c.area.value() != "re: Review ready\n" {
		t.Fatalf("r opened %+v, not the reply to master", c)
	}
	if !strings.Contains(m.ui.flash, "reviewer-codex") {
		t.Fatalf("the held draft is not said: flash %q", m.ui.flash)
	}
	m.sentCompose()
	if d := m.ui.msgCompose; d.to != "@reviewer-codex" || d.body != "my own words" {
		t.Fatalf("the draft did not come back after the reply: %+v", d)
	}
}

// A draft already to the sender is the reply under way: it opens as it was.
func TestReplyOpensADraftToTheSender(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	m.ui.msgCompose = composeDraft{to: "@master", body: "half an answer"}
	m.pushMessage(bus.Event{Kind: "report", Agent: "master", ID: "9-0", Message: "Review ready"}, shellNow)
	m = keys(t, m, 'r')
	if c := m.ui.compose; c == nil || c.recipient() != "master" || c.area.value() != "half an answer" {
		t.Fatalf("r opened %+v", c)
	}
	if len(m.ui.heldDrafts) != 0 {
		t.Fatalf("held %+v", m.ui.heldDrafts)
	}
}
