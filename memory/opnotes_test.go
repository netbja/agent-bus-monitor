package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeNotes(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "operator-notes.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// The notes are the header, then the migrated fragments in their numbered
// order, then each change's own fragment by name (the person: one fragment
// per change, no PR edits the shared file; operator-notes-fragments).
func TestAssembleNotes(t *testing.T) {
	root := writeNotes(t, map[string]string{
		"_header.md":         "# Operator notes\n\n### Things that bite\n\n",
		"0020-second.md":     "- **Second.** two\n  lines\n",
		"0010-first.md":      "- **First.** one\n",
		"merge-queue-fix.md": "- **A change's own.** later\n",
		"attach-notes.md":    "- **Another change.** by name\n",
	})
	got, err := AssembleNotes(root)
	if err != nil {
		t.Fatal(err)
	}
	want := "# Operator notes\n\n### Things that bite\n\n- **First.** one\n- **Second.** two\n  lines\n" +
		"- **Another change.** by name\n- **A change's own.** later\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	changed, err := WriteNotes(root)
	if err != nil || !changed {
		t.Fatalf("first write: %v %v", changed, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "docs", "operator-notes.md")); string(b) != want {
		t.Fatalf("written %q", b)
	}
	if changed, err := WriteNotes(root); err != nil || changed {
		t.Fatalf("second write changed %v %v", changed, err)
	}
}

// A malformed fragment is refused, never assembled into the notes: no
// header, a fragment that is not one bullet, a name that is not a task's.
func TestAssembleNotesRefuses(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"no header":      {"0010-a.md": "- **A.**\n"},
		"not a bullet":   {"_header.md": "# N\n\n", "task-a.md": "text\n"},
		"two bullets":    {"_header.md": "# N\n\n", "task-a.md": "- **A.**\n- **B.**\n"},
		"no newline":     {"_header.md": "# N\n\n", "task-a.md": "- **A.**"},
		"a heading":      {"_header.md": "# N\n\n", "task-a.md": "- **A.**\n## B\n"},
		"bad name":       {"_header.md": "# N\n\n", "Task A.md": "- **A.**\n"},
		"not markdown":   {"_header.md": "# N\n\n", "task-a.txt": "- **A.**\n"},
		"empty fragment": {"_header.md": "# N\n\n", "task-a.md": ""},
	} {
		if _, err := AssembleNotes(writeNotes(t, files)); err == nil {
			t.Errorf("%s: assembled", name)
		}
	}
}

// This repository's fragments assemble: well formed, the header first.
// (The migration lost no line: assembled right after the split, the file was
// byte for byte the same; that proof is the PR's, not a test that a later
// fragment's edit would break until the next release.)
func TestRepositoryNotesAssemble(t *testing.T) {
	got, err := AssembleNotes("..")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "# Operator notes\n") {
		t.Fatalf("no header: %.80q", got)
	}
	// No PR edits the shared file: it is what the last mem notes wrote.
	if err := NotesCurrent(".."); err != nil {
		t.Fatal(err)
	}
}

// The file is never edited by hand: mem notes keeps the hash of what it
// wrote, and refuses to overwrite a file that no longer has it, so an edit
// made there is never lost silently (foureyes); the hash file is no
// fragment.
func TestWriteNotesRefusesAHandEdit(t *testing.T) {
	root := writeNotes(t, map[string]string{"_header.md": "# Operator notes\n\n", "0010-a.md": "- **A.** one\n"})
	if _, err := WriteNotes(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, NotesFile)
	if err := NotesCurrent(root); err != nil {
		t.Fatalf("just written: %v", err)
	}
	os.WriteFile(path, []byte("# Operator notes\n\n- **A.** one\n- **Hand.** edited\n"), 0o644)
	if err := NotesCurrent(root); err == nil || !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("a hand edit: %v", err)
	}
	if _, err := WriteNotes(root); err == nil {
		t.Fatal("a hand edit was overwritten")
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "Hand") {
		t.Fatal("the hand edit is gone")
	}
	// Moved into a fragment and the file put back, it assembles again.
	os.WriteFile(filepath.Join(root, NotesDir, "hand-edit.md"), []byte("- **Hand.** edited\n"), 0o644)
	os.WriteFile(path, []byte("# Operator notes\n\n- **A.** one\n"), 0o644)
	if changed, err := WriteNotes(root); err != nil || !changed {
		t.Fatalf("after the move: %v %v", changed, err)
	}
	// No file yet: written (a first assembly).
	os.Remove(path)
	if _, err := WriteNotes(root); err != nil {
		t.Fatalf("no file: %v", err)
	}
}
