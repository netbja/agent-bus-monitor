package main

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// ideas-triage part 2 (the person's feedback, master's yes to y on the
// Ideas pages): y decided, l later, d doing, x dropped set the selected
// idea's state at once, as the person, on the list and on an idea's page;
// u undoes this busmon's last change; s keeps its picker; Y copies the
// whole idea, since y is decided there. Other pages keep y = copy.

func TestIdeasDirectKeys(t *testing.T) {
	for _, c := range []struct {
		key  rune
		want string
	}{{'y', bus.IdeaDecided}, {'l', bus.IdeaLater}, {'d', bus.IdeaDoing}, {'x', bus.IdeaDropped}} {
		m, w := ideasShell(t, "charlie")
		m = keys(t, m, kF6)
		m = press1(t, m, c.key)
		if !slices.Contains(w.sent, "idea set i1 to discuss -> "+c.want+" charlie") {
			t.Fatalf("%c: sent %v", c.key, w.sent)
		}
		if m.snap.ideas[0].State != c.want || !strings.Contains(m.ui.flash, "to discuss → "+c.want) {
			t.Fatalf("%c: shown %q, flash %q", c.key, m.snap.ideas[0].State, m.ui.flash)
		}
	}
}

// On an idea's page too; the same state again writes nothing.
func TestIdeaPageDirectKeys(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	m = keys(t, m, kF6, kDown, kEnter)
	if p := m.rd.top(); p.kind != ovIdea || p.key != "i4" {
		t.Fatalf("page %+v", p)
	}
	m = press1(t, m, 'l')
	if !slices.Contains(w.sent, "idea set i4 to discuss -> later charlie") {
		t.Fatalf("sent %v", w.sent)
	}
	n := len(w.sent)
	m = press1(t, m, 'l')
	if len(w.sent) != n || !strings.Contains(m.ui.flash, "already later") {
		t.Fatalf("same state: sent %v flash %q", w.sent, m.ui.flash)
	}
}

// u puts back this busmon's last change, as a compare-and-set from the
// state it set; a second u has nothing left to undo.
func TestIdeasUndo(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	m = keys(t, m, kF6)
	m = press1(t, m, 'x')
	m = press1(t, m, 'u')
	if !slices.Contains(w.sent, "idea set i1 dropped -> to discuss charlie") || m.snap.ideas[0].State != bus.IdeaToDiscuss {
		t.Fatalf("undo: sent %v, state %q", w.sent, m.snap.ideas[0].State)
	}
	n := len(w.sent)
	m = press1(t, m, 'u')
	if len(w.sent) != n || !strings.Contains(m.ui.flash, "nothing to undo") {
		t.Fatalf("second u: sent %v flash %q", w.sent, m.ui.flash)
	}
}

// Only the person: in an agent's environment, no key writes.
func TestIdeasDirectKeysRefusedInAnAgentsEnv(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	m.cfg.getenv = func(k string) string {
		if k == "AGENT_BUS_AGENT" {
			return "coder"
		}
		return ""
	}
	m = keys(t, m, kF6)
	m = press1(t, m, 'y')
	if len(w.sent) != 0 || !m.ui.flashErr {
		t.Fatalf("an agent's busmon set a state: %v", w.sent)
	}
}

// Y copies the whole idea on its page and on the list; the frame says so,
// and no longer offers y as copy there.
func TestIdeasCopyIsY(t *testing.T) {
	m, _ := ideasShell(t, "charlie")
	m = keys(t, m, kF6, kDown, kDown)
	p := m.rd.top()
	if p.threadPlain != "busmon update without setup.sh\n\none line\nthen more" || p.threadWhat != "idea" || p.plain != "" {
		t.Fatalf("list: plain %q thread %q (%s)", p.plain, p.threadPlain, p.threadWhat)
	}
	m = keys(t, m, kEnter)
	p = m.rd.top()
	if p.kind != ovIdea || !strings.HasPrefix(p.threadPlain, "busmon update without setup.sh") || p.plain != "" {
		t.Fatalf("page: %+v", p)
	}
	frame := m.grid().plain()
	if !strings.Contains(frame, "Y copy idea") || strings.Contains(frame, "y copy") {
		t.Fatalf("frame:\n%s", frame)
	}
}

// Elsewhere y is still copy and l still the Services log's: the Ideas keys
// are offered on the Ideas pages only.
func TestIdeasDirectKeysStayOnTheirPages(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	m = keys(t, m, tea.Key{Code: tea.KeyF5})
	for _, r := range "yldxu" {
		m = press1(t, m, r)
	}
	if len(w.sent) != 0 {
		t.Fatalf("an Ideas key wrote from Health: %v", w.sent)
	}
	if got := lookupOn(ModeOverlay, PanelAgents, TierWide, Key{Code: keyRune, Rune: 'y'}, func(a Action) bool { return false }); got != ActCopy {
		t.Fatalf("y off the Ideas pages is %v, want copy", got)
	}
}
