package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/memory"
	"github.com/netbja/agent-bus-monitor/memory/memtest"
)

var memToday = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// mem runs `agentbus mem args` from cwd with only AGENT_BUS_AGENT set: no
// project, no broker.
func mem(t *testing.T, cwd string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	env := func(k string) string {
		if k == "AGENT_BUS_AGENT" {
			return "codex"
		}
		return ""
	}
	code = runMem(args, env, cwd, memToday, &out, &errb)
	return code, out.String(), errb.String()
}

func lastLine(s string) string {
	l := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return l[len(l)-1]
}

func TestMemCheckPassAndFail(t *testing.T) {
	root := memtest.WriteTree(t)
	if code, out, _ := mem(t, root, "check"); code != 0 || !strings.HasPrefix(lastLine(out), "PASS: ") {
		t.Fatalf("clean tree: %d %q", code, out)
	}
	memtest.Append(t, root, "docs/memory/PROTOCOL.md", "[gone](gone.md)\n")
	code, out, _ := mem(t, root, "check", "--root", root)
	if code != 1 || !strings.Contains(out, "FAIL: docs/memory/PROTOCOL.md: broken local link: gone.md") || strings.Contains(out, "PASS") {
		t.Fatalf("broken link: %d %q", code, out)
	}
}

func TestMemCheckStrict(t *testing.T) {
	root := memtest.WriteTree(t)
	memtest.Write(t, root, "docs/memory/lessons/old.md", "---\nid: old\nproject: test\nscope: project\nkind: lesson\nstatus: active\ncreated: 2026-01-01\nlast_verified: 2026-01-01\nauthor: test\nconfidence: verified\n---\n[e](../PROTOCOL.md)\n")
	memtest.Append(t, root, "docs/memory/INDEX.md", "- [Old](lessons/old.md)\n")
	if _, err := memory.Sync(root); err != nil {
		t.Fatal(err)
	}
	code, out, _ := mem(t, root, "check")
	if code != 0 || strings.Count(out, "WARN: ") != 1 || !strings.Contains(lastLine(out), "1 warning(s)") {
		t.Fatalf("without --strict: %d %q", code, out)
	}
	if code, out, _ := mem(t, root, "check", "--strict"); code != 1 || strings.Contains(out, "PASS") {
		t.Fatalf("with --strict: %d %q", code, out)
	}
	if code, out, _ := mem(t, root, "check", "--stale-after", "400d"); code != 0 || strings.Contains(out, "WARN") {
		t.Fatalf("--stale-after 400d: %d %q", code, out)
	}
}

func TestMemUsage(t *testing.T) {
	root := memtest.WriteTree(t)
	for _, args := range [][]string{{}, {"bogus"}, {"check", "--typo"}, {"check", "--stale-after", "2w"},
		{"check", "--stale-after", "0d"}, {"new", "lesson"}, {"new", "lesson", "x"}, {"sync", "extra"}} {
		if code, _, errOut := mem(t, root, args...); code != 2 || !strings.Contains(errOut, "usage: agentbus mem") {
			t.Errorf("%q: exit %d, stderr %q", args, code, errOut)
		}
	}
}

func TestMemNeedsNoProject(t *testing.T) {
	root := memtest.WriteTree(t)
	if code, out, errOut := mem(t, root, "new", "lesson", "x", "--title", "X", "--note-project", "demo"); code != 0 {
		t.Fatalf("new: %d %q %q", code, out, errOut)
	}
	if code, _, _ := mem(t, root, "sync"); code != 0 {
		t.Fatal("sync needs no project")
	}
}

func TestMemNoMemoryDir(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".git"), 0o755)
	for _, sub := range []string{"check", "sync"} {
		code, _, errOut := mem(t, root, sub)
		if code != 1 || !strings.HasPrefix(errOut, "error: no docs/memory/INDEX.md under ") {
			t.Errorf("%s: %d %q", sub, code, errOut)
		}
	}
	code, _, errOut := mem(t, t.TempDir(), "check")
	if code != 1 || !strings.HasPrefix(errOut, "error: ") {
		t.Errorf("outside a git tree: %d %q", code, errOut)
	}
}

func TestMemNewThenSync(t *testing.T) {
	root := memtest.WriteTree(t)
	sub := filepath.Join(root, "docs")
	code, out, errOut := mem(t, sub, "new", "lesson", "x", "--title", "X", "--note-project", "demo", "--scope", "transferable")
	if code != 0 || strings.TrimSpace(out) != "docs/memory/lessons/2026-10-11-x.md" {
		t.Fatalf("new: %d %q %q", code, out, errOut)
	}
	if code, out, _ := mem(t, root, "sync"); code != 0 || strings.TrimSpace(out) != "AGENTS.md: index block already up to date" {
		t.Fatalf("sync: %d %q", code, out)
	}
	memtest.Append(t, root, "docs/memory/INDEX.md", "- [P](PROTOCOL.md)\n")
	if code, out, _ := mem(t, root, "sync"); code != 0 || strings.TrimSpace(out) != "AGENTS.md: index block updated" {
		t.Fatalf("sync after edit: %d %q", code, out)
	}
	if code, _, errOut := mem(t, root, "new", "lesson", "x", "--title", "X", "--note-project", "demo"); code != 1 || !strings.HasPrefix(errOut, "error: ") {
		t.Fatalf("existing note: %d %q", code, errOut)
	}
}
