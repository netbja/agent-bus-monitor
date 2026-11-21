package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// single-approval §3.1 and §4.8 (build step 2): five per-project delivery
// keys. The three that widen the gate (single_approval, small_max_lines,
// small_max_files) and same_provider_ok are the person's (§6 Q3, §4.8):
// PersonOnly, no env variable, Set refuses them. never_small only narrows
// it, so agentbus may write it.
func TestSingleApprovalKeys(t *testing.T) {
	for _, c := range []struct {
		name        string
		kind        Kind
		def         any
		person      bool
		list        bool
		projectOnly bool
	}{
		{"delivery.single_approval", String, "off", true, false, false},
		{"delivery.small_max_lines", Int, 150, true, false, false},
		{"delivery.small_max_files", Int, 5, true, false, false},
		{"delivery.never_small", String, "", false, true, false},
		{"delivery.same_provider_ok", String, "off", true, false, true},
	} {
		k, ok := Lookup(c.name)
		if !ok {
			t.Errorf("%s: not a key", c.name)
			continue
		}
		if k.Kind != c.kind || k.Default != c.def || !k.Project || k.Env != "" || !k.MachineOnly ||
			k.PersonOnly != c.person || k.List != c.list || k.ProjectOnly != c.projectOnly || k.Group() != GroupServices {
			t.Errorf("%s: %+v", c.name, k)
		}
		if c.person {
			if err := Set(filepath.Join(t.TempDir(), "config.toml"), c.name, c.def); err == nil || !strings.Contains(err.Error(), "the person's own setting") {
				t.Errorf("%s: Set: %v", c.name, err)
			}
		}
	}
	if err := Set(filepath.Join(t.TempDir(), "config.toml"), "delivery.never_small", []string{"cmd/busmon/**", "holder/**"}); err != nil {
		t.Errorf("never_small: Set: %v", err)
	}
}

// same_provider_ok is read from [project.<name>.delivery] alone (§4.8): another
// project's value does not leak, a machine-level line is ignored with a
// warning, and a value outside off|docs|small is refused. The other keys
// follow ProjectValue (the project's table, then the machine's, then the
// default). A PersonOnly key with a per-project value is new: this test is
// where it is pinned.
func TestSingleApprovalKeysPerProject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	text := `[delivery]
same_provider_ok = "small"
single_approval = "small"
small_max_lines = 300

[project.morning.delivery]
same_provider_ok = "docs"
small_max_files = 9

[project.bad.delivery]
same_provider_ok = "yes"
single_approval = "on"
small_max_lines = 0
`
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	f := Load(path, "config.toml")
	get := func(project, name string) any { return ProjectValue(f, nil, project, name).Value }
	for _, c := range []struct {
		project, name string
		want          any
	}{
		{"morning", "delivery.same_provider_ok", "docs"},
		{"other", "delivery.same_provider_ok", "off"}, // neither leaks: not morning's, not the machine line
		{"bad", "delivery.same_provider_ok", "off"},
		{"morning", "delivery.single_approval", "small"}, // the machine's
		{"bad", "delivery.single_approval", "small"},     // its own refused: the machine's
		{"morning", "delivery.small_max_lines", 300},
		{"bad", "delivery.small_max_lines", 300},
		{"morning", "delivery.small_max_files", 9},
		{"other", "delivery.small_max_files", 5},
		{"other", "delivery.never_small", ""},
	} {
		if got := get(c.project, c.name); got != c.want {
			t.Errorf("%s %s: %v, want %v", c.project, c.name, got, c.want)
		}
	}
	warned := map[string]bool{}
	for _, w := range f.Warnings {
		warned[w.Key] = true
		if w.Key == "delivery.same_provider_ok" && !strings.Contains(w.Msg, "[project.<name>]") {
			t.Errorf("warning %q", w.Msg)
		}
	}
	for _, k := range []string{"delivery.same_provider_ok", "project.bad.delivery.same_provider_ok", "project.bad.delivery.single_approval", "project.bad.delivery.small_max_lines"} {
		if !warned[k] {
			t.Errorf("no warning for %s: %+v", k, f.Warnings)
		}
	}
}
