package main

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/attention"
	"github.com/netbja/agent-bus-monitor/bus"
	healthpkg "github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/internal/bustest"
)

// status-summary §3.2, the one-definition golden (foureyes on step 2): on
// one fixture broker, busmon's polled For you items in attention's ranks
// equal what agentbus summary counts, attention.Items over attention.Read,
// key for key; reports and unread stay busmon's. The fixture holds one of
// each: an ask, a waiting agent, a wait the person answered, a gate the
// person opened, a gate on the person, the stall guard and a merge.
func TestForYouParityWithSummary(t *testing.T) {
	b, r := bustest.Open(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { bus.DeleteProject(ctx, r, p) })
	const person = "charlie"
	for _, s := range []struct{ a, st, msg string }{{"master", "idle", "x"}, {"coder", "waiting", "which way?"},
		{"coder2", "working", "x"}, {"foureyes", "working", "x"}, {"docs", "waiting", "may I?"}} {
		if _, err := b.Status(ctx, s.a, s.st, s.msg, bus.AgentIdent{}); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := b.HumanCmd(ctx, person, "docs", "yes, go"); err != nil { // the person answered docs' wait
		t.Fatal(err)
	}
	if _, _, err := b.Ask(ctx, bus.AskDef{From: "coder2", Title: "Which one?", Questions: []bus.AskQuestion{{
		ID: "q1", Header: "One", Question: "Which?", Options: []bus.AskOption{{Label: "a"}}}}}, ""); err != nil {
		t.Fatal(err)
	}
	ref := strconv.FormatInt(time.Now().Add(-time.Minute).UnixMilli(), 10) + "-0"
	if err := b.OpenChallenge(ctx, "foureyes", ref, person+"|review the spec"); err != nil {
		t.Fatal(err)
	}
	if err := b.OpenChallenge(ctx, person, ref, "master|sign off"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	stall, _ := json.Marshal(healthpkg.Record{ID: "team-stall", Status: healthpkg.Warn, Reason: "waiting-person", ObservedAt: now, Since: now, Detail: "the team waits"})
	r.HSet(ctx, bus.HealthKey(p), "team-stall", stall)
	items, _ := json.Marshal([]bus.MergeItem{{Number: 841, Title: "t", Branch: "b", Head: "h", Approvers: []string{"a", "b"}, ApprovedAt: now}})
	r.HSet(ctx, bus.MergeQueueKey(p), "attempted_at", now, "items_at", now, "source", "gh", "items", string(items))

	st := newShared(person, busmonSettings{})
	for tick := 1; tick <= 2; tick++ { // the first tick learns the roster, the second reads each agent's facts
		pollOnce(ctx, b, st, tick*requestPoll+1)
	}
	at := time.Now()
	var mine []string
	ranks := map[int]bool{}
	for _, it := range forYou(takeSnapshot(st, at), nil) {
		if it.Rank != rankReport && it.Rank != rankUnread {
			mine = append(mine, it.Key)
			ranks[it.Rank] = true
		}
	}
	in, failed := attention.Read(ctx, b, attention.ReadOpts{Person: person})
	if len(failed) != 0 {
		t.Fatalf("read failed: %v", failed)
	}
	var theirs []string
	for _, it := range attention.Items(in, at) {
		theirs = append(theirs, it.Key)
	}
	if !slices.Equal(mine, theirs) {
		t.Fatalf("busmon's For you %v\nsummary's (attention.Read) %v", mine, theirs)
	}
	for _, rk := range []int{rankAsk, rankHealth, rankWait, rankGate, rankMerge} {
		if !ranks[rk] {
			t.Errorf("the fixture holds no item of rank %d: %v", rk, mine)
		}
	}
	waits := func(name string) bool {
		return slices.ContainsFunc(theirs, func(k string) bool { return strings.HasPrefix(k, "wait:"+name+":") })
	}
	if !waits("coder") || waits("docs") {
		t.Errorf("coder waits, docs' wait was answered: %v", theirs)
	}
}
