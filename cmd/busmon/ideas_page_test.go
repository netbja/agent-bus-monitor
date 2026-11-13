package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

var ideasT0 = time.Date(2026, 11, 13, 21, 0, 0, 0, time.UTC)

func ideasFixture() []bus.Idea {
	at := ideasT0.Add(-2 * time.Hour).UnixMilli()
	return []bus.Idea{
		{ID: "i1", IdeaDef: bus.IdeaDef{Title: "espeak-ng voice", By: "coder", At: at}, State: bus.IdeaToDiscuss},
		{ID: "i2", IdeaDef: bus.IdeaDef{Title: "busmon update without setup.sh", By: "master", At: at, Source: "memory", Text: "one line\nthen more"},
			State: bus.IdeaDecided, Log: []bus.IdeaChange{{From: bus.IdeaToDiscuss, To: bus.IdeaDecided, By: "charlie", At: ideasT0.Add(-time.Hour).UnixMilli()}}},
		{ID: "i3", IdeaDef: bus.IdeaDef{Title: "child rows", By: "master", At: at},
			State: bus.IdeaDropped, Log: []bus.IdeaChange{{From: bus.IdeaToDiscuss, To: bus.IdeaDropped, By: "dana", At: ideasT0.Add(-time.Minute).UnixMilli()}}},
		{ID: "i4", IdeaDef: bus.IdeaDef{Title: "second to discuss", By: "coder", At: at}, State: bus.IdeaToDiscuss},
	}
}

// F6 lists every idea by state, in the order of life; the title counts them.
func TestIdeasPageAll(t *testing.T) {
	title, body, items, plain := ideasPage(ideasFixture(), false, "charlie", ideasView{}, 0, ideasT0, 100)
	if title != "Ideas "+GlyphNone+" 2 to discuss "+GlyphNone+" 1 decided "+GlyphNone+" 1 dropped" {
		t.Fatalf("title %q", title)
	}
	text := plainOf(body)
	for _, want := range []string{"To discuss", "Decided", "Dropped", "espeak-ng voice", "busmon update without setup.sh", "set by charlie", "set by dana"} {
		if !strings.Contains(text, want) {
			t.Fatalf("no %q in\n%s", want, text)
		}
	}
	if strings.Index(text, "To discuss") > strings.Index(text, "Decided") || strings.Index(text, "Decided") > strings.Index(text, "Dropped") {
		t.Fatalf("sections out of order\n%s", text)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it.id)
	}
	if strings.Join(ids, ",") != "i1,i4,i2,i3" {
		t.Fatalf("items %v", ids)
	}
	if plain != "espeak-ng voice" {
		t.Fatalf("plain %q", plain)
	}
}

// A state set by someone other than the configured person is flagged.
func TestIdeasPageFlagsAnotherSetter(t *testing.T) {
	_, body, items, _ := ideasPage(ideasFixture(), false, "charlie", ideasView{}, 0, ideasT0, 100)
	for _, it := range items {
		line := body[it.line]
		flagged := false
		for _, s := range line {
			if strings.Contains(s.Text, "set by") && s.Role == RoleAttention {
				flagged = true
			}
		}
		if want := it.id == "i3"; flagged != want {
			t.Fatalf("%s flagged %v: %q", it.id, flagged, plainOf([]Line{line}))
		}
	}
	// No configured person: nobody to compare with, nothing flagged.
	_, body, items, _ = ideasPage(ideasFixture(), false, "", ideasView{}, 0, ideasT0, 100)
	for _, it := range items {
		for _, s := range body[it.line] {
			if s.Role == RoleAttention {
				t.Fatalf("%s flagged with no person", it.id)
			}
		}
	}
}

// tab's filter: one state only, said in the title.
func TestIdeasPageFilter(t *testing.T) {
	title, body, items, _ := ideasPage(ideasFixture(), false, "charlie", ideasView{filter: 2}, 0, ideasT0, 100)
	if len(items) != 1 || items[0].id != "i2" || !strings.Contains(title, "decided only") {
		t.Fatalf("title %q items %+v", title, items)
	}
	if strings.Contains(plainOf(body), "espeak") {
		t.Fatal("a filtered-out idea shown")
	}
	_, body, items, _ = ideasPage(ideasFixture(), false, "charlie", ideasView{filter: 3}, 0, ideasT0, 100)
	if len(items) != 0 || !strings.Contains(plainOf(body), "no idea doing") {
		t.Fatalf("empty filter\n%s", plainOf(body))
	}
}

// s: the four states under the selected row, its own marked, the cursor on
// one; y confirms (the page says so).
func TestIdeasPagePicker(t *testing.T) {
	v := ideasView{pick: &ideaPick{id: "i2", from: bus.IdeaDecided, sel: 2}}
	_, body, items, _ := ideasPage(ideasFixture(), false, "charlie", v, 2, ideasT0, 100)
	text := plainOf(body)
	row := strings.Index(text, "busmon update")
	for _, st := range bus.IdeaStates {
		if i := strings.Index(text[row:], st); i < 0 {
			t.Fatalf("no %q under the row\n%s", st, text)
		}
	}
	if !strings.Contains(text, "(now)") || !strings.Contains(text, "y sets") || !strings.Contains(text, "esc") {
		t.Fatalf("picker\n%s", text)
	}
	// The cursor is the reversed or pill segment on "doing".
	found := false
	for _, l := range body {
		for _, s := range l {
			if strings.Contains(s.Text, bus.IdeaDoing) && s.Pill {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("the cursor is not on doing\n%s", text)
	}
	if len(items) != 4 {
		t.Fatalf("the picker's rows are not items: %d", len(items))
	}
}

func TestIdeasPageUnread(t *testing.T) {
	title, body, _, _ := ideasPage(nil, true, "charlie", ideasView{}, 0, ideasT0, 100)
	if title != "Ideas" || !strings.Contains(plainOf(body), "could not be read") {
		t.Fatalf("%q\n%s", title, plainOf(body))
	}
	_, body, _, _ = ideasPage(nil, false, "charlie", ideasView{}, 0, ideasT0, 100)
	if !strings.Contains(plainOf(body), "no idea yet") || !strings.Contains(plainOf(body), "a adds one") {
		t.Fatalf("empty\n%s", plainOf(body))
	}
}

// An idea's page: its state, who added and set it, where it came from, its
// text and its changes.
func TestIdeaDetail(t *testing.T) {
	it := ideasFixture()[1]
	text := plainOf(ideaDetail(it, "charlie", ideasT0, 100))
	for _, want := range []string{"decided", "added by master", "memory", "one line", "then more", "to discuss → decided", "charlie"} {
		if !strings.Contains(text, want) {
			t.Fatalf("no %q in\n%s", want, text)
		}
	}
	text = plainOf(ideaDetail(ideasFixture()[2], "charlie", ideasT0, 100))
	if !strings.Contains(text, "not you") {
		t.Fatalf("another setter not flagged\n%s", text)
	}
}

// The filter cycles all → each state → all.
func TestIdeasNextFilter(t *testing.T) {
	v := ideasView{}
	var seen []string
	for i := 0; i < 6; i++ {
		seen = append(seen, ideaFilterName(v.filter))
		v.filter = nextIdeaFilter(v.filter)
	}
	if strings.Join(seen, ",") != "all,to discuss,decided,doing,dropped,all" {
		t.Fatalf("filters %v", seen)
	}
}
