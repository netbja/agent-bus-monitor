package mission

import (
	"strings"
	"testing"
)

// A missions.json without "base" takes the project's delivery base
// (dev-branch-flow §4.2), main only when none is given; its own base wins.
func TestParseBaseDefault(t *testing.T) {
	cases := []struct{ src, def, want string }{
		{validJSON, "dev", "dev"},
		{validJSON, "", "main"},
		{strings.Replace(validJSON, `"change":`, `"base": "release", "change":`, 1), "dev", "release"},
	}
	for _, c := range cases {
		f, err := ParseBase([]byte(c.src), c.def)
		if err != nil {
			t.Fatal(err)
		}
		if f.Base != c.want {
			t.Errorf("default %q: base %q, want %q", c.def, f.Base, c.want)
		}
	}
	if f, _ := Parse([]byte(validJSON)); f.Base != "main" {
		t.Errorf("Parse: base %q, want main", f.Base)
	}
}

// The conductor loads a change without a base of its own onto the project's
// base: the change branch starts from dev, and the record says so.
func TestLoadTakesTheConductorsBase(t *testing.T) {
	h := newHarness(t, "true")
	run(t, h.repo, "git", "branch", "dev", "main")
	devHead := strings.TrimSpace(run(t, h.repo, "git", "rev-parse", "dev"))
	commitFile(t, h.repo, "README.md", "main moves on\n", "main only")
	h.c.Base = "dev"
	data := `{"change": "other", "reviewer": "codex", "missions": [
 {"id": "a", "title": "A", "agent": "kimi", "allowed_paths": ["src/**"], "test": "true", "brief": "plan.md#a"}]}`
	commitFile(t, h.repo, "docs/changes/other/plan.md", "# a\n", "brief")
	commitFile(t, h.repo, "docs/changes/other/missions.json", data, "other")
	if ok, msg := h.op("load", "docs/changes/other/missions.json@HEAD"); !ok {
		t.Fatalf("load: %q", msg)
	}
	rec, ok, err := h.c.Store.Change(h.ctx, "other")
	if err != nil || !ok || rec.Base != "dev" {
		t.Fatalf("record base %q (ok %v, err %v), want dev", rec.Base, ok, err)
	}
	if got := strings.TrimSpace(run(t, h.repo, "git", "rev-parse", "change/other")); got != devHead {
		t.Errorf("change/other at %s, want dev's head %s", got, devHead)
	}
	// A later pass keeps the recorded base even if the project's moves:
	// the change is parsed with its record's base, never the conductor's.
	h.c.Base = "main"
	ch, err := h.c.loadChange(h.ctx, "other")
	if err != nil || ch == nil || ch.file.Base != "dev" {
		t.Fatalf("loadChange: file base %v (err %v), want dev", ch, err)
	}
	h.pass()
	if rec, _, _ := h.c.Store.Change(h.ctx, "other"); rec.Base != "dev" {
		t.Errorf("after a pass the record says %q, want dev", rec.Base)
	}
}
