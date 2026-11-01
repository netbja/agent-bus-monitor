package main

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// i on a waiting agent's page opens the window to that agent with the start
// of what it needs quoted (the person: the reply should recall the question),
// short and editable, the cursor after it; an existing draft is kept.
func TestAgentPageReplyQuotesWhatItNeeds(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	full := "Decision needed on the width rule.\n\n1. keep 80 columns\n2. go to 100\n3. let the person choose\n4. a fourth line never quoted"
	m.feed.feed = append(m.feed.feed, feedLine{ev: bus.Event{Kind: "report", Agent: "foureyes", ID: "9-0", Message: "Decision needed…", Full: full}, at: shellNow})
	m.pushAgent("foureyes") // waiting in the fixture, no open question
	m = keys(t, m, 'i')
	c := m.ui.compose
	if c == nil || c.recipient() != "foureyes" {
		t.Fatalf("i opened %+v", c)
	}
	body := c.area.value()
	want := "> Decision needed on the width rule.\n> 1. keep 80 columns\n> 2. go to 100\n\n"
	if body != want {
		t.Fatalf("quote %q, want %q", body, want)
	}
	if c.area.cur != len([]rune(body)) || c.onTo {
		t.Fatalf("cursor %d in %v, want after the quote in the text", c.area.cur, c.onTo)
	}
	// A draft to the agent is never overwritten by the quote.
	m = keys(t, m, kEsc) // the draft (the quote) is kept
	m.ui.msgCompose = composeDraft{to: "@foureyes", body: "my own words"}
	m.pushAgent("foureyes")
	if m = keys(t, m, 'i'); m.ui.compose.area.value() != "my own words" {
		t.Fatalf("the draft was replaced: %q", m.ui.compose.area.value())
	}
}

// No quote where the page shows none: an agent that is not waiting.
func TestAgentPageReplyWithoutNeedQuotesNothing(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	m.feed.feed = append(m.feed.feed, feedLine{ev: bus.Event{Kind: "report", Agent: "coder", ID: "9-0", Message: "a report"}, at: shellNow})
	m.pushAgent("coder")
	if m = keys(t, m, 'i'); m.ui.compose == nil || m.ui.compose.area.value() != "" {
		t.Fatalf("a quote for an agent that needs nothing: %+v", m.ui.compose)
	}
}

// The quote is short: at most three lines, each cut, and a long line ends in …
func TestReplyQuoteIsShort(t *testing.T) {
	q := replyQuote(strings.Repeat("word ", 100))
	if lines := strings.Split(strings.TrimRight(q, "\n"), "\n"); len(lines) != 1 || len([]rune(lines[0])) > replyQuoteMax+2 || !strings.HasSuffix(lines[0], GlyphEllipsis) {
		t.Fatalf("quote %q", q)
	}
	if replyQuote("   \n\n ") != "" {
		t.Error("an empty report gave a quote")
	}
}
