package bus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// request-priority §3/§9 step 3: no production code writes a board entry
// from a Go struct, so a field the struct does not know (a request's
// priority) survives every write: every board writer is a Lua script's cjson
// round trip. Type-checked over the module's non-test files: any
// json.Marshal of a BoardEntry or RequestInfo (value or pointer), and any
// go-redis HSet, HSetNX or HMSet whose key reaches BoardKey through
// assignments and parameters (call sites followed to a fixpoint).

const modulePath = "github.com/netbja/agent-bus-monitor"

type checkedPkg struct {
	path  string
	files []*ast.File
	info  *types.Info
}

type listedPkg struct {
	ImportPath, Dir, Export string
	GoFiles                 []string
	DepOnly                 bool
	Module                  *struct{ Path string }
}

// loadModule type-checks the module's packages from source, their imports
// from the export data `go list -export` builds.
func loadModule(t *testing.T) (*token.FileSet, types.Importer, []checkedPkg) {
	t.Helper()
	cmd := exec.Command("go", "list", "-export", "-deps", "-json", "./...")
	cmd.Dir = ".."
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list: %v: %s", err, errb.String())
	}
	exports := map[string]string{}
	var mine []listedPkg
	for dec := json.NewDecoder(&out); ; {
		var p listedPkg
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		exports[p.ImportPath] = p.Export
		if !p.DepOnly && p.Module != nil && p.Module.Path == modulePath {
			mine = append(mine, p)
		}
	}
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		f, ok := exports[path]
		if !ok || f == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(f)
	})
	var pkgs []checkedPkg
	for _, p := range mine {
		var files []*ast.File
		for _, name := range p.GoFiles {
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}
		pkgs = append(pkgs, checkFiles(t, fset, imp, p.ImportPath, files))
	}
	if len(pkgs) < 10 {
		t.Fatalf("only %d module packages loaded", len(pkgs))
	}
	return fset, imp, pkgs
}

func checkFiles(t *testing.T, fset *token.FileSet, imp types.Importer, path string, files []*ast.File) checkedPkg {
	t.Helper()
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}}
	if _, err := (&types.Config{Importer: imp}).Check(path, fset, files, info); err != nil {
		t.Fatalf("type-check %s: %v", path, err)
	}
	return checkedPkg{path: path, files: files, info: info}
}

// edge: the node gets what expr holds, read with info.
type edge struct {
	expr ast.Expr
	info *types.Info
}

// boardWriters lists every struct write of a board entry in pkgs.
func boardWriters(fset *token.FileSet, pkgs []checkedPkg) []string {
	boardKey := modulePath + "/bus.BoardKey"
	params := map[types.Object]string{} // a declared function's parameter -> FullName#i
	node := func(obj types.Object) string {
		if k, ok := params[obj]; ok {
			return k
		}
		return fset.Position(obj.Pos()).String() + "|" + obj.Name()
	}
	callee := func(info *types.Info, call *ast.CallExpr) *types.Func {
		var id *ast.Ident
		switch f := ast.Unparen(call.Fun).(type) {
		case *ast.Ident:
			id = f
		case *ast.SelectorExpr:
			id = f.Sel
		}
		if id == nil {
			return nil
		}
		fn, _ := info.Uses[id].(*types.Func)
		return fn
	}
	for _, p := range pkgs {
		for _, f := range p.files {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok {
					continue
				}
				fn, _ := p.info.Defs[fd.Name].(*types.Func)
				if fn == nil {
					continue
				}
				i := 0
				for _, field := range fd.Type.Params.List {
					for _, name := range field.Names {
						params[p.info.Defs[name]] = fmt.Sprintf("%s#%d", fn.FullName(), i)
						i++
					}
					if len(field.Names) == 0 {
						i++
					}
				}
			}
		}
	}
	edges := map[string][]edge{}
	for _, p := range pkgs {
		for _, f := range p.files {
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.AssignStmt:
					if len(n.Lhs) == len(n.Rhs) {
						for i, l := range n.Lhs {
							if id, ok := l.(*ast.Ident); ok {
								obj := p.info.Defs[id]
								if obj == nil {
									obj = p.info.Uses[id]
								}
								if obj != nil {
									edges[node(obj)] = append(edges[node(obj)], edge{n.Rhs[i], p.info})
								}
							}
						}
					}
				case *ast.ValueSpec:
					if len(n.Names) == len(n.Values) {
						for i, id := range n.Names {
							if obj := p.info.Defs[id]; obj != nil {
								edges[node(obj)] = append(edges[node(obj)], edge{n.Values[i], p.info})
							}
						}
					}
				case *ast.CallExpr:
					fn := callee(p.info, n)
					if fn == nil {
						break
					}
					sig := fn.Type().(*types.Signature)
					for i, arg := range n.Args {
						j := i
						if j >= sig.Params().Len() {
							j = sig.Params().Len() - 1
						}
						if j >= 0 {
							k := fmt.Sprintf("%s#%d", fn.FullName(), j)
							edges[k] = append(edges[k], edge{arg, p.info})
						}
					}
				}
				return true
			})
		}
	}
	reached := map[string]bool{}
	var reaches func(e ast.Expr, info *types.Info) bool
	reaches = func(e ast.Expr, info *types.Info) bool {
		switch e := ast.Unparen(e).(type) {
		case *ast.CallExpr:
			fn := callee(info, e)
			return fn != nil && fn.FullName() == boardKey
		case *ast.Ident:
			obj := info.Uses[e]
			if obj == nil {
				obj = info.Defs[e]
			}
			return obj != nil && reached[node(obj)]
		case *ast.BinaryExpr:
			return reaches(e.X, info) || reaches(e.Y, info)
		}
		return false
	}
	for changed := true; changed; {
		changed = false
		for k, es := range edges {
			if reached[k] {
				continue
			}
			for _, ed := range es {
				if reaches(ed.expr, ed.info) {
					reached[k], changed = true, true
					break
				}
			}
		}
	}
	var found []string
	for _, p := range pkgs {
		for _, f := range p.files {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				fn := callee(p.info, call)
				if fn == nil {
					return true
				}
				at := fset.Position(call.Pos())
				switch name := fn.FullName(); {
				case name == "encoding/json.Marshal" || name == "encoding/json.MarshalIndent":
					if len(call.Args) > 0 && isBoardStruct(p.info.TypeOf(call.Args[0])) {
						found = append(found, fmt.Sprintf("%s:%d: json.Marshal of %s", filepath.Base(at.Filename), at.Line, p.info.TypeOf(call.Args[0])))
					}
				case fn.Pkg() != nil && strings.HasPrefix(fn.Pkg().Path(), "github.com/redis/go-redis") &&
					(fn.Name() == "HSet" || fn.Name() == "HSetNX" || fn.Name() == "HMSet"):
					if len(call.Args) > 1 && reaches(call.Args[1], p.info) {
						found = append(found, fmt.Sprintf("%s:%d: %s on the board", filepath.Base(at.Filename), at.Line, fn.Name()))
					}
				}
				return true
			})
		}
	}
	sort.Strings(found)
	return found
}

func isBoardStruct(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return false
	}
	return n.Obj().Pkg().Path() == modulePath+"/bus" && (n.Obj().Name() == "BoardEntry" || n.Obj().Name() == "RequestInfo")
}

func TestNoStructWriterOfTheBoard(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the module's export data")
	}
	fset, imp, pkgs := loadModule(t)
	if found := boardWriters(fset, pkgs); len(found) > 0 {
		t.Fatalf("a board entry written from a Go struct drops fields it does not know (a request's priority); write it with a Lua script:\n%s",
			strings.Join(found, "\n"))
	}
	// The walk itself, on a fixture: each way in is caught, and nothing else.
	const src = `package fx

import (
	"context"
	"encoding/json"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/redis/go-redis/v9"
)

func direct(c *redis.Client) { c.HSet(context.Background(), bus.BoardKey("p"), "t", "v") }
func viaVar(c *redis.Client) { k := bus.BoardKey("p"); c.HSetNX(context.Background(), k, "t", "v") }
func viaParam(c *redis.Client, key string) { c.HSet(context.Background(), key, "t", "v") }
func caller(c *redis.Client) { viaParam(c, bus.BoardKey("p")) }
func other(c *redis.Client) { c.HSet(context.Background(), bus.AgentsKey("p"), "a", "v") }
func marshal(e bus.BoardEntry, r *bus.RequestInfo) { json.Marshal(e); json.Marshal(r) }
func fine() { json.Marshal(bus.AgentSnapshot{}) }
`
	f, err := parser.ParseFile(fset, "fx.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := boardWriters(fset, []checkedPkg{checkFiles(t, fset, imp, "fx", []*ast.File{f})})
	want := []string{"fx.go:11: HSet on the board", "fx.go:12: HSetNX on the board", "fx.go:13: HSet on the board",
		"fx.go:16: json.Marshal of github.com/netbja/agent-bus-monitor/bus.BoardEntry",
		"fx.go:16: json.Marshal of *github.com/netbja/agent-bus-monitor/bus.RequestInfo"}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("fixture: got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
