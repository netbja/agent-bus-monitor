package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestWaitingAgentDetailShowsWhatItNeeds(t *testing.T) {
	now := time.Now()
	a := &agentState{state: "waiting", message: "DCA act 4 approved: opening date... see my last report", stateAt: now.Add(-time.Minute)}
	feed := []feedLine{
		{ev: bus.Event{Kind: "report", Agent: "master", Message: "older"}, at: now.Add(-time.Hour)},
		{ev: bus.Event{Kind: "report", Agent: "coder", Message: "not master's"}, at: now.Add(-30 * time.Minute)},
		{ev: bus.Event{Kind: "report", Agent: "master", Message: "preview", Full: "Decision needed:\n1. opening date T\n2. budget cap"}, at: now.Add(-2 * time.Minute)},
	}
	_, lines := agentDetail("master", a, true, nil, feed, now)
	body := plainOf(lines)
	status := strings.Index(body, "Status message")
	need := strings.Index(body, "What master needs from you")
	if status < 0 || need < status || !strings.Contains(body, "1. opening date T") || !strings.Contains(body, "Answer: i (reply to @master)") {
		t.Fatalf("body:\n%s", body)
	}
	section := body[need:]
	section = section[:strings.Index(section, "\n\n")]
	if strings.Contains(section, "older") || strings.Contains(section, "not master's") {
		t.Fatalf("the wrong report was shown:\n%s", section)
	}
	a.state = "working"
	if _, lines = agentDetail("master", a, true, nil, feed, now); !strings.Contains(plainOf(lines), "Last report") || strings.Contains(plainOf(lines), "Answer:") {
		t.Fatalf("a working agent's detail:\n%s", plainOf(lines))
	}
}

func TestRequestsCountOnlyWhatIsForYou(t *testing.T) {
	threads := []openThread{{ID: "1", Target: "bernard"}, {ID: "2", Target: "foureyes"}, {ID: "3", Target: "coder"}}
	if got := requestsLabel(nil, threads, "bernard", 0).text(); got != "1 for you · 2 between agents" {
		t.Fatalf("label %q", got)
	}
	if got := requestsLabel(nil, threads[1:], "bernard", 0).text(); got != "2 between agents" {
		t.Fatalf("label %q", got)
	}
	if got := requestsLabel(nil, nil, "bernard", 0).text(); got != "nothing waiting" {
		t.Fatalf("label %q", got)
	}
	mineR, mineT := forPerson(nil, threads, "bernard")
	if len(mineR) != 0 || len(mineT) != 1 {
		t.Fatal("forPerson must keep the person's thread")
	}
	if _, none := forPerson(nil, threads[1:], "bernard"); len(none) != 0 {
		t.Fatal("forPerson must keep nothing when nothing is for the person")
	}
}
