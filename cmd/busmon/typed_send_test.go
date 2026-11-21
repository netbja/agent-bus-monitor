package main

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// person-memory step 1: busmon's input line and compose window (a
// directive) and its broadcast write typed=yes through the real bus.
func TestBusmonSendIsTyped(t *testing.T) {
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	project := "ty" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, _ := bus.Open(c, project)
	t.Cleanup(func() { c.Del(ctx, bus.StreamKey(project, "cmd"), bus.StreamKey(project, "notify")) })

	for _, p := range []sendPlan{{targets: []string{"master"}, body: "yes, dev first"}, {notify: true, body: "back at 2"}} {
		for _, err := range p.exec(ctx, b, "charlie") {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	evs, _, err := b.Recent(ctx, []string{"cmd", "notify"}, 10)
	if err != nil || len(evs) != 2 {
		t.Fatalf("entries %+v %v", evs, err)
	}
	for _, e := range evs {
		if !e.Human || !e.Typed || e.From != "charlie" {
			t.Errorf("%s entry not typed: %+v", e.Kind, e)
		}
	}
}
