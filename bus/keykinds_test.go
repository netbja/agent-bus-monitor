package bus

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writtenKinds reads every key kind bus/ builds (backup spec, "Record keys
// and runtime keys"): a literal ":kind" or ":kind:" joined with +, and the
// literal kind given to StreamKey. A dynamic tail reads as "kind:*".
func writtenKinds(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		lit := func(e ast.Expr) (string, bool) {
			b, ok := e.(*ast.BasicLit)
			if !ok || b.Kind != token.STRING {
				return "", false
			}
			s, err := strconv.Unquote(b.Value)
			return s, err == nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BinaryExpr:
				if n.Op != token.ADD {
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
				if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "StreamKey" && len(n.Args) == 2 {
					if s, ok := lit(n.Args[1]); ok && validKindText(s) {
						kinds[s] = fset.Position(n.Pos()).String()
					}
				}
			}
			return true
		})
	}
	return kinds
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
