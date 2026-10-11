package memory_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/memory"
	"github.com/netbja/agent-bus-monitor/memory/memtest"
)

// The cases below are tests/test_memory_check.py, one Go test per Python test.

var today = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func check(root string) memory.Result { return memory.Check(memory.Options{Root: root, Today: today}) }

func hasErr(t *testing.T, r memory.Result, sub string) {
	t.Helper()
	for _, e := range r.Errors {
		if strings.Contains(e, sub) {
			return
		}
	}
	t.Fatalf("no error containing %q in %q", sub, r.Errors)
}

func TestCompleteStructureAndBoot(t *testing.T) {
	if r := check(memtest.WriteTree(t)); len(r.Errors) != 0 {
		t.Fatalf("errors: %q", r.Errors)
	}
}

func TestMissingEntryPointFails(t *testing.T) {
	root := memtest.WriteTree(t)
	os.Remove(filepath.Join(root, "MEMORY.md"))
	hasErr(t, check(root), "missing required entry point: MEMORY.md")
}

func TestMissingSkillReferenceFails(t *testing.T) {
	root := memtest.WriteTree(t)
	memtest.Append(t, root, "skills/peer/SKILL.md", "Read `docs/MISSING.md` before acting.\n")
	hasErr(t, check(root), "missing skill file reference")
}

func TestOptionalReferenceRequiresExplicitFallback(t *testing.T) {
	root := memtest.WriteTree(t)
	memtest.Append(t, root, "skills/peer/SKILL.md", "Read `docs/OPTIONAL.md`. If missing, report it and continue with the index.\n<!-- optional-file: docs/OPTIONAL.md | If missing, report it and continue with the index. -->\n")
	r := check(root)
	if len(r.Errors) != 0 || len(r.Optional) != 1 || !strings.Contains(r.Optional[0], "docs/OPTIONAL.md") {
		t.Fatalf("errors %q optional %q", r.Errors, r.Optional)
	}
}

func TestBootWithoutMemoryFails(t *testing.T) {
	root := memtest.WriteTree(t)
	memtest.Write(t, root, "roles/peer.md", "Start work.\n")
	hasErr(t, check(root), "boot path does not reference")
}

func TestMissingKnownRoleIsNotSilentlyIgnored(t *testing.T) {
	root := memtest.WriteTree(t)
	os.Remove(filepath.Join(root, "roles/sentinel.md"))
	hasErr(t, check(root), "boot path does not reference memory index: roles/sentinel.md")
}

func TestBrokenRelativeLinkFails(t *testing.T) {
	root := memtest.WriteTree(t)
	memtest.Write(t, root, "docs/memory/INDEX.md", "[Missing](missing.md)\n")
	hasErr(t, check(root), "broken local link")
}

func TestUnindexedNoteFails(t *testing.T) {
	root := memtest.WriteTree(t)
	memtest.Write(t, root, "docs/memory/forgotten.md", "A forgotten lesson.\n")
	hasErr(t, check(root), "unreachable")
}

func note(created, verified, confidence string) string {
	return "---\nid: n\nproject: test\nscope: project\nkind: lesson\nstatus: active\ncreated: " + created +
		"\nlast_verified: " + verified + "\nauthor: test\nconfidence: " + confidence + "\n---\n[Evidence](../PROTOCOL.md)\n"
}

func TestVerifiedNoteNeedsVerificationDate(t *testing.T) {
	root := memtest.WriteTree(t)
	memtest.Write(t, root, "docs/memory/lessons/lesson.md", note("2026-09-19", "unknown", "verified"))
	memtest.Append(t, root, "docs/memory/INDEX.md", "[Lesson](lessons/lesson.md)\n")
	hasErr(t, check(root), "invalid last_verified")
}

func TestOptionalCommentCannotHideBrokenMemoryLink(t *testing.T) {
	root := memtest.WriteTree(t)
	memtest.Append(t, root, "docs/memory/INDEX.md", "[Ghost](lessons/ghost.md)\n<!-- optional-file: lessons/ghost.md | x -->\n")
	hasErr(t, check(root), "broken local link: lessons/ghost.md")
}

func TestOptionalSkillCommentNeedsVisibleFallback(t *testing.T) {
	root := memtest.WriteTree(t)
	memtest.Append(t, root, "skills/peer/SKILL.md", "Read `docs/OPTIONAL.md`.\n<!-- optional-file: docs/OPTIONAL.md | If missing, report it and continue with the index. -->\n")
	hasErr(t, check(root), "visible nearby fallback")
}

func TestOptionalSkillCannotHideMissingMemoryNote(t *testing.T) {
	root := memtest.WriteTree(t)
	memtest.Append(t, root, "skills/peer/SKILL.md", "Read `docs/memory/ghost.md`. If missing, report it and continue with the index.\n<!-- optional-file: docs/memory/ghost.md | If missing, report it and continue with the index. -->\n")
	hasErr(t, check(root), "missing skill file reference: docs/memory/ghost.md")
}

func TestFutureAndInvertedDatesFail(t *testing.T) {
	for _, c := range []struct{ created, verified, want string }{
		{"2999-01-01", "2999-01-02", "in the future"},
		{"2026-01-02", "2026-01-01", "predates created"},
	} {
		t.Run(c.want, func(t *testing.T) {
			root := memtest.WriteTree(t)
			memtest.Write(t, root, "docs/memory/lessons/dates.md", note(c.created, c.verified, "verified"))
			hasErr(t, check(root), c.want)
		})
	}
}

func TestImageLinkIsNotALink(t *testing.T) {
	root := memtest.WriteTree(t)
	memtest.Append(t, root, "docs/memory/INDEX.md", "![diagram](missing.png)\n")
	if _, err := memory.Sync(root); err != nil {
		t.Fatal(err)
	}
	if r := check(root); len(r.Errors) != 0 {
		t.Fatalf("errors: %q", r.Errors)
	}
}

func TestFindRootInWorktree(t *testing.T) {
	root := t.TempDir()
	memtest.Write(t, root, ".git", "gitdir: /elsewhere\n")
	memtest.Write(t, root, "a/b/x.md", "")
	got, err := memory.FindRoot(filepath.Join(root, "a", "b"))
	if err != nil || got != root {
		t.Fatalf("FindRoot = %q, %v; want %q", got, err, root)
	}
}

// Codex review: a link through a symlinked directory to a missing file
// outside the repository escapes it, as pathlib's non-strict resolve says.
func TestSymlinkedMissingTargetEscapes(t *testing.T) {
	root := memtest.WriteTree(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "docs/memory/out")); err != nil {
		t.Fatal(err)
	}
	memtest.Append(t, root, "docs/memory/INDEX.md", "[x](out/missing.md)\n")
	hasErr(t, check(root), "docs/memory/INDEX.md: local link escapes repository: out/missing.md")
}
