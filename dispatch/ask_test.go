package dispatch

import (
	"context"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// An ask entry (target people, wake=no) wakes nobody and creates no group; the
// person's answer, a reply to the asking agent, wakes it.
func TestRunAskWakesOnlyOnTheAnswer(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(ctx, bus.AsksKey(b.Project())) })
	register(t, b, "master", "p-master")
	id, _, err := b.Ask(ctx, bus.AskDef{From: "master", Title: "T", Questions: []bus.AskQuestion{
		{ID: "d", Header: "Date", Question: "When?", Options: []bus.AskOption{{Label: "Mon"}}}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, fastConfig())
	time.Sleep(400 * time.Millisecond)
	if n := len(rt.Prompts()); n != 0 {
		t.Fatalf("%d prompts for an ask", n)
	}
	if hasGroup(t, b, bus.PeopleTarget) {
		t.Fatal("a consumer group was created for people")
	}
	if _, _, err := b.Answer(ctx, id, "d", "bernard", "Mon", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, "master woken by the answer", func() bool { return len(rt.Prompts()) == 1 })
}
