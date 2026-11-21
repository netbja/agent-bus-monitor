package main

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/history"
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
	id, err := sendReport(ctx, b, []string{"master", "--to", "charlie", "--to", "coder", "Review ready"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if e, err := b.ReportByID(ctx, id); err != nil || e.To != "charlie,coder" {
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

// triage-dedupe step 1: report --needs answer|read writes needs on the
// report and on each notice, the subscribe JSON and history say it; any
// other value is refused before anything is written.
func TestReportNeedsFlag(t *testing.T) {
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
	id, err := sendReport(ctx, b, []string{"master", "--needs", "answer", "--to", "charlie", "Merge order for 694-696?"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if e, err := b.ReportByID(ctx, id); err != nil || e.Needs != bus.NeedsAnswer {
		t.Fatalf("report %+v %v", e, err)
	}
	evs, _ := b.Thread(ctx, id)
	var notice bus.Event
	for _, e := range evs {
		if e.Kind == "cmd" && e.Target == "charlie" {
			notice = e
		}
	}
	if notice.Needs != bus.NeedsAnswer {
		t.Fatalf("notice %+v", notice)
	}
	if j := cmdEvent(notice, nil); j.Needs != bus.NeedsAnswer {
		t.Fatalf("subscribe JSON %+v", j)
	}
	if line := historyLine(history.Entry{Stream: "report", ID: id, F: map[string]string{"agent": "master", "message": "Merge order?", "needs": "answer"}}, time.UTC); !strings.Contains(line, "needs answer") {
		t.Fatalf("history line %q", line)
	}
	if line := historyLine(history.Entry{Stream: "report", ID: id, F: map[string]string{"agent": "master", "message": "status"}}, time.UTC); strings.Contains(line, "needs") {
		t.Fatalf("history line without needs %q", line)
	}
	n0, _ := client.XLen(ctx, bus.StreamKey(project, "report")).Result()
	if _, err := sendReport(ctx, b, []string{"master", "--needs", "maybe", "--to", "charlie", "x"}, io.Discard); err == nil || !strings.Contains(err.Error(), "answer or read") {
		t.Fatalf("--needs maybe: %v", err)
	}
	if n1, _ := client.XLen(ctx, bus.StreamKey(project, "report")).Result(); n1 != n0 {
		t.Fatal("a refused --needs was written")
	}
	// reviewer-codex: a --needs with no value (last on the line) or a second
	// --needs is refused, never left in the message as a report needing nothing.
	for _, args := range [][]string{
		{"master", "--to", "charlie", "Please choose", "--needs"},
		{"master", "--needs", "answer", "--to", "charlie", "Please choose", "--needs"},
		{"master", "--needs", "answer", "--needs", "read", "x"},
	} {
		if _, err := sendReport(ctx, b, args, io.Discard); err == nil || !strings.Contains(err.Error(), "--needs") {
			t.Errorf("%q: %v", args, err)
		}
	}
	if n1, _ := client.XLen(ctx, bus.StreamKey(project, "report")).Result(); n1 != n0 {
		t.Fatal("a refused trailing --needs was written")
	}
	id, _ = sendReport(ctx, b, []string{"master", "--needs", "read", "Release notes are ready"}, io.Discard)
	if e, _ := b.ReportByID(ctx, id); e.Needs != bus.NeedsRead {
		t.Fatalf("--needs read without --to: %+v", e)
	}
}
