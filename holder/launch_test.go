package holder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testManifest = "schema_version = 2\n[defaults]\nprofile = \"main\"\n" +
	"[profiles.main]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\noptions = { permission = \"manual\" }\n" +
	"[profiles.alt]\nclient = \"claude-code\"\nprovider = \"other\"\nmodel = \"m2\"\noptions = { permission = \"manual\" }\n" +
	"[profiles.spare]\nclient = \"claude-code\"\nprovider = \"third\"\nmodel = \"m3\"\noptions = { permission = \"manual\" }\n" +
	"[profiles.grok]\nclient = \"grok\"\nprovider = \"xai\"\nmodel = \"grok-4.6\"\n" +
	"[roles.coder]\ntier = \"boot\"\nfallback_profiles = [\"alt\"]\n" +
	"[roles.scout]\ntier = \"pop\"\nprofile = \"grok\"\n"

func testLauncher(t *testing.T) TemplateLaunch {
	t.Helper()
	dir := t.TempDir()
	m := filepath.Join(dir, "roles.toml")
	if err := os.WriteFile(m, []byte(testManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return TemplateLaunch{Project: "demo", Launcher: "/r/scripts/agent-launch", Manifest: m, Dir: dir}
}

// Start builds the command itself from the role's launch template (spec 4.2,
// security S2a), as agent-launch runs today: a role of the manifest, its own
// profile or one of its fallback_profiles, a resume only for a resumable
// client and a session id. Never anything the frame names beyond that.
func TestTemplateLaunch(t *testing.T) {
	l := testLauncher(t)
	const sess = "0f1e2d3c-aaaa-bbbb-cccc-000000000001"
	for _, c := range []struct {
		start Start
		want  string
	}{
		{Start{Role: "scout"}, "env ROLES_TOML=" + l.Manifest + " /r/scripts/agent-launch scout demo"}, // fresh: fine
		{Start{Role: "coder"}, "env ROLES_TOML=" + l.Manifest + " /r/scripts/agent-launch coder demo"},
		{Start{Role: "coder", Profile: "main"}, "env ROLES_TOML=" + l.Manifest + " /r/scripts/agent-launch coder demo"},
		{Start{Role: "coder", Profile: "alt"}, "env ROLES_TOML=" + l.Manifest + " /r/scripts/agent-launch coder demo --profile alt"},
		{Start{Role: "coder", ResumeSession: sess}, "env ROLES_TOML=" + l.Manifest + " /r/scripts/agent-launch coder demo --resume " + sess},
		{Start{Role: "coder", Profile: "alt", ResumeSession: sess}, "env ROLES_TOML=" + l.Manifest + " /r/scripts/agent-launch coder demo --profile alt --resume " + sess},
	} {
		cmd, err := l.Command(c.start)
		if err != nil || strings.Join(cmd.Argv, " ") != c.want || cmd.Dir != l.Dir {
			t.Errorf("%+v: %q in %q %v, want %q", c.start, cmd.Argv, cmd.Dir, err, c.want)
		}
	}
	for name, s := range map[string]Start{
		"a role not in the manifest":                     {Role: "master"},
		"a profile not the role's":                       {Role: "coder", Profile: "spare"},
		"a profile not in the manifest":                  {Role: "coder", Profile: "ghost"},
		"an invalid frame":                               {Role: "Coder"},
		"a resume of a client without a verified resume": {Role: "scout", ResumeSession: "0f1e2d3c-aaaa-bbbb-cccc-000000000001"},
	} {
		if cmd, err := l.Command(s); err == nil {
			t.Errorf("%s: %q", name, cmd.Argv)
		}
	}
	bad := l
	bad.Manifest = filepath.Join(t.TempDir(), "none.toml")
	if _, err := bad.Command(Start{Role: "coder"}); err == nil {
		t.Error("a missing manifest")
	}
	rel := l
	rel.Launcher = "agent-launch"
	if _, err := rel.Command(Start{Role: "coder"}); err == nil {
		t.Error("a relative launcher")
	}
}
