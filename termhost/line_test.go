package termhost

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The wake line typed today, byte for byte (spec 4.2: the same line typed).
func TestWakeLineIsTodaysLine(t *testing.T) {
	l, err := WakeLine("demo", "coder", 3, "a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	want := "[agentbus#a1b2c3] demo: 3 message(s) waiting for coder. Run: agentbus --project demo subscribe --drain coder"
	if l.Text() != want || l.Kind() != KindWake || l.Marker() != "[agentbus#a1b2c3]" || l.Nonce() != "a1b2c3" || l.Count() != 3 {
		t.Fatalf("line %q kind %v marker %q", l.Text(), l.Kind(), l.Marker())
	}
	if c := ClearLine(); c.Text() != "/clear" || c.Kind() != KindClear || c.Marker() != "/clear" {
		t.Fatalf("clear %q kind %v", c.Text(), c.Kind())
	}
	var zero Line
	if zero.Valid() || zero.Text() != "" || !l.Valid() || !ClearLine().Valid() {
		t.Fatal("validity")
	}
}

// WakeLine refuses a name outside ValidName, a count below 1 and a nonce
// outside ^[0-9a-f]{6,16}$; a count above 999 is capped, never refused (a
// large backlog must still wake its agent).
func TestWakeLineRules(t *testing.T) {
	for _, c := range []struct {
		project, agent, nonce string
		count                 int64
	}{
		{"Demo", "coder", "a1b2c3", 1}, {"demo", "coder; rm -rf", "a1b2c3", 1}, {"demo", "", "a1b2c3", 1},
		{"demo", "coder", "a1b2c3", 0}, {"demo", "coder", "a1b2c3", -4},
		{"demo", "coder", "a1b2c", 1}, {"demo", "coder", "A1B2C3", 1}, {"demo", "coder", "a1b2c3a1b2c3a1b2c", 1}, {"demo", "coder", "a1b2c3 x", 1},
	} {
		if l, err := WakeLine(c.project, c.agent, c.count, c.nonce); err == nil {
			t.Errorf("%+v: accepted as %q", c, l.Text())
		}
	}
	for count, want := range map[int64]string{999: " 999 message(s)", 1000: " 999+ message(s)", 1 << 40: " 999+ message(s)"} {
		l, err := WakeLine("demo", "coder", count, "abcdef")
		if err != nil || !strings.Contains(l.Text(), want) || l.Count() != min(count, 999) {
			t.Errorf("count %d: %q count %d err %v", count, l.Text(), l.Count(), err)
		}
	}
}

// The closed set (spec 4.2): Line has no exported field, and the only
// functions of this package that return a Line are WakeLine and ClearLine, so
// no caller can make one carry other text.
func TestLineIsAClosedSet(t *testing.T) {
	typ := reflect.TypeOf(Line{})
	for i := range typ.NumField() {
		if typ.Field(i).IsExported() {
			t.Errorf("Line.%s is exported", typ.Field(i).Name)
		}
	}
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() || fn.Type.Results == nil {
				continue
			}
			if fn.Recv != nil && recvName(fn) == "Line" { // Line's own accessors return no Line
				continue
			}
			for _, r := range fn.Type.Results.List {
				if id, ok := r.Type.(*ast.Ident); ok && id.Name == "Line" && fn.Name.Name != "WakeLine" && fn.Name.Name != "ClearLine" {
					t.Errorf("%s returns a Line", fn.Name.Name)
				}
			}
		}
	}
}

func recvName(fn *ast.FuncDecl) string {
	switch r := fn.Recv.List[0].Type.(type) {
	case *ast.Ident:
		return r.Name
	case *ast.StarExpr:
		if id, ok := r.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

// Submit takes the terminal and a Line only: no string crosses the seam.
func TestSubmitTakesNoText(t *testing.T) {
	m, _ := reflect.TypeOf((*Host)(nil)).Elem().MethodByName("Submit")
	ft := m.Type
	if ft.NumIn() != 3 || ft.In(1) != reflect.TypeOf(TermID("")) || ft.In(2) != reflect.TypeOf(Line{}) {
		t.Fatalf("Submit %v", ft)
	}
}

// A Spec carries a role and its options, never a command (spec 4.2, security
// S2a): no argv, env or cwd crosses the seam; the host builds the command
// from the role's launch template.
func TestSpecCarriesNoCommand(t *testing.T) {
	want := []string{"Project", "Name", "Profile", "ResumeSession", "Cols", "Rows"}
	typ := reflect.TypeOf(Spec{})
	var got []string
	for i := range typ.NumField() {
		got = append(got, typ.Field(i).Name)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Spec's fields %v, want %v", got, want)
	}
}
