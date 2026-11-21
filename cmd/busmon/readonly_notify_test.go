package main

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/config"
)

// foureyes' point 1 on project-switch-ux, master's yes: read-only covers the
// notification set-up (notify.toml, config.toml's notify.*) and the
// palette's follow, which write without settingsCmd.

func readOnlyOn(t *testing.T, m *Model) {
	t.Helper()
	m.cfg.from, m.snap.dispatch = "trading", "down"
	m.refresh()
	if !m.readOnly() {
		t.Fatal("not read-only")
	}
}

func TestReadOnlyNotifySetupWritesNothing(t *testing.T) {
	w := newSetupWorld(t, "# mine\n")
	readOnlyOn(t, &w.m)
	s := w.open(t, 0)
	before, _ := os.ReadFile(w.notifyPath())
	w.key(t, kEnter, kEnter, kEnter)
	if after, _ := os.ReadFile(w.notifyPath()); string(after) != string(before) || !strings.Contains(w.m.ui.flash, readOnlyLine) {
		t.Fatalf("the write step while read-only: flash %q\n%s", w.m.ui.flash, after)
	}
	if s.busy {
		t.Fatal("a refused write left the page busy")
	}
	if on, targets := w.targets(); on || len(targets) != 0 {
		t.Fatalf("config written: on %v targets %q", on, targets)
	}
}

func TestReadOnlyNotifyTargetRemoveAndRename(t *testing.T) {
	w := newSetupWorld(t, "[ntfy.phone]\ntopic = \""+goldenTopic+"\"\n")
	if err := config.Set(config.Path(w.getenv), "notify.targets", []string{"phone"}); err != nil {
		t.Fatal(err)
	}
	w.key(t, 'C')
	w.m = showGroup(t, w.m, config.GroupNotifications)
	w.m.rd.top().set.keys = true
	readOnlyOn(t, &w.m)
	w.open(t, len(notifyAssistKinds))
	w.key(t, 'n')
	for range "phone" {
		w.key(t, tea.Key{Code: tea.KeyBackspace})
	}
	w.typeText(t, "mobile")
	w.key(t, kEnter)
	if _, ok := w.file(t).Target("phone"); !ok || !strings.Contains(w.m.ui.flash, readOnlyLine) {
		t.Fatalf("a rename while read-only: flash %q\n%s", w.m.ui.flash, w.body())
	}
	w.m.ui.flash = ""
	w.key(t, 'x', 'y')
	if _, ok := w.file(t).Target("phone"); !ok || !strings.Contains(w.m.ui.flash, readOnlyLine) {
		t.Fatalf("x y while read-only: flash %q", w.m.ui.flash)
	}
	if s := w.m.rd.top().ns; s == nil || s.busy {
		t.Fatal("a refused removal left the page busy")
	}
	w.key(t, kEsc) // the page still answers
	if p := w.m.rd.top(); p.kind == ovNotifySetup {
		t.Fatal("Esc swallowed after a refused removal")
	}
}

func TestReadOnlyPaletteFollow(t *testing.T) {
	w := startWorld(t, nil, "down")
	if cmd := w.m.palFollow(nil); cmd != nil || w.m.ui.flash != readOnlyLine {
		t.Fatalf("follow while read-only: cmd %v flash %q", cmd != nil, w.m.ui.flash)
	}
}
