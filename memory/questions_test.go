package memory

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeNote(t *testing.T, root, name, status, title string) {
	t.Helper()
	dir := filepath.Join(root, "docs", "memory", "open-questions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nid: " + name + "\nkind: question\nstatus: " + status + "\n---\n# " + title + "\n\n## Questions\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// IDEAS (requests-plan-redesign): the open questions still undecided, read
// from the notes; a resolved or superseded one is no idea any more.
func TestOpenQuestionsListsTheUndecidedNotes(t *testing.T) {
	root := t.TempDir()
	writeNote(t, root, "2026-11-03-deepseek-ideas", "active", "Three ideas from DeepSeek")
	writeNote(t, root, "2026-10-01-old", "resolved", "An answered one")
	writeNote(t, root, "2026-10-02-gone", "superseded", "A replaced one")
	writeNote(t, root, "2026-11-05-draft", "proposed", "A proposed one")
	if err := os.WriteFile(filepath.Join(root, "docs", "memory", "open-questions", "README.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := OpenQuestions(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Question{
		{ID: "2026-11-03-deepseek-ideas", Title: "Three ideas from DeepSeek", Path: "docs/memory/open-questions/2026-11-03-deepseek-ideas.md"},
		{ID: "2026-11-05-draft", Title: "A proposed one", Path: "docs/memory/open-questions/2026-11-05-draft.md"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestOpenQuestionsNoDirectoryIsNone(t *testing.T) {
	got, err := OpenQuestions(t.TempDir())
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want none, nil", got, err)
	}
}

// A note without a title line keeps its id as its title.
func TestOpenQuestionsUntitledKeepsTheID(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "memory", "open-questions")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "x.md"), []byte("---\nid: x\nstatus: active\n---\nno heading\n"), 0o644)
	got, _ := OpenQuestions(root)
	if len(got) != 1 || got[0].Title != "x" {
		t.Fatalf("got %+v", got)
	}
}
