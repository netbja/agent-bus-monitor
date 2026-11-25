package rolecfg

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// observer-role (the person, 2026-11-25): a read-only role for a session
// agentbus does not launch (claude-ao, the Orchestrator). kind = "observer"
// in roles.toml: no profile nor any launch field; it is kept out of the
// launch order (RoleNames, tiers, Resolve), so start, pop, relaunch and
// self-repair never see it, and named by Observers and IsObserver.
func TestObserverRoleOutOfTheLaunchOrder(t *testing.T) {
	m, err := loadManifestText(t, observerBase+"[roles.observer]\nkind = \"observer\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsObserver("observer") || !slices.Contains(m.Observers(), "observer") {
		t.Fatalf("observers %v", m.Observers())
	}
	if slices.Contains(m.RoleNames(), "observer") || slices.Contains(m.RolesByTier("boot"), "observer") || slices.Contains(m.RolesByTier("pop"), "observer") {
		t.Fatal("an observer is in the launch order")
	}
	if _, err := m.Resolve("observer"); err == nil || !strings.Contains(err.Error(), "observer") {
		t.Fatalf("Resolve(observer): %v", err)
	}
	if m.IsObserver("coder") || m.IsObserver("nobody") {
		t.Fatal("a launched or unknown role is an observer")
	}
}

// The person's choice (a) on foureyes' reject: the previous release's
// update parses this release's roles.toml with its own loader, which refuses
// kind = "observer". So the repository's manifest declares no observer until
// the release after the one that carries update-old-binary: the person adds
// it to their own manifest (ROLES_TOML) meanwhile.
func TestRepositoryManifestDeclaresNoObserverYet(t *testing.T) {
	m, err := Load(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if obs := m.Observers(); len(obs) != 0 {
		t.Fatalf("the repository's roles.toml declares %v: the previous release's update cannot read it (one release after update-old-binary)", obs)
	}
}

func loadManifestText(t *testing.T, text string) (*Manifest, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "roles.toml")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

const observerBase = `schema_version = 2
[defaults]
profile = "p"
[profiles.p]
client = "claude-code"
provider = "anthropic"
model = "opus"
[profiles.p.options]
permission = "auto"
[roles.coder]
tier = "boot"
`

func TestObserverRoleRules(t *testing.T) {
	m, err := loadManifestText(t, observerBase+"[roles.watcher]\nkind = \"observer\"\n[roles.audit-bot]\nkind = \"observer\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Observers(); !slices.Equal(got, []string{"watcher", "audit-bot"}) || !slices.Equal(m.RoleNames(), []string{"coder"}) {
		t.Fatalf("observers %v, roles %v", got, m.RoleNames())
	}
	for name, text := range map[string]string{
		"a launch field":    "[roles.watcher]\nkind = \"observer\"\nprofile = \"p\"\n",
		"a tier":            "[roles.watcher]\nkind = \"observer\"\ntier = \"pop\"\n",
		"tool rules":        "[roles.watcher]\nkind = \"observer\"\n[roles.watcher.tool_rules]\nallow = [\"Bash(ls)\"]\n",
		"delivers":          "[roles.watcher]\nkind = \"observer\"\ndelivers = true\n",
		"an unknown kind":   "[roles.watcher]\nkind = \"external\"\n",
		"kind not a string": "[roles.watcher]\nkind = true\n",
		"a bad name":        "[roles.Watcher]\nkind = \"observer\"\n",
		"the system sender": "[roles.agentbus]\nkind = \"observer\"\n",
	} {
		if _, err := loadManifestText(t, observerBase+text); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
