package main

import (
	"context"
	"io"
	"strconv"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// busmon-reply-target: `report --to` names its addressees on the report
// itself (busmon shows them, not "broadcast"), and still tells each one.
func TestReportToNamesItsAddressees(t *testing.T) {
	testredis.Configure(t)
	client, err := bus.Connect("")
	if err != nil {
		t.Fatalf("explicit test Redis unavailable: %v", err)
	}
	project := "r" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), client, project); client.Close() })
	b, err := bus.Open(client, project)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, err := sendReport(ctx, b, []string{"master", "--to", "bernard", "--to", "coder", "Review ready"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if e, err := b.ReportByID(ctx, id); err != nil || e.To != "bernard,coder" {
		t.Fatalf("the report reads %+v, %v", e, err)
	}
	evs, err := b.Thread(ctx, id) // the notices carry the report's id as ref
	if err != nil {
		t.Fatal(err)
	}
	told := false
	for _, e := range evs {
		told = told || e.Kind == "cmd" && e.Target == "coder" && e.From == "master"
	}
	if !told {
		t.Fatalf("coder was not told: %+v", evs)
	}
	if _, err := sendReport(ctx, b, []string{"master", "--to", "Bad Name", "x"}, io.Discard); err == nil {
		t.Fatal("an invalid addressee was accepted")
	}
}
