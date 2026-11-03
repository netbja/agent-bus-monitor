package bus

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writtenKinds reads every key kind written in the module (backup spec,
// "Record keys and runtime keys") from the non-test sources of bus/ and, for
// bus's exported writers, of every package.
func writtenKinds(t *testing.T) map[string]string {
	t.Helper()
	busFiles, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var others []string
	filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (strings.HasPrefix(d.Name(), ".") && path != ".." || d.Name() == "testdata" || path == filepath.Join("..", "bus")) {
			return filepath.SkipDir
		}
		if strings.HasSuffix(path, ".go") {
			others = append(others, path)
		}
		return nil
	})
	read := func(names []string) map[string]string {
		out := map[string]string{}
		for _, n := range names {
			if strings.HasSuffix(n, "_test.go") {
				continue
			}
			b, err := os.ReadFile(n)
			if err != nil {
				t.Fatal(err)
			}
			out[n] = string(b)
		}
		return out
	}
	kinds, err := scanKinds(read(busFiles), read(others))
	if err != nil {
		t.Fatal(err)
	}
	return kinds
}

// scanKinds finds the key kinds written by sources: in bus's files, a literal
// ":kind" or ":kind:" joined with + (a dynamic tail reads "kind:*"); and a
// literal passed where a bus function routes its argument into StreamKey's
// kind, directly or through another such function (b.add(ctx, "x", ...),
// b.fencedAdd, fencedEntryOnce, StreamKey itself), found to a fixpoint. The
// other packages' calls count only for bus's exported functions. A kind held
// in a variable (a list ranged over) is not seen.
func scanKinds(busSrc, otherSrc map[string]string) (map[string]string, error) {
	fset := token.NewFileSet()
	parse := func(src map[string]string) ([]*ast.File, error) {
		var out []*ast.File
		for name, s := range src {
			f, err := parser.ParseFile(fset, name, s, 0)
			if err != nil {
				return nil, err
			}
			out = append(out, f)
		}
		return out, nil
	}
	busFiles, err := parse(busSrc)
	if err != nil {
		return nil, err
	}
	otherFiles, err := parse(otherSrc)
	if err != nil {
		return nil, err
	}
	lit := func(e ast.Expr) (string, bool) {
		b, ok := e.(*ast.BasicLit)
		if !ok || b.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(b.Value)
		return s, err == nil
	}
	callName := func(c *ast.CallExpr) string {
		switch f := c.Fun.(type) {
		case *ast.Ident:
			return f.Name
		case *ast.SelectorExpr:
			return f.Sel.Name
		}
		return ""
	}
	// kindArg: function name -> the index of the argument that becomes a kind.
	kindArg := map[string]int{"StreamKey": 1}
	for changed := true; changed; {
		changed = false
		for _, f := range busFiles {
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				if _, known := kindArg[fn.Name.Name]; known {
					continue
				}
				params := map[string]int{}
				i := 0
				for _, field := range fn.Type.Params.List {
					for _, n := range field.Names {
						params[n.Name] = i
						i++
					}
					if len(field.Names) == 0 {
						i++
					}
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					c, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					at, ok := kindArg[callName(c)]
					if !ok || at >= len(c.Args) {
						return true
					}
					if id, ok := c.Args[at].(*ast.Ident); ok {
						if pi, ok := params[id.Name]; ok {
							kindArg[fn.Name.Name] = pi
							changed = true
						}
					}
					return true
				})
			}
		}
	}
	kinds := map[string]string{}
	collect := func(files []*ast.File, exportedOnly bool) {
		for _, f := range files {
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.BinaryExpr:
					if exportedOnly || n.Op != token.ADD {
						return true
					}
					s, ok := lit(n.Y)
					if !ok || len(s) < 2 || s[0] != ':' || !validKindText(s[1:]) {
						return true
					}
					k := s[1:]
					if strings.HasSuffix(k, ":") {
						k += "*"
					}
					kinds[k] = fset.Position(n.Pos()).String()
				case *ast.CallExpr:
					name := callName(n)
					at, ok := kindArg[name]
					if !ok || at >= len(n.Args) || exportedOnly && !ast.IsExported(name) {
						return true
					}
					if exportedOnly {
						if sel, ok := n.Fun.(*ast.SelectorExpr); ok {
							if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name != "bus" && name == "StreamKey" {
								return true
							}
						}
					}
					if s, ok := lit(n.Args[at]); ok && validKindText(s) {
						kinds[s] = fset.Position(n.Pos()).String()
					}
				}
				return true
			})
		}
	}
	collect(busFiles, false)
	collect(otherFiles, true)
	return kinds, nil
}

// The scan sees a kind given to a generic writer, directly or through
// another (reviewer-codex): a new b.add(ctx, "newkind", ...) is caught.
func TestScanKindsFollowsGenericWriters(t *testing.T) {
	busSrc := map[string]string{"w.go": `package bus
func StreamKey(p, kind string) string { return p + ":" + kind }
func (b *Bus) add(ctx int, kind string, v map[string]any) { _ = StreamKey(b.project, kind) }
func (b *Bus) wrap(ctx int, token, k string) { b.add(ctx, k, nil) }
func (b *Bus) Recent(ctx int, kind string) { _ = StreamKey(b.project, kind) }
func (b *Bus) use() {
	b.add(0, "newkind", nil)
	b.wrap(0, "tok", "wrapped")
	_ = b.project + ":lease"
	_ = b.project + ":tail:" + "x"
}`}
	otherSrc := map[string]string{"o.go": `package other
func f(b *bus.Bus) { b.Recent(0, "outside"); b.add(0, "private", nil) }`}
	kinds, err := scanKinds(busSrc, otherSrc)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"newkind", "wrapped", "lease", "tail:*", "outside"} {
		if _, ok := kinds[k]; !ok {
			t.Errorf("%q not found in %v", k, kinds)
		}
	}
	if _, ok := kinds["tok"]; ok {
		t.Error("a non-kind argument was read as a kind")
	}
	if _, ok := kinds["private"]; ok {
		t.Error("another package's unexported call was counted")
	}
}

func validKindText(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r == ':' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// Every key kind written in bus/ is classified record or runtime, so a new
// key is classified when it is added (backup spec).
func TestEveryWrittenKeyKindIsClassified(t *testing.T) {
	kinds := writtenKinds(t)
	if len(kinds) < 30 {
		t.Fatalf("found %d kinds, the scan is broken: %v", len(kinds), kinds)
	}
	for k, at := range kinds {
		if _, ok := keyKinds[k]; !ok {
			t.Errorf("%s: key kind %q is neither record nor runtime in keyKinds", at, k)
		}
	}
}

func TestKeyClassOf(t *testing.T) {
	for _, c := range []struct {
		key  string
		want KeyClass
		kind string
	}{
		{"demo:cmd", KeyRecord, "cmd"},
		{"demo:verdicts", KeyRecord, "verdicts"},
		{"demo:board", KeyRecord, "board"},
		{"demo:plan:facts", KeyRecord, "plan:facts"},
		{"demo:gate:coder", KeyRecord, "gate:*"},
		{"demo:health:log", KeyRecord, "health:log"},
		{"demo:quota:hist", KeyRecord, "quota:hist"},
		{"demo:missions:c1", KeyRecord, "missions:*"},
		{"demo:services", KeyRuntime, "services"},
		{"demo:agents", KeyRuntime, "agents"},
		{"demo:wake:coder", KeyRuntime, "wake:*"},
		{"demo:quota", KeyRuntime, "quota"},
		{"demo:health:daily", KeyRuntime, "health:daily"},
		{"demo:missionops", KeyRuntime, "missionops"},
		// Not ours (a foreign suffix): kept in the archive, never restored
		// by default, like a runtime key.
		{"demo:somebodyelse", KeyUnknown, ""},
		{"demo-2:cmd", KeyUnknown, ""},
		// A tail is one valid name (reviewer-codex): a nested or malformed
		// one is foreign, never restored by default.
		{"demo:gate:coder:foreign", KeyUnknown, ""},
		{"demo:change:c1:x", KeyUnknown, ""},
		{"demo:wake:Coder", KeyUnknown, ""},
		{"demo:swap:", KeyUnknown, ""},
		{"demo:missions:c-1", KeyRecord, "missions:*"},
		{"demo", KeyUnknown, ""},
	} {
		class, kind := ClassOf("demo", c.key)
		if class != c.want || kind != c.kind {
			t.Errorf("ClassOf(demo, %q) = %v %q, want %v %q", c.key, class, kind, c.want, c.kind)
		}
	}
	// The spec's record list, each restored by default.
	for _, k := range []string{"status", "report", "notify", "cmd", "verdicts", "board", "asks", "plan", "plan:facts",
		"gate:*", "swaps", "health", "health:log", "health:notes", "quota:hist", "change:*", "missions:*"} {
		if keyKinds[k] != KeyRecord {
			t.Errorf("%q: %v, the spec makes it record", k, keyKinds[k])
		}
	}
	for _, k := range []string{"services", "agents", "wake:*", "handoff", "quota", "pause", "fallback", "usage",
		"budget", "tokens", "sessions", "doctor"} {
		if keyKinds[k] != KeyRuntime {
			t.Errorf("%q: %v, the spec makes it runtime", k, keyKinds[k])
		}
	}
}
