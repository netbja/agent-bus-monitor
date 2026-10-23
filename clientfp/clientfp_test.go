package clientfp

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func touch(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// A symlink switched to a new target is an update; the same target is not.
func TestSymlinkSwitch(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "versions", "2.1.305"), "old")
	touch(t, filepath.Join(dir, "versions", "2.1.306"), "new")
	link := filepath.Join(dir, "bin", "claude")
	os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(filepath.Join(dir, "versions", "2.1.305"), link); err != nil {
		t.Fatal(err)
	}
	fp, err := Take(link)
	if err != nil {
		t.Fatal(err)
	}
	if fp.Label != "2.1.305" {
		t.Fatalf("label %q", fp.Label)
	}
	if changed, known := fp.Changed(); changed || !known {
		t.Fatalf("unchanged reads changed=%v known=%v", changed, known)
	}
	os.Remove(link)
	os.Symlink(filepath.Join(dir, "versions", "2.1.306"), link)
	changed, known := fp.Changed()
	if !changed || !known {
		t.Fatalf("switched link: changed=%v known=%v", changed, known)
	}
	now, _ := Take(link)
	if now.Label != "2.1.306" {
		t.Fatalf("new label %q", now.Label)
	}
}

// A file rewritten in place (new size and mtime) is an update.
func TestRewrittenInPlace(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "grok-1.0.62-linux-x86_64")
	touch(t, bin, "v1")
	fp, err := Take(bin)
	if err != nil || fp.Label != "1.0.62" {
		t.Fatalf("%+v %v", fp, err)
	}
	time.Sleep(10 * time.Millisecond)
	touch(t, bin, "v2 longer")
	if changed, known := fp.Changed(); !changed || !known {
		t.Fatalf("rewritten: changed=%v known=%v", changed, known)
	}
}

// A Node client: the script and the package.json beside it; npm rewrites
// both in place, and the label is the package's version.
func TestNodeScriptAndPackage(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "lib", "node_modules", "@openai", "codex")
	touch(t, filepath.Join(pkg, "package.json"), `{"name":"@openai/codex","version":"0.163.0","x":1}`)
	touch(t, filepath.Join(pkg, "bin", "codex.js"), "#!/usr/bin/env node\n")
	link := filepath.Join(dir, "bin", "codex")
	os.MkdirAll(filepath.Dir(link), 0o755)
	os.Symlink(filepath.Join(pkg, "bin", "codex.js"), link)
	fp, err := Take(link)
	if err != nil || fp.Pkg == nil || fp.Label != "0.163.0" {
		t.Fatalf("%+v %v", fp, err)
	}
	time.Sleep(10 * time.Millisecond)
	touch(t, filepath.Join(pkg, "package.json"), `{"name":"@openai/codex","version":"0.164.0","x":12}`)
	if changed, known := fp.Changed(); !changed || !known {
		t.Fatalf("package rewritten: changed=%v known=%v", changed, known)
	}
}

// An unreadable path is unknown, never "changed".
func TestMissingIsUnknown(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	touch(t, bin, "x")
	fp, _ := Take(bin)
	os.Remove(bin)
	if changed, known := fp.Changed(); changed || known {
		t.Fatalf("missing: changed=%v known=%v", changed, known)
	}
	if _, err := Take(filepath.Join(dir, "none")); err == nil {
		t.Fatal("took a missing path")
	}
}

func TestEncodeDecode(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "x")
	touch(t, bin, "x")
	fp, _ := Take(bin)
	s := fp.Encode()
	back, err := Decode(s)
	if err != nil || back.Target != fp.Target || back.Path != fp.Path {
		t.Fatalf("%+v %v", back, err)
	}
	if fp.Hash() == "" || fp.Hash() != back.Hash() {
		t.Fatal("hash")
	}
	if _, err := Decode(""); err == nil {
		t.Fatal("decoded empty")
	}
}

// Claude Code's layout, for agents launched before fingerprints existed.
func TestClaudeLayout(t *testing.T) {
	dir := t.TempDir()
	v := filepath.Join(dir, "claude", "versions")
	touch(t, filepath.Join(v, "2.1.305"), "")
	touch(t, filepath.Join(v, "2.1.306"), "")
	touch(t, filepath.Join(v, "2.1.99"), "")
	touch(t, filepath.Join(v, ".lock"), "")
	cases := []struct {
		exe                string
		running, installed string
		outdated, ok       bool
	}{
		{filepath.Join(v, "2.1.305"), "2.1.305", "2.1.306", true, true},
		{filepath.Join(v, "2.1.306"), "2.1.306", "2.1.306", false, true},
		{filepath.Join(v, "2.1.304") + " (deleted)", "2.1.304", "2.1.306", true, true},
		{"/usr/bin/node", "", "", false, false},
	}
	for _, c := range cases {
		r, i, out, ok := ClaudeLayout(c.exe)
		if r != c.running || i != c.installed || out != c.outdated || ok != c.ok {
			t.Errorf("ClaudeLayout(%s) = %q %q %v %v", c.exe, r, i, out, ok)
		}
	}
}
