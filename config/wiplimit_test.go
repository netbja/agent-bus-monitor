package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wip-limit r2 §3.3: board.wip_limit, the person's alone (Q3), from busmon's
// Shift+C or config.toml. A Project key with no env variable; PersonEdit:
// Set and Unset refuse it for everyone (agentbus config set and unset),
// SetAsPerson and UnsetAsPerson write it.
func TestWIPLimitKey(t *testing.T) {
	k, ok := Lookup("board.wip_limit")
	if !ok {
		t.Fatal("board.wip_limit is not a key")
	}
	if k.Kind != Int || k.Default != 2 || !k.Project || k.Env != "" || !k.PersonEdit || k.PersonOnly || k.Group() != GroupServices {
		t.Fatalf("%+v", k)
	}
	// What reads it at this head (foureyes): busmon shows it; request accept
	// and send are named only as the step that wires them.
	if !strings.HasPrefix(k.ReadBy, "busmon (shown") || !strings.Contains(k.ReadBy, "from wip-limit step 2b") {
		t.Fatalf("ReadBy %q", k.ReadBy)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	want := "board.wip_limit: the person's own setting: set it in busmon (Shift+C) or config.toml"
	if err := Set(path, "board.wip_limit", 3); err == nil || err.Error() != want {
		t.Fatalf("Set: %v", err)
	}
	if err := Unset(path, "board.wip_limit"); err == nil || err.Error() != want {
		t.Fatalf("Unset: %v", err)
	}
	if err := SetAsPerson(path, "board.wip_limit", 3); err != nil {
		t.Fatal(err)
	}
	if got := Load(path, "config.toml"); ProjectValue(got, nil, "demo", "board.wip_limit").Value != 3 {
		t.Fatalf("read back: %+v", ProjectValue(got, nil, "demo", "board.wip_limit"))
	}
	if err := SetAsPerson(path, "board.wip_limit", -1); err == nil {
		t.Fatal("a negative limit was written")
	}
	if err := UnsetAsPerson(path, "board.wip_limit"); err != nil {
		t.Fatal(err)
	}
	if got := Load(path, "config.toml"); ProjectValue(got, nil, "demo", "board.wip_limit").Source != "default" {
		t.Fatal("UnsetAsPerson left the value")
	}
	// A PersonOnly key stays the person's file alone: SetAsPerson refuses it too.
	if err := SetAsPerson(path, "backup.after_command", "x"); err == nil || !strings.Contains(err.Error(), "edit config.toml yourself") {
		t.Fatalf("SetAsPerson of a PersonOnly key: %v", err)
	}
	if err := UnsetAsPerson(path, "backup.after_command"); err == nil {
		t.Fatal("UnsetAsPerson of a PersonOnly key")
	}
}

// A [project.<name>] value beats the machine value, for that project only.
func TestWIPLimitPerProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[board]\nwip_limit = 3\n[project.demo.board]\nwip_limit = 1\n[project.bad.board]\nwip_limit = -2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := Load(path, "config.toml")
	for project, want := range map[string]int{"demo": 1, "other": 3, "bad": 3} {
		if got := ProjectValue(f, nil, project, "board.wip_limit").Value; got != want {
			t.Errorf("%s: %v, want %d", project, got, want)
		}
	}
}

// SetAsPerson and UnsetAsPerson are the person's writers: no package but
// busmon (the person's own tool) calls them.
func TestOnlyBusmonWritesAsThePerson(t *testing.T) {
	root := filepath.Join("..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == ".worktrees" || n == "testdata" || n == "node_modules" || strings.HasPrefix(n, ".") && n != ".." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if ok && (sel.Sel.Name == "SetAsPerson" || sel.Sel.Name == "UnsetAsPerson") && !strings.HasPrefix(filepath.ToSlash(rel), "cmd/busmon/") {
				t.Errorf("%s calls config.%s: only busmon writes as the person", rel, sel.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
