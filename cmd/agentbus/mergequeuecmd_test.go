package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// mqCmdHead is the queued head printMergeQueue shows (f98091b in its output).
const mqCmdHead = "f98091be429b220f63dc8b4b2dde3ef7d6d832a5"

func mqCmdRecord() bus.MergeQueueRecord {
	return bus.MergeQueueRecord{AttemptedAt: 1793830000000, ItemsAt: 1793830000000, Source: "gh", Forge: "github", Base: "main", Unapproved: 1, Conflicting: []bus.MergeConflict{{Number: 500, Base: "main"}, {Number: 512, Base: "release"}},
		Items: []bus.MergeItem{{Number: 309, Title: "request priority", URL: "https://github.com/o/r/pull/309", Branch: "feat/request-priority",
			Head: mqCmdHead, Subjects: []string{"request-priority"}, ApprovedAt: 1793820000000,
			Approvals: []bus.MergeApproval{{Reviewer: "foureyes"}, {Reviewer: "reviewer-codex"}}}}}
}

func TestMergeQueueCmdOutput(t *testing.T) {
	var out bytes.Buffer
	code := printMergeQueue(mqCmdRecord(), false, &out)
	got := out.String()
	if code != 0 || !strings.Contains(got, "#309  request-priority  f98091b  approved by foureyes, reviewer-codex  https://github.com/o/r/pull/309") ||
		!strings.Contains(got, "source gh") || !strings.Contains(got, "1 open PR not on an approved head") ||
		!strings.Contains(got, "approved but conflicting with their base, to rebase: #500 onto main, #512 onto release") {
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

// master-idle §3.1: the undelivered heads are printed for master.
func TestMergeQueueCmdUndelivered(t *testing.T) {
	rec := mqCmdRecord()
	rec.Undelivered = []bus.MergeUndelivered{{Subject: "herdr-6c2-2", Head: "fa2d796f00000000000000000000000000000000", Branch: "feat/herdr-6c2-2"}, {Subject: "x", Head: "abcdef1000000000000000000000000000000000"}}
	var out bytes.Buffer
	printMergeQueue(rec, false, &out)
	if !strings.Contains(out.String(), "approved, not pushed (master delivers or drops them): herdr-6c2-2 fa2d796 feat/herdr-6c2-2, x abcdef1") {
		t.Fatalf("%s", out.String())
	}
}

// merge-queue drop / undrop: a short SHA resolved against the approved heads
// (undrop: the dropped ones), an ambiguous or unknown one refused; only who
// may deliver drops; the dropped heads are listed with their reasons.
func TestMergeQueueDrop(t *testing.T) {
	b, _ := dialMain(t)
	ctx := context.Background()
	const a1, a2, other = "fa2d796f00000000000000000000000000000000", "fa2d111100000000000000000000000000000000", "9b7106db00000000000000000000000000000000"
	for i, h := range []string{a1, a2, other} {
		if _, err := b.AppendVerdict(ctx, bus.Verdict{Subject: "s" + string(rune('a'+i)), Author: "coder", Reviewer: "foureyes", Decision: "approve", Head: h}); err != nil {
			t.Fatal(err)
		}
	}
	allow := func(string) error { return nil }
	run := func(self string, may func(string) error, args ...string) (int, string) {
		var out, errw bytes.Buffer
		code := runMergeDrop(ctx, b, self, may, args, &out, &errw)
		return code, out.String() + errw.String()
	}
	if code, msg := run("coder", func(s string) error { return errors.New(s + " may not deliver") }, "drop", "9b7106db", "redundant"); code != 1 || !strings.Contains(msg, "may not deliver") {
		t.Fatalf("a role without delivers: %d %q", code, msg)
	}
	if code, msg := run("master", allow, "drop", "fa2d", "x"); code != 2 || !strings.Contains(msg, "ambiguous") {
		t.Fatalf("ambiguous: %d %q", code, msg)
	}
	if code, msg := run("master", allow, "drop", "deadbeef", "x"); code != 2 || !strings.Contains(msg, "no approved head") {
		t.Fatalf("unknown: %d %q", code, msg)
	}
	if code, msg := run("master", allow, "drop", "9b7106db"); code != 2 || !strings.Contains(msg, "usage") {
		t.Fatalf("no reason: %d %q", code, msg)
	}
	if ds, _ := b.DroppedHeads(ctx); len(ds) != 0 {
		t.Fatalf("a refused drop wrote %+v", ds)
	}
	if code, msg := run("master", allow, "drop", "9b7106db", "redundant after operator-notes-fragments"); code != 0 || !strings.Contains(msg, "dropped 9b7106d") {
		t.Fatalf("drop: %d %q", code, msg)
	}
	if ds, _ := b.DroppedHeads(ctx); ds[other].Reason != "redundant after operator-notes-fragments" || ds[other].By != "master" {
		t.Fatalf("dropped %+v", ds)
	}
	var out bytes.Buffer
	printDropped(map[string]bus.MergeDrop{other: {Reason: "redundant", By: "master", At: 1793830000000}}, &out)
	if !strings.Contains(out.String(), "dropped (never to be delivered): 9b7106d redundant (master,") {
		t.Fatalf("listing %q", out.String())
	}
	if code, msg := run("master", allow, "undrop", "fa2d796f"); code != 2 || !strings.Contains(msg, "not dropped") {
		t.Fatalf("undrop of a head not dropped: %d %q", code, msg)
	}
	if code, msg := run("master", allow, "undrop", "9b71"); code != 0 || !strings.Contains(msg, "undropped 9b7106d") {
		t.Fatalf("undrop: %d %q", code, msg)
	}
	if ds, _ := b.DroppedHeads(ctx); len(ds) != 0 {
		t.Fatalf("after undrop %+v", ds)
	}
}

// TestMergeQueueCmdCIRed: a queued PR whose head's checks are red carries
// the mark on its row, its failing checks named as the loop's hand-off names
// them (at most 10, then "and N more"); a PR with no failure has no mark (ci-loop §8 Q8).
func TestMergeQueueCmdCIRed(t *testing.T) {
	rec := mqCmdRecord()
	rec.Items[0].CIRed = []string{"build", "test"}
	var out bytes.Buffer
	printMergeQueue(rec, false, &out)
	if !strings.Contains(out.String(), "#309  request-priority  f98091b  approved by foureyes, reviewer-codex  CI red: `build`, `test`  https://github.com/o/r/pull/309") {
		t.Fatalf("%s", out.String())
	}
	var many []string
	for i := range 12 {
		many = append(many, fmt.Sprintf("c%02d", i))
	}
	rec.Items[0].CIRed = many
	out.Reset()
	printMergeQueue(rec, false, &out)
	if !strings.Contains(out.String(), "CI red: `c00`, `c01`, `c02`, `c03`, `c04`, `c05`, `c06`, `c07`, `c08`, `c09`, and 2 more  https://") {
		t.Fatalf("%s", out.String())
	}
	out.Reset()
	if printMergeQueue(rec, true, &out); !strings.Contains(out.String(), `"ci_red":["c00",`) {
		t.Fatalf("json %s", out.String())
	}
	out.Reset()
	printMergeQueue(mqCmdRecord(), false, &out)
	if strings.Contains(out.String(), "CI red") {
		t.Fatalf("a green row has a mark: %s", out.String())
	}
}

// ACL answer e (the person's yes): dropping a head is master's; docs, though
// it delivers, asks master. Another agent is refused before the deliverers'
// check; master and the person go on to it.
func TestMergeDropMayIsMasters(t *testing.T) {
	calls := 0
	inner := func(string) error { calls++; return nil }
	for _, c := range []struct {
		env  []string
		ok   bool
		next bool
	}{
		{[]string{"AGENT_BUS_AGENT=docs"}, false, false},
		{[]string{"AGENT_BUS_AGENT=coder"}, false, false},
		{[]string{"AGENT_BUS_AGENT=master"}, true, true},
		{nil, true, true}, // the person
	} {
		calls = 0
		err := mergeDropMay(c.env, inner)("x")
		if (err == nil) != c.ok || (calls == 1) != c.next {
			t.Errorf("%v: err %v, deliverers' check called %d", c.env, err, calls)
		}
		if err != nil && !strings.Contains(err.Error(), "master's") {
			t.Errorf("%v: %v", c.env, err)
		}
	}
}
