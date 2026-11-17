package rolecfg

import (
	"slices"
	"testing"
)

// alt_profiles (fable-profile-architect): other profiles a role may run on
// when asked (pop --profile, a template's --profile), the same provider
// allowed, never a quota fallback (swap reads fallback_profiles alone).
func TestAltProfiles(t *testing.T) {
	head := "schema_version = 2\n[defaults]\nprofile = \"opus\"\n" +
		"[profiles.opus]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\noptions = { permission = \"manual\" }\n" +
		"[profiles.fable]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"fable\"\noptions = { permission = \"manual\" }\n" +
		"[profiles.sonnet]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"sonnet\"\noptions = { permission = \"manual\" }\n" +
		"[profiles.haiku]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"haiku\"\noptions = { permission = \"manual\" }\n" +
		"[profiles.k]\nclient = \"kimi-cli\"\nprovider = \"moonshot\"\nmodel = \"alias\"\n" +
		"[profiles.x]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"m\"\n"
	role := func(extra string) string {
		return head + "[roles.architect]\ntier = \"pop\"\nskills = [\"s\"]\n" + extra
	}
	refusals := map[string]string{
		`alt_profiles = ["missing"]`:                          "role architect: unknown alt profile missing",
		`alt_profiles = ["opus"]`:                             "role architect: alt profile opus is the role's own profile",
		`alt_profiles = ["fable", "fable"]`:                   "role architect: alt profile fable listed twice",
		`alt_profiles = ["fable", "sonnet", "haiku", "k"]`:    "role architect: at most 3 alt_profiles",
		`alt_profiles = "fable"`:                              "role architect: alt_profiles must be an array of profile names",
		"alt_profiles = [\"k\"]\nfallback_profiles = [\"k\"]": "role architect: profile k is both an alt and a fallback profile",
	}
	for extra, want := range refusals {
		if _, err := Load(writeConfig(t, role(extra))); err == nil || err.Error() != want {
			t.Errorf("%s: err = %v, want %q", extra, err, want)
		}
	}
	v1 := "schema_version = 1\n[roles.alpha]\nmodel=\"m\"\ntier=\"boot\"\npermission=\"auto\"\nalt_profiles=[\"x\"]\n"
	if _, err := Load(writeConfig(t, v1)); err == nil || err.Error() != "role alpha: alt_profiles needs schema_version 2" {
		t.Fatalf("v1: %v", err)
	}

	// The same provider is allowed: an alternate is chosen, never a quota escape.
	m, err := Load(writeConfig(t, role("alt_profiles = [\"fable\"]\nfallback_profiles = [\"x\"]")))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Alternates("architect"); !slices.Equal(got, []string{"fable"}) {
		t.Fatalf("Alternates = %v", got)
	}
	if got := m.Fallbacks("architect"); !slices.Equal(got, []string{"x"}) {
		t.Fatalf("Fallbacks = %v (an alternate is never a fallback)", got)
	}
	for p, want := range map[string]bool{"": true, "opus": true, "fable": true, "x": true, "sonnet": false, "k": false} {
		if got := m.MayRunOn("architect", p); got != want {
			t.Errorf("MayRunOn(%q) = %v, want %v", p, got, want)
		}
	}
	// A session recorded on an alternate resumes on it, never silently on
	// the role's own profile; another same-client profile still resumes on
	// its own (resume-record-profile).
	if run, onOwn, err := m.ResumeProfile("architect", "fable"); err != nil || run != "fable" || onOwn {
		t.Errorf("ResumeProfile(fable) = %q, %v, %v; want fable", run, onOwn, err)
	}
	if run, onOwn, err := m.ResumeProfile("architect", "sonnet"); err != nil || run != "" || !onOwn {
		t.Errorf("ResumeProfile(sonnet) = %q, %v, %v; want its own", run, onOwn, err)
	}
}

// The shipped manifest: architect may be popped on Fable, by the model
// table's alias (never a model id from memory), and its own profile stays
// Opus.
func TestShippedArchitectFable(t *testing.T) {
	m, err := Load(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	own, err := m.Resolve("architect")
	if err != nil || own.Model != "opus" {
		t.Fatalf("architect's own: %+v, %v", own, err)
	}
	if !slices.Contains(m.Alternates("architect"), "claude-fable") {
		t.Fatalf("architect's alt_profiles = %v", m.Alternates("architect"))
	}
	p, err := m.ResolveWith("architect", "claude-fable")
	if err != nil || p.Model != "fable" || p.Client != "claude-code" || p.Options["permission"] != own.Options["permission"] {
		t.Fatalf("claude-fable: %+v, %v", p, err)
	}
}
