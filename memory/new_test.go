package memory_test

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/memory"
	"github.com/netbja/agent-bus-monitor/memory/memtest"
)

// noteTree is WriteTree plus one note naming the project "test".
func noteTree(t *testing.T) string {
	root := memtest.WriteTree(t)
	addNote(t, root, "lessons/2026-10-01-seed.md", "Seed", "lesson", "active", "2026-10-01", "2026-10-01", "verified")
	return root
}

func newOpts(root string) memory.NewOptions {
	return memory.NewOptions{Root: root, Kind: "lesson", Slug: "pel-floor", Title: "PEL floor", Author: "codex", Today: today}
}

func TestNewNoteGolden(t *testing.T) {
	root := noteTree(t)
	rel, err := memory.NewNote(newOpts(root))
	if err != nil || rel != "docs/memory/lessons/2026-10-11-pel-floor.md" {
		t.Fatalf("NewNote = %q, %v", rel, err)
	}
	golden, _ := os.ReadFile("testdata/new-lesson.golden")
	if got := read(t, root, rel); got != string(golden) {
		t.Fatalf("note:\n%s\nwant:\n%s", got, golden)
	}
	index := read(t, root, "docs/memory/INDEX.md")
	lessons := index[strings.Index(index, "## Lessons"):]
	lessons = lessons[:strings.Index(lessons, "## Open questions")]
	if !strings.HasSuffix(strings.TrimSpace(lessons), "- [PEL floor](lessons/2026-10-11-pel-floor.md)") {
		t.Fatalf("Lessons section:\n%s", lessons)
	}
	if !strings.Contains(read(t, root, "AGENTS.md"), "(docs/memory/lessons/2026-10-11-pel-floor.md)") {
		t.Fatal("AGENTS.md block not synced")
	}
}

func TestNewNoteSections(t *testing.T) {
	root := noteTree(t)
	for _, c := range []struct{ kind, slug, heading, dir string }{
		{"decision", "d", "## Decisions", "decisions/"}, {"question", "q", "## Open questions", "open-questions/"},
	} {
		o := newOpts(root)
		o.Kind, o.Slug, o.Title = c.kind, c.slug, "T "+c.slug
		if _, err := memory.NewNote(o); err != nil {
			t.Fatal(err)
		}
		index := read(t, root, "docs/memory/INDEX.md")
		line := "- [T " + c.slug + "](" + c.dir + "2026-10-11-" + c.slug + ".md)"
		at, h := strings.Index(index, line), strings.Index(index, c.heading)
		next := strings.Index(index[h+len(c.heading):], "\n## ")
		if at < h || (next >= 0 && at > h+len(c.heading)+next) {
			t.Fatalf("%s line outside %s:\n%s", c.kind, c.heading, index)
		}
	}
}

func treeHash(t *testing.T, root string) string {
	h := sha256.New()
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			fmt.Fprintf(h, "%s\x00%x\x00", p, sha256.Sum256(b))
		}
		return nil
	})
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestNewNoteRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
		edit  func(o *memory.NewOptions)
		fresh bool // start from WriteTree, with no note naming a project
	}{
		{name: "journal kind", edit: func(o *memory.NewOptions) { o.Kind = "journal" }},
		{name: "upper slug", edit: func(o *memory.NewOptions) { o.Slug = "Bad_Slug" }},
		{name: "empty slug", edit: func(o *memory.NewOptions) { o.Slug = "" }},
		{name: "long slug", edit: func(o *memory.NewOptions) { o.Slug = strings.Repeat("a", 49) }},
		{name: "empty title", edit: func(o *memory.NewOptions) { o.Title = "  " }},
		{name: "multiline title", edit: func(o *memory.NewOptions) { o.Title = "a\nb" }},
		{name: "bracket title", edit: func(o *memory.NewOptions) { o.Title = "a ] b" }},
		{name: "open bracket title", edit: func(o *memory.NewOptions) { o.Title = "[x" }},
		{name: "no author", edit: func(o *memory.NewOptions) { o.Author = "" }},
		{name: "bad author", edit: func(o *memory.NewOptions) { o.Author = "Bad Name" }},
		{name: "bad scope", edit: func(o *memory.NewOptions) { o.Scope = "global" }},
		{name: "bad note project", edit: func(o *memory.NewOptions) { o.NoteProject = "Bad Name" }},
		{name: "existing file", setup: func(t *testing.T, root string) {
			memtest.Write(t, root, "docs/memory/lessons/2026-10-11-pel-floor.md", "mine\n")
		}},
		{name: "no section", setup: func(t *testing.T, root string) {
			memtest.Write(t, root, "docs/memory/INDEX.md", "[Protocol](PROTOCOL.md)\n[Journal](journal/INDEX.md)\n[Seed](lessons/2026-10-01-seed.md)\n")
		}},
		{name: "no index", setup: func(t *testing.T, root string) { os.Remove(filepath.Join(root, "docs/memory/INDEX.md")) }},
		{name: "projects disagree", setup: func(t *testing.T, root string) {
			memtest.Write(t, root, "docs/memory/lessons/other.md", strings.Replace(read(t, root, "docs/memory/lessons/2026-10-01-seed.md"), "project: test", "project: other", 1))
		}},
		{name: "no project known", fresh: true},
		{name: "no markers in AGENTS.md", setup: func(t *testing.T, root string) { memtest.Write(t, root, "AGENTS.md", "Read docs/memory/INDEX.md\n") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := ""
			if c.fresh {
				root = memtest.WriteTree(t)
			} else {
				root = noteTree(t)
			}
			if c.setup != nil {
				c.setup(t, root)
			}
			o := newOpts(root)
			if c.edit != nil {
				c.edit(&o)
			}
			before := treeHash(t, root)
			if _, err := memory.NewNote(o); err == nil {
				t.Fatal("accepted")
			}
			if treeHash(t, root) != before {
				t.Fatal("a refused note changed the tree")
			}
		})
	}
}

func TestNewNoteProjectOverride(t *testing.T) {
	root := memtest.WriteTree(t) // no note names a project
	o := newOpts(root)
	o.NoteProject, o.Scope = "agent-bus-monitor", "transferable"
	rel, err := memory.NewNote(o)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, root, rel); !strings.Contains(got, "\nproject: agent-bus-monitor\nscope: transferable\n") {
		t.Fatalf("frontmatter:\n%s", got)
	}
}

func TestFreshStubFailsCheck(t *testing.T) {
	root := noteTree(t)
	rel, err := memory.NewNote(newOpts(root))
	if err != nil {
		t.Fatal(err)
	}
	hasErr(t, check(root), rel+": template not filled")
	hasErr(t, check(root), rel+": no linked evidence/context")
	filled := strings.ReplaceAll(read(t, root, rel), memory.FillMarker+" ", "")
	filled = strings.Replace(filled, "## Limits", "[x](../PROTOCOL.md)\n\n## Limits", 1)
	memtest.Write(t, root, rel, filled)
	for _, e := range check(root).Errors {
		if strings.HasPrefix(e, rel) {
			t.Fatalf("filled note still fails: %s", e)
		}
	}
}

// Codex review: a dangling symlink at the note's path must not be followed.
func TestNewNoteRefusesDanglingSymlink(t *testing.T) {
	root := noteTree(t)
	outside := filepath.Join(t.TempDir(), "created-outside.md")
	if err := os.Symlink(outside, filepath.Join(root, "docs/memory/lessons/2026-10-11-pel-floor.md")); err != nil {
		t.Fatal(err)
	}
	before := treeHash(t, root)
	if _, err := memory.NewNote(newOpts(root)); err == nil {
		t.Fatal("accepted")
	}
	if _, err := os.Lstat(outside); err == nil {
		t.Fatal("wrote through the symlink, outside the repository")
	}
	if treeHash(t, root) != before {
		t.Fatal("tree changed")
	}
}

// Codex review: a note whose index line would break the budget is refused
// before anything is written.
func TestNewNoteRefusesIndexOverBudget(t *testing.T) {
	root := noteTree(t)
	memtest.Write(t, root, "docs/memory/INDEX.md", "[Protocol](PROTOCOL.md)\n[Journal](journal/INDEX.md)\n\n## Lessons\n\n- [Seed](lessons/2026-10-01-seed.md)\n"+strings.Repeat("x\n", 54))
	sync(t, root)
	before := treeHash(t, root)
	if _, err := memory.NewNote(newOpts(root)); err == nil || !strings.Contains(err.Error(), "60 lines") {
		t.Fatalf("err = %v", err)
	}
	if treeHash(t, root) != before {
		t.Fatal("tree changed")
	}
}
