package main

import (
	"strings"
	"testing"
)

// busmon-detail-labels (Bernard): a page's "key  value" rows read as
// "Label:  value": the label capitalised with a colon, every value of the
// block in one column two cells after the longest label, a long value
// wrapping under its value and never under the label; an empty label
// continues the row above, a gap is a blank line in the same block.
func TestFieldsAlignAndWrap(t *testing.T) {
	var f fields
	f.add("state", S(RoleAttention, "waiting for you"))
	f.add("title", S(RoleText, "Live trial of the auto /clear at 80% (AGENT_BUS_AUTO_CLEAR) with Bernard, then its default"))
	f.add("", S(RoleMuted, "a continuation"))
	f.gap()
	f.add("last fact", S(RoleText, "3m ago"))
	f.add("PR", S(RoleText, "#12"))
	got := f.lines(40)
	want := []string{
		"State:      waiting for you",
		"Title:      Live trial of the auto",
		"            /clear at 80%",
		"            (AGENT_BUS_AUTO_CLEAR) with",
		"            Bernard, then its default",
		"            a continuation",
		"",
		"Last fact:  3m ago",
		"PR:         #12",
	}
	var texts []string
	for _, l := range got {
		texts = append(texts, l.text())
	}
	if strings.Join(texts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("fields:\n%s\nwant:\n%s", strings.Join(texts, "\n"), strings.Join(want, "\n"))
	}
	if got[0][0].Role != RoleMuted || got[0][1].Role != RoleAttention {
		t.Errorf("label muted, value its own role: %+v", got[0])
	}
	for _, l := range got {
		if l.Width() > 40 {
			t.Errorf("%q is %d cells, over 40", l.text(), l.Width())
		}
	}
	// A value that fits keeps its segments as built (two spaces inside, roles).
	var g fields
	g.add("when", S(RoleText, "2026-10-28 15:04:05  "), S(RoleMuted, "(3m ago)"))
	if l := g.lines(80)[0]; l.text() != "When:  2026-10-28 15:04:05  (3m ago)" || len(l) != 3 {
		t.Errorf("a fitting value was rewrapped: %q %+v", l.text(), l)
	}
}

func TestFieldLabel(t *testing.T) {
	for in, want := range map[string]string{"state": "State:", "PR": "PR:", "last fact": "Last fact:", "Receiver lease": "Receiver lease:", "": ""} {
		if got := fieldLabel(in); got != want {
			t.Errorf("fieldLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
