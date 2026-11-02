package main

import (
	"strings"
	"testing"
)

// sessionHerdr answers like herdr for one session's first workspace.
type sessionHerdr struct {
	calls                   []string
	workspaces, tabs, panes string
	foreground              string // process-info's foreground argv JSON
}

func (s *sessionHerdr) run(args ...string) ([]byte, error) {
	s.calls = append(s.calls, strings.Join(args, " "))
	switch strings.Join(args[:2], " ") {
	case "workspace list":
		return []byte(`{"result":{"workspaces":[` + s.workspaces + `]}}`), nil
	case "tab list":
		return []byte(`{"result":{"tabs":[` + s.tabs + `]}}`), nil
	case "pane list":
		return []byte(`{"result":{"panes":[` + s.panes + `]}}`), nil
	case "pane process-info":
		return []byte(`{"result":{"process_info":{"foreground_processes":[` + s.foreground + `]}}}`), nil
	}
	return []byte(`{"result":{}}`), nil
}

func firstWorkspace() *sessionHerdr {
	return &sessionHerdr{
		workspaces: `{"label":"shop","workspace_id":"w1","tab_count":1,"pane_count":1}`,
		tabs:       `{"tab_id":"w1:t1","workspace_id":"w1","pane_count":1}`,
		panes:      `{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1"}`,
		foreground: `{"argv":["-bash"]}`,
	}
}

// The first workspace of a session start created is its only workspace, with
// one tab and one pane; anything else is not taken for it.
func TestInitialWorkspace(t *testing.T) {
	if ws, err := initialWorkspace(firstWorkspace().run); err != nil || ws != "w1" {
		t.Fatalf("first workspace = %q %v", ws, err)
	}
	for name, edit := range map[string]func(*sessionHerdr){
		"two workspaces": func(s *sessionHerdr) {
			s.workspaces += `,{"label":"x","workspace_id":"w2","tab_count":1,"pane_count":1}`
		},
		"two tabs": func(s *sessionHerdr) {
			s.workspaces = `{"label":"shop","workspace_id":"w1","tab_count":2,"pane_count":2}`
		},
		"two panes": func(s *sessionHerdr) {
			s.workspaces = `{"label":"shop","workspace_id":"w1","tab_count":1,"pane_count":2}`
		},
		"none": func(s *sessionHerdr) { s.workspaces = "" },
	} {
		s := firstWorkspace()
		edit(s)
		if ws, _ := initialWorkspace(s.run); ws != "" {
			t.Errorf("%s: taken for the first workspace: %q", name, ws)
		}
	}
}

// Closed only while it is still untouched: one tab, one pane, a plain shell
// in it, read again right before; never typed into. Anything else is kept
// and said why.
func TestCloseInitialWorkspace(t *testing.T) {
	s := firstWorkspace()
	if note, err := closeInitialWorkspace(s.run, "w1"); err != nil || !strings.Contains(strings.Join(s.calls, "\n"), "workspace close w1") || !strings.Contains(note, "closed") {
		t.Fatalf("untouched: %q %v\n%s", note, err, strings.Join(s.calls, "\n"))
	}
	for name, edit := range map[string]func(*sessionHerdr){
		"a command running": func(s *sessionHerdr) { s.foreground = `{"argv":["vim","notes"]}` },
		"a second tab": func(s *sessionHerdr) {
			s.workspaces = `{"label":"shop","workspace_id":"w1","tab_count":2,"pane_count":2}`
		},
		"a split pane": func(s *sessionHerdr) {
			s.workspaces = `{"label":"shop","workspace_id":"w1","tab_count":1,"pane_count":2}`
		},
		"already gone": func(s *sessionHerdr) {
			s.workspaces = `{"label":"shop","workspace_id":"w9","tab_count":3,"pane_count":3}`
		},
	} {
		s := firstWorkspace()
		edit(s)
		note, _ := closeInitialWorkspace(s.run, "w1")
		joined := strings.Join(s.calls, "\n")
		if strings.Contains(joined, "workspace close") || note == "" {
			t.Errorf("%s: closed or not said (%q):\n%s", name, note, joined)
		}
		for _, c := range s.calls {
			if strings.HasPrefix(c, "pane run") || strings.HasPrefix(c, "pane send") {
				t.Errorf("%s: typed into the pane: %s", name, c)
			}
		}
	}
}
