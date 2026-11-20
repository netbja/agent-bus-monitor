package textfmt

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestOneLine(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "",
		"  a  b ":                "a b",
		"line one\nline two\t x": "line one line two x",
		"\n\n":                   "",
	} {
		if got := OneLine(in); got != want {
			t.Errorf("OneLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPluralAndCount(t *testing.T) {
	for _, c := range []struct {
		n             int
		plural, count string
	}{{0, "agents", "0 agents"}, {1, "agent", "1 agent"}, {2, "agents", "2 agents"}, {-1, "agents", "-1 agents"}} {
		if got := Plural(c.n, "agent"); got != c.plural {
			t.Errorf("Plural(%d) = %q, want %q", c.n, got, c.plural)
		}
		if got := Count(c.n, "agent"); got != c.count {
			t.Errorf("Count(%d) = %q, want %q", c.n, got, c.count)
		}
	}
}

func TestTokCount(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 873: "873", 999: "999", 1000: "1k", 206_400: "206k", 999_999: "999k", 1_000_000: "1.0M", 5_120_000: "5.1M"} {
		if got := TokCount(n); got != want {
			t.Errorf("TokCount(%d) = %q, want %q", n, got, want)
		}
	}
}

// No second copy comes back (cmd-0b): a top-level function with a merged
// helper's name and shape is refused in every package's code, and the two
// binaries use the standard library's slices.Contains and cmp.Or instead of
// their own contains and or.
func TestNoCopyOfTheSharedHelpers(t *testing.T) {
	root := "../.."
	// name -> the parameter list and results it had as a copy: a function of
	// that name with another shape (plan's oneLine(s, max) bool, projfiles'
	// plural(n, one, many)) does something else and may keep its name.
	anywhere := map[string]string{
		"oneLine":        "(string) string",
		"plural":         "(int, string) string",
		"tokCount":       "*",
		"notifyStateDir": "*",
	}
	binariesOnly := map[string]bool{"contains": true, "or": true, "oneLine": true, "plural": true}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); p != root && (strings.HasPrefix(n, ".") || n == "testdata" || n == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		dir := filepath.ToSlash(filepath.Dir(p))
		inBinary := strings.HasSuffix(dir, "cmd/agentbus") || strings.HasSuffix(dir, "cmd/busmon") || strings.HasSuffix(dir, "internal/dispatchsvc") // the runtime moved from cmd/agentbus
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			name := fn.Name.Name
			if inBinary && binariesOnly[name] {
				t.Errorf("%s: func %s in a binary: use textfmt, slices.Contains or cmp.Or", fset.Position(fn.Pos()), name)
				continue
			}
			if shape, ok := anywhere[name]; ok && (shape == "*" || shape == signature(fn.Type)) {
				t.Errorf("%s: func %s%s is a copy: use textfmt (or notify.StateDir)", fset.Position(fn.Pos()), name, signature(fn.Type))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// signature is a function type's parameter and result types, "(int, string) string".
func signature(ft *ast.FuncType) string {
	types := func(fl *ast.FieldList) []string {
		var out []string
		if fl == nil {
			return out
		}
		for _, f := range fl.List {
			n := max(1, len(f.Names))
			for range n {
				out = append(out, exprString(f.Type))
			}
		}
		return out
	}
	s := "(" + strings.Join(types(ft.Params), ", ") + ")"
	if r := types(ft.Results); len(r) == 1 {
		s += " " + r[0]
	} else if len(r) > 1 {
		s += " (" + strings.Join(r, ", ") + ")"
	}
	return s
}

func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.ArrayType:
		return "[]" + exprString(x.Elt)
	case *ast.StarExpr:
		return "*" + exprString(x.X)
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	case *ast.FuncType:
		return "func" + signature(x)
	}
	return "?"
}
