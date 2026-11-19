package relaunch

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// A template tab may name a role's alt profile (fable-profile-architect):
// architect on claude-fable resolves, a profile that is none of the role's
// own, fallbacks or alternates is refused, naming both lists.
func TestLaunchProfileTakesAnAlternate(t *testing.T) {
	m, err := rolecfg.Load(filepath.Join("..", "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := LaunchProfile(m, "architect", "claude-fable")
	if err != nil || p.Profile != "claude-fable" || p.Model != "fable" {
		t.Fatalf("architect on claude-fable: %+v, %v", p, err)
	}
	if _, err := LaunchProfile(m, "architect", "claude-sonnet"); err == nil || !strings.Contains(err.Error(), "alt_profiles") {
		t.Fatalf("architect on claude-sonnet: %v, want a refusal naming alt_profiles", err)
	}
	if own, err := LaunchProfile(m, "architect", ""); err != nil || own.Model != "opus" {
		t.Fatalf("architect's own: %+v, %v", own, err)
	}
}
