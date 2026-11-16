package bus

import (
	"context"
	"testing"
)

// busmon-reply-target (2026-11-16): a report sent with --to named nobody on
// the report itself (only the notices were addressed), so busmon showed it
// as a broadcast. The report entry records its addressees, field "to":
// additive, an older entry has none.
func TestReportRecordsItsAddressees(t *testing.T) {
	e := ParseEntry("p:report", "1-0", map[string]string{"agent": "master", "kind": "note", "message": "m", "to": "bernard,coder"})
	if e.To != "bernard,coder" {
		t.Fatalf("To = %q", e.To)
	}
	if e := ParseEntry("p:report", "1-0", map[string]string{"agent": "master", "message": "m"}); e.To != "" {
		t.Fatalf("an entry without addressees: To = %q", e.To)
	}
	// A name that is no agent name is dropped, never shown.
	if e := ParseEntry("p:report", "1-0", map[string]string{"agent": "master", "message": "m", "to": "bernard,Bad Name,,\x1b[2J"}); e.To != "bernard" {
		t.Fatalf("To = %q", e.To)
	}
}

func TestReportToWritesTheAddressees(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	id, err := b.ReportTo(ctx, "master", ReportNote, "for you", []string{"bernard"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := b.ReportByID(ctx, id)
	if err != nil || e.To != "bernard" {
		t.Fatalf("read back %+v, %v", e, err)
	}
	if _, err := b.ReportTo(ctx, "master", ReportNote, "x", []string{"Bad Name"}); err == nil {
		t.Fatal("an invalid addressee was written")
	}
	id, _ = b.Report(ctx, "master", ReportNote, "to nobody")
	if e, _ := b.ReportByID(ctx, id); e.To != "" {
		t.Fatalf("a report without --to: To = %q", e.To)
	}
}
