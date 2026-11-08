package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// model-detection §3.6: nothing writes roles.toml on its own. A source scan
// of the code that runs by itself (models, dispatch, health and every
// agentbus command): no file-writing call takes roles.toml's path, whether
// written as a literal, ROLES_TOML or manifestPath(...), directly or through
// a local variable. The one writer is `roles adopt`, the person's act after
// an ask (draftcmd.go).
var profileWriterAllowed = map[string]bool{"cmd/agentbus/draftcmd.go": true}

var manifestText = regexp.MustCompile(`(?i)roles\.toml|ROLES_TOML|manifestPath\(`)

// writeCalls are the os calls that create, replace or open a file to write.
var writeCalls = map[string]bool{"WriteFile": true, "Create": true, "CreateTemp": true, "Rename": true, "OpenFile": true}

// profileWriters returns "file:line call" for each write of the manifest in f.
func profileWriters(fset *token.FileSet, f *ast.File, src []byte) []string {
	text := func(n ast.Node) string {
		return string(src[fset.Position(n.Pos()).Offset:fset.Position(n.End()).Offset])
	}
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		tainted := map[string]bool{} // locals holding the manifest's path
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.AssignStmt:
				for i, rhs := range s.Rhs {
					if !manifestText.MatchString(text(rhs)) && !mentions(rhs, tainted) {
						continue
					}
					for j, lhs := range s.Lhs {
						if id, ok := lhs.(*ast.Ident); ok && (len(s.Rhs) == 1 || i == j) {
							tainted[id.Name] = true
						}
					}
				}
			case *ast.CallExpr:
				sel, ok := s.Fun.(*ast.SelectorExpr)
				if !ok || !writeCalls[sel.Sel.Name] {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "os" {
					return true
				}
				if sel.Sel.Name == "OpenFile" && len(s.Args) > 1 && !strings.ContainsAny(text(s.Args[1]), "W") && !strings.Contains(text(s.Args[1]), "O_CREATE") {
					return true // a read-only open
				}
				for _, a := range s.Args {
					if manifestText.MatchString(text(a)) || mentions(a, tainted) {
						found = append(found, fset.Position(s.Pos()).String()+" os."+sel.Sel.Name)
						break
					}
				}
			}
			return true
		})
		return false
	})
	return found
}

// mentions reports whether e uses one of the tainted locals.
func mentions(e ast.Node, tainted map[string]bool) bool {
	hit := false
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && tainted[id.Name] {
			hit = true
		}
		return !hit
	})
	return hit
}

func TestNoProfileWriter(t *testing.T) {
	var found []string
	for _, dir := range []string{"../../models", "../../dispatch", "../../health", "."} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			root, _ := filepath.Abs("../..")
			abs, _ := filepath.Abs(path)
			rel, _ := filepath.Rel(root, abs)
			if profileWriterAllowed[filepath.ToSlash(rel)] {
				continue
			}
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, src, 0)
			if err != nil {
				t.Fatal(err)
			}
			found = append(found, profileWriters(fset, f, src)...)
		}
	}
	sort.Strings(found)
	if len(found) > 0 {
		t.Fatalf("roles.toml written by code that runs on its own (model-detection §3.6: only the person changes it):\n%s", strings.Join(found, "\n"))
	}
	// The scan itself, on a fixture: each way in is caught, and nothing else.
	const fx = `package fx

import "os"

func lit()        { os.WriteFile("roles.toml", nil, 0o600) }
func env()        { os.Create(os.Getenv("ROLES_TOML")) }
func viaVar()     { p, _ := manifestPath(nil); tmp := p + ".tmp"; os.Rename(tmp, p) }
func openW()      { p, _ := manifestPath(nil); os.OpenFile(p, os.O_WRONLY, 0) }
func openR()      { p, _ := manifestPath(nil); os.OpenFile(p, os.O_RDONLY, 0) }
func other()      { os.WriteFile("models.json", nil, 0o600) }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "fx.go", fx, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := profileWriters(fset, f, []byte(fx))
	if len(got) != 4 {
		t.Fatalf("fixture: %d writers, want 4 (lit, env, viaVar, openW):\n%s", len(got), strings.Join(got, "\n"))
	}
	for i, want := range []string{"fx.go:5:", "fx.go:6:", "fx.go:7:", "fx.go:8:"} {
		if !strings.HasPrefix(got[i], want) {
			t.Errorf("writer %d is %s, want %s…", i, got[i], want)
		}
	}
}
