package rolecfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A project's template may name its delivery base (dev-branch-flow §4.1);
// a project without the line, or without a template, keeps the machine's.
func TestDeliveryBaseFromTheTemplate(t *testing.T) {
	cases := []struct {
		name, tpl string // tpl "" = no template file
		want      string
		ok        bool
		err       string
	}{
		{"no template", "", "", false, ""},
		{"no delivery table", "name = \"demo\"\n[[tabs]]\nname = \"coder\"\ncommand = \"x\"\n", "", false, ""},
		{"an empty base", "[delivery]\nbase = \"\"\n", "", false, ""},
		{"dev", "name = \"demo\"\n[delivery]\nbase = \"dev\"\n[[tabs]]\nname = \"coder\"\ncommand = \"x\"\n", "dev", true, ""},
		{"a nested branch", "[delivery]\nbase = \"release/next\"\n", "release/next", true, ""},
		{"not a branch name", "[delivery]\nbase = \"a..b\"\n", "", false, "not a valid branch name"},
		{"HEAD", "[delivery]\nbase = \"HEAD\"\n", "", false, "not a valid branch name"},
		{"not a string", "[delivery]\nbase = 3\n", "", false, "demo.toml"},
		{"broken TOML", "[delivery\nbase = \"dev\"\n", "", false, "demo.toml"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if c.tpl != "" {
				if err := os.WriteFile(filepath.Join(dir, "demo.toml"), []byte(c.tpl), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			env := func(k string) string {
				if k == "AGENTBUS_PROJECTS_DIR" {
					return dir
				}
				return ""
			}
			got, ok, err := DeliveryBase(env, "demo")
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want one naming %q", err, c.err)
				}
				return
			}
			if err != nil || got != c.want || ok != c.ok {
				t.Fatalf("DeliveryBase = %q, %v, %v; want %q, %v, nil", got, ok, err, c.want, c.ok)
			}
		})
	}
}

// The path it reads is the one the person edits: the projects directory's
// <project>.toml, and it is named in every error.
func TestDeliveryBaseTemplatePath(t *testing.T) {
	env := func(k string) string {
		if k == "AGENTBUS_PROJECTS_DIR" {
			return "/x/projects"
		}
		return ""
	}
	if got := TemplatePath(env, "demo"); got != "/x/projects/demo.toml" {
		t.Fatalf("TemplatePath = %q", got)
	}
}
