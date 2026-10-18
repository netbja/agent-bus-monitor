package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rolesFixture writes a schema-2 manifest with a boot and a pop role and
// returns an environment pointing ROLES_TOML at it.
func rolesFixture(t *testing.T) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	manifest := filepath.Join(dir, "roles.toml")
	body := "schema_version = 2\n[defaults]\nprofile = \"main\"\n[profiles.main]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"m1\"\n" +
		"[profiles.main.options]\nsandbox = \"workspace-write\"\n" +
		"[roles.coder]\ntier = \"boot\"\nskills = [\"a\", \"b\"]\n[roles.architect]\ntier = \"pop\"\n[roles.master]\ntier = \"boot\"\n"
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return manifest, []string{"ROLES_TOML=" + manifest, "AGENTBUS_REPO=" + dir}
}

func roles(env []string, args ...string) (int, string, string) {
	var out, errw bytes.Buffer
	code := runRoles(args, env, &out, &errw)
	return code, out.String(), errw.String()
}

func TestRolesQueries(t *testing.T) {
	_, env := rolesFixture(t)
	if code, out, _ := roles(env, "tier", "boot"); code != 0 || out != "coder\nmaster\n" {
		t.Fatalf("tier boot = %d %q, want manifest order", code, out)
	}
	if code, _, _ := roles(env, "exists", "coder"); code != 0 {
		t.Fatalf("exists coder = %d", code)
	}
	if code, _, _ := roles(env, "exists", "nobody"); code != 3 {
		t.Fatalf("exists nobody = %d, want 3", code)
	}
	for field, want := range map[string]string{"model": "m1\n", "skills": "a\nb\n", "sandbox": "workspace-write\n", "tier": "boot\n", "fallback": ""} {
		if code, out, _ := roles(env, "field", "coder", field); code != 0 || out != want {
			t.Fatalf("field %s = %d %q, want %q", field, code, out, want)
		}
	}
	if code, _, _ := roles(env, "field", "nobody", "model"); code != 3 {
		t.Fatalf("field of a missing role = %d, want 3", code)
	}
	if code, _, _ := roles(env, "validate"); code != 0 {
		t.Fatalf("validate = %d", code)
	}
}

func TestRolesErrors(t *testing.T) {
	manifest, env := rolesFixture(t)
	if err := os.WriteFile(manifest, []byte("schema_version = 3\n[roles.x]\ntier = \"boot\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := roles(env, "validate"); code != 1 || errOut != "agent roles: unsupported schema_version (expected 1 or 2)\n" {
		t.Fatalf("validate = %d %q", code, errOut)
	}
	if code, _, errOut := roles(env, "bogus"); code != 1 || !strings.HasPrefix(errOut, "agent roles: ") {
		t.Fatalf("unknown action = %d %q", code, errOut)
	}
	if code, _, errOut := roles(env); code != 1 || errOut != "agent roles: expected action: launch, install, validate, exists, tier, field, template, check-toml\n" {
		t.Fatalf("no action = %d %q", code, errOut)
	}
}

func TestRolesLaunchResumeRoutesToClaudeLauncher(t *testing.T) {
	manifest, env := rolesFixture(t)
	repo := strings.TrimPrefix(env[1], "AGENTBUS_REPO=")
	if err := os.MkdirAll(filepath.Join(repo, "roles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "roles", "coder.md"), []byte("coder prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `schema_version = 2
[defaults]
profile = "main"
[profiles.main]
client = "claude-code"
provider = "anthropic"
model = "sonnet"
options = { permission = "bypassPermissions" }
[tool_rules]
deny = ["Read(**/.env)"]
[roles.coder]
tier = "boot"
`
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	env = append(env, "AGENT_LAUNCH_DRYRUN=1")
	code, out, errOut := roles(env, "launch", "coder", "demo", "--resume", "session-123")
	if code != 0 {
		t.Fatalf("launch resume = %d: %s", code, errOut)
	}
	for _, want := range []string{"claude --model sonnet --resume session-123", "--permission-mode bypassPermissions", "--settings", "Read(**/.env)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("resume dry run lacks %q: %s", want, out)
		}
	}
	code, _, errOut = roles(env, "launch", "coder", "demo", "--resume")
	if code != 1 || !strings.Contains(errOut, "usage: agent-launch") {
		t.Fatalf("missing resume id = %d %q", code, errOut)
	}
	code, _, errOut = roles(env, "launch", "coder", "demo", "--resume", "")
	if code != 1 || !strings.Contains(errOut, "resume session id is required") {
		t.Fatalf("empty resume id = %d %q", code, errOut)
	}
}

func TestRolesTemplateAndCheckToml(t *testing.T) {
	manifest, env := rolesFixture(t)
	code, out, errOut := roles(env, "template", "demo", "/work dir", "/repo/scripts", "/repo", "1", "0")
	if code != 0 {
		t.Fatalf("template = %d %s", code, errOut)
	}
	for _, want := range []string{`name = "demo"`, `working_dir = "/work dir"`, `name = "coder"`, `name = "master"`, `"env ROLES_TOML=` + manifest, `name = "dispatch"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("template lacks %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, `name = "architect"`) || strings.Contains(out, `name = "missions"`) {
		t.Fatalf("template has a pop role or missions:\n%s", out)
	}
	tpl := filepath.Join(t.TempDir(), "t.toml")
	if err := os.WriteFile(tpl, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := roles(env, "check-toml", tpl); code != 0 {
		t.Fatalf("check-toml of the template = %d %s", code, errOut)
	}
	if err := os.WriteFile(tpl, []byte("name = \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := roles(env, "check-toml", tpl); code != 1 {
		t.Fatalf("check-toml of broken TOML = %d, want 1", code)
	}
}

func TestRolesManifestDefaultsToRepo(t *testing.T) {
	manifest, _ := rolesFixture(t)
	env := []string{"AGENTBUS_REPO=" + filepath.Dir(manifest)}
	if code, out, errOut := roles(env, "tier", "pop"); code != 0 || out != "architect\n" {
		t.Fatalf("tier pop with <repo>/roles.toml = %d %q %s", code, out, errOut)
	}
}
