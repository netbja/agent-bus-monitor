package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

var errTestRead = errors.New("read failed")

func TestBuildRecapIsDeterministicAndCoversTheExpectedSections(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	since := now.Add(-time.Hour)
	tasks := map[string]bus.BoardEntry{
		"later":  {State: "working", Owner: "coder", Branch: "feat/later"},
		"first":  {State: "open", Owner: "master", Branch: "main"},
		"second": {State: "open", Owner: "coder", Branch: "feat/second"},
	}
	input := recapInput{
		Now: now, Since: since, Human: "bernard",
		Agents: map[string]*agentState{
			"master":  {state: "working", message: "coordinating", stateAt: now.Add(-5 * time.Minute), registered: true, provider: "anthropic"},
			"coder":   {state: "waiting", message: "review this", stateAt: now.Add(-30 * time.Minute), registered: true},
			"done":    {state: "waiting", stateAt: now.Add(-time.Hour), answeredAt: now.Add(-40 * time.Minute), registered: true},
			"blocked": {state: "blocked", message: "tests fail", stateAt: now.Add(-2 * time.Minute), registered: true},
		},
		Asks:  []bus.Ask{{ID: "ask-1", From: "coder", State: "open", Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "q1", Header: "Review", Question: "Approve this?"}}}}},
		Board: tasks,
		Commands: []bus.Event{
			{ID: "1799999990000-0", Kind: "cmd", From: "coder", Target: bus.PeopleTarget, Type: bus.CmdDirective, Message: "Need a decision"},
			{ID: "1799999991000-0", Kind: "cmd", From: "dispatch", Target: "master", Type: bus.CmdEscalation},
			{ID: "1799999992000-0", Kind: "cmd", From: "dispatch", Target: "master", Type: bus.CmdComplete, Message: "change integrated"},
			{ID: "1799999993000-0", Kind: "cmd", From: "dispatch", Target: "master", Type: bus.CmdDirective, Message: "Project resumed: team ready"},
		},
		Reports:  []bus.Event{{ID: "1799999994000-0", Kind: "report", Agent: "coder"}, {ID: "1799999995000-0", Kind: "report", Agent: "coder"}},
		Verdicts: []bus.Verdict{{ID: "1799999996000-0", Subject: "pr:7", Author: "coder", Reviewer: "foureyes", Decision: "approve", TS: 1_799_999_996_000}},
		Swaps:    []bus.SwapEvent{{ID: "1799999997000-0", Role: "foureyes", Event: bus.SwapBack}},
		Services: map[string]bus.ServiceRecord{"dispatch": {State: "failed", Error: "exited", At: 1_799_999_998_000}},
		Holds:    map[string]int64{},
	}
	first := buildRecap(input)
	second := buildRecap(input)
	if len(first.Rows) != len(second.Rows) {
		t.Fatalf("row count changed: %d / %d", len(first.Rows), len(second.Rows))
	}
	var labels []string
	for i, row := range first.Rows {
		if row.Title != second.Rows[i].Title {
			t.Fatalf("row %d changed order: %q / %q", i, row.Title, second.Rows[i].Title)
		}
		labels = append(labels, row.Title+" "+row.Detail)
	}
	joined := strings.Join(labels, "\n")
	for _, want := range []string{"coder asks", "coder waiting", "blocked blocked", "master", "working", "first", "second", "Change completed", "Project resumed", "APPROVE", "swap back", "service failure", "2 reports", "history before", "pause history is limited"} {
		if !strings.Contains(joined, want) {
			t.Errorf("recap missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "done waiting") {
		t.Fatal("answered waiting declaration was reported as still owed")
	}
	if first.Target != "master" || first.SummaryDisabled != "" {
		t.Fatalf("summary target state: %+v", first)
	}
}

func TestBuildRecapLabelsReadFailuresAndDisablesUnsafeSummary(t *testing.T) {
	now := time.Now()
	m := buildRecap(recapInput{Now: now, Since: now.Add(-time.Hour), Human: "bernard",
		Agents: map[string]*agentState{"master": {registered: true, provider: "anthropic"}},
		Holds:  map[string]int64{"anthropic": now.Add(time.Hour).Unix()}, Pause: &bus.PauseRecord{},
		Errors: map[string]error{"cmd": errTestRead, "Board": errTestRead}})
	if m.SummaryDisabled != "project is paused" {
		t.Fatalf("summary disabled reason %q", m.SummaryDisabled)
	}
	var text string
	for _, row := range m.Rows {
		text += row.Title + " " + row.Detail + "\n"
	}
	if !strings.Contains(text, "cmd unavailable") || !strings.Contains(text, "Board unavailable") || !strings.Contains(text, "disabled: project is paused") {
		t.Fatalf("missing read failure / disable notice:\n%s", text)
	}
	if m.Rows[len(m.Rows)-1].Selectable {
		t.Fatal("disabled summary row selectable")
	}
}

func TestSummaryReplyStatusUsesRetainedBusHistory(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	requestID := "1799999999000-0"
	base := recapInput{Now: now, Agents: map[string]*agentState{"master": {registered: true}},
		Commands: []bus.Event{{ID: requestID, Type: bus.CmdDirective, Human: true, Message: "Summarise where the project stands for me: latest", Target: "master"}}}
	if got := recapSummaryStatus(buildRecap(base)); !strings.Contains(got, "awaiting reply") {
		t.Fatalf("retained request status %q", got)
	}
	base.Commands = append(base.Commands, bus.Event{ID: "1799999999500-0", Type: bus.CmdReply, Ref: requestID})
	if got := recapSummaryStatus(buildRecap(base)); !strings.Contains(got, "reply received") {
		t.Fatalf("retained reply status %q", got)
	}
	base.Errors = map[string]error{"cmd": errTestRead}
	if got := recapSummaryStatus(buildRecap(base)); got != "request status unavailable" {
		t.Fatalf("failed history read status %q", got)
	}
}

func TestRecapSnapshotKeepsAnsweredCorrelationOnlyForSameDeclaration(t *testing.T) {
	answered := time.UnixMilli(1_800_000_000_010)
	previous := map[string]*agentState{
		"same": {state: "waiting", stateAt: time.UnixMilli(1_800_000_000_000), answeredAt: answered},
		"new":  {state: "waiting", stateAt: time.UnixMilli(1_800_000_000_000), answeredAt: answered},
	}
	snaps := map[string]bus.AgentSnapshot{
		"same": {State: "waiting", TS: 1_800_000_000_000},
		"new":  {State: "waiting", TS: 1_800_000_000_020},
	}
	got := recapAgents(snaps, previous)
	if !got["same"].answeredAt.Equal(answered) {
		t.Fatalf("same declaration lost answer correlation: %v", got["same"].answeredAt)
	}
	if !got["new"].answeredAt.IsZero() {
		t.Fatalf("new declaration inherited stale answer correlation: %v", got["new"].answeredAt)
	}
}
