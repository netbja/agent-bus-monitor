package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

func mqCmdRecord() bus.MergeQueueRecord {
	return bus.MergeQueueRecord{AttemptedAt: 1793830000000, ItemsAt: 1793830000000, Source: "gh", Forge: "github", Base: "main", Unapproved: 1,
		Items: []bus.MergeItem{{Number: 309, Title: "request priority", URL: "https://github.com/o/r/pull/309", Branch: "feat/request-priority",
			Head: mqHead, Subjects: []string{"request-priority"}, ApprovedAt: 1793820000000,
			Approvals: []bus.MergeApproval{{Reviewer: "foureyes"}, {Reviewer: "reviewer-codex"}}}}}
}

func TestMergeQueueCmdOutput(t *testing.T) {
	var out bytes.Buffer
	code := printMergeQueue(mqCmdRecord(), false, &out)
	got := out.String()
	if code != 0 || !strings.Contains(got, "#309  request-priority  f98091b  approved by foureyes, reviewer-codex  https://github.com/o/r/pull/309") ||
		!strings.Contains(got, "source gh") || !strings.Contains(got, "1 open PR not on an approved head") {
		t.Fatalf("code %d\n%s", code, got)
	}
	out.Reset()
	if code := printMergeQueue(mqCmdRecord(), true, &out); code != 0 {
		t.Fatalf("json code %d", code)
	}
	var back bus.MergeQueueRecord
	if err := json.Unmarshal(out.Bytes(), &back); err != nil || len(back.Items) != 1 || back.Items[0].Number != 309 {
		t.Fatalf("json %q: %v", out.String(), err)
	}
	// Nothing waits: 64. Unknown: 2, with the reason, the last items still printed.
	empty := bus.MergeQueueRecord{AttemptedAt: 1, ItemsAt: 1, Source: "gh"}
	out.Reset()
	if code := printMergeQueue(empty, false, &out); code != 64 || !strings.Contains(out.String(), "nothing waits") {
		t.Fatalf("empty: %d %q", code, out.String())
	}
	stale := mqCmdRecord()
	stale.Reason = "gh timed out"
	out.Reset()
	if code := printMergeQueue(stale, false, &out); code != 2 || !strings.Contains(out.String(), "unknown: gh timed out") || !strings.Contains(out.String(), "#309") {
		t.Fatalf("stale: %d %q", code, out.String())
	}
	out.Reset()
	if code := printMergeQueue(bus.MergeQueueRecord{}, false, &out); code != 2 || !strings.Contains(out.String(), "not computed") {
		t.Fatalf("no record: %d %q", code, out.String())
	}
}

// --live derives in the caller's checkout and writes nothing.
func TestMergeQueueLiveWritesNothing(t *testing.T) {
	c, b := mqChore(t, "git@github.com:o/r.git")
	rec := c.derive(context.Background())
	if len(rec.Items) != 1 || rec.Reason != "" {
		t.Fatalf("%+v", rec)
	}
	if got, _ := b.MergeQueue(context.Background()); got.AttemptedAt != 0 {
		t.Fatalf("live wrote the record: %+v", got)
	}
}

func TestMergeQueueIsReadOnly(t *testing.T) {
	pane := func(k string) string {
		if k == "HERDR_PANE_ID" {
			return "p1"
		}
		return ""
	}
	if err := identityGuard(pane, "merge-queue", nil); err != nil {
		t.Fatal(err)
	}
}
