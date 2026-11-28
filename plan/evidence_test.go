package plan

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// onboarding §9.1 (spec docs/superpowers/specs/2026-11-27-project-onboarding-design.md):
// a verdict proves a task's check when it names its head (--head) and its
// text holds "check: <the check> => exit 0 at <that head>", whitespace runs
// collapsed on both sides; a grade line carries an item id before it (§11.1).

const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestEvidenceLines(t *testing.T) {
	text := strings.Join([]string{
		"approve: ran it",
		"check: go test ./plan -run X => exit 0 at " + headA,
		"  dod-3 check: scripts/check.sh   --all => exit 1 at " + headB + "  ",
		"PASS 12 tests",
		"check: a => b => exit 2 at " + headA, // the last "=> exit" splits
		"check: no exit here",
		"check:  => exit 0 at " + headA, // an empty check is no line
		"check: x => exit zero at " + headA,
	}, "\n")
	got := EvidenceLines(text)
	want := []Evidence{
		{Check: "go test ./plan -run X", Exit: 0, Head: headA},
		{Item: "dod-3", Check: "scripts/check.sh --all", Exit: 1, Head: headB},
		{Check: "a => b", Exit: 2, Head: headA},
	}
	if len(got) != len(want) {
		t.Fatalf("lines %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCheckEvidence(t *testing.T) {
	check := "go test ./plan  -run 'TestX|TestY'"
	v := func(head, text string) bus.Verdict {
		return bus.Verdict{ID: "5-0", Decision: "approve", Author: "coder", Reviewer: "foureyes", Head: head, Message: text}
	}
	if h, ok := CheckEvidence(v(headA, "approve\ncheck:   go test ./plan -run 'TestX|TestY' => exit 0 at "+headA+"\nok"), check); !ok || h != headA {
		t.Fatalf("the line at the verdict's head: %q %v", h, ok)
	}
	full := v(headA, "approve (cut)")
	full.Full = "approve\ncheck: " + check + " => exit 0 at " + headA
	if _, ok := CheckEvidence(full, check); !ok {
		t.Fatal("the full text is not read")
	}
	short := v(headA, "approve\ncheck: "+check+" => exit 0 at "+headA)
	short.Full = "approve, the full text without the line"
	if _, ok := CheckEvidence(short, check); ok {
		t.Fatal("Message read while Full is set")
	}
	for name, c := range map[string]bus.Verdict{
		"no evidence line":        v(headA, "approve: looks good"),
		"exit 1":                  v(headA, "check: "+check+" => exit 1 at "+headA),
		"another check":           v(headA, "check: go test ./plan -run TestX => exit 0 at "+headA),
		"a verdict without head":  v("", "check: "+check+" => exit 0 at "+headA),
		"a line at another head":  v(headA, "check: "+check+" => exit 0 at "+headB),
		"a short head":            v(headA, "check: "+check+" => exit 0 at aaaaaaa"),
		"an invalid verdict head": v("aaaaaaa", "check: "+check+" => exit 0 at aaaaaaa"),
	} {
		if h, ok := CheckEvidence(c, check); ok || h != "" {
			t.Errorf("%s: proves the check (%q)", name, h)
		}
	}
	if evidenceOf(v(headA, "check: "+check+" => exit 1 at "+headA), check) != evidenceFailed {
		t.Error("exit 1 is not a failed check")
	}
	if evidenceOf(v(headA, "approve"), check) != evidenceNone {
		t.Error("no line is not 'not run'")
	}
}

// KeepEvidence is the verdict busmon keeps (it drops the texts): its
// evidence lines only, so the check's reading is unchanged.
func TestKeepEvidence(t *testing.T) {
	check := "go test ./plan"
	for _, v := range []bus.Verdict{
		{Head: headA, Message: "approve: long prose\ncheck:  go test   ./plan => exit 0 at " + headA + "\nPASS"},
		{Head: headA, Message: "cut", Full: "prose\ncheck: go test ./plan => exit 1 at " + headA},
		{Head: headA, Message: "check: go test ./plan => exit 0 at " + headA, Full: "the full text, no line"},
		{Head: headA, Message: "dod-2 check: go test ./plan => exit 0 at " + headA},
		{Head: headA, Message: "approve, no line"},
	} {
		k := KeepEvidence(v)
		if k.Full != "" || strings.Contains(k.Message, "prose") || strings.Contains(k.Message, "PASS") {
			t.Errorf("%q kept text: %+v", v.Message, k)
		}
		if evidenceOf(k, check) != evidenceOf(v, check) {
			t.Errorf("%q: kept %v, was %v", v.Message, evidenceOf(k, check), evidenceOf(v, check))
		}
		if a, b := EvidenceLines(k.Message), EvidenceLines(cmpText(v)); len(a) != len(b) {
			t.Errorf("%q: lines %+v, was %+v", v.Message, a, b)
		}
	}
}

func cmpText(v bus.Verdict) string {
	if v.Full != "" {
		return v.Full
	}
	return v.Message
}
