package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// quota-per-provider (the person, 2026-11-25): a provider's own warn, drain
// and hold, for each provider the model table and the quota readers know, in
// a [quota.<provider>] table; per project in [project.<p>.quota.<provider>].
// The person's alone (busmon's Shift+C or config.toml, no env variable),
// 0 = the default's (dispatch.quota_*), read by the dispatcher each minute.
func TestQuotaProviderKeys(t *testing.T) {
	if want := []string{"anthropic", "google", "moonshot", "openai", "xai"}; !slices.Equal(QuotaProviders, want) {
		t.Fatalf("QuotaProviders %v, want %v", QuotaProviders, want)
	}
	for _, p := range QuotaProviders {
		for _, level := range QuotaLevelNames {
			name := QuotaProviderKey(p, level)
			if name != "quota."+p+"."+level {
				t.Fatalf("QuotaProviderKey(%s, %s) = %s", p, level, name)
			}
			k, ok := Lookup(name)
			if !ok {
				t.Fatalf("%s is not a key", name)
			}
			if k.Kind != Float || k.Default != 0.0 || !k.Project || k.Env != "" || !k.PersonEdit || k.PersonOnly ||
				k.ReadBy != "dispatch, agentbus quota" || k.Group() != GroupServices || k.Applies() != AppliesLive {
				t.Fatalf("%s: %+v", name, k)
			}
		}
	}
	path := writeConfig(t, "[quota.openai]\nhold = 80\nwarn = 0\n\n[quota.xai]\nhold = 101\n\n[project.demo.quota.openai]\nwarn = 70\n")
	f := Load(path, "config.toml")
	if len(f.Warnings) != 1 || f.Warnings[0].Key != "quota.xai.hold" || !strings.Contains(f.Warnings[0].Msg, "0 to 100") {
		t.Fatalf("warnings %v, want one for quota.xai.hold", f.Warnings)
	}
	for _, c := range []struct {
		project, name string
		want          float64
		source        string
	}{
		{"demo", "quota.openai.warn", 70, "config.toml [project.demo]"}, // the project's table first
		{"demo", "quota.openai.hold", 80, "config.toml"},                // then the machine's
		{"other", "quota.openai.warn", 0, "config.toml"},                // 0: the default's, as written
		{"demo", "quota.xai.hold", 0, "default"},                        // refused: unset
	} {
		got := ProjectValue(f, nil, c.project, c.name)
		if got.Value != c.want || got.Source != c.source {
			t.Errorf("%s for %s: %v from %q, want %v from %q", c.name, c.project, got.Value, got.Source, c.want, c.source)
		}
	}
	// The person's: agentbus config set refuses it, busmon writes it.
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := Set(cfg, "quota.openai.hold", 80.0); err == nil {
		t.Fatal("Set wrote a person's key")
	}
	if err := SetAsPerson(cfg, "quota.openai.hold", 80.0); err != nil {
		t.Fatal(err)
	}
	if got := ProjectValue(Load(cfg, "config.toml"), nil, "demo", "quota.openai.hold"); got.Value != 80.0 {
		t.Fatalf("after SetAsPerson: %+v", got)
	}
}

// master's directive (the person's providers): [quota.<provider>] takes any
// valid provider id, not only the five with a usage reader; Lookup gives such
// a key its row (the five's rules), the file names the providers it sets.
func TestQuotaAnyProvider(t *testing.T) {
	k, ok := Lookup("quota.deepseek.hold")
	if !ok || k.Kind != Float || !k.Project || !k.PersonEdit || k.Env != "" || k.Group() != GroupServices || k.Applies() != AppliesLive {
		t.Fatalf("quota.deepseek.hold: %+v %v", k, ok)
	}
	if again, _ := Lookup("quota.deepseek.hold"); again != k {
		t.Fatal("Lookup gives a provider key a new row each call")
	}
	for _, bad := range []string{"quota.Deep.hold", "quota.deepseek.pause", "quota.deepseek", "quota.claude.probe.hold", "quota..hold"} {
		if _, ok := Lookup(bad); ok {
			t.Errorf("Lookup(%q) is a key", bad)
		}
	}
	if k, ok := Lookup("quota.claude.probe"); !ok || k.Kind != Bool {
		t.Fatalf("quota.claude.probe: %+v", k)
	}
	path := writeConfig(t, "[quota.deepseek]\nhold = 80\n\n[quota.Bad]\nhold = 70\n\n[project.demo.quota.mistral]\nwarn = 60\n\n[quota.openai]\nwarn = 70\n")
	f := Load(path, "config.toml")
	if len(f.Warnings) != 1 || f.Warnings[0].Key != "quota.Bad.hold" {
		t.Fatalf("warnings %v, want one for quota.Bad.hold", f.Warnings)
	}
	if got := ProjectValue(f, nil, "demo", "quota.mistral.warn"); got.Value != 60.0 {
		t.Fatalf("a project's provider: %+v", got)
	}
	if got := ProjectValue(f, nil, "demo", "quota.deepseek.hold"); got.Value != 80.0 {
		t.Fatalf("a machine provider: %+v", got)
	}
	if got := f.QuotaProviders(); !slices.Equal(got, []string{"deepseek", "mistral", "openai"}) {
		t.Fatalf("QuotaProviders %v", got)
	}
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := SetAsPerson(cfg, "quota.groq.hold", 90.0); err != nil {
		t.Fatal(err)
	}
	if got := ProjectValue(Load(cfg, "config.toml"), nil, "x", "quota.groq.hold"); got.Value != 90.0 {
		t.Fatalf("after SetAsPerson: %+v", got)
	}
}
