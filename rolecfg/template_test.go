package rolecfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplateMatchesBootstrapOutput(t *testing.T) {
	m := writeSkillsManifest(t,
		"[profiles.main]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"test\"\n",
		"[roles.coder]\ntier = \"boot\"\nskills = []\n[roles.reviewer]\ntier = \"pop\"\nskills = []\n")
	root := filepath.Join(t.TempDir(), "repo with space")
	manifest := filepath.Join(root, "roles.toml")
	cwd := filepath.Join(root, "project with space")
	scripts := filepath.Join(root, "scripts")
	project := "équipe & <x>"

	base := "name = \"équipe & <x>\"\ndescription = \"Agent Bus team for équipe & <x>\"\nworking_dir = \"" + cwd + "\"\n" +
		"\n[[tabs]]\nname = \"coder\"\ncommand = \"env 'ROLES_TOML=" + manifest + "' '" + scripts + "/agent-launch' coder 'équipe & <x>'\"\n" +
		"\n[[tabs]]\nname = \"busmon\"\ncommand = \"'" + root + "/busmon' --project 'équipe & <x>'\"\n"
	dispatchTab := "\n[[tabs]]\nname = \"dispatch\"\ncommand = \"'" + root + "/agentbus' --project 'équipe & <x>' dispatch\"\n"
	missionsTab := "\n[[tabs]]\nname = \"missions\"\ncommand = \"'" + root + "/agentbus' --project 'équipe & <x>' missions --repo '" + cwd + "'\"\n"
	for _, tc := range []struct {
		dispatch, missions bool
	}{
		{true, true}, {true, false}, {false, true}, {false, false},
	} {
		got := Template(m, manifest, project, cwd, scripts, root, tc.dispatch, tc.missions)
		want := base
		if tc.dispatch {
			want += dispatchTab
		}
		if tc.missions {
			want += missionsTab
		}
		if got != want {
			t.Errorf("Template(%t, %t):\n got %q\nwant %q", tc.dispatch, tc.missions, got, want)
		}
	}
}

func TestTemplateBootRolesFollowManifestOrder(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "roles.toml")
	if err := os.WriteFile(manifest, []byte("schema_version = 2\n[defaults]\nprofile = \"main\"\n"+
		"[profiles.main]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"test\"\n"+
		"[roles.zeta]\ntier = \"boot\"\nskills = []\n[roles.alpha]\ntier = \"boot\"\nskills = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Load(manifest)
	if err != nil {
		t.Fatal(err)
	}
	got := Template(m, manifest, "demo", "/work", "/repo/scripts", "/repo", false, false)
	if !containsInOrder(got, "name = \"zeta\"", "name = \"alpha\"", "name = \"busmon\"") {
		t.Fatalf("tabs do not preserve manifest order:\n%s", got)
	}
}

func containsInOrder(text string, parts ...string) bool {
	last := -1
	for _, part := range parts {
		i := strings.Index(text, part)
		if i <= last {
			return false
		}
		last = i
	}
	return true
}

func TestTemplateJSONStringMatchesPythonEnsureASCIIFalse(t *testing.T) {
	value := "line\n\u2028 literal \\u2028 <>&\x7f\x01"
	want := "\"line\\n\u2028 literal \\\\u2028 <>&\x7f\\u0001\""
	if got := templateJSONString(value); got != want {
		t.Fatalf("templateJSONString(%q) = %q, want %q", value, got, want)
	}
}
