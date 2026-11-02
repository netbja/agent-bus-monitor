package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// ownProfileManifest: foureyes on claude, with codex as its one fallback;
// coder with none; a pop role with no tab.
const ownProfileManifest = `schema_version = 2
[profiles.claude]
client = "claude-code"
provider = "anthropic"
model = "sonnet"
options = { permission = "manual" }
[profiles.review]
client = "codex"
provider = "openai"
model = "gpt-x"
[profiles.other]
client = "codex"
provider = "openai"
model = "gpt-y"
[defaults]
profile = "claude"
[roles.master]
tier = "boot"
skills = ["agent-bus"]
[roles.foureyes]
tier = "boot"
skills = ["agent-bus"]
fallback_profiles = ["review"]
[roles.docs]
tier = "pop"
skills = ["agent-bus"]
`

func ownProfileFixture(t *testing.T, tabs string) (*rolecfg.Manifest, string, func(string) string) {
	t.Helper()
	dir := t.TempDir()
	manifest := write(t, filepath.Join(dir, "roles.toml"), ownProfileManifest)
	m, err := rolecfg.Load(manifest)
	if err != nil {
		t.Fatal(err)
	}
	projects := filepath.Join(dir, "projects")
	if tabs != "" {
		if err := os.MkdirAll(projects, 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(projects, "demo.toml"), "name = \"demo\"\nworking_dir = \"/w\"\n"+strings.ReplaceAll(tabs, "MANIFEST", manifest))
	}
	env := map[string]string{"AGENTBUS_PROJECTS_DIR": projects}
	return m, manifest, func(k string) string { return env[k] }
}

const (
	tabMaster      = "\n[[tabs]]\nname = \"master\"\ncommand = \"env ROLES_TOML=MANIFEST /r/scripts/agent-launch master demo\"\n"
	tabFoureyesOwn = "\n[[tabs]]\nname = \"foureyes\"\ncommand = \"env ROLES_TOML=MANIFEST /r/scripts/agent-launch foureyes demo\"\n"
	tabFoureyesRev = "\n[[tabs]]\nname = \"foureyes\"\ncommand = \"env ROLES_TOML=MANIFEST /r/scripts/agent-launch foureyes demo --profile review\"\n"
)

// A role's own profile in a project (spec "A role's own profile in a
// project"): its template tab's --profile, else roles.toml's; a profile that
// is neither its own nor a fallback, one roles.toml no longer defines, or an
// unreadable template is an error, never roles.toml's in silence.
func TestOwnProfile(t *testing.T) {
	for _, c := range []struct {
		name, tabs, role, want, err string
	}{
		{"no template", "", "foureyes", "claude", ""},
		{"a tab without --profile", tabMaster + tabFoureyesOwn, "foureyes", "claude", ""},
		{"a tab on a fallback", tabMaster + tabFoureyesRev, "foureyes", "review", ""},
		{"a pop with no tab", tabMaster, "docs", "claude", ""},
		{"a profile not allowed", strings.Replace(tabFoureyesRev, "review", "other", 1), "foureyes", "", "profile other is not foureyes's own nor one of its fallback_profiles"},
		{"a profile roles.toml lacks", strings.Replace(tabFoureyesRev, "review", "gone", 1), "foureyes", "", "profile gone is not foureyes's own nor one of its fallback_profiles"},
		{"an unparsable template", "this is [ not toml", "foureyes", "", "demo.toml"},
	} {
		m, _, env := ownProfileFixture(t, c.tabs)
		p, err := ownProfile(m, env, "demo", c.role)
		switch {
		case c.err == "" && (err != nil || p.Profile != c.want):
			t.Errorf("%s: %q, %v; want %q", c.name, p.Profile, err, c.want)
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%s: %q, %v; want an error naming %q", c.name, p.Profile, err, c.err)
		}
	}
}

// A tab's --profile resolves to its client: foureyes on review is codex, so
// checkClients and the trust gate look for codex (spec, reader table).
func TestTabProfileReadsTheTabsProfile(t *testing.T) {
	_, manifest, _ := ownProfileFixture(t, "")
	env := func(string) string { return "" }
	cmd := "env ROLES_TOML=" + manifest + " /r/scripts/agent-launch foureyes demo --profile review"
	if got := tabClient(cmd, env); got != "codex" {
		t.Fatalf("tabClient = %q, want codex", got)
	}
	if got := tabExecutable(cmd, env); got != "codex" {
		t.Fatalf("tabExecutable = %q, want codex", got)
	}
}

// A template tab whose --profile roles.toml no longer allows stops up before
// any service, workspace, tab or pane, with the tab and profile named, even
// with no client lookup wired (foureyes: not behind checkClients' early
// return); a busmon tab and an unknown role keep today's skip.
func TestUpRefusesATabProfileThatDoesNotResolve(t *testing.T) {
	work, templates, h, _, deps := upFixture(t)
	_, manifest, _ := ownProfileFixture(t, "")
	boot := deps.Bootstrap
	deps.Bootstrap = func(args []string) error {
		if err := boot(args); err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(templates, "demo.toml"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString("\n[[tabs]]\nname = \"foureyes\"\ncommand = \"env ROLES_TOML=" + manifest + " /r/scripts/agent-launch foureyes demo --profile other\"\n" +
			"\n[[tabs]]\nname = \"ghost\"\ncommand = \"env ROLES_TOML=" + manifest + " /r/scripts/agent-launch ghost demo\"\n")
		return err
	}
	deps.TabExe, deps.LookPath = nil, nil
	var started bool
	deps.Services = nil
	var out, errw bytes.Buffer
	code := runUp([]string{"demo", work}, deps, &out, &errw)
	if code != 1 || !strings.Contains(errw.String(), "tab foureyes: profile other is not foureyes's own nor one of its fallback_profiles") {
		t.Fatalf("up = %d, %q", code, errw.String())
	}
	for _, c := range h.calls {
		if strings.HasPrefix(c, "workspace create") || strings.HasPrefix(c, "tab create") || strings.HasPrefix(c, "pane run") {
			started = true
		}
	}
	if started || strings.Contains(errw.String(), "ghost") {
		t.Fatalf("opened something, or judged the unknown role: %q %q", h.calls, errw.String())
	}
}

// A tab for a role roles.toml no longer has keeps today's skip: up goes on
// past checkProfiles and opens the team (foureyes: with only that odd tab,
// the branch is held).
func TestUpSkipsAnUnknownRolesTab(t *testing.T) {
	work, templates, h, _, deps := upFixture(t)
	_, manifest, _ := ownProfileFixture(t, "")
	boot := deps.Bootstrap
	deps.Bootstrap = func(args []string) error {
		if err := boot(args); err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(templates, "demo.toml"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString("\n[[tabs]]\nname = \"ghost\"\ncommand = \"env ROLES_TOML=" + manifest + " /r/scripts/agent-launch ghost demo\"\n")
		return err
	}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 || strings.Contains(errw.String(), "ghost") {
		t.Fatalf("up = %d, %q", code, errw.String())
	}
	if !strings.Contains(strings.Join(h.calls, "\n"), "workspace create") {
		t.Fatalf("the team was not opened: %q", h.calls)
	}
}
