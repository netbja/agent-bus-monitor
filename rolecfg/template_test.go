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
		"\n[[tabs]]\nname = \"busmon\"\ncommand = \"'" + root + "/busmon' --project 'équipe & <x>'\"\n" +
		"\n[[tabs]]\nname = \"coder\"\ncommand = \"env 'ROLES_TOML=" + manifest + "' '" + scripts + "/agent-launch' coder 'équipe & <x>'\"\n"
	dispatchTab := "\n[[tabs]]\nname = \"dispatch\"\ncommand = \"'" + root + "/agentbus' --project 'équipe & <x>' dispatch\"\n"
	missionsTab := "\n[[tabs]]\nname = \"missions\"\ncommand = \"'" + root + "/agentbus' --project 'équipe & <x>' missions --repo '" + cwd + "'\"\n"
	for _, tc := range []struct {
		dispatch, missions bool
	}{
		{true, true}, {true, false}, {false, true}, {false, false},
	} {
		got := Template(m, manifest, project, cwd, scripts, root, "", tc.dispatch, tc.missions)
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
	got := Template(m, manifest, "demo", "/work", "/repo/scripts", "/repo", "", false, false)
	if !containsInOrder(got, "name = \"busmon\"", "name = \"zeta\"", "name = \"alpha\"") {
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

func TestTemplateBusmonFirstWithName(t *testing.T) {
	m := writeSkillsManifest(t,
		"[profiles.main]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"test\"\n",
		"[roles.coder]\ntier = \"boot\"\nskills = []\n")
	got := Template(m, "/r/roles.toml", "demo", "/w", "/r/scripts", "/r", "charlie", false, false)
	first := strings.Index(got, "[[tabs]]")
	if first < 0 || !strings.HasPrefix(got[first:], "[[tabs]]\nname = \"busmon\"\ncommand = \"/r/busmon --project demo --as charlie\"\n") {
		t.Fatalf("busmon is not the first tab with --as:\n%s", got)
	}
}

// teamManifest: three boot roles in this order, foureyes with one fallback,
// and a pop role.
func teamManifest(t *testing.T) (*Manifest, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "roles.toml")
	body := "schema_version = 2\n[defaults]\nprofile = \"main\"\n" +
		"[profiles.main]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\noptions = { permission = \"manual\" }\n" +
		"[profiles.review]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"gpt\"\n" +
		"[roles.master]\ntier = \"boot\"\nskills = []\n" +
		"[roles.coder]\ntier = \"boot\"\nskills = []\n" +
		"[roles.foureyes]\ntier = \"boot\"\nskills = []\nfallback_profiles = [\"review\"]\n" +
		"[roles.docs]\ntier = \"pop\"\nskills = []\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return m, path
}

// A team is boot roles, each optionally on its own profile or one of its
// fallback_profiles (agentbus-start spec, --roles r[:profile],...); anything
// else is refused with the rule.
func TestParseTeam(t *testing.T) {
	m, _ := teamManifest(t)
	team, err := m.ParseTeam("foureyes:review, master,coder:main")
	if err != nil {
		t.Fatal(err)
	}
	// Manifest order; the role's own profile reads as none.
	if len(team) != 3 || team[0] != (TeamRole{"master", ""}) || team[1] != (TeamRole{"coder", ""}) || team[2] != (TeamRole{"foureyes", "review"}) {
		t.Fatalf("team = %+v", team)
	}
	for spec, want := range map[string]string{
		"master,ghost":         "ghost is not a boot role",
		"master,docs":          "docs is not a boot role",
		"master,master":        "master twice",
		"master,coder:review":  "review is not coder's own profile nor one of its fallback_profiles",
		"master,foureyes:nope": "nope is not foureyes's own profile nor one of its fallback_profiles",
		"master,a:b:c":         "a:b:c",
		"master:":              "master:: want role or role:profile",
		"master,,coder":        "an empty role",
		"":                     "an empty role",
	} {
		if _, err := m.ParseTeam(spec); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseTeam(%q) = %v, want an error naming %q", spec, err, want)
		}
	}
}

// Without a team the template is today's, byte for byte; with one, only its
// roles get a tab, in manifest order, and a role off its own profile gets
// --profile.
func TestTemplateTeam(t *testing.T) {
	m, manifest := teamManifest(t)
	plain := Template(m, manifest, "demo", "/work", "/repo/scripts", "/repo", "charlie", true, false)
	if got := TemplateTeam(m, manifest, "demo", "/work", "/repo/scripts", "/repo", "charlie", true, false, nil); got != plain {
		t.Fatalf("TemplateTeam(nil) differs from Template:\n%s\n---\n%s", got, plain)
	}
	team, err := m.ParseTeam("master,foureyes:review")
	if err != nil {
		t.Fatal(err)
	}
	got := TemplateTeam(m, manifest, "demo", "/work", "/repo/scripts", "/repo", "", false, false, team)
	if strings.Contains(got, "name = \"coder\"") || !containsInOrder(got, "name = \"busmon\"", "name = \"master\"", "name = \"foureyes\"") {
		t.Fatalf("tabs:\n%s", got)
	}
	if !strings.Contains(got, "agent-launch foureyes demo --profile review\"") || strings.Contains(got, "agent-launch master demo --profile") {
		t.Fatalf("--profile:\n%s", got)
	}
}
