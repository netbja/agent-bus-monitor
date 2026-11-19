package cmdtest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/models"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// CheckManifest: every model-detection §3.4 case, used and unused.
const CheckManifest = `schema_version = 2
[defaults]
profile = "main"
[profiles.main]
client = "claude-code"
provider = "anthropic"
model = "opus"
options = { permission = "manual" }
[profiles.pinned]
client = "claude-code"
provider = "anthropic"
model = "claude-opus-4-8"
options = { permission = "manual" }
[profiles.spare]
client = "codex"
provider = "openai"
tier = "workhorse"
[profiles.sol]
client = "codex"
provider = "openai"
model = "gpt-6-sol"
[profiles.old]
client = "codex"
provider = "openai"
model = "gpt-5-old"
[profiles.grok]
client = "grok"
provider = "xai"
model = "grok-4.6"
[profiles.kimi]
client = "kimi-cli"
provider = "moonshot"
tier = "frontier"
[profiles.grokfast]
client = "grok"
provider = "xai"
tier = "fast"
[profiles.gem]
client = "agy"
provider = "google"
model = "gemini-3.8-flash-high"
[roles.coder]
tier = "boot"
fallback_profiles = ["spare", "grok"]
[roles.master]
tier = "boot"
[roles.ui]
tier = "pop"
profile = "sol"
`

// ModelsCheckFixture writes, in dir, a fresh table (and its file young) and
// the manifest. It sets no getenv: call your side's isolate on dir first, or
// the code under test reads the real settings.
func ModelsCheckFixture(t *testing.T, dir string, now time.Time) (cache string, manifest func() (*rolecfg.Manifest, error)) {
	t.Helper()
	cache = filepath.Join(dir, "models.json")
	at := now.UnixMilli()
	tbl := &models.Table{Providers: map[string]*models.Provider{
		"anthropic": {OK: true, At: at, ReadAt: at, Partial: true, Rows: []models.Row{{ID: "opus", Alias: true, Default: true, Tier: models.Frontier}}},
		"openai": {OK: true, At: at, ReadAt: at, Rows: []models.Row{
			{ID: "gpt-6.1-sol", Tier: models.Workhorse, Default: true},
			{ID: "gpt-6-sol", Tier: models.Legacy, Newer: "gpt-6.1-sol"},
		}},
		"xai": {OK: true, At: at, ReadAt: at, Rows: []models.Row{{ID: "grok-4.7", Default: true, Tier: models.Frontier}, {ID: "grok-4.6"}}},
		// Kimi: one tier for the family (§6 Q3); a tier profile gets kimi-for-coding.
		"moonshot": {OK: true, At: at, ReadAt: at, Rows: []models.Row{{ID: "kimi-code/k3", Default: true, Tier: models.Frontier},
			{ID: "kimi-code/kimi-for-coding-highspeed", Tier: models.Frontier}, {ID: "kimi-code/kimi-for-coding", Tier: models.Frontier}}},
		"google": {OK: false, At: at, Error: "timeout", ReadAt: now.Add(-9 * 24 * time.Hour).UnixMilli(), Rows: []models.Row{{ID: "gemini-3.8-flash-high"}}},
	}}
	if err := tbl.Save(cache); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "roles.toml")
	if err := os.WriteFile(path, []byte(CheckManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return cache, func() (*rolecfg.Manifest, error) { return rolecfg.Load(path) }
}

// AdapterRoles writes CheckManifest plus an adapter with a models argv
// (deep, provider deepseek) as roles.toml, points ROLES_TOML at it and
// returns its path.
func AdapterRoles(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/roles.toml"
	m := CheckManifest + "[adapters.deep]\nexecutable = \"./d\"\nmodels = [\"./d\"]\n[profiles.ds]\nclient = \"deep\"\nprovider = \"deepseek\"\nmodel = \"v4\"\n"
	if err := os.WriteFile(path, []byte(m), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROLES_TOML", path)
	return path
}

// ModelsEnv is the models settings' getenv of an isolated home: dir for
// HOME and XDG_CONFIG_HOME, maxAge for the source's max age when not "".
func ModelsEnv(dir, maxAge string) func(string) string {
	return func(k string) string {
		switch k {
		case "HOME", "XDG_CONFIG_HOME":
			return dir
		case config.EnvName("models.source_max_age"):
			return maxAge
		}
		return ""
	}
}

// NoEnv: a client is never called (a test reads no real client).
func NoEnv() models.Env {
	return models.Env{Getenv: func(string) string { return "" }, Home: "/nonexistent",
		Read: func(string) ([]byte, error) { return nil, errors.New("no file") },
		Run:  func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("not installed") }}
}

// Age makes the table's last refresh d old (every provider's last attempt).
func Age(t *testing.T, path string, d time.Duration) {
	t.Helper()
	tbl, err := models.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range tbl.Providers {
		p.At = time.Now().Add(-d).UnixMilli()
	}
	if err := tbl.Save(path); err != nil {
		t.Fatal(err)
	}
}

// CountingEnv lists grok-4.8 from the grok client and counts the calls.
func CountingEnv(calls *int, block <-chan struct{}) models.Env {
	e := NoEnv()
	e.Run = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name != "grok" {
			return nil, errors.New("not installed")
		}
		*calls++
		if block != nil {
			<-block // ignores its deadline: the refresh must not wait for it
		}
		return []byte("Default model: grok-4.8\n\nAvailable models:\n  * grok-4.8 (default)\n"), nil
	}
	return e
}
