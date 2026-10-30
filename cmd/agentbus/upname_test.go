package main

import (
	"bytes"
	"strings"
	"testing"
)

// stranger-test: a stranger who types "agentbus up MyProject" is told why the name is refused
// and what would be accepted, not only the usage line.
func TestUpSaysWhyAProjectNameIsRefused(t *testing.T) {
	for _, c := range []struct{ name, suggest string }{
		{"MyProject", "myproject"},
		{"my project", ""},
		{"9lives", ""},
	} {
		var out, errw bytes.Buffer
		if code := runUp([]string{c.name, t.TempDir()}, upDeps{}, &out, &errw); code != 1 {
			t.Fatalf("%q: up = %d", c.name, code)
		}
		msg := errw.String()
		for _, want := range []string{`invalid project name "` + c.name + `"`, "a lowercase letter", "up to 32"} {
			if !strings.Contains(msg, want) {
				t.Errorf("%q: stderr lacks %q:\n%s", c.name, want, msg)
			}
		}
		if c.suggest != "" && !strings.Contains(msg, "agentbus up "+c.suggest) {
			t.Errorf("%q: no suggestion %q:\n%s", c.name, c.suggest, msg)
		}
		if c.suggest == "" && strings.Contains(msg, "try:") {
			t.Errorf("%q: suggested a name that is not valid either:\n%s", c.name, msg)
		}
	}
	var out, errw bytes.Buffer // the suggestion keeps the rest of the command, quoted when needed
	runUp([]string{"Demo", "/home/x/my code", "--as", "charlie"}, upDeps{}, &out, &errw)
	if !strings.Contains(errw.String(), "try: agentbus up demo '/home/x/my code' --as charlie") {
		t.Errorf("suggestion lost the rest of the command:\n%s", errw.String())
	}
	// every shell metacharacter is quoted, so the line pasted runs up and nothing else
	// (reviewer-codex, foureyes); a plain path and ~ stay as typed
	out.Reset()
	errw.Reset()
	runUp([]string{"Demo", "/tmp/work;id", "c`d`", "a|b", "~/code/x"}, upDeps{}, &out, &errw)
	if want := `try: agentbus up demo '/tmp/work;id' 'c` + "`d`" + `' 'a|b' ~/code/x`; !strings.Contains(errw.String(), want) {
		t.Errorf("metacharacters not quoted:\n%s\nwant %s", errw.String(), want)
	}
	out.Reset()
	errw.Reset() // no project at all: the usage line, as before
	if code := runUp(nil, upDeps{}, &out, &errw); code != 1 || !strings.HasPrefix(errw.String(), "usage: agentbus up") {
		t.Fatalf("no args: %d %q", code, errw.String())
	}
}
