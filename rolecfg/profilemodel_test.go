package rolecfg

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// model-detection §3.4: models check walks every profile, used or not, in
// declaration order, and reads each one's provider and model or tier.
func TestProfileModelAndNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roles.toml")
	manifest := `schema_version = 2
[defaults]
profile = "main"
[profiles.main]
client = "claude-code"
provider = "anthropic"
model = "opus"
options = { permission = "manual" }
[profiles.spare]
client = "codex"
provider = "openai"
tier = "workhorse"
[roles.coder]
tier = "boot"
fallback_profiles = ["spare"]
[roles.master]
tier = "boot"
`
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.ProfileNames(); !reflect.DeepEqual(got, []string{"main", "spare"}) {
		t.Fatalf("profiles %v", got)
	}
	if got := m.RoleNames(); !reflect.DeepEqual(got, []string{"coder", "master"}) {
		t.Fatalf("roles %v", got)
	}
	if p, model, tier, ok := m.ProfileModel("main"); !ok || p != "anthropic" || model != "opus" || tier != "" {
		t.Fatalf("main: %s %s %s %v", p, model, tier, ok)
	}
	if p, model, tier, ok := m.ProfileModel("spare"); !ok || p != "openai" || model != "" || tier != "workhorse" {
		t.Fatalf("spare: %s %s %s %v", p, model, tier, ok)
	}
	if _, _, _, ok := m.ProfileModel("none"); ok {
		t.Fatal("an unknown profile")
	}
}
