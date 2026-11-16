package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Quota settings step 2 (§2.2, the person's answers Q4, Q4b): the quota
// thresholds have per-project values in a [project.<name>] table of
// config.toml, read before the project's env file (the shell at up), then
// the machine keys, then the default. The process environment is never
// read for them: a service's environment is a start-time copy.
func TestProjectValue(t *testing.T) {
	path := writeConfig(t, "[dispatch]\nquota_warn = 70\nquota_hold = 97\n\n"+
		"[project.demo.dispatch]\nquota_warn = 80\nquota_drain = 90\n\n[project.other.dispatch]\nquota_warn = 60\n")
	f := Load(path, "config.toml")
	if len(f.Warnings) != 0 {
		t.Fatalf("a project table read as unknown keys: %v", f.Warnings)
	}
	env := map[string]string{EnvName("dispatch.quota_drain"): "88", EnvName("dispatch.quota_hold"): "96"}
	for _, c := range []struct {
		name, project string
		want          any
		source        string
	}{
		{"dispatch.quota_warn", "demo", 80.0, "config.toml [project.demo]"},  // the project's table first
		{"dispatch.quota_drain", "demo", 90.0, "config.toml [project.demo]"}, // over the env file's 88 (Q4b)
		{"dispatch.quota_hold", "demo", 96.0, "env file"},                    // then the env file
		{"dispatch.quota_warn", "nope", 70.0, "config.toml"},                 // then the machine key
		{"dispatch.quota_max_age", "demo", Lookup1("dispatch.quota_max_age").Default, "default"},
	} {
		got := ProjectValue(f, env, c.project, c.name)
		if got.Value != c.want || got.Source != c.source {
			t.Errorf("%s for %s: %v from %q, want %v from %q", c.name, c.project, got.Value, got.Source, c.want, c.source)
		}
	}
	// A value the key's rule refuses is never taken, from the table or the env file.
	bad := Load(writeConfig(t, "[project.demo.dispatch]\nquota_warn = 300\n"), "config.toml")
	if got := ProjectValue(bad, map[string]string{EnvName("dispatch.quota_warn"): "nope"}, "demo", "dispatch.quota_warn"); got.Source != "default" {
		t.Fatalf("a refused value used: %+v", got)
	}
	// Only the project-scoped keys have a project table; an invalid project
	// name is no table.
	odd := Load(writeConfig(t, "[project.demo.display]\ntheme = \"dark\"\n[project.Bad.dispatch]\nquota_warn = 80\n"), "config.toml")
	if len(odd.Warnings) != 2 {
		t.Fatalf("warnings %v", odd.Warnings)
	}
}

func Lookup1(name string) *Key { k, _ := Lookup(name); return k }

// projects delete removes the project's table (step 2, foureyes on r2):
// every [project.<p>...] section, nothing else.
func TestRemoveProject(t *testing.T) {
	text := "# mine\n[dispatch]\nquota_warn = 70\n\n[project.demo.dispatch]\nquota_warn = 80 # the demo\nquota_drain = 90\n\n" +
		"[project.demo2.dispatch]\nquota_warn = 60\n\n[display]\ntheme = \"dark\"\n"
	path := writeConfig(t, text)
	if err := RemoveProject(path, "demo"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	got := string(b)
	if strings.Contains(got, "project.demo.") || strings.Contains(got, "quota_drain") {
		t.Fatalf("the table stayed:\n%s", got)
	}
	for _, keep := range []string{"# mine", "quota_warn = 70", "[project.demo2.dispatch]", "quota_warn = 60", "theme = \"dark\""} {
		if !strings.Contains(got, keep) {
			t.Fatalf("%q lost:\n%s", keep, got)
		}
	}
	if err := RemoveProject(path, "demo"); err != nil { // nothing left: no edit
		t.Fatal(err)
	}
	if err := RemoveProject(filepath.Join(t.TempDir(), "none.toml"), "demo"); err != nil {
		t.Fatalf("a missing file: %v", err)
	}
	if err := RemoveProject(path, "Bad Name"); err == nil {
		t.Fatal("an invalid project name accepted")
	}
}
