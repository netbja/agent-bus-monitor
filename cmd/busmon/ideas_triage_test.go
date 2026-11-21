package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// ideas-triage, the person's feedback on F6: a Later section, each row
// saying its state (not only its section), and an idea's text wrapped to
// the frame instead of cut on the right.

func triageFixture() []bus.Idea {
	ideas := ideasFixture()
	at := ideasT0.Add(-3 * time.Hour).UnixMilli()
	return append(ideas,
		bus.Idea{ID: "i5", IdeaDef: bus.IdeaDef{Title: "a later one", By: "coder", At: at}, State: bus.IdeaLater},
		bus.Idea{ID: "i6", IdeaDef: bus.IdeaDef{Title: "being built", By: "coder", At: at}, State: bus.IdeaDoing})
}

// The sections in the order the person asked: To discuss, Decided, Doing,
// Later, Dropped.
func TestIdeasPageLaterSection(t *testing.T) {
	title, body, _, _ := ideasPage(triageFixture(), false, "charlie", ideasView{}, 0, ideasT0, 100)
	text := plainOf(body)
	last := -1
	for _, s := range []string{"To discuss", "Decided", "Doing", "Later", "Dropped"} {
		i := strings.Index(text, s)
		if i <= last {
			t.Fatalf("section %q missing or out of order\n%s", s, text)
		}
		last = i
	}
	if !strings.Contains(title, "1 later") {
		t.Fatalf("title %q", title)
	}
}

// Every row carries its state as a word in the state's role, at every width.
func TestIdeasPageRowSaysItsState(t *testing.T) {
	for _, width := range []int{60, 100, 160} {
		_, body, items, _ := ideasPage(triageFixture(), false, "charlie", ideasView{}, 0, ideasT0, width)
		state := map[string]string{}
		for _, it := range triageFixture() {
			state[it.ID] = it.State
		}
		for _, it := range items {
			found := false
			for _, s := range body[it.line] {
				if strings.TrimSpace(s.Text) == state[it.id] && s.Role == ideaStateRole(state[it.id]) {
					found = true
				}
			}
			if !found {
				t.Errorf("%d: %s's row does not say %q: %q", width, it.id, state[it.id], plainOf([]Line{body[it.line]}))
			}
		}
	}
	// Each state its own role, so the colour tells them apart too.
	seen := map[Role]string{}
	for _, st := range bus.IdeaStates {
		if o, dup := seen[ideaStateRole(st)]; dup {
			t.Errorf("%s and %s share a role", o, st)
		}
		seen[ideaStateRole(st)] = st
	}
}

// An idea's text wraps to the frame: no line wider than it, no word lost.
func TestIdeaDetailWrapsItsText(t *testing.T) {
	it := ideasFixture()[1]
	it.Text = strings.Repeat("a long description of the idea that runs on ", 6) + "END"
	for _, width := range []int{40, 80} {
		lines := ideaDetail(it, "charlie", ideasT0, width)
		for _, l := range lines {
			if l.Width() > width {
				t.Fatalf("%d: a line is %d cells: %q", width, l.Width(), l.text())
			}
		}
		text := strings.Join(strings.Fields(plainOf(lines)), " ")
		if !strings.Contains(text, strings.TrimSpace(strings.Repeat("a long description of the idea that runs on ", 6))+" END") {
			t.Fatalf("%d: the text is cut:\n%s", width, plainOf(lines))
		}
	}
}
