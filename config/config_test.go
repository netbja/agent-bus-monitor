package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The registry is the spec's table: every row, once, with a default of its kind.
func TestRegistryRows(t *testing.T) {
	want := []string{
		"waiting.popup_after", "waiting.snooze", "waiting.bell", "waiting.fade", "recap.auto_after",
		"display.theme", "display.limit", "identity.as", "updates.check",
		"dispatch.context_alert", "dispatch.prompt_grace", "dispatch.quota", "dispatch.quota_warn",
		"dispatch.quota_hold", "dispatch.quota_max_age", "dispatch.quota_long_hold", "dispatch.tokens",
		"dispatch.auto_clear", "dispatch.stall_after", "dispatch.clear_stall_after", "dispatch.quiet_after", "history.keep_days", "history.max_mb", "bus.report_max", "bus.report_full_max", "bus.verdict_max",
		"health.runaway.silent_after", "health.runaway.context_growth", "health.runaway.growth_span",
		"health.runaway.screen_repeats",
	}
	if len(Keys) != len(want) {
		t.Fatalf("registry has %d rows, want %d", len(Keys), len(want))
	}
	names, envs := map[string]bool{}, map[string]bool{}
	for i, k := range Keys {
		if k.Name != want[i] {
			t.Errorf("row %d = %s, want %s", i, k.Name, want[i])
		}
		if names[k.Name] || (k.Env != "" && envs[k.Env]) {
			t.Errorf("%s: duplicate name or env %q", k.Name, k.Env)
		}
		names[k.Name], envs[k.Env] = true, true
		if k.ReadBy == "" {
			t.Errorf("%s: no reader named", k.Name)
		}
		if !k.Kind.holds(k.Default) {
			t.Errorf("%s: default %#v is not a %v", k.Name, k.Default, k.Kind)
		}
		if k.env != nil {
			if _, res := k.env("\x00junk"); (res == envInvalid) != (k.Invalid != "") {
				t.Errorf("%s: an invalid env value and its consequence (%q) do not match", k.Name, k.Invalid)
			}
		}
		if got, ok := Lookup(k.Name); !ok || got != k {
			t.Errorf("Lookup(%s) does not return its row", k.Name)
		}
	}
	// The connection and every secret are env only: no row names them.
	for _, secret := range []string{"REDIS_URL", "REDIS_PASSWORD", "REDIS_PASSWORD_FILE"} {
		if envs[secret] {
			t.Errorf("%s has a file key", secret)
		}
	}
}

// Each env rule is today's, byte for byte: accepted values win, ignored ones
// fall through, invalid ones stop at env (the caller decides what that means).
func TestEnvRulesUnchanged(t *testing.T) {
	type c struct {
		key, raw string
		want     any    // value when used
		res      string // "use", "skip", "invalid"
	}
	cases := []c{
		{"display.theme", "dark", "dark", "use"},
		{"display.theme", "blue", nil, "invalid"},
		{"display.limit", " -1 ", -1, "use"},
		{"display.limit", "0", 0, "use"},
		{"display.limit", "abc", nil, "skip"},
		{"identity.as", "charlie", "charlie", "use"},
		{"identity.as", "Charlie!", nil, "invalid"},
		{"updates.check", "1", false, "use"},
		{"updates.check", "0", true, "use"},
		{"dispatch.context_alert", "70", 70.0, "use"},
		{"dispatch.context_alert", "0", 0.0, "use"},
		{"dispatch.context_alert", "101", nil, "skip"},
		{"dispatch.context_alert", "x", nil, "skip"},
		{"dispatch.prompt_grace", "0", time.Duration(0), "use"},
		{"dispatch.prompt_grace", "3600", time.Hour, "use"},
		{"dispatch.stall_after", "0", time.Duration(0), "use"},
		{"dispatch.stall_after", "900", 15 * time.Minute, "use"},
		{"dispatch.quiet_after", "30m", 30 * time.Minute, "use"},
		{"dispatch.clear_stall_after", "-5m", 15 * time.Minute, "skip"},
		{"dispatch.clear_stall_after", "48h", 15 * time.Minute, "skip"},
		{"dispatch.prompt_grace", "3601", nil, "skip"},
		{"dispatch.prompt_grace", "1.5", nil, "skip"},
		{"dispatch.quota", "off", false, "use"},
		{"dispatch.quota", "no", true, "use"},
		{"dispatch.quota_warn", "70", 70.0, "use"},
		{"dispatch.quota_warn", "x", nil, "skip"},
		{"dispatch.quota_warn", "0", nil, "skip"},
		{"dispatch.quota_hold", "-5", nil, "skip"},
		{"dispatch.quota_max_age", "0s", nil, "skip"},
		{"dispatch.quota_max_age", "-1m", nil, "skip"},
		{"dispatch.quota_hold", "99.5", 99.5, "use"},
		{"dispatch.quota_max_age", "10m", 10 * time.Minute, "use"},
		{"dispatch.quota_max_age", "ten", nil, "skip"},
		{"dispatch.quota_long_hold", "0", time.Duration(0), "use"},
		{"dispatch.quota_long_hold", "3h", 3 * time.Hour, "use"},
		{"dispatch.quota_long_hold", "-1h", nil, "skip"},
		{"dispatch.quota_long_hold", "x", nil, "skip"},
		{"dispatch.tokens", "0", false, "use"},
		{"dispatch.tokens", "false", true, "use"},
		{"dispatch.auto_clear", "1", true, "use"},
		{"dispatch.auto_clear", "true", false, "use"},
		{"bus.report_max", "200", 200, "use"},
		{"bus.report_max", "0", nil, "skip"},
		{"bus.report_full_max", "-3", nil, "skip"},
		{"bus.verdict_max", "x", nil, "skip"},
		{"health.runaway.silent_after", "45m", 45 * time.Minute, "use"},
		{"health.runaway.silent_after", "0s", nil, "invalid"},
		{"health.runaway.context_growth", "5000", 5000, "use"},
		{"health.runaway.context_growth", "x", nil, "invalid"},
		{"health.runaway.growth_span", "x", nil, "invalid"},
		{"health.runaway.screen_repeats", "-2", nil, "invalid"},
	}
	for _, x := range cases {
		k, ok := Lookup(x.key)
		if !ok {
			t.Fatalf("no row %s", x.key)
		}
		got, res := k.env(x.raw)
		names := map[envResult]string{envUse: "use", envSkip: "skip", envInvalid: "invalid"}
		if names[res] != x.res || (res == envUse && got != x.want) {
			t.Errorf("%s=%q: got %#v %s, want %#v %s", k.Env, x.raw, got, names[res], x.want, x.res)
		}
	}
}

func TestPrecedencePerKey(t *testing.T) {
	cfg := Load(writeFile(t, "config.toml", "[dispatch]\ncontext_alert = 70\n[display]\nlimit = 40\n"), "config.toml")
	legacy := LoadLegacy(writeFile(t, "busmon.toml", "[display]\nlimit = 10\ntheme = \"light\"\n[waiting]\nsnooze = \"20m\"\n"), "busmon.toml (legacy)")
	r := Resolver{Getenv: env(map[string]string{"AGENT_BUS_BUSMON_THEME": "dark"}), Files: []*File{cfg, legacy}}
	for _, x := range []struct {
		key    string
		value  any
		source string
	}{
		{"display.theme", "dark", "env AGENT_BUS_BUSMON_THEME"}, // env over both files
		{"display.limit", 40, "config.toml"},                    // config.toml over busmon.toml
		{"waiting.snooze", 20 * time.Minute, "busmon.toml (legacy)"},
		{"dispatch.context_alert", 70.0, "config.toml"},
		{"dispatch.prompt_grace", 30 * time.Second, "default"},
	} {
		got := r.Get(x.key)
		if got.Value != x.value || got.Source != x.source || got.Invalid {
			t.Errorf("%s = %#v from %q, want %#v from %q", x.key, got.Value, got.Source, x.value, x.source)
		}
	}
	// An ignored env value falls to the next source: the spec's own example.
	r.Getenv = env(map[string]string{"AGENT_BUS_BUSMON_LIMIT": "abc"})
	if got := r.Get("display.limit"); got.Value != 40 || got.Source != "config.toml" {
		t.Errorf("limit with an ignored env = %#v from %s, want 40 from config.toml", got.Value, got.Source)
	}
	if got := (Resolver{Getenv: r.Getenv}).Get("display.limit"); got.Value != 25 || got.Source != "default" {
		t.Errorf("limit with no file = %#v from %s, want 25 from default", got.Value, got.Source)
	}
	// An invalid env value stops at env, whatever the file says.
	r.Getenv = env(map[string]string{"AGENTBUS_HEALTH_RUNAWAY_SILENT_AFTER": "x"})
	r.Files = []*File{Load(writeFile(t, "config.toml", "[health.runaway]\nsilent_after = \"1h\"\n"), "config.toml")}
	if got := r.Get("health.runaway.silent_after"); !got.Invalid || got.Source != "env AGENTBUS_HEALTH_RUNAWAY_SILENT_AFTER" {
		t.Errorf("invalid runaway env = %+v, want invalid from env", got)
	}
}

// No file at all: every key is its default, and nothing warns.
func TestNoFileIsTheDefaults(t *testing.T) {
	f := Load(filepath.Join(t.TempDir(), "config.toml"), "config.toml")
	if f.Exists || len(f.Warnings) != 0 {
		t.Fatalf("absent file: exists=%v warnings=%v", f.Exists, f.Warnings)
	}
	r := Resolver{Getenv: env(nil), Files: []*File{f}}
	for _, k := range Keys {
		if got := r.Get(k.Name); got.Value != k.Default || got.Source != "default" {
			t.Errorf("%s = %#v from %s, want its default", k.Name, got.Value, got.Source)
		}
	}
}

func TestFileWarnings(t *testing.T) {
	body := strings.Join([]string{
		"[dispatch]",
		"context_alert = 150",      // 2: out of range
		"tokens = false",           // 3: a setting, not a secret
		"quota_warn = \"high\"",    // 4: wrong type
		"api_key = \"sk-SECRET1\"", // 5: unknown, a secret's name
		"[display]",
		"colour = \"PASTED-VALUE\"", // 7: unknown
		"[redis]",
		"url = \"redis://:pw@h\"", // 9: unknown, a secret's name
	}, "\n") + "\n"
	f := Load(writeFile(t, "config.toml", body), "config.toml")
	all := ""
	for _, w := range f.Warnings {
		all += w.String() + "\n"
	}
	for _, want := range []string{
		"config.toml:2: dispatch.context_alert:",
		"config.toml:4: dispatch.quota_warn:",
		"config.toml:5: dispatch.api_key: unknown key; a secret does not belong in this file",
		"config.toml:7: display.colour: unknown key",
		"config.toml:9: redis.url: unknown key; a secret does not belong in this file",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("warnings miss %q:\n%s", want, all)
		}
	}
	for _, leak := range []string{"SECRET1", "PASTED-VALUE", ":pw@"} {
		if strings.Contains(all, leak) {
			t.Errorf("a warning echoes a value (%s):\n%s", leak, all)
		}
	}
	if strings.Contains(all, "tokens") {
		t.Errorf("dispatch.tokens was refused:\n%s", all)
	}
	r := Resolver{Getenv: env(nil), Files: []*File{f}}
	if got := r.Get("dispatch.tokens"); got.Value != false || got.Source != "config.toml" {
		t.Errorf("dispatch.tokens = %#v from %s", got.Value, got.Source)
	}
	// A rejected value falls back to the next source down.
	if got := r.Get("dispatch.context_alert"); got.Value != 80.0 || got.Source != "default" {
		t.Errorf("out-of-range context_alert = %#v from %s, want the default", got.Value, got.Source)
	}
}

func TestUnparsableFileWarnsAndFallsBack(t *testing.T) {
	f := Load(writeFile(t, "config.toml", "[dispatch]\ncontext_alert = 70\nthis is not toml\n"), "config.toml")
	if !f.Exists || len(f.Warnings) != 1 || f.Warnings[0].Line != 3 || !strings.HasPrefix(f.Warnings[0].String(), "config.toml:3: ") {
		t.Fatalf("warnings = %v, want one at line 3", f.Warnings)
	}
	if got := (Resolver{Getenv: env(nil), Files: []*File{f}}).Get("dispatch.context_alert"); got.Source != "default" {
		t.Errorf("a key of an unparsable file was used: %+v", got)
	}
}

// busmon.toml holds only busmon's keys: anything else in it is unknown.
func TestLegacyFileHoldsOnlyBusmonKeys(t *testing.T) {
	f := LoadLegacy(writeFile(t, "busmon.toml", "[display]\nlimit = 5\n[dispatch]\ntokens = false\n"), "busmon.toml (legacy)")
	r := Resolver{Getenv: env(nil), Files: []*File{f}}
	if got := r.Get("dispatch.tokens"); got.Source != "default" {
		t.Errorf("busmon.toml set dispatch.tokens: %+v", got)
	}
	if got := r.Get("display.limit"); got.Value != 5 {
		t.Errorf("busmon.toml limit not read: %+v", got)
	}
	if len(f.Warnings) != 1 || f.Warnings[0].Key != "dispatch.tokens" {
		t.Errorf("warnings = %v, want dispatch.tokens unknown", f.Warnings)
	}
}

func TestFileRules(t *testing.T) {
	for _, x := range []struct {
		line string
		key  string
		ok   bool
		want any
	}{
		{"[waiting]\npopup_after = \"0\"", "waiting.popup_after", true, time.Duration(0)},
		{"[waiting]\nsnooze = \"0\"", "waiting.snooze", false, nil},
		{"[waiting]\nsnooze = \"-5m\"", "waiting.snooze", false, nil},
		{"[waiting]\nbell = false", "waiting.bell", true, false},
		{"[display]\ntheme = \"blue\"", "display.theme", false, nil},
		{"[display]\nlimit = -1", "display.limit", true, -1},
		{"[identity]\nas = \"charlie\"", "identity.as", true, "charlie"},
		{"[identity]\nas = \"\"", "identity.as", true, ""},
		{"[identity]\nas = \"Not Valid\"", "identity.as", false, nil},
		{"[dispatch]\ncontext_alert = 70.5", "dispatch.context_alert", true, 70.5},
		{"[dispatch]\nprompt_grace = 45", "dispatch.prompt_grace", true, 45 * time.Second},
		{"[dispatch]\nprompt_grace = 4000", "dispatch.prompt_grace", false, nil},
		{"[dispatch]\nquota_long_hold = \"0\"", "dispatch.quota_long_hold", true, time.Duration(0)},
		{"[dispatch]\nquota_max_age = \"0s\"", "dispatch.quota_max_age", false, nil},
		{"[bus]\nreport_max = 0", "bus.report_max", false, nil},
		{"[dispatch]\nquota_warn = 0", "dispatch.quota_warn", false, nil},
		{"[dispatch]\nquota_hold = 90", "dispatch.quota_hold", true, 90.0},
		{"[dispatch]\nquota_hold = 101", "dispatch.quota_hold", false, nil},
		{"[dispatch]\nquota_hold = nan", "dispatch.quota_hold", false, nil},
		{"[dispatch]\ncontext_alert = nan", "dispatch.context_alert", false, nil},
		{"[dispatch]\nquota_warn = inf", "dispatch.quota_warn", false, nil},
		{"[health.runaway]\nscreen_repeats = 6", "health.runaway.screen_repeats", true, 6},
		{"[updates]\ncheck = false", "updates.check", true, false},
	} {
		f := Load(writeFile(t, "config.toml", x.line+"\n"), "config.toml")
		v, ok := f.values[x.key]
		if ok != x.ok || (ok && v.value != x.want) || ok == (len(f.Warnings) > 0) {
			t.Errorf("%q: value %#v ok=%v warnings=%v; want %#v ok=%v", x.line, v.value, ok, f.Warnings, x.want, x.ok)
		}
	}
}

func TestPaths(t *testing.T) {
	if got := Path(env(map[string]string{"XDG_CONFIG_HOME": "/x"})); got != "/x/agentbus/config.toml" {
		t.Errorf("Path = %s", got)
	}
	if got := LegacyPath(env(map[string]string{"XDG_CONFIG_HOME": "/x"})); got != "/x/agentbus/busmon.toml" {
		t.Errorf("LegacyPath = %s", got)
	}
	if got := Path(env(map[string]string{"HOME": "/h"})); got != "/h/.config/agentbus/config.toml" {
		t.Errorf("Path without XDG = %s", got)
	}
	// Neither, or a relative one: no path, never the process's real home.
	for _, m := range []map[string]string{nil, {"XDG_CONFIG_HOME": "rel", "HOME": "also-rel"}} {
		if got := Path(env(m)); got != "" {
			t.Errorf("Path(%v) = %q, want none", m, got)
		}
	}
	if f := Load("", "config.toml"); f.Exists || len(f.Warnings) != 0 {
		t.Errorf("Load of no path = %+v", f)
	}
}

// A quota threshold of 0 in the env was always the default: it never masks the file.
func TestZeroQuotaEnvDoesNotMaskTheFile(t *testing.T) {
	f := Load(writeFile(t, "config.toml", "[dispatch]\nquota_hold = 90\n"), "config.toml")
	r := Resolver{Getenv: env(map[string]string{"AGENT_BUS_QUOTA_HOLD": "0"}), Files: []*File{f}}
	if got := r.Get("dispatch.quota_hold"); got.Value != 90.0 || got.Source != "config.toml" {
		t.Errorf("quota_hold = %#v from %s, want 90 from config.toml", got.Value, got.Source)
	}
}

// A parse error's warning names the line, never toml's text, which can quote the file.
func TestParseWarningQuotesNothing(t *testing.T) {
	f := Load(writeFile(t, "config.toml", "[dispatch]\nPASTE%q-HERE = 1\n"), "config.toml")
	if len(f.Warnings) != 1 || f.Warnings[0].Line != 2 || strings.Contains(f.Warnings[0].String(), "PASTE") || strings.Contains(f.Warnings[0].String(), "%") {
		t.Errorf("warnings = %v", f.Warnings)
	}
}
