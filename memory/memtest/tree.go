// Package memtest builds the minimal repository tree the memory checker
// accepts, for the tests of memory and of the agentbus mem command.
package memtest

import (
	"os"
	"path/filepath"
	"testing"
)

// WriteTree writes, in a fresh temp dir, the tree the Python checker's tests
// started from: every entry point and boot file referencing the index, and an
// INDEX linking the protocol and the journal. Its three empty sections are
// where mem new appends. A .git directory marks the root.
func WriteTree(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	boot := "Read docs/memory/INDEX.md\n"
	files := map[string]string{
		"AGENTS.md": boot, "MEMORY.md": boot, "docs/PROJECT-JOURNAL.md": boot, "CLAUDE.md": boot,
		"docs/memory/PROTOCOL.md": boot, "docs/memory/journal/INDEX.md": boot,
		"skills/peer/SKILL.md": boot, "roles/peer.md": boot,
		"docs/memory/INDEX.md": "[Protocol](PROTOCOL.md)\n[Journal](journal/INDEX.md)\n\n## Decisions\n\n## Lessons\n\n## Open questions\n",
	}
	for _, n := range []string{"agent-bus", "agent-bus-master", "agent-bus-sentinel"} {
		files["skills/"+n+"/SKILL.md"] = boot
	}
	for _, n := range []string{"architect", "coder", "deploy", "foureyes", "master", "sentinel"} {
		files["roles/"+n+".md"] = boot
	}
	for p, body := range files {
		Write(t, root, p, body)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// Write creates root/rel (and its parents) with body.
func Write(t testing.TB, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Append adds body to the end of root/rel.
func Append(t testing.TB, root, rel, body string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(root, filepath.FromSlash(rel)), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
}
