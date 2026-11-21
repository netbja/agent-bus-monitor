package main

import (
	"fmt"
	"strings"
	"testing"
)

// numstat builds `git diff --no-renames --numstat` output: n files of l
// lines each (added), under dir.
func numstat(dir string, n, l int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "%d\t0\t%s/f%d.go\n", l, dir, i)
	}
	return b.String()
}

// single-approval §3.2, step 1: small decided by code from the merge-base
// diff.
func TestClassifySmall(t *testing.T) {
	for _, c := range []struct {
		name    string
		numstat string
		extra   []string
		small   bool
		docs    bool
		lines   int
		files   int
		matched string
	}{
		{"150 lines, 5 files", numstat("cmd/busmon", 5, 30), nil, true, false, 150, 5, ""},
		{"151 lines", numstat("cmd/busmon", 5, 30) + "1\t0\tcmd/busmon/g.go\n", nil, false, false, 151, 6, ""},
		{"151 lines, 5 files", numstat("cmd/busmon", 4, 30) + "30\t1\tcmd/busmon/g.go\n", nil, false, false, 151, 5, ""},
		{"6 files", numstat("cmd/busmon", 6, 1), nil, false, false, 6, 6, ""},
		{"2000-line docs (Q2: docs within the limits too)", "1500\t0\tdocs/superpowers/specs/a.md\n400\t100\tREADME.md\n0\t0\tdocs/plans/v.json\n", nil, false, true, 2000, 3, ""},
		{"150-line docs", "100\t0\tdocs/a.md\n50\t0\tREADME.md\n", nil, true, true, 150, 2, ""},
		{"6 docs files", numstat("docs", 6, 1), nil, false, true, 6, 6, ""},
		{"a .md outside docs", "10\t0\tcmd/busmon/NOTES.md\n", nil, true, true, 10, 1, ""},
		{"docs and code", "1500\t0\tdocs/a.md\n1\t0\tcmd/busmon/x.go\n", nil, false, false, 1501, 2, ""},
		{"bus, 1 line", "1\t0\tbus/x.go\n", nil, false, false, 1, 1, "bus/x.go"},
		{"a skill", "1\t0\tskills/a/SKILL.md\n", nil, false, false, 1, 1, "skills/a/SKILL.md"},
		{"config/keys.go", "1\t1\tconfig/keys.go\n", nil, false, false, 2, 1, "config/keys.go"},
		{"an .acl file at any depth", "1\t0\tdeploy/users.acl\n", nil, false, false, 1, 1, "deploy/users.acl"},
		{"the classifier itself", "1\t0\tcmd/agentbus/deliversmall.go\n", nil, false, false, 1, 1, "cmd/agentbus/deliversmall.go"},
		{"the single-approval rule", "1\t0\tcmd/agentbus/deliversingle.go\n", nil, false, false, 1, 1, "cmd/agentbus/deliversingle.go"},
		{"the project-only read of the settings", "1\t0\tconfig/file.go\n", nil, false, false, 1, 1, "config/file.go"},
		{"the refusal of an agent's write", "1\t0\tconfig/write.go\n", nil, false, false, 1, 1, "config/write.go"},
		{"any settings file", "1\t0\tconfig/branch.go\n", nil, false, false, 1, 1, "config/branch.go"},
		{"the glob matcher", "1\t0\tfilepick/ignore.go\n", nil, false, false, 1, 1, "filepick/ignore.go"},
		{"a binary", "-\t-\tdocs/logo.png\n", nil, false, false, 0, 1, ""},
		{"a rename counts two", "0\t10\tcmd/busmon/old.go\n10\t0\tcmd/busmon/new.go\n" + numstat("cmd/busmon", 4, 1), nil, false, false, 24, 6, ""},
		{"an added glob", "1\t0\tcmd/busmon/x.go\n", []string{"cmd/busmon/**"}, false, false, 1, 1, "cmd/busmon/x.go"},
		{"an empty setting keeps the defaults", "1\t0\tbus/x.go\n", []string{"", " "}, false, false, 1, 1, "bus/x.go"},
		{"-z: a non-ASCII never-small path", "1\t0\tbus/\u00e9.go\x00", nil, false, false, 1, 1, "bus/\u00e9.go"},
		{"-z: a newline in a path", "1\t0\tbus/a\nb.go\x001\t0\tdocs/a.md\x00", nil, false, false, 2, 2, "bus/a\nb.go"},
		{"-z: docs", "3\t0\tdocs/a.md\x004\t0\tREADME.md\x00", nil, true, true, 7, 2, ""},
		{"a quoted path cannot be read", "1\t0\t\"bus/\\303\\251.go\"\n", nil, false, false, 1, 1, ""},
		{"an added glob that matches nothing", "1\t0\tcmd/busmon/x.go\n", []string{"holder/**"}, true, false, 1, 1, ""},
	} {
		got := classify(c.numstat, c.extra, 150, 5)
		if got.Small != c.small || got.Docs != c.docs || got.Lines != c.lines || got.Files != c.files || got.Never != c.matched {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

// The line --dry-run prints, and its reason when not small.
func TestClassifyLine(t *testing.T) {
	for _, c := range []struct{ numstat, want string }{
		{"10\t2\tcmd/busmon/x.go\n", "small: 12 lines, 1 files"},
		{"20\t0\tdocs/a.md\n", "small (docs only): 20 lines, 1 files"},
		{"2000\t0\tdocs/a.md\n", "not small (2000 lines > 150): 2000 lines, 1 files"},
		{numstat("cmd/busmon", 2, 100), "not small (200 lines > 150): 200 lines, 2 files"},
		{numstat("cmd/busmon", 6, 1), "not small (6 files > 5): 6 lines, 6 files"},
		{"1\t0\tbus/stream.go\n", "not small (bus/stream.go is never small): 1 lines, 1 files"},
		{"-\t-\tdocs/logo.png\n", "not small (docs/logo.png is binary): 0 lines, 1 files"},
		{"1\t0\t\"q\\303.go\"\n", "not small (a path git quoted cannot be read): 1 lines, 1 files"},
		{"garbage\n", "not small (a row of the diff cannot be read): 0 lines, 1 files"},
	} {
		if got := classify(c.numstat, nil, 150, 5).String(); got != c.want {
			t.Errorf("%q: %q, want %q", c.numstat, got, c.want)
		}
	}
}
