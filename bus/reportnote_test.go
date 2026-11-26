package bus

import (
	"errors"
	"testing"
)

// board-drift §4 (step 3): a report names its request (report --task). The
// note transition sets reported_at and report_id with no state change; it
// refuses only an unknown task, one that is not a tracked request, a request
// whose target is not the reporter, and a done one; never the WIP limit, an
// expired request or a lost body. CheckReportTask is the same check before
// anything is published; with done it also refuses where done would (not
// accepted, blocked).
func TestNoteRequest(t *testing.T) {
	b, ctx := wipWorld(t, "a", "b")
	if err := b.BoardClaim(ctx, "free", "peer", ""); err != nil { // an untracked board task
		t.Fatal(err)
	}
	if err := b.ReviewRequest(ctx, "b", "peer", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := b.BlockRequest(ctx, "c", "peer", "waits"); err != nil { // blocked before any accept
		t.Fatal(err)
	}
	before := stateOf(t, b, ctx, "a")
	n := cmdLen(t, b)
	if err := b.CheckReportTask(ctx, "a", "peer", false); err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := b.NoteRequest(ctx, "a", "peer", "1795000000000-0"); err != nil {
		t.Fatalf("note: %v", err)
	}
	board, err := b.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q := board["a"].Request
	if q.ReportedAt == 0 || q.ReportID != "1795000000000-0" || board["a"].State != before || q.AcceptedAt == 0 {
		t.Fatalf("after the note: state %s, %+v", board["a"].State, q)
	}
	if cmdLen(t, b) != n {
		t.Fatal("a note wrote a stream entry")
	}
	// a note changes no state, so a request not accepted, in review or
	// blocked takes one (the WIP limit is never read)
	for _, task := range []string{"b", "c", "d"} {
		if err := b.CheckReportTask(ctx, task, "peer", false); err != nil {
			t.Errorf("%s: %v", task, err)
		}
		if err := b.NoteRequest(ctx, task, "peer", "1795000000001-0"); err != nil {
			t.Errorf("%s: %v", task, err)
		}
	}
	if s := stateOf(t, b, ctx, "d"); s != "requested" {
		t.Errorf("d is %s after a note", s)
	}
	if _, err := b.CompleteRequest(ctx, "a", "peer", "finished"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ task, agent, want string }{
		{"nope", "peer", "unknown request"},
		{"free", "peer", "board task is not a tracked request"},
		{"o", "peer", "only the target agent may"},
		{"d", "master", "only the target agent may"}, // the sender is not the target either
		{"a", "peer", "request already completed"},
	} {
		for name, err := range map[string]error{
			"check": b.CheckReportTask(ctx, c.task, c.agent, false),
			"note":  b.NoteRequest(ctx, c.task, c.agent, "1795000000002-0"),
		} {
			if !errors.Is(err, ErrRequestRefused) || len(err.Error()) < len(c.want) || err.Error()[:len(c.want)] != c.want {
				t.Errorf("%s %s by %s: %v, want %q", name, c.task, c.agent, err, c.want)
			}
		}
	}
	// --done: refused where done would be, nothing written
	for _, c := range []struct{ task, want string }{
		{"d", "request must be explicitly accepted before completion"},
		{"c", "request must be explicitly accepted before completion"},
	} {
		if err := b.CheckReportTask(ctx, c.task, "peer", true); !errors.Is(err, ErrRequestRefused) || err.Error() != c.want {
			t.Errorf("done check %s: %v, want %q", c.task, err, c.want)
		}
	}
	if err := b.AcceptRequest(ctx, "c", "peer"); err != nil {
		t.Fatal(err)
	}
	if err := b.BlockRequest(ctx, "c", "peer", "again"); err != nil {
		t.Fatal(err)
	}
	if err := b.CheckReportTask(ctx, "c", "peer", true); !errors.Is(err, ErrRequestRefused) || err.Error() != "blocked request: accept to resume before completion" {
		t.Errorf("done check on a blocked request: %v", err)
	}
	if err := b.CheckReportTask(ctx, "b", "peer", true); err != nil { // in review: done passes
		t.Errorf("done check in review: %v", err)
	}
	if err := b.NoteRequest(ctx, "b", "peer", "not-an-id"); err == nil {
		t.Error("a report id that is no stream id was written")
	}
}

// The report itself carries the task (field task).
func TestReportTaskField(t *testing.T) {
	b, ctx := wipWorld(t, "a")
	id, err := b.ReportTask(ctx, "peer", ReportNote, "a: step 1 up", []string{"master"}, "", "a")
	if err != nil {
		t.Fatal(err)
	}
	e, err := b.ReportByID(ctx, id)
	if err != nil || e.Task != "a" || e.To != "master" {
		t.Fatalf("report %+v, %v", e, err)
	}
	if _, err := b.ReportTask(ctx, "peer", ReportNote, "x", nil, "", "Bad Name"); err == nil {
		t.Error("an invalid task was written")
	}
	plain, err := b.Report(ctx, "peer", ReportNote, "no task")
	if err != nil {
		t.Fatal(err)
	}
	if e, _ := b.ReportByID(ctx, plain); e.Task != "" {
		t.Errorf("a report without --task carries %q", e.Task)
	}
}

// cmdLen is the cmd stream's length.
func cmdLen(t *testing.T, b *Bus) int64 {
	t.Helper()
	n, err := b.r.XLen(t.Context(), StreamKey(b.project, "cmd")).Result()
	if err != nil {
		t.Fatal(err)
	}
	return n
}
