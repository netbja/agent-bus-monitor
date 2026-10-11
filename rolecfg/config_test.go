package rolecfg

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func repoPath(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadRepositoryManifests(t *testing.T) {
	for _, name := range []string{"roles.toml", filepath.Join("examples", "roles.mixed.toml")} {
		t.Run(name, func(t *testing.T) {
			path := repoPath(t, name)
			m, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if m.manifestPath != path {
				t.Fatalf("manifest path = %q, want %q", m.manifestPath, path)
			}
		})
	}
}

func TestRolesByTierPreservesManifestOrder(t *testing.T) {
	path := writeConfig(t, `schema_version = 2
[profiles.p]
client = "codex"
provider = "openai"
model = "test"
[roles.zed]
profile = "p"
tier = "boot"
[roles.alpha]
profile = "p"
tier = "pop"
[roles.beta]
profile = "p"
tier = "boot"
[roles.gamma]
profile = "p"
tier = "boot"
`)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := m.RolesByTier("boot"), []string{"zed", "beta", "gamma"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RolesByTier(boot) = %#v, want %#v", got, want)
	}
	if got, want := m.RolesByTier("pop"), []string{"alpha"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RolesByTier(pop) = %#v, want %#v", got, want)
	}
}

func TestRolesByTierPreservesInlineTableOrder(t *testing.T) {
	path := writeConfig(t, `roles = { zed = { model = "z", tier = "boot" }, alpha = { model = "a", tier = "pop" } }
`)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := m.RolesByTier("boot"), []string{"zed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RolesByTier(boot) = %#v, want %#v", got, want)
	}
}

func TestResolveSchemaTwoAndAdapterPath(t *testing.T) {
	path := writeConfig(t, `schema_version = 2
[defaults]
profile = "base"
[profiles.base]
client = "claude-code"
provider = "anthropic"
model = "sonnet"
[profiles.base.options]
permission = "acceptEdits"
[profiles.custom]
client = "local-client"
provider = "local"
model = "agent"
[profiles.custom.options]
max_turns = 3
labels = ["one", "two"]
[adapters.local-client]
executable = "bin/agent"
[roles.coder]
tier = "boot"
skills = ["tdd"]
[roles.custom]
profile = "custom"
tier = "pop"
skills = []
`)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Resolve("coder")
	if err != nil {
		t.Fatal(err)
	}
	if p.Client != "claude-code" || p.Provider != "anthropic" || p.Model != "sonnet" || p.Profile != "base" || p.Role != "coder" || p.Legacy || p.Manifest != path {
		t.Fatalf("resolved default profile = %+v", p)
	}
	if p.Options["permission"] != "acceptEdits" || !reflect.DeepEqual(p.Skills, []string{"tdd"}) || len(p.EnvRefs) != 0 {
		t.Fatalf("resolved fields = %+v", p)
	}
	custom, err := m.Resolve("custom")
	if err != nil {
		t.Fatal(err)
	}
	wantAdapter := filepath.Join(filepath.Dir(path), "bin", "agent")
	if custom.Adapter != wantAdapter || custom.Profile != "custom" || custom.Legacy {
		t.Fatalf("custom profile = %+v, adapter want %q", custom, wantAdapter)
	}
	if got := custom.OptionsRaw; got["max_turns"] != int64(3) || !reflect.DeepEqual(got["labels"], []interface{}{"one", "two"}) {
		t.Fatalf("custom options raw = %#v, want original TOML values", got)
	}
	if _, err := m.Resolve("missing"); err == nil || err.Error() != "unknown role: missing" {
		t.Fatalf("Resolve missing error = %v", err)
	}
}

func TestResolveAbsoluteAdapterPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "roles.toml")
	contents := fmt.Sprintf(`schema_version = 2
[profiles.p]
client = "local-client"
provider = "local"
model = "agent"
[roles.coder]
profile = "p"
tier = "boot"
[adapters.local-client]
executable = %q
`, filepath.Join(string(filepath.Separator), "opt", "agent"))
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Resolve("coder")
	if err != nil {
		t.Fatal(err)
	}
	if p.Adapter != filepath.Join(string(filepath.Separator), "opt", "agent") {
		t.Fatalf("adapter = %q, want absolute path", p.Adapter)
	}
}

func TestResolveLegacyProfile(t *testing.T) {
	path := writeConfig(t, `[roles.coder]
model = "opus"
permission = "manual"
fallback = "sonnet"
tier = "boot"
skills = ["agent-bus"]
`)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Resolve("coder")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Legacy || p.Client != "claude-code" || p.Provider != "anthropic" || p.Model != "opus" || p.Profile != "legacy" || p.Manifest != path {
		t.Fatalf("legacy profile = %+v", p)
	}
	if p.Options["permission"] != "manual" || p.Options["fallback"] != "sonnet" {
		t.Fatalf("legacy options = %#v", p.Options)
	}
}

func TestFieldRoleProfileAndOptionsPrecedence(t *testing.T) {
	path := writeConfig(t, `[roles.coder]
model = "role-model"
permission = "manual"
fallback = "sonnet"
tier = "boot"
skills = []
`)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		want []string
	}{{"model", []string{"role-model"}}, {"client", []string{"claude-code"}}, {"permission", []string{"manual"}}, {"fallback", []string{"sonnet"}}, {"tier", []string{"boot"}}} {
		values, ok, err := m.Field("coder", tc.name)
		if err != nil || ok != (tc.want != nil) || !reflect.DeepEqual(values, tc.want) {
			t.Errorf("Field(%q) = %#v, %v, %v; want %#v, %v", tc.name, values, ok, err, tc.want, tc.want != nil)
		}
	}
	if values, ok, err := m.Field("unknown", "model"); err != nil || ok || values != nil {
		t.Fatalf("missing role Field = %#v, %v, %v", values, ok, err)
	}
	v2 := writeConfig(t, `schema_version = 2
[defaults]
profile = "p"
[profiles.p]
client = "codex"
provider = "openai"
model = "test"
[profiles.p.options]
sandbox = "read-only"
[roles.coder]
tier = "boot"
`)
	m2, err := Load(v2)
	if err != nil {
		t.Fatal(err)
	}
	if values, ok, err := m2.Field("coder", "sandbox"); err != nil || !ok || !reflect.DeepEqual(values, []string{"read-only"}) {
		t.Fatalf("Field option = %#v, %v, %v", values, ok, err)
	}
}

func TestLoadRefusals(t *testing.T) {
	valid := func(root, defaults, profiles, role, suffix string) string {
		return root + defaults + profiles + role + suffix
	}
	base := func(profileFields, roleFields, root string) string {
		return valid("schema_version = 2\n", "[defaults]\nprofile = \"p\"\n", "[profiles.p]\n"+profileFields,
			"[roles.alpha]\nprofile = \"p\"\ntier = \"boot\"\nskills = []\n"+roleFields, root)
	}
	cases := []struct{ name, text, want string }{
		{"unsupported schema", "schema_version = 3\n[roles.alpha]\ntier=\"boot\"\n", "unsupported schema_version (expected 1 or 2)"},
		{"roles not table", "schema_version = 2\nroles = \"bad\"\n", "roles must be a table"},
		{"invalid role name", "schema_version = 1\n[roles.Bad]\ntier = \"boot\"\n", "invalid role name"},
		{"role not table", "schema_version = 1\nroles = { alpha = \"bad\" }\n", "role alpha must be a table"},
		{"invalid tier", "schema_version = 1\n[roles.alpha]\nmodel=\"m\"\ntier=\"middle\"\n", "role alpha: tier must be boot or pop"},
		{"invalid skills", "schema_version = 1\n[roles.alpha]\nmodel=\"m\"\ntier=\"boot\"\nskills=[1]\n", "role alpha: invalid skills"},
		{"unknown manifest key", "schema_version=2\nextra=1\n[defaults]\nprofile=\"p\"\n[profiles.p]\nclient=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\n[roles.alpha]\ntier=\"boot\"\n", "unknown fields in manifest: extra"},
		{"unknown defaults key", "schema_version=2\n[defaults]\nprofile=\"p\"\nextra=1\n[profiles.p]\nclient=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\n[roles.alpha]\ntier=\"boot\"\n", "unknown fields in defaults: extra"},
		{"defaults not table", "schema_version=2\ndefaults=\"bad\"\n[profiles.p]\nclient=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\n[roles.alpha]\ntier=\"boot\"\n", "defaults must be a table"},
		{"profiles empty", "schema_version=2\n[defaults]\nprofile=\"p\"\n[profiles]\n[roles.alpha]\ntier=\"boot\"\n", "profiles must be a non-empty table"},
		{"profiles not table", "schema_version=2\nprofiles=\"bad\"\n[defaults]\nprofile=\"p\"\n[roles.alpha]\ntier=\"boot\"\n", "profiles must be a non-empty table"},
		{"invalid profile name", "schema_version=2\n[defaults]\nprofile=\"Bad\"\n[profiles.Bad]\nclient=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\n[roles.alpha]\ntier=\"boot\"\n", "invalid profile name"},
		{"unknown profile", "schema_version=2\n[defaults]\nprofile=\"missing\"\n[profiles.p]\nclient=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\n[roles.alpha]\ntier=\"boot\"\n", "role alpha: unknown or missing profile"},
		{"missing role profile", "schema_version=2\n[defaults]\n[profiles.p]\nclient=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\n[roles.alpha]\ntier=\"boot\"\n", "role alpha: unknown or missing profile"},
		{"profile not table", "schema_version=2\n[defaults]\nprofile=\"p\"\n[profiles]\np=\"bad\"\n[roles.alpha]\ntier=\"boot\"\n", "profile p must be a table"},
		{"unknown role key", base("client=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\n", "extra=1\n", ""), "unknown fields in role alpha (move execution fields into a profile): extra"},
		{"unknown profile key", base("client=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\nextra=1\n", "", ""), "unknown fields in profile p: extra"},
		{"client must be string", base("client=1\nprovider=\"openai\"\nmodel=\"m\"\n", "", ""), "client must be a non-empty string without control characters"},
		{"provider must be nonempty", base("client=\"codex\"\nprovider=\" \"\nmodel=\"m\"\n", "", ""), "provider must be a non-empty string without control characters"},
		{"model must be nonempty", base("client=\"codex\"\nprovider=\"openai\"\nmodel=\"\"\n", "", ""), "model must be a non-empty string without control characters"},
		{"provider identifier", base("client=\"codex\"\nprovider=\"OpenAI\"\nmodel=\"m\"\n", "", ""), "invalid client/provider identifier"},
		{"model may not start with dash", base("client=\"codex\"\nprovider=\"openai\"\nmodel=\"-m\"\n", "", ""), "model must not start with '-'"},
		{"invalid client identifier", base("client=\"Bad client\"\nprovider=\"openai\"\nmodel=\"m\"\n", "", ""), "invalid client/provider identifier"},
		{"options must be table", base("client=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\noptions=\"bad\"\n", "", ""), "options must be a table"},
		{"builtin option must be string", base("client=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\n[profiles.p.options]\nsandbox=1\n", "", ""), "codex option sandbox must be a non-empty string without control characters"},
		{"unsupported Codex option", base("client=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\n[profiles.p.options]\npermission=\"manual\"\n", "", ""), "unknown fields in Codex options: permission"},
		{"unsupported Claude option", base("client=\"claude-code\"\nprovider=\"anthropic\"\nmodel=\"m\"\n[profiles.p.options]\nsandbox=\"read-only\"\n", "", ""), "unknown fields in Claude options: sandbox"},
		{"Claude permission", base("client=\"claude-code\"\nprovider=\"anthropic\"\nmodel=\"m\"\n[profiles.p.options]\npermission=\"yolo\"\n", "", ""), "unsupported Claude permission"},
		{"Claude fallback type", base("client=\"claude-code\"\nprovider=\"anthropic\"\nmodel=\"m\"\n[profiles.p.options]\nfallback=1\n", "", ""), "claude-code option fallback must be a non-empty string without control characters"},
		{"Claude fallback valid type", base("client=\"claude-code\"\nprovider=\"anthropic\"\nmodel=\"m\"\nreasoning=\"standard\"\nreasoning_levels=[\"standard\"]\n[profiles.p.options]\nfallback=\"sonnet\"\n", "", ""), "unsupported Claude reasoning level"},
		{"Codex sandbox", base("client=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\n[profiles.p.options]\nsandbox=\"full\"\n", "", ""), "unsupported Codex sandbox"},
		{"Codex approval", base("client=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\n[profiles.p.options]\napproval=\"ask\"\n", "", ""), "unsupported Codex approval"},
		{"Codex reasoning", base("client=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\nreasoning=\"standard\"\nreasoning_levels=[\"standard\"]\n", "", ""), "unsupported Codex reasoning level"},
		{"Kimi unknown option", base("client=\"kimi-cli\"\nprovider=\"moonshot\"\nmodel=\"alias\"\n[profiles.p.options]\nsandbox=\"read-only\"\n", "", ""), "unknown fields in Kimi options: sandbox"},
		{"Kimi permission", base("client=\"kimi-cli\"\nprovider=\"moonshot\"\nmodel=\"alias\"\n[profiles.p.options]\npermission=\"bypass\"\n", "", ""), "unsupported Kimi permission"},
		{"unsupported reasoning level", base("client=\"claude-code\"\nprovider=\"anthropic\"\nmodel=\"m\"\nreasoning=\"minimal\"\nreasoning_levels=[\"minimal\"]\n", "", ""), "unsupported Claude reasoning level"},
		{"Kimi cannot override reasoning", base("client=\"kimi-cli\"\nprovider=\"moonshot\"\nmodel=\"alias\"\nreasoning=\"low\"\nreasoning_levels=[\"low\"]\n", "", ""), "kimi-cli adapter has no verified reasoning override; omit reasoning and configure the native model alias"},
		{"invalid env ref", base("client=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\nenv_refs={bad=\"TEAM\"}\n", "", ""), "env_refs must map environment variable names to source variable names"},
		{"forbidden env ref", base("client=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\nenv_refs={PATH=\"TEAM\"}\n", "", ""), "env_refs cannot override launcher identity or process controls"},
		{"invalid reasoning levels", base("client=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\nreasoning_levels=[1]\n", "", ""), "reasoning_levels must be an array of level names"},
		{"undeclared reasoning", base("client=\"codex\"\nprovider=\"openai\"\nmodel=\"m\"\nreasoning=\"high\"\n", "", ""), "reasoning requires reasoning_levels declaring support for this model/provider"},
		{"unknown adapter", base("client=\"local\"\nprovider=\"local\"\nmodel=\"m\"\n", "", ""), "unknown client local: register an adapter"},
		{"adapter not table", base("client=\"local\"\nprovider=\"local\"\nmodel=\"m\"\n", "", "[adapters]\nlocal=\"bad\"\n"), "adapter local must be a table"},
		{"unknown adapter key", base("client=\"local\"\nprovider=\"local\"\nmodel=\"m\"\n", "", "[adapters.local]\nexecutable=\"/bin/true\"\nextra=1\n"), "unknown fields in adapter local: extra"},
		{"empty adapter executable", base("client=\"local\"\nprovider=\"local\"\nmodel=\"m\"\n", "", "[adapters.local]\nexecutable=\" \"\n"), "executable must be a non-empty string without control characters"},
		{"schema version is an integer", "schema_version=2.0\n[roles.alpha]\ntier=\"boot\"\n", "unsupported schema_version (expected 1 or 2)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.text)
			_, err := Load(path)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("Load error = %v, want %q", err, tc.want)
			}
		})
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "roles.toml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
