package models

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidTier(t *testing.T) {
	cases := map[string]bool{
		Frontier: true, Workhorse: true, Fast: true, Legacy: true,
		"": false, "Frontier": false, "slow": false,
	}
	for in, want := range cases {
		if got := ValidTier(in); got != want {
			t.Errorf("ValidTier(%q) = %v, want %v", in, got, want)
		}
	}
}

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfig(t *testing.T) {
	builtins := len(DefaultTiers())
	cases := []struct {
		name     string
		body     *string // nil = no file
		wantErr  string
		rules    int
		families map[string][]string
		tierOf   map[string]string // model id -> tier through the loaded rules
	}{
		{name: "missing file is the built-in rules", rules: builtins, families: map[string][]string{}},
		{name: "empty file", body: ptr(""), rules: builtins, families: map[string][]string{}},
		{
			name: "the person's rules come first",
			body: ptr(`
[[tier]]
match = "^my-model-"
tier = "legacy"

[families.grok]
order = ["grok-4.7", "grok-4.6"]
`),
			rules:    builtins + 1,
			families: map[string][]string{"grok": {"grok-4.7", "grok-4.6"}},
			tierOf:   map[string]string{"my-model-x": Legacy},
		},
		{name: "not TOML", body: ptr("[[tier"), wantErr: "models.toml"},
		{name: "bad regexp", body: ptr("[[tier]]\nmatch = \"(\"\ntier = \"fast\"\n"), wantErr: "tier rule 1"},
		{name: "unknown tier", body: ptr("[[tier]]\nmatch = \"x\"\ntier = \"slow\"\n"), wantErr: "tier rule 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "models.toml")
			if c.body != nil {
				writeFile(t, path, *c.body)
			}
			cfg, err := LoadConfig(path)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.Tiers) != c.rules {
				t.Errorf("%d rules, want %d", len(cfg.Tiers), c.rules)
			}
			if !reflect.DeepEqual(cfg.Families, c.families) {
				t.Errorf("families = %v, want %v", cfg.Families, c.families)
			}
			for id, want := range c.tierOf {
				if got := cfg.Tiers.Tier(Row{ID: id}); got != want {
					t.Errorf("Tier(%q) = %q, want %q", id, got, want)
				}
			}
		})
	}
}

func TestLoadConfigUnreadable(t *testing.T) {
	// A directory where the file should be: a read error, not "missing".
	if _, err := LoadConfig(t.TempDir()); err == nil {
		t.Fatal("a directory read as models.toml")
	}
}

func ptr(s string) *string { return &s }

func TestNewerByOrder(t *testing.T) {
	cfg := Config{Families: map[string][]string{
		"grok": {"grok-4.7", "grok-4.6", "grok-4.5"},
		"solo": {"solo-1"},
	}}
	cases := map[string]string{
		"grok-4.6":          "grok-4.7",
		"grok-4.5-20260101": "grok-4.7", // a dated release is its base id
		"grok-4.5[1m]":      "grok-4.7", // a context suffix too
		"grok-4.7":          "",         // already the newest
		"solo-1":            "",
		"unknown":           "",
	}
	for id, want := range cases {
		if got := cfg.NewerByOrder(id); got != want {
			t.Errorf("NewerByOrder(%q) = %q, want %q", id, got, want)
		}
	}
	if got := (Config{}).NewerByOrder("grok-4.6"); got != "" {
		t.Errorf("no families: %q", got)
	}
}

func TestPathsFollowXDG(t *testing.T) {
	cases := []struct {
		name string
		fn   func() string
		env  string
		want []string
	}{
		{"config", ConfigPath, "XDG_CONFIG_HOME", []string{"agentbus", "models.toml"}},
		{"cache", CachePath, "XDG_CACHE_HOME", []string{"agentbus", "models.json"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(c.env, dir)
			want := filepath.Join(append([]string{dir}, c.want...)...)
			if got := c.fn(); got != want {
				t.Errorf("= %q, want %q", got, want)
			}
		})
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "models.json")
	in := &Table{Providers: map[string]*Provider{
		"anthropic": {OK: true, At: 2, ReadAt: 1, Partial: true, Rows: []Row{{Provider: "anthropic", Client: "claude", ID: "opus", Alias: true}}},
	}}
	if err := in.Save(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temporary file left behind: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("round trip = %+v, want %+v", out.Providers["anthropic"], in.Providers["anthropic"])
	}
}

func TestLoad(t *testing.T) {
	cases := []struct {
		name    string
		body    *string
		dir     bool
		wantErr bool
		n       int
	}{
		{name: "missing", n: 0},
		{name: "null providers", body: ptr(`{"providers":null}`), n: 0},
		{name: "one provider", body: ptr(`{"providers":{"openai":{"ok":true,"rows":[]}}}`), n: 1},
		{name: "not JSON", body: ptr(`{`), wantErr: true},
		{name: "a directory", dir: true, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "models.json")
			if c.body != nil {
				writeFile(t, path, *c.body)
			}
			if c.dir {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			tab, err := Load(path)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if tab == nil || tab.Providers == nil {
				t.Fatal("Load returned no usable table")
			}
			if !c.wantErr && len(tab.Providers) != c.n {
				t.Errorf("%d providers, want %d", len(tab.Providers), c.n)
			}
		})
	}
}

func TestSaveFailsWhereNoFolderCanBeMade(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	writeFile(t, file, "x")
	tab := &Table{Providers: map[string]*Provider{}}
	if err := tab.Save(filepath.Join(file, "sub", "models.json")); err == nil {
		t.Fatal("saved under a regular file")
	}
}

func TestFresh(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	tab := &Table{Providers: map[string]*Provider{
		"recent": {ReadAt: now.Add(-time.Hour).UnixMilli()},
		"old":    {ReadAt: now.Add(-MaxAge - time.Minute).UnixMilli()},
		"never":  {},
	}}
	cases := map[string]bool{"recent": true, "old": false, "never": false, "absent": false}
	for p, want := range cases {
		if _, got := tab.Fresh(p, now); got != want {
			t.Errorf("Fresh(%q) = %v, want %v", p, got, want)
		}
	}
}

// RealEnv's runner looks the command up first: a name on no PATH is an
// error and nothing is run.
func TestRealEnvRunRefusesAMissingCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	env := RealEnv()
	if env.Getenv == nil || env.Read == nil || env.Run == nil {
		t.Fatal("RealEnv left a hook nil")
	}
	if _, err := env.Run(context.Background(), "agentbus-no-such-client"); err == nil {
		t.Fatal("a missing command ran")
	}
}
