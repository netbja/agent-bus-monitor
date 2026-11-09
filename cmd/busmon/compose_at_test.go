package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/config"
)

// compose-at-file §4.1: an @ typed at the start of the text, or after
// whitespace or one of ( [ " ', opens the query; it runs to the cursor and
// holds no whitespace. An email (a@b.c) never opens it.
func TestAtToken(t *testing.T) {
	for _, c := range []struct {
		text  string // | marks the cursor
		query string
		start int
		ok    bool
	}{
		{"@|", "", 0, true},
		{"@cmd/bu|", "cmd/bu", 0, true},
		{"see @tea|", "tea", 4, true},
		{"see\t@tea|", "tea", 4, true},
		{"line\n@x|", "x", 5, true},
		{"(@x|", "x", 1, true},
		{"[@x|", "x", 1, true},
		{"\"@x|", "x", 1, true},
		{"'@x|", "x", 1, true},
		{"a@b.c|", "", 0, false},         // an email
		{"@cmd bu|", "", 0, false},       // a space closed it
		{"plain text|", "", 0, false},    // no @
		{"@cmd|/busmon", "cmd", 0, true}, // the query runs to the cursor
		{"x @a@b|", "", 0, false},        // the nearest @ follows a letter
	} {
		cur := strings.Index(c.text, "|")
		text := []rune(strings.Replace(c.text, "|", "", 1))
		q, start, ok := atToken(text, len([]rune(c.text[:cur])))
		if ok != c.ok || ok && (q != c.query || start != c.start) {
			t.Errorf("%q: got %q at %d %v, want %q at %d %v", c.text, q, start, ok, c.query, c.start, c.ok)
		}
	}
}

// §5 and the person's answers: the path inserted is absolute by default (a
// home path always), relative when the setting says so; a directory ends
// with the separator; a path with a space or a bracket is wrapped in
// backticks; one with a backtick is refused.
func TestAtPathText(t *testing.T) {
	root, home := filepath.FromSlash("/data/p"), filepath.FromSlash("/home/u")
	sep := string(filepath.Separator)
	for _, c := range []struct {
		rel      string
		dir      bool
		fromHome bool
		form     string
		want     string
		why      string
	}{
		{"cmd/busmon/keys.go", false, false, "absolute", filepath.Join(root, "cmd", "busmon", "keys.go"), ""},
		{"cmd/busmon", true, false, "absolute", filepath.Join(root, "cmd", "busmon") + sep, ""},
		{"cmd/busmon/keys.go", false, false, "relative", filepath.Join("cmd", "busmon", "keys.go"), ""},
		{"Documents/a.md", false, true, "relative", filepath.Join(home, "Documents", "a.md"), ""}, // home: always absolute
		{"docs/my notes.md", false, false, "absolute", "`" + filepath.Join(root, "docs", "my notes.md") + "`", ""},
		{"docs/a(1).md", false, false, "relative", "`" + filepath.Join("docs", "a(1).md") + "`", ""},
		{"we`ird.md", false, false, "absolute", "", "unusual name: type it yourself"},
	} {
		got, why := atPathText(root, home, c.rel, c.dir, c.fromHome, c.form)
		if got != c.want || why != c.why {
			t.Errorf("%q (%s): got %q %q, want %q %q", c.rel, c.form, got, why, c.want, c.why)
		}
	}
}

// §5: the @query is replaced by the path in one undo step; one space
// follows unless the next rune is whitespace or punctuation; a path that
// would overflow the cap is not inserted, never cut; two files in a row.
func TestAtReplace(t *testing.T) {
	ta := textArea{limit: 100}
	ta.set("see @tea")
	ta.cur = len(ta.text)
	if !ta.replaceAt(4, ta.cur, "/p/tea_compose.go") || ta.value() != "see /p/tea_compose.go " || ta.cur != len(ta.text) {
		t.Fatalf("insert: %q cur %d", ta.value(), ta.cur)
	}
	if !ta.undoStep() || ta.value() != "see @tea" {
		t.Fatalf("one undo step: %q", ta.value())
	}
	// the insert is its own step, never joined to the typing before it
	ta.set("see @te")
	ta.cur = len(ta.text)
	ta.edit("type", func() { ta.insert("a") })
	if !ta.replaceAt(4, ta.cur, "/p/x.go") || !ta.undoStep() || ta.value() != "see @tea" {
		t.Fatalf("undo after typing: %q", ta.value())
	}
	ta.set("see @tea, and")
	if !ta.replaceAt(4, 8, "/p/x.go") || ta.value() != "see /p/x.go, and" {
		t.Fatalf("punctuation follows: %q", ta.value())
	}
	ta.set("@a now")
	if !ta.replaceAt(0, 2, "/p/a") || ta.value() != "/p/a now" {
		t.Fatalf("whitespace follows: %q", ta.value())
	}
	// two files, one after the other
	ta.set("@a")
	ta.cur = 2
	ta.replaceAt(0, 2, "/p/a")
	ta.insert("@b")
	q, start, ok := atToken(ta.text, ta.cur)
	if !ok || q != "b" || !ta.replaceAt(start, ta.cur, "/p/b") || ta.value() != "/p/a /p/b " {
		t.Fatalf("two files: %q (%q %d %v)", ta.value(), q, start, ok)
	}
	// the cap: never a path cut in two
	ta = textArea{limit: 12}
	ta.set("see @tea")
	if ta.replaceAt(4, 8, "/p/tea_compose.go") || ta.value() != "see @tea" {
		t.Fatalf("over the cap: %q", ta.value())
	}
}

// §5: compose.at_path is registered, absolute by default, and takes only
// absolute or relative.
func TestAtPathSetting(t *testing.T) {
	k, found := config.Lookup("compose.at_path")
	if !found || k.Default != "absolute" || k.Kind != config.String || k.Group() == "" {
		t.Fatalf("compose.at_path: %+v", k)
	}
	for v, ok := range map[string]bool{"absolute": true, "relative": true, "Absolute": false, "": false, "home": false} {
		if k.Valid(v) != ok {
			t.Errorf("%q: valid %v, want %v", v, !ok, ok)
		}
	}
}
