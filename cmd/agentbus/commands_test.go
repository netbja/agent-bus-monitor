package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/cli"
)

// dispatchedNames reads main.go and returns every command name main()
// dispatches: the `args[0] == "x"` tests above the project exit and the cases
// of `switch cmd`.
func dispatchedNames(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var fn *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "main" {
			fn = fd
		}
	}
	if fn == nil {
		t.Fatal("no main() in main.go")
	}
	seen := map[string]bool{}
	lit := func(e ast.Expr) {
		if bl, ok := e.(*ast.BasicLit); ok && bl.Kind == token.STRING {
			s, _ := strconv.Unquote(bl.Value)
			seen[s] = true
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BinaryExpr:
			if ix, ok := n.X.(*ast.IndexExpr); ok && n.Op == token.EQL {
				if id, ok := ix.X.(*ast.Ident); ok && id.Name == "args" {
					if bl, ok := ix.Index.(*ast.BasicLit); ok && bl.Value == "0" {
						lit(n.Y)
					}
				}
			}
		case *ast.SwitchStmt:
			if id, ok := n.Tag.(*ast.Ident); ok && id.Name == "cmd" {
				for _, s := range n.Body.List {
					for _, e := range s.(*ast.CaseClause).List {
						lit(e)
					}
				}
			}
		}
		return true
	})
	var out []string
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func TestEveryDispatchedCommandIsInTheTable(t *testing.T) {
	names := dispatchedNames(t)
	if len(names) < 30 {
		t.Fatalf("found only %d dispatched names, the AST walk is broken: %v", len(names), names)
	}
	for _, n := range names {
		if _, ok := cli.Lookup(commands, n); !ok {
			t.Errorf("main() dispatches %q but the help table has no entry for it", n)
		}
	}
	// And the reverse: no entry for a command main() no longer dispatches.
	for _, c := range commands {
		for _, n := range append([]string{c.Name}, c.Aliases...) {
			if n == "help" {
				continue
			}
			if !slices.Contains(names, n) {
				t.Errorf("the help table lists %q but main() does not dispatch it", n)
			}
		}
	}
}

func TestCommandTableIsValid(t *testing.T) {
	if err := cli.Validate(globalFlags, commands); err != nil {
		t.Fatal(err)
	}
	for _, c := range commands {
		if c.Summary == "" || len(c.Examples) == 0 {
			t.Errorf("%s: needs a summary and at least one example", c.Name)
		}
		for _, e := range c.Examples {
			if !strings.HasPrefix(e, "agentbus ") {
				t.Errorf("%s: example %q does not start with \"agentbus \"", c.Name, e)
			}
		}
	}
}

// foreignFlags are "--x" literals in this package that belong to other
// programs (herdr, the client a role launches, bootstrap) or to argv this
// binary builds for itself, never to an agentbus command line.
var foreignFlags = []string{
	"--cwd", "--direction", "--entrypoint", "--env", "--filter", "--focus", "--format",
	"--git-dir", "--interactive", "--label", "--login", "--name", "--no-focus", "--plugin",
	"--settings", "--target-pane", "--version", "--workspace", "--model",
}

// Every flag an agentbus command reads (a "--x" literal in a non-test file of
// this package) is in the table, unless it belongs to another program.
func TestEveryFlagLiteralIsInTheTable(t *testing.T) {
	known := map[string]bool{}
	for _, g := range globalFlags {
		known["--"+g.Long] = true
	}
	for _, c := range commands {
		for _, f := range c.Flags {
			known["--"+f.Long] = true
		}
	}
	files, _ := filepath.Glob("*.go")
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			bl, ok := n.(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING {
				return true
			}
			s, _ := strconv.Unquote(bl.Value)
			if strings.HasPrefix(s, "--") && len(s) > 2 && !strings.ContainsAny(s, " =") &&
				!known[s] && !slices.Contains(foreignFlags, s) {
				t.Errorf("%s: flag %s is read but not in the help table", path, s)
			}
			return true
		})
	}
}
