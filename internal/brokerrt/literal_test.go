package brokerrt

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// broker-hardening §4, step 3: the runtime's binary is named here only. A
// string literal "docker" or "docker-compose" in the Go code (tests aside;
// the settings registry, whose broker.runtime lists docker as a value, runs
// nothing and cannot import this package),
// or a script line that calls docker (comments aside, busmon-demo.sh, the
// docs' screenshot tool, aside), fails: it would run docker under
// broker.runtime=podman.
func TestNoRuntimeLiteral(t *testing.T) {
	root := filepath.Join("..", "..")
	registry := map[string]bool{"../../config/keys.go": true, "../../config/branch.go": true} // broker.runtime's values
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", "node_modules", "testdata", "brokerrt":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || registry[filepath.ToSlash(p)] {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil && (v == "docker" || v == "docker-compose") {
					t.Errorf("%s: the runtime %q named outside brokerrt", fset.Position(lit.Pos()), v)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := regexp.MustCompile(`(^|[^-.\w/])docker([ -]compose)?(\s|$|")`)
	scripts, _ := filepath.Glob(filepath.Join(root, "scripts", "*"))
	lib, _ := filepath.Glob(filepath.Join(root, "scripts", "lib", "*"))
	scripts = append(scripts, lib...)
	for _, p := range scripts {
		if st, err := os.Stat(p); err != nil || st.IsDir() || filepath.Base(p) == "busmon-demo.sh" {
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		for n := 1; sc.Scan(); n++ {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "#") {
				continue
			}
			if calls.MatchString(line) {
				t.Errorf("%s:%d calls docker: %s", p, n, line)
			}
		}
		f.Close()
	}
}
