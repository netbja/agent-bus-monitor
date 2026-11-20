package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/netbja/agent-bus-monitor/memory"
	"github.com/netbja/agent-bus-monitor/plan"
)

// views-redesign §7 step 1: IDEAS reads the idea notes of the repository
// (docs/memory/open-questions, memory.OpenQuestions) beside the broker's
// ideas. A note shows once: not when a plan task covers it, nor when a
// broker idea already stands for it (same id, same title, or its Source).
func TestWorkIdeasReadTheNotes(t *testing.T) {
	in := workFixture(shellNow)
	in.notes = []memory.Question{
		{ID: "deepseek", Title: "Three ideas taken from DeepSeek's Super-Agent prototype", Path: "docs/memory/open-questions/a.md"}, // i1's title
		{ID: "lang", Title: "A language setting", Path: "docs/memory/open-questions/2026-11-18-lang.md"},
		{ID: "notif", Title: "Notifications order", Path: "docs/memory/open-questions/n.md"}, // t5 covers it
		{ID: "i1", Title: "another title, i1's id", Path: "docs/memory/open-questions/i1.md"},
	}
	w := workOf(in)
	if len(w.Ideas) != 2 || w.Ideas[0].ID != "i1" || w.Ideas[0].Path != "" {
		t.Fatalf("IDEAS = %+v", w.Ideas)
	}
	n := w.Ideas[1]
	if n.ID != "lang" || n.Title != "A language setting" || n.Path != "docs/memory/open-questions/2026-11-18-lang.md" || n.key() != workNotePrefix+"lang" {
		t.Fatalf("the note's row: %+v key %q", n, n.key())
	}
	if k := w.Ideas[0].key(); k != workIdeaPrefix+"i1" {
		t.Fatalf("a broker idea's key: %q", k)
	}
	in.ideas[0].Source = "docs/memory/open-questions/2026-11-18-lang.md" // i1 now stands for the note
	if w := workOf(in); len(w.Ideas) != 1 || w.Ideas[0].ID != "i1" {
		t.Fatalf("a broker idea whose source is the note: %+v", w.Ideas)
	}
	in = workFixture(shellNow)
	in.notes = []memory.Question{{ID: "lang", Title: "A language setting", Path: "p.md"}}
	in.plan.rows = append(in.plan.rows, plan.Row{Task: plan.Task{ID: "language-setting", Title: "x", Owner: "coder", Board: "lang"}, State: plan.Upcoming})
	if w := workOf(in); len(w.Ideas) != 1 {
		t.Fatalf("a task whose board names the note covers it: %+v", w.Ideas)
	}
}

// Notes that cannot be read are said, never "none"; the broker's ideas stay.
func TestWorkNotesErrorSaid(t *testing.T) {
	in := workFixture(shellNow)
	in.notesErr = true
	w := workOf(in)
	if !w.NotesErr || w.IdeasErr || len(w.Ideas) != 1 {
		t.Fatalf("%+v", w)
	}
}

// readIdeas reads the notes too; a failed read keeps the last notes and
// flags them, a good one clears the flag. No reader (no checkout on this
// machine): nothing read, no flag.
func TestReadIdeasReadsTheNotes(t *testing.T) {
	q := []memory.Question{{ID: "lang", Title: "A", Path: "p.md"}}
	var err error
	st := &shared{notesOf: func() ([]memory.Question, error) { return q, err }}
	readIdeas(st)
	if len(st.notes) != 1 || st.notesErr {
		t.Fatalf("first read: %+v %v", st.notes, st.notesErr)
	}
	err = errors.New("unreadable")
	readIdeas(st)
	if len(st.notes) != 1 || !st.notesErr {
		t.Fatalf("failed read: %+v %v", st.notes, st.notesErr)
	}
	q, err = nil, nil
	readIdeas(st)
	if len(st.notes) != 0 || st.notesErr {
		t.Fatalf("good read: %+v %v", st.notes, st.notesErr)
	}
	st = &shared{}
	readIdeas(st)
	if st.notes != nil || st.notesErr {
		t.Fatalf("no reader: %+v", st)
	}
}

// The notes are read in the project's checkout on this machine, the
// template's working_dir (as the @ popup finds it); a project with no
// checkout here has no reader.
func TestLocalNotesOfReadsTheCheckout(t *testing.T) {
	dir := t.TempDir()
	root, projects := filepath.Join(dir, "repo"), filepath.Join(dir, "projects")
	notes := filepath.Join(root, "docs", "memory", "open-questions")
	for _, d := range []string{notes, projects} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(projects, "demo.toml"), []byte("working_dir = \""+root+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	note := "---\nid: lang\nstatus: proposed\n---\n\n# A language setting\n"
	if err := os.WriteFile(filepath.Join(notes, "2026-11-18-lang.md"), []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"AGENTBUS_PROJECTS_DIR": projects}
	getenv := func(k string) string { return env[k] }
	read := localNotesOf("demo", getenv)
	if read == nil {
		t.Fatal("a project with a checkout has no reader")
	}
	got, err := read()
	if err != nil || len(got) != 1 || got[0].ID != "lang" || got[0].Title != "A language setting" {
		t.Fatalf("notes %+v %v", got, err)
	}
	if localNotesOf("other", getenv) != nil {
		t.Fatal("a project with no template has a reader")
	}
	if projectCheckout("demo", getenv) != root {
		t.Fatalf("checkout %q", projectCheckout("demo", getenv))
	}
	// working_dir as agentbus up expands it: ~ then $VARS (foureyes).
	env["HOME"] = dir
	if err := os.WriteFile(filepath.Join(projects, "demo.toml"), []byte("working_dir = \"~/repo\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := projectCheckout("demo", getenv); got != root {
		t.Fatalf("~ checkout %q, want %q", got, root)
	}
	if got, err := localNotesOf("demo", getenv)(); err != nil || len(got) != 1 {
		t.Fatalf("~ notes %+v %v", got, err)
	}
	env["REPOS"] = dir
	if err := os.WriteFile(filepath.Join(projects, "demo.toml"), []byte("working_dir = \"$REPOS/repo\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := projectCheckout("demo", getenv); got != root {
		t.Fatalf("$VARS checkout %q, want %q", got, root)
	}
}

// On Work, a note's row draws its title and where it lives; enter says
// where (its triage panel is step 5), y copies its title. The Kanban card
// says note.
func TestWorkTabNoteRow(t *testing.T) {
	pinLocal(t)
	st := workShared()
	st.notes = []memory.Question{{ID: "lang", Title: "A language setting", Path: "docs/memory/open-questions/2026-11-18-lang.md"}}
	m := keys(t, workShellOf(t, st, 140, 40), '3')
	key := workNotePrefix + "lang"
	m.lists.work.Select(key)
	m.ui.workLevel = workAtCards
	if m.lists.work.Selected() != key {
		t.Fatalf("cannot select %q", key)
	}
	if text, what := m.workCopy(key); what != "title" || text != "A language setting" {
		t.Fatalf("y: %q %q", text, what)
	}
	r := keys(t, m, kEnter)
	if r.rd.open() || r.ui.flash != "idea note: docs/memory/open-questions/2026-11-18-lang.md" {
		t.Fatalf("enter: open %v flash %q", r.rd.open(), r.ui.flash)
	}
	if h, ok := rowHint(PanelWork, ListItem{Key: key}); !ok || h != "where" {
		t.Fatalf("hint %q %v", h, ok)
	}
}
