// Package scrub keeps private references out of the tracked tree (scrub,
// v0.25): hosts, projects and people of the machines this was developed on,
// and the CLI's old default sender. docs/memory is left out: its notes are
// edited by hand when a reader reaches them.
package scrub

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// denied are matched case-insensitively, as whole words.
var denied = regexp.MustCompile(`(?i)\b(host1|hermes|hyperliquid|ai-tradex(-solana)?|polymarket|sysnet|project-c|myprojecttools|herdr-radar)\b`)

// maintainer is the maintainer's name: allowed only in history (master:
// specs, releases, plans, memory and the CHANGELOG); anywhere else, tests and
// fixtures included, the sample person is charlie and prose says "the
// person" (reviewer-codex).
var maintainer = regexp.MustCompile(`(?i)\bbernard\b`)

func maintainerAllowed(path string) bool {
	for _, p := range []string{"docs/superpowers/", "docs/releases/", "docs/plans/", "docs/memory/", "specs/"} {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return path == "CHANGELOG.md"
}

func TestNoPrivateReferences(t *testing.T) {
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("not a git checkout: nothing tracked to check")
	}
	top := strings.TrimSpace(string(root))
	out, err := exec.Command("git", "-C", top, "ls-files", "-z").Output()
	if err != nil {
		t.Fatal(err)
	}
	self := "internal/scrub/scrub_test.go"
	for _, f := range bytes.Split(out, []byte{0}) {
		path := string(f)
		if path == "" || path == self || strings.HasPrefix(path, "docs/memory/") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(top, path))
		if err != nil || bytes.IndexByte(data, 0) >= 0 { // gone from the worktree, or binary
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			if m := denied.FindString(line); m != "" {
				t.Errorf("%s:%d: %q is a private reference (scrub): use a neutral name (demo, myproject, your-host, user, anonymous)", path, i+1, m)
			}
			if m := maintainer.FindString(line); m != "" && !maintainerAllowed(path) {
				t.Errorf("%s:%d: %q outside history and tests (scrub): a sample person is charlie, prose says the person", path, i+1, m)
			}
		}
	}
}

// The pattern itself: each term caught, a neighbour word not.
func TestDenylistPattern(t *testing.T) {
	for _, s := range []string{"ssh user@host1", "AGENT_BUS_AGENT=hermes", "Hyperliquid", "ai-tradex-solana", "the project-c hook"} {
		if !denied.MatchString(s) {
			t.Errorf("%q not caught", s)
		}
	}
	for _, c := range []struct {
		path string
		ok   bool
	}{{"README.md", false}, {"skills/x/SKILL.md", false}, {"roles.toml", false}, {"cmd/busmon/human.go", false},
		{"cmd/busmon/human_test.go", false}, {"cmd/busmon/testdata/golden/x.txt", false}, {"tests/bootstrap_test.sh", false},
		{"docs/releases/v0.6.0.md", true}, {"docs/memory/INDEX.md", true}, {"CHANGELOG.md", true}} {
		if maintainerAllowed(c.path) != c.ok {
			t.Errorf("%s: allowed %v, want %v", c.path, !c.ok, c.ok)
		}
	}
	for _, s := range []string{"host10", "hermetic", "project-config", "helper", "anonymous"} {
		if denied.MatchString(s) {
			t.Errorf("%q caught", s)
		}
	}
}
