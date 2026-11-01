package main

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// On an agent's page, y copies the status message and Y the whole report
// shown under "What <agent> needs from you" (the person: the status alone
// was not enough); the frame and the footer say which.
func TestAgentPageCopiesStatusAndReport(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	full := "Decision needed:\n1. merge lot 1\n2. tag a release"
	m.feed.feed = append(m.feed.feed, feedLine{ev: bus.Event{Kind: "report", Agent: "foureyes", ID: "9-0", Message: "Decision needed: 1. merge…", Full: full}, at: shellNow})
	m.pushAgent("foureyes")
	p := m.rd.top()
	if p == nil || p.kind != ovAgent {
		t.Fatal("no agent page")
	}
	if p.plain != CopyText(m.snap.agents["foureyes"].message) {
		t.Errorf("y copies %q", p.plain)
	}
	if p.threadPlain != CopyText(full) {
		t.Errorf("Y copies %q, want the whole report", p.threadPlain)
	}
	screen := m.grid().plain()
	if !strings.Contains(screen, "Y copy report") || !strings.Contains(screen, "y copy status") || strings.Contains(screen, "copy thread") {
		t.Errorf("frame and footer do not say what each key copies:\n%s", screen)
	}
	m = keys(t, m, 'Y')
	if !strings.Contains(m.ui.flash, "report copied") {
		t.Errorf("Y flashed %q", m.ui.flash)
	}
}

// An agent with no report in the Activity window offers no Y.
func TestAgentPageWithoutReportOffersNoY(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	m.feed.feed = nil
	m.pushAgent("coder")
	if p := m.rd.top(); p.threadPlain != "" || strings.Contains(m.grid().plain(), "Y copy") {
		t.Errorf("Y offered without a report: %q", p.threadPlain)
	}
}
