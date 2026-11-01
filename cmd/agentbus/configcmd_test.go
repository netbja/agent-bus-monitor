package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type configFixture struct {
	xdg string
	env map[string]string
}

func newConfigFixture(t *testing.T) *configFixture {
	t.Helper()
	xdg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(xdg, "agentbus", "services"), 0o700); err != nil {
		t.Fatal(err)
	}
	return &configFixture{xdg: xdg, env: map[string]string{"XDG_CONFIG_HOME": xdg, "HOME": t.TempDir()}}
}

func (f *configFixture) write(t *testing.T, rel, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.xdg, "agentbus", rel), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *configFixture) run(t *testing.T, project string, args ...string) (string, string, int) {
	t.Helper()
	var out, errw bytes.Buffer
	code := runConfig(args, project, func(k string) string { return f.env[k] }, &out, &errw)
	return out.String(), errw.String(), code
}

func rowOf(t *testing.T, out, key string) []string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == key {
			return f
		}
	}
	t.Fatalf("no row %s in:\n%s", key, out)
	return nil
}

func TestConfigRowsAndSources(t *testing.T) {
	f := newConfigFixture(t)
	f.write(t, "config.toml", "[dispatch]\ncontext_alert = 70\n[display]\nlimit = 40\n")
	f.write(t, "busmon.toml", "[display]\nlimit = 10\n[waiting]\nsnooze = \"20m\"\n")
	f.env["AGENT_BUS_BUSMON_THEME"] = "light"
	out, errw, code := f.run(t, "")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	for key, want := range map[string][]string{
		"dispatch.context_alert": {"70", "config.toml"},
		"dispatch.prompt_grace":  {"30s", "default"},
		"display.theme":          {"light", "env", "AGENT_BUS_BUSMON_THEME"},
		"display.limit":          {"40", "config.toml"},
		"waiting.snooze":         {"20m", "busmon.toml", "(legacy)"},
		"recap.auto_after":       {"8h", "default"},
	} {
		got := rowOf(t, out, key)
		if strings.Join(got[1:1+len(want)], " ") != strings.Join(want, " ") {
			t.Errorf("%s row = %v, want %v", key, got, want)
		}
	}
	for _, want := range []string{
		"files: config.toml ok · busmon.toml present (legacy: 1 key still read from it)",
		"quota.toml absent", "models.toml absent",
		"connection: REDIS_HOST localhost (default), REDIS_PORT 6380 (default), password: the built-in default",
		"warnings: none",
		"display.theme: busmon --theme",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}
}

func TestConfigWarningsAndInvalidEnv(t *testing.T) {
	f := newConfigFixture(t)
	f.write(t, "config.toml", "[dispatch]\nquota_warn = 0\n[redis]\npassword = \"hunter2-SECRET\"\n")
	f.env["AGENTBUS_HEALTH_RUNAWAY_SILENT_AFTER"] = "soon-VALUE"
	out, _, code := f.run(t, "")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		"config.toml:2: dispatch.quota_warn:",
		"config.toml:4: redis.password: unknown key; a secret does not belong in this file",
		"env AGENTBUS_HEALTH_RUNAWAY_SILENT_AFTER: invalid; the runaway check reads unknown",
		"files: config.toml 2 warnings",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}
	if row := rowOf(t, out, "health.runaway.silent_after"); row[1] != "invalid" {
		t.Errorf("runaway row = %v", row)
	}
	for _, leak := range []string{"hunter2", "soon-VALUE"} {
		if strings.Contains(out, leak) {
			t.Errorf("output echoes %s:\n%s", leak, out)
		}
	}
}

// The connection is facts only: never a password, never a URL, from the shell
// or from a project's env file.
func TestConfigNeverPrintsTheConnectionSecrets(t *testing.T) {
	f := newConfigFixture(t)
	f.env["REDIS_URL"] = "redis://:shellpw-SECRET@host:6380/0"
	f.env["REDIS_PASSWORD"] = "shellpass-SECRET"
	f.write(t, "services/demo.env", "REDIS_URL=\"redis://:filepw-SECRET@h:1/0\"\nREDIS_PASSWORD=\"filepass-SECRET\"\nAGENT_BUS_CONTEXT_ALERT=\"60\"\nHERDR_SESSION=\"other-VALUE\"\n")
	for _, args := range [][]string{nil, {"--json"}} {
		out, errw, code := f.run(t, "demo", args...)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errw)
		}
		for _, leak := range []string{"SECRET", "other-VALUE"} {
			if strings.Contains(out+errw, leak) {
				t.Errorf("%v output echoes %s:\n%s", args, leak, out)
			}
		}
		if args == nil && !strings.Contains(out, "connection: REDIS_URL set (env), REDIS_PASSWORD set (env)") {
			t.Errorf("connection line:\n%s", out)
		}
	}
}

func TestConfigProjectColumn(t *testing.T) {
	f := newConfigFixture(t)
	f.write(t, "config.toml", "[dispatch]\ncontext_alert = 70\nprompt_grace = 45\n[health.runaway]\nscreen_repeats = 6\n")
	f.write(t, "services/demo.env", "AGENT_BUS_CONTEXT_ALERT=\"60\"\nAGENT_BUS_ESCALATE_TO=\"lead\"\nAGENT_BUS_RELOAD=\"propose\"\nXDG_CONFIG_HOME=\""+f.xdg+"\"\nAGENTBUS_HEALTH_RUNAWAY_SCREEN_REPEATS=\"9\"\n")
	out, errw, code := f.run(t, "demo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	for key, want := range map[string]string{
		"dispatch.context_alert":        "60 env file (frozen at up) *",
		"dispatch.prompt_grace":         "45s config.toml",
		"health.runaway.screen_repeats": "6 config.toml 6 config.toml dispatch", // never copied by up: the env file is not read for it
	} {
		row := strings.Join(rowOf(t, out, key), " ")
		if !strings.Contains(row, want) {
			t.Errorf("%s row %q misses %q", key, row, want)
		}
	}
	if !strings.Contains(out, "project demo: escalate_to lead (env file), reload propose (env file)") {
		t.Errorf("project facts:\n%s", out)
	}
	// No env file: the command says why and fails.
	if _, errw, code := f.run(t, "nosuch"); code != 1 || !strings.Contains(errw, "agentbus up") {
		t.Errorf("missing env file: exit %d, %q", code, errw)
	}
}

// The services find config.toml through the env file's XDG_CONFIG_HOME, not
// the shell's.
func TestConfigServicesReadTheirOwnConfigHome(t *testing.T) {
	f := newConfigFixture(t)
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, "agentbus"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "agentbus", "config.toml"), []byte("[dispatch]\nprompt_grace = 50\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.write(t, "services/demo.env", "XDG_CONFIG_HOME=\""+other+"\"\n")
	out, _, _ := f.run(t, "demo")
	if row := strings.Join(rowOf(t, out, "dispatch.prompt_grace"), " "); !strings.Contains(row, "30s default 50s config.toml *") {
		t.Errorf("prompt_grace row = %q", row)
	}
}

func TestConfigJSON(t *testing.T) {
	f := newConfigFixture(t)
	f.write(t, "config.toml", "[dispatch]\ntokens = false\n")
	out, _, code := f.run(t, "", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var doc configDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(doc.Rows) != 25 {
		t.Errorf("%d rows", len(doc.Rows))
	}
	for _, r := range doc.Rows {
		if r.Key == "dispatch.tokens" && (r.Value != "false" || r.Source != "config.toml" || r.Line != 2) {
			t.Errorf("tokens row = %+v", r)
		}
		if r.Key == "display.theme" && r.Flag != "busmon --theme" {
			t.Errorf("theme row = %+v", r)
		}
	}
}

func TestConfigUsage(t *testing.T) {
	f := newConfigFixture(t)
	if _, errw, code := f.run(t, "", "extra"); code != 2 || !strings.Contains(errw, "usage") {
		t.Errorf("exit %d %q", code, errw)
	}
}

// An invalid value in the env file warns with its consequence, never its
// value; busmon-only keys get no services column.
func TestConfigProjectInvalidAndBusmonOnly(t *testing.T) {
	f := newConfigFixture(t)
	f.write(t, "services/demo.env", "AGENT_BUS_BUSMON_THEME=\"bogus-VALUE\"\nAGENT_BUS_CONTEXT_ALERT=\"60\"\n")
	for _, args := range [][]string{nil, {"--json"}} {
		out, _, code := f.run(t, "demo", args...)
		if code != 0 || strings.Contains(out, "bogus-VALUE") {
			t.Fatalf("exit %d or the value echoed:\n%s", code, out)
		}
		if args == nil {
			if row := strings.Join(rowOf(t, out, "display.theme"), " "); row != "display.theme auto default - busmon" {
				t.Errorf("busmon-only row = %q", row)
			}
			if strings.Contains(out, "env file AGENT_BUS_BUSMON_THEME") {
				t.Errorf("a busmon-only key was judged for the services:\n%s", out)
			}
		}
	}
	// A key the services read keeps its column.
	f.write(t, "services/demo.env", "AGENT_BUS_CONTEXT_ALERT=\"60\"\n")
	f.write(t, "config.toml", "")
	out, _, _ := f.run(t, "demo")
	if row := strings.Join(rowOf(t, out, "dispatch.context_alert"), " "); !strings.Contains(row, "80 default 60 env file (frozen at up) *") {
		t.Errorf("service row = %q", row)
	}
}

// The services' own config.toml warns too, once; a set value a rule skips says so.
func TestConfigServiceFileWarningsAndSkippedEnv(t *testing.T) {
	f := newConfigFixture(t)
	other := t.TempDir()
	os.MkdirAll(filepath.Join(other, "agentbus"), 0o700)
	os.WriteFile(filepath.Join(other, "agentbus", "config.toml"), []byte("[dispatch]\nprompt_grace = 4000\n"), 0o600)
	f.write(t, "services/demo.env", "XDG_CONFIG_HOME=\""+other+"\"\nAGENT_BUS_QUOTA_HOLD=\"nope-VALUE\"\n")
	f.env["AGENT_BUS_QUOTA_WARN"] = "high-VALUE"
	out, _, code := f.run(t, "demo")
	if code != 0 || strings.Contains(out, "VALUE") {
		t.Fatalf("exit %d or a value echoed:\n%s", code, out)
	}
	for _, want := range []string{
		"project demo services' config.toml:2: dispatch.prompt_grace:",
		"env AGENT_BUS_QUOTA_WARN: set but not a value its rule takes; default applies",
		"project demo env file AGENT_BUS_QUOTA_HOLD: set but not a value its rule takes; default applies",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("misses %q:\n%s", want, out)
		}
	}
	// The same file as the shell's warns once.
	f2 := newConfigFixture(t)
	f2.write(t, "config.toml", "[dispatch]\nprompt_grace = 4000\n")
	f2.write(t, "services/demo.env", "XDG_CONFIG_HOME=\""+f2.xdg+"\"\n")
	out, _, _ = f2.run(t, "demo")
	if strings.Count(out, "dispatch.prompt_grace:") != 1 {
		t.Errorf("the shared file warned twice:\n%s", out)
	}
}
