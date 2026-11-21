package bus

import (
	"context"
	"testing"
)

// triage-dedupe §3, step 1: a report says it needs the person with the
// field needs, answer or read; its notice says it too; any other value is
// refused, nothing written; no value writes no field (additive).
func TestReportNeedsParses(t *testing.T) {
	for _, kind := range []string{"report", "cmd"} {
		if e := ParseEntry("p:"+kind, "1-0", map[string]string{"agent": "master", "message": "m", "needs": "answer"}); e.Needs != NeedsAnswer {
			t.Fatalf("%s: Needs = %q", kind, e.Needs)
		}
		if e := ParseEntry("p:"+kind, "1-0", map[string]string{"agent": "master", "message": "m", "needs": "later"}); e.Needs != "" {
			t.Fatalf("%s: an unknown value read as %q", kind, e.Needs)
		}
		if e := ParseEntry("p:"+kind, "1-0", map[string]string{"agent": "master", "message": "m"}); e.Needs != "" {
			t.Fatalf("%s: no field read as %q", kind, e.Needs)
		}
	}
}

func TestReportNeedsWrites(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	for _, needs := range []string{NeedsAnswer, NeedsRead} {
		id, err := b.ReportNeeds(ctx, "master", ReportNote, "merge order?", []string{"charlie"}, needs)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := b.r.XRange(ctx, StreamKey(b.project, "report"), id, id).Result()
		if len(raw) != 1 || raw[0].Values["needs"] != needs {
			t.Fatalf("%s: entry %+v", needs, raw)
		}
		if e, err := b.ReportByID(ctx, id); err != nil || e.Needs != needs || e.To != "charlie" {
			t.Fatalf("%s: read back %+v %v", needs, e, err)
		}
		nid, err := b.ReportNotice(ctx, "master", "charlie", id, "Report "+id+" for you", needs)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := b.r.XRange(ctx, StreamKey(b.project, "cmd"), nid, nid).Result()
		if len(c) != 1 || c[0].Values["needs"] != needs || c[0].Values["ref"] != id || c[0].Values["type"] != CmdDirective {
			t.Fatalf("%s: notice %+v", needs, c)
		}
	}
	before, _ := b.r.XLen(ctx, StreamKey(b.project, "report")).Result()
	if _, err := b.ReportNeeds(ctx, "master", ReportNote, "x", nil, "maybe"); err == nil {
		t.Fatal("needs=maybe was written")
	}
	if _, err := b.ReportNotice(ctx, "master", "charlie", "1-0", "x", "maybe"); err == nil {
		t.Fatal("a notice with needs=maybe was written")
	}
	if after, _ := b.r.XLen(ctx, StreamKey(b.project, "report")).Result(); after != before {
		t.Fatal("a refused report was written")
	}
	id, _ := b.ReportNeeds(ctx, "master", ReportNote, "status only", nil, "")
	raw, _ := b.r.XRange(ctx, StreamKey(b.project, "report"), id, id).Result()
	if _, ok := raw[0].Values["needs"]; ok {
		t.Fatalf("a report without needs has the field: %+v", raw[0].Values)
	}
}
