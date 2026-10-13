package rolecfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Grok's security read (2026-10-13): env_refs must not reach the variables that
// change how the client or its tools load code.
func TestEnvRefsRefuseLoaderAndHookVariables(t *testing.T) {
	f := newLaunchFixture(t)
	for _, target := range []string{"LD_LIBRARY_PATH", "LD_AUDIT", "DYLD_INSERT_LIBRARIES", "BASH_ENV", "ENV",
		"NODE_OPTIONS", "GIT_SSH_COMMAND", "PYTHONSTARTUP", "PERL5OPT", "RUBYOPT", "HERDR_PANE_ID", "HERDR_ENV", "PATH"} {
		body := "schema_version = 2\n[defaults]\nprofile = \"main\"\n[profiles.main]\nclient = \"codex\"\nprovider = \"p\"\nmodel = \"m\"\nenv_refs = { " +
			target + " = \"SRC\" }\n[roles.coder]\ntier = \"boot\"\nskills = []\n"
		must(t, os.WriteFile(f.manifest, []byte(body), 0o644))
		if _, err := Load(f.manifest); err == nil || !strings.Contains(err.Error(), "env_refs cannot override") {
			t.Errorf("env_refs %s accepted: %v", target, err)
		}
	}
}

// The API key reaches no client unless the profile asks for it by name.
func TestAPIKeyIsDroppedForEveryClient(t *testing.T) {
	f := newLaunchFixture(t)
	for _, client := range []string{"codex", "kimi-cli", "claude-code"} {
		m := f.config(client, "", "", "")
		if _, e, err := f.launch(m, "coder", "ANTHROPIC_API_KEY=secret"); err != nil {
			t.Fatal(err, e)
		}
		if _, ok := envOf(f.execEnv, "ANTHROPIC_API_KEY"); ok {
			t.Fatalf("%s inherited ANTHROPIC_API_KEY", client)
		}
	}
	m := f.config("codex", "env_refs = { ANTHROPIC_API_KEY = \"TEAM_KEY\" }", "", "")
	if _, e, err := f.launch(m, "coder", "TEAM_KEY=team", "ANTHROPIC_API_KEY=secret"); err != nil {
		t.Fatal(err, e)
	}
	if v, _ := envOf(f.execEnv, "ANTHROPIC_API_KEY"); v != "team" {
		t.Fatalf("env_refs ANTHROPIC_API_KEY = %q, want the named source", v)
	}
}

// Elevated permissions are said out loud before the client starts.
func TestElevatedPermissionsAreAnnounced(t *testing.T) {
	f := newLaunchFixture(t)
	for _, c := range []struct{ client, options, want string }{
		{"claude-code", "[profiles.main.options]\npermission = \"bypassPermissions\"", "permission=bypassPermissions"},
		{"codex", "[profiles.main.options]\nsandbox = \"danger-full-access\"\napproval = \"never\"", "sandbox=danger-full-access approval=never"},
		{"kimi-cli", "[profiles.main.options]\npermission = \"yolo\"", "permission=yolo"},
	} {
		m := f.config(c.client, "", "", c.options)
		_, errOut, err := f.launch(m, "coder")
		if err != nil || !strings.Contains(errOut, "notice: coder runs "+c.client+" with elevated "+c.want) {
			t.Fatalf("%s: stderr %q err %v", c.client, errOut, err)
		}
	}
	m := f.config("codex", "", "", "")
	if _, errOut, _ := f.launch(m, "coder"); strings.Contains(errOut, "elevated") {
		t.Fatalf("default sandbox announced as elevated: %q", errOut)
	}
}

// A skill link is named after the manifest's skill, not after whatever its
// directory resolves to.
func TestSkillLinkKeepsTheManifestName(t *testing.T) {
	f := newLaunchFixture(t)
	other := filepath.Join(f.root, "elsewhere", "agent-bus-security")
	must(t, os.MkdirAll(other, 0o755))
	must(t, os.WriteFile(filepath.Join(other, "SKILL.md"), []byte("x"), 0o644))
	must(t, os.RemoveAll(filepath.Join(f.skills, "test-skill")))
	must(t, os.Symlink(other, filepath.Join(f.skills, "test-skill")))
	m := f.config("codex", "", "", "")
	dest := filepath.Join(f.root, "dest")
	if err := InstallSkills(m, append(f.env, "SKILLS_DEST="+dest), &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "test-skill")); err != nil {
		t.Fatalf("link not named after the manifest skill: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "agent-bus-security")); err == nil {
		t.Fatal("a symlinked skill created a link under another skill's name")
	}
}

// The bind child names the pane the launcher was started in, before env_refs.
func TestBindPinsThePaneBeforeEnvRefs(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("codex", "", "", "")
	out, _, err := f.launch(m, "coder", "AGENT_LAUNCH_DRYRUN=1", "HERDR_ENV=1", "HERDR_PANE_ID=w9:p9", "AGENTBUS_BIN=/x/agentbus")
	must(t, err)
	if first := strings.SplitN(out, "\n", 2)[0]; !strings.Contains(first, " --pane w9:p9") {
		t.Fatalf("bind line %q does not pin the pane", first)
	}
}
