package memory_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/memory"
	"github.com/netbja/agent-bus-monitor/memory/memtest"
)

// addNote writes a note under docs/memory, links it from INDEX with label,
// and re-syncs AGENTS.md so the block rule never masks the rule under test.
func addNote(t *testing.T, root, rel, label, kind, status, created, verified, confidence string) {
	t.Helper()
	memtest.Write(t, root, "docs/memory/"+rel, fmt.Sprintf("---\nid: n\nproject: test\nscope: project\nkind: %s\nstatus: %s\ncreated: %s\nlast_verified: %s\nauthor: test\nconfidence: %s\n---\n# N\n\n[Evidence](../PROTOCOL.md)\n",
		kind, status, created, verified, confidence))
	memtest.Append(t, root, "docs/memory/INDEX.md", "- ["+label+"]("+rel+")\n")
	sync(t, root)
}

func sync(t *testing.T, root string) {
	t.Helper()
	if _, err := memory.Sync(root); err != nil {
		t.Fatal(err)
	}
}

func TestIndexLabelMismatch(t *testing.T) {
	for _, c := range []struct {
		label string
		fail  bool
	}{{"Dispatch (proposed)", true}, {"Dispatch (active)", false}, {"Dispatch", false}} {
		root := memtest.WriteTree(t)
		addNote(t, root, "decisions/d.md", c.label, "decision", "active", "2026-10-01", "2026-10-01", "verified")
		r := check(root)
		found := false
		for _, e := range r.Errors {
			if strings.Contains(e, "labelled (proposed)") && strings.Contains(e, "decisions/d.md has status active") {
				found = true
			}
		}
		if found != c.fail || (!c.fail && len(r.Errors) != 0) {
			t.Errorf("%q: errors %q", c.label, r.Errors)
		}
	}
}

func TestIndexBudget(t *testing.T) {
	base := "[Protocol](PROTOCOL.md)\n[Journal](journal/INDEX.md)\n"
	for _, c := range []struct {
		name  string
		index string
		fail  bool
	}{
		{"60 lines", base + strings.Repeat("x\n", 58), false},
		{"61 lines", base + strings.Repeat("x\n", 59), true},
		{"61 without final newline", base + strings.Repeat("x\n", 58) + "x", true},
		{"4097 bytes", base + strings.Repeat(strings.Repeat("y", 450)+"\n", 10)[:4097-len(base)], true},
	} {
		root := memtest.WriteTree(t)
		memtest.Write(t, root, "docs/memory/INDEX.md", c.index)
		sync(t, root)
		r := check(root)
		failed := false
		for _, e := range r.Errors {
			if strings.Contains(e, "must stay within 60 lines and 4096 bytes") {
				failed = true
			}
		}
		if failed != c.fail {
			t.Errorf("%s: errors %q", c.name, r.Errors)
		}
	}
}

func TestFillMarkerFails(t *testing.T) {
	root := memtest.WriteTree(t)
	addNote(t, root, "lessons/l.md", "L", "lesson", "active", "2026-10-01", "2026-10-01", "verified")
	memtest.Append(t, root, "docs/memory/lessons/l.md", "\n"+memory.FillMarker+" to do\n")
	hasErr(t, check(root), "docs/memory/lessons/l.md: template not filled (<!-- mem:fill --> remains)")
}

func TestStaleWarnings(t *testing.T) {
	root := memtest.WriteTree(t)
	addNote(t, root, "lessons/old.md", "Old", "lesson", "active", "2026-01-01", "2026-08-11", "verified")
	addNote(t, root, "lessons/edge.md", "Edge", "lesson", "active", "2026-01-01", "2026-08-12", "verified")
	addNote(t, root, "decisions/p.md", "P", "decision", "proposed", "2026-08-01", "unknown", "unknown")
	addNote(t, root, "journal/j.md", "J", "journal", "active", "2026-01-01", "2026-01-01", "verified")
	addNote(t, root, "lessons/r.md", "R", "lesson", "resolved", "2026-01-01", "2026-01-01", "verified")
	r := memory.Check(memory.Options{Root: root, Today: today, StaleAfter: 60 * 24 * time.Hour})
	if len(r.Errors) != 0 {
		t.Fatalf("stale notes must not fail: %q", r.Errors)
	}
	want := []string{
		"docs/memory/decisions/p.md: proposed since 2026-08-01, older than 60d; decide or supersede it",
		"docs/memory/lessons/old.md: verified on 2026-08-11, older than 60d; re-check its evidence",
	}
	if strings.Join(r.Warnings, "\n") != strings.Join(want, "\n") {
		t.Fatalf("warnings:\n%s\nwant:\n%s", strings.Join(r.Warnings, "\n"), strings.Join(want, "\n"))
	}
	if r := check(root); len(r.Warnings) != 2 { // zero StaleAfter means 60 days
		t.Fatalf("default threshold: %q", r.Warnings)
	}
}
