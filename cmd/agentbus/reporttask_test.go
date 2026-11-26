package main

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// reportTaskWorld: master asked coder for "a" (accepted) and "b" (not).
func reportTaskWorld(t *testing.T) (*bus.Bus, context.Context) {
	t.Helper()
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
	for _, task := range []string{"a", "b"} {
		if _, err := b.Request(ctx, task, "master", "coder", "", "do "+task, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.AcceptRequest(ctx, "a", "coder"); err != nil {
		t.Fatal(err)
	}
	return b, ctx
}

func reportCount(t *testing.T, b *bus.Bus, ctx context.Context) int {
	t.Helper()
	evs, err := b.Reports(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	return len(evs)
}

// board-drift §4 (step 3): report --task names the request on the report
// and notes it (reported_at, report_id), its state unchanged; a refused
// note publishes nothing; a report without --task is as before.
func TestReportTask(t *testing.T) {
	b, ctx := reportTaskWorld(t)
	id, err := sendReport(ctx, b, []string{"coder", "--task", "a", "--to", "master", "a: step 1 in review"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if e, err := b.ReportByID(ctx, id); err != nil || e.Task != "a" {
		t.Fatalf("report %+v, %v", e, err)
	}
	board, err := b.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if q := board["a"].Request; q.ReportID != id || q.ReportedAt == 0 || board["a"].State != "accepted" {
		t.Fatalf("a after the report: %s %+v", board["a"].State, q)
	}
	n := reportCount(t, b, ctx)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"coder", "--task", "nope", "x"}, "unknown request"},
		{[]string{"master", "--task", "a", "x"}, "only the target agent may"},
		{[]string{"coder", "--task", "Bad Name", "x"}, "invalid"},
		{[]string{"coder", "--done", "x"}, "--done needs --task"},
		{[]string{"coder", "--task", "b", "--done", "x"}, "request must be explicitly accepted before completion"},
		{[]string{"coder", "--task", "a", "--task", "b", "x"}, "--task takes one value"},
	} {
		if _, err := sendReport(ctx, b, c.args, io.Discard); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: %v, want %q", c.args, err, c.want)
		}
	}
	if got := reportCount(t, b, ctx); got != n {
		t.Fatalf("a refused report was published: %d reports, %d before", got, n)
	}
	if st := board["b"].State; st != "requested" {
		t.Fatalf("b is %s", st)
	}
}

// --done also completes it, as request done: the report's first line and
// "(report <id>)" are the reply.
func TestReportDone(t *testing.T) {
	b, ctx := reportTaskWorld(t)
	id, err := sendReport(ctx, b, []string{"coder", "--task", "a", "--done", "a done: merged as #9\n\nDetails below."}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	board, err := b.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q := board["a"].Request
	if board["a"].State != "done" || q.ReportID != id || q.ResponseID == "" {
		t.Fatalf("a: %s %+v", board["a"].State, q)
	}
	evs, err := b.Thread(ctx, q.Thread)
	if err != nil {
		t.Fatal(err)
	}
	want := "a done: merged as #9 (report " + id + ")"
	found := false
	for _, e := range evs {
		found = found || (e.Type == bus.CmdReply && e.Message == want)
	}
	if !found {
		t.Fatalf("no reply %q in %+v", want, evs)
	}
}
