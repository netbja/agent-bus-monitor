package rolecfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// agyHelp is agy 1.5.3's real --help (agy-client spec §11). agy prints it on
// stderr, so the fake does too.
func agyFake(t *testing.T, f *launchFixture, help string) {
	t.Helper()
	path := filepath.Join(f.root, "agy-help.txt")
	must(t, os.WriteFile(path, []byte(help), 0o644))
	f.fake("agy", "#!/bin/sh\n[ \"$1\" = --help ] && cat '"+path+"' >&2\nexit 0\n")
}

func agyHelp(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "agy-1.5.3-help.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func agyFixture(t *testing.T) *launchFixture {
	f := newLaunchFixture(t)
	agyFake(t, f, agyHelp(t))
	return f
}

// sessionOf reads the --settings file of an agy argv.
func sessionOf(t *testing.T, argv []string) map[string]any {
	t.Helper()
	for i, a := range argv {
		if a == "--settings" && i+1 < len(argv) {
			b, err := os.ReadFile(argv[i+1])
			if err != nil {
				t.Fatalf("settings file: %v", err)
			}
			var v map[string]any
			if err := json.Unmarshal(b, &v); err != nil {
				t.Fatalf("settings %q: %v", b, err)
			}
			return v
		}
	}
	t.Fatalf("no --settings in %q", argv)
	return nil
}

// A default agy profile: --model, the session settings, the role prompt as
// the initial turn (-i). No --mode: agy asks before each tool (§4.2).
func TestLaunchAgy(t *testing.T) {
	f := agyFixture(t)
	m := f.config("agy", "", "", "")
	if _, e, err := f.launch(m, "coder"); err != nil {
		t.Fatal(err, e)
	}
	argv := f.execed[1:]
	if len(argv) != 7 || argv[0] != "agy" || argv[1] != "--model" || argv[2] != "test-model" ||
		argv[3] != "--settings" || argv[5] != "-i" {
		t.Fatalf("agy argv = %q", argv)
	}
	if p := argv[6]; !strings.Contains(p, "ROLE INSTRUCTIONS") || !strings.Contains(p, "test-skill/SKILL.md") {
		t.Fatalf("role prompt not the initial turn: %q", p)
	}
	for k, want := range map[string]string{"AGENT_BUS_CLIENT": "agy", "AGENT_BUS_MODEL": "test-model", "AGENT_BUS_PROVIDER": "test-provider"} {
		if v, _ := envOf(f.execEnv, k); v != want {
			t.Errorf("%s=%q, want %q", k, v, want)
		}
	}
	if !strings.HasPrefix(argv[4], filepath.Join(f.root, "cache")+string(filepath.Separator)) {
		t.Fatalf("settings file %q is not in the launch cache", argv[4])
	}
}

// The session file pins the least permissive tool permission, keeps the role
// in its workspace and empties the person's allows, whatever the profile; it
// holds only keys agy 1.5.3 accepts (an unknown one stops agy, §3.3).
func TestAgySessionSettings(t *testing.T) {
	accepted := map[string]bool{"toolPermission": true, "allowNonWorkspaceAccess": true, "permissions": true}
	for _, opts := range []string{"", "permission = \"skip\"\nsandbox = true", "permission = \"auto\"", "permission = \"accept-edits\""} {
		f := agyFixture(t)
		m := f.config("agy", "", "[roles.coder.tool_rules]\nallow = [\"Bash(git status)\"]\ndeny = [\"Bash(agentbus suspend *)\"]\n",
			"[profiles.main.options]\n"+opts)
		if _, e, err := f.launch(m, "coder"); err != nil {
			t.Fatal(opts, err, e)
		}
		s := sessionOf(t, f.execed)
		for k := range s {
			if !accepted[k] {
				t.Errorf("%q: key %s is not an agy 1.5.3 setting", opts, k)
			}
		}
		if s["toolPermission"] != "request-review" || s["allowNonWorkspaceAccess"] != false {
			t.Errorf("%q: %v", opts, s)
		}
		perms, _ := s["permissions"].(map[string]any)
		if allow, ok := perms["allow"].([]any); !ok || len(allow) != 0 {
			t.Errorf("%q: allow = %#v, want [] (it replaces the person's allows)", opts, perms["allow"])
		}
		if deny, _ := perms["deny"].([]any); !reflect.DeepEqual(deny, []any{"command(agentbus suspend)"}) {
			t.Errorf("%q: deny = %#v", opts, perms["deny"])
		}
	}
}

// No deny that translates: the session names none, so the person's own agy
// denies still apply (a session list replaces the file's, §3.3).
func TestAgySessionWithoutDenies(t *testing.T) {
	var s map[string]any
	text, _ := agySettings(ToolRules{Deny: []string{"Read(**/.env)"}})
	if err := json.Unmarshal([]byte(text), &s); err != nil {
		t.Fatal(err)
	}
	if _, ok := s["permissions"].(map[string]any)["deny"]; ok {
		t.Fatalf("an empty deny list would drop the person's denies: %s", text)
	}
}

// Each permission value, the sandbox and the reasoning become agy's flags,
// in the spec's order (§4.3).
func TestAgyFlags(t *testing.T) {
	for _, c := range []struct {
		opts, extra string
		want        []string
	}{
		{"permission = \"default\"", "", nil},
		{"permission = \"accept-edits\"", "", []string{"--mode", "accept-edits"}},
		{"permission = \"auto\"", "", []string{"--mode", "auto"}},
		{"permission = \"plan\"", "", []string{"--mode", "plan"}},
		{"permission = \"skip\"", "", []string{"--dangerously-skip-permissions"}},
		{"sandbox = true", "", []string{"--sandbox"}},
		{"sandbox = false", "", nil},
		{"permission = \"auto\"\nsandbox = true", "reasoning = \"max\"\nreasoning_levels = [\"high\", \"max\"]\n",
			[]string{"--mode", "auto", "--effort", "max", "--sandbox"}},
	} {
		f := agyFixture(t)
		m := f.config("agy", c.extra, "", "[profiles.main.options]\n"+c.opts)
		if _, e, err := f.launch(m, "coder"); err != nil {
			t.Fatal(c.opts, err, e)
		}
		argv := f.execed[1:]
		got := argv[3 : len(argv)-4] // between the model and --settings
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%q: flags %q, want %q (argv %q)", c.opts, got, c.want, argv)
		}
	}
}

func TestAgyOptions(t *testing.T) {
	head := "schema_version = 2\n[defaults]\nprofile = \"g\"\n[profiles.g]\nclient = \"agy\"\nprovider = \"google\"\nmodel = \"gemini-3.8-flash-high\"\n"
	tail := "[roles.a]\ntier = \"boot\"\n"
	for _, body := range []string{
		"", "options = { permission = \"default\" }\n", "options = { permission = \"accept-edits\" }\n",
		"options = { permission = \"auto\" }\n", "options = { permission = \"plan\" }\n", "options = { permission = \"skip\" }\n",
		"options = { sandbox = true }\n", "options = { sandbox = false }\n",
		"reasoning = \"xhigh\"\nreasoning_levels = [\"low\", \"medium\", \"high\", \"xhigh\", \"max\"]\n",
	} {
		p, err := mustLoadResolve(t, head+body+tail)
		if err != nil {
			t.Errorf("%q refused: %v", body, err)
			continue
		}
		if p.Client != "agy" {
			t.Errorf("%q: client %q", body, p.Client)
		}
	}
	for name, body := range map[string]string{
		"unknown option":      "options = { mode = \"auto\" }\n",
		"unknown permission":  "options = { permission = \"bypassPermissions\" }\n",
		"permission not text": "options = { permission = true }\n",
		"sandbox as text":     "options = { sandbox = \"true\" }\n",
		"sandbox as a number": "options = { sandbox = 1 }\n",
		"remote":              "options = { remote-agent = \"x\" }\n",
		"unknown reasoning":   "reasoning = \"ultra\"\nreasoning_levels = [\"ultra\"]\n",
	} {
		if _, err := mustLoadResolve(t, head+body+tail); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// The dry run prints the plan with placeholders and writes nothing.
func TestAgyDryRunWritesNothing(t *testing.T) {
	f := agyFixture(t)
	m := f.config("agy", "", "", "[profiles.main.options]\nsandbox = true")
	out, _, err := f.launch(m, "coder", "AGENT_LAUNCH_DRYRUN=1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "agy --model test-model --sandbox --settings '<generated agy settings>' -i '<role prompt and skill paths>'\n") {
		t.Fatalf("dry run = %q", out)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.root, "cache")); len(entries) != 0 {
		t.Fatalf("dry run wrote %d cache entries", len(entries))
	}
}

// A rule with no agy form is named at launch, never dropped silently (§4.4).
func TestAgyUntranslatedRulesNamed(t *testing.T) {
	f := agyFixture(t)
	m := f.config("agy", "", "[roles.coder.tool_rules]\ndeny = [\"Read(**/.env)\", \"Bash(agentbus deliver *)\", \"Bash(git * --force)\"]\n", "")
	_, errOut, err := f.launch(m, "coder")
	if err != nil {
		t.Fatal(err)
	}
	// The project's deliver spellings with --host hold a * inside: named too.
	if !strings.Contains(errOut, "agy: 11 tool rules have no agy form and are not enforced: Read(**/.env), Bash(git * --force), Bash(agentbus --project demo --host * deliver *),") {
		t.Fatalf("stderr = %q", errOut)
	}
	if strings.Contains(errOut, "tool_rules not applied") {
		t.Fatalf("agy applies the rules it can: %q", errOut)
	}
	perms := sessionOf(t, f.execed)["permissions"].(map[string]any)
	want := []any{"command(agentbus deliver)", "command(agentbus --project demo deliver)",
		"command(agentbus --project=demo deliver)", "command(agentbus -p demo deliver)"}
	if deny, _ := perms["deny"].([]any); !reflect.DeepEqual(deny, want) {
		t.Fatalf("deny = %#v", deny)
	}
}

// The translation, a pure function: a command prefix and only that.
func TestAgyDenyForm(t *testing.T) {
	for rule, want := range map[string]string{
		"Bash(agentbus deliver *)":               "command(agentbus deliver)",
		"Bash(agentbus suspend)":                 "command(agentbus suspend)",
		"Bash(rm -rf*)":                          "command(rm -rf)",
		"Bash(git push:*)":                       "command(git push)",
		"Bash(agentbus --project p deliver *)":   "command(agentbus --project p deliver)",
		"Bash(agentbus --host * deliver *)":      "",
		"Bash(agentbus reload *--as*)":           "",
		"Bash(*)":                                "",
		"Bash()":                                 "",
		"Bash( *)":                               "",
		"Read(**/.env)":                          "",
		"Edit(~/.ssh/**)":                        "",
		"WebFetch":                               "",
		"Bash(echo (x) *)":                       "",
		"Bash(agentbus deliver *) and something": "",
	} {
		got, ok := agyDeny(rule)
		if ok != (want != "") || got != want {
			t.Errorf("agyDeny(%q) = %q %v, want %q", rule, got, ok, want)
		}
	}
}

// preflight reads agy's --help on stderr, and refuses an agy that lacks a
// flag this launch passes.
func TestPreflightAgy(t *testing.T) {
	help := agyHelp(t)
	for _, c := range []struct{ flag, opts, extra string }{
		{"--model ", "", ""},
		{"--settings ", "", ""},
		{"--prompt-interactive ", "", ""},
		{"--mode ", "permission = \"auto\"", ""},
		{"--dangerously-skip-permissions ", "permission = \"skip\"", ""},
		{"--sandbox ", "sandbox = true", ""},
		{"--effort ", "", "reasoning = \"high\"\nreasoning_levels = [\"high\"]\n"},
	} {
		f := newLaunchFixture(t)
		if !strings.Contains(help, "  "+c.flag) {
			t.Fatalf("the 1.5.3 help has no %q", c.flag)
		}
		agyFake(t, f, withoutFlag(help, c.flag))
		_, _, err := f.launch(f.config("agy", c.extra, "", "[profiles.main.options]\n"+c.opts), "coder")
		if err == nil || err.Error() != "agy lacks required launch flags; inspect its version/help" {
			t.Errorf("agy without %s: %v", c.flag, err)
		}
	}
	// A flag this launch does not pass is not required.
	f := newLaunchFixture(t)
	agyFake(t, f, withoutFlag(help, "--mode "))
	if _, e, err := f.launch(f.config("agy", "", "", ""), "coder"); err != nil {
		t.Fatalf("a default profile needs no --mode: %v %s", err, e)
	}
}

// withoutFlag is help with every mention of flag (a word) removed: the -i
// line names --prompt-interactive too.
func withoutFlag(help, flag string) string {
	return regexp.MustCompile(regexp.QuoteMeta(strings.TrimSpace(flag))+`(\s)`).ReplaceAllString(help, "$1")
}

// skip and auto are risks the launch names; skip without the sandbox too.
func TestAgyElevated(t *testing.T) {
	for opts, want := range map[string]string{
		"":                                       "",
		"permission = \"accept-edits\"":          "",
		"permission = \"plan\"\nsandbox = false": "",
		"permission = \"auto\"":                  "permission=auto",
		"permission = \"skip\"":                  "permission=skip sandbox=false",
		"permission = \"skip\"\nsandbox = true":  "permission=skip",
	} {
		f := agyFixture(t)
		_, errOut, err := f.launch(f.config("agy", "", "", "[profiles.main.options]\n"+opts), "coder")
		if err != nil {
			t.Fatal(err)
		}
		line := ""
		if want != "" {
			line = "notice: coder runs agy with elevated " + want + "\n"
		}
		if got := strings.Contains(errOut, "elevated"); got != (want != "") || want != "" && !strings.Contains(errOut, line) {
			t.Errorf("%q: stderr %q, want %q", opts, errOut, line)
		}
	}
}

// agy's resume is not verified yet (self-repair step 5); agy has no turn hooks (§4.5): nothing is
// written where the client starts.
func TestAgyResumeAndNoHooks(t *testing.T) {
	f := agyFixture(t)
	if _, _, err := f.launchWithResume(f.config("agy", "", "", ""), "coder", "s1"); err == nil || !strings.Contains(err.Error(), "session resume is not verified for agy") {
		t.Fatalf("agy resume: %v", err)
	}
	if _, _, err := f.launch(f.config("agy", "", "", ""), "coder"); err != nil {
		t.Fatal(err)
	}
	work, _ := launchDir()
	if entries, _ := os.ReadDir(work); len(entries) != 0 {
		t.Fatalf("agy launch wrote into its working folder: %v", entries)
	}
	if ClientExecutable(Profile{Client: "agy"}) != "agy" {
		t.Fatal("ClientExecutable")
	}
}

// Both shipped manifests carry the agy reviewer profile: a Gemini id as agy
// models prints it (the model table's key, and AGENT_BUS_MODEL unchanged),
// default permission, the sandbox on (agy-client §5).
func TestShippedAgyProfile(t *testing.T) {
	for _, name := range []string{"roles.toml", filepath.Join("examples", "roles.mixed.toml")} {
		m, err := Load(repoPath(t, name))
		if err != nil {
			t.Fatal(name, err)
		}
		p, err := m.ResolveWith("coder", "agy-review")
		if err != nil {
			t.Fatal(name, err)
		}
		if p.Client != "agy" || p.Provider != "google" || p.Model != "gemini-3.8-flash-high" ||
			p.Options["sandbox"] != "true" || p.Options["permission"] != "" || p.Reasoning != "" {
			t.Errorf("%s: %+v", name, p)
		}
		if el := elevated(p, ToolRules{}); el != "" {
			t.Errorf("%s: the shipped agy profile is elevated: %s", name, el)
		}
	}
}

// roles doctor's view of an agy role (reviewer-codex): the same elevated
// parts and untranslated denies the launch names, from the same logic.
func TestAgyLaunchRisk(t *testing.T) {
	f := agyFixture(t)
	m := f.config("agy", "", "[roles.coder.tool_rules]\ndeny = [\"Read(**/.env)\", \"Bash(agentbus suspend *)\"]\n",
		"[profiles.main.options]\npermission = \"skip\"\n[profiles.calm]\nclient = \"agy\"\nprovider = \"google\"\nmodel = \"g\"\n[profiles.claude]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\noptions = { permission = \"bypassPermissions\" }\n")
	got, err := m.LaunchRisk("coder", "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := "elevated permission=skip sandbox=false; 1 tool rule has no agy form and is not enforced: Read(**/.env)"; got != want {
		t.Errorf("risk %q, want %q", got, want)
	}
	if got, _ := m.LaunchRisk("coder", "demo", "calm"); got != "1 tool rule has no agy form and is not enforced: Read(**/.env)" {
		t.Errorf("calm risk %q", got)
	}
	if got, _ := m.LaunchRisk("coder", "demo", "claude"); got != "" {
		t.Errorf("a Claude profile's risk %q: doctor names agy's only", got)
	}
	if _, err := m.LaunchRisk("nobody", "demo", ""); err == nil {
		t.Error("an unknown role has a risk")
	}
	// The launch names them in the same words.
	_, errOut, err := f.launch(m, "coder")
	if err != nil || !strings.Contains(errOut, "agy: 1 tool rule has no agy form and is not enforced: Read(**/.env)\n") {
		t.Fatalf("launch: %v %q", err, errOut)
	}
}
