package rolecfg

import (
	"strings"
	"testing"
)

// Grok Build (grok 1.0.67): --model, the role prompt appended to the system
// prompt with --rules, --permission-mode with the values its --help lists.
func TestLaunchGrok(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("grok", "", "", "[profiles.main.options]\npermission = \"acceptEdits\"")
	if _, e, err := f.launch(m, "coder"); err != nil {
		t.Fatal(err, e)
	}
	want := []string{"grok", "--model", "test-model", "--permission-mode", "acceptEdits", "--rules"}
	if got := f.execed[1 : len(f.execed)-1]; strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("grok argv = %q", got)
	}
	if rules := f.execed[len(f.execed)-1]; !strings.Contains(rules, "ROLE INSTRUCTIONS") || !strings.Contains(rules, "test-skill/SKILL.md") {
		t.Fatalf("role prompt not in --rules: %q", rules)
	}
	if v, _ := envOf(f.execEnv, "AGENT_BUS_CLIENT"); v != "grok" {
		t.Fatalf("AGENT_BUS_CLIENT=%q", v)
	}
	m = f.config("grok", "", "", "")
	out, _, err := f.launch(m, "coder", "AGENT_LAUNCH_DRYRUN=1")
	if err != nil || !strings.Contains(out, "grok --model test-model --rules '<role prompt and skill paths>'\n") {
		t.Fatalf("dry run = %q %v", out, err)
	}
}

func TestGrokOptions(t *testing.T) {
	head := "schema_version = 2\n[defaults]\nprofile = \"g\"\n[profiles.g]\nclient = \"grok\"\nprovider = \"xai\"\nmodel = \"grok-4.6\"\n"
	for _, perm := range []string{"default", "acceptEdits", "auto", "dontAsk", "bypassPermissions", "plan"} {
		if _, err := mustLoadResolve(t, head+"options = { permission = \""+perm+"\" }\n[roles.a]\ntier = \"boot\"\n"); err != nil {
			t.Errorf("permission %s: %v", perm, err)
		}
	}
	for name, body := range map[string]string{
		"unknown permission": "options = { permission = \"yolo\" }\n",
		"sandbox":            "options = { sandbox = \"x\" }\n",
		"reasoning":          "reasoning = \"high\"\nreasoning_levels = [\"high\"]\n",
	} {
		if _, err := mustLoadResolve(t, head+body+"[roles.a]\ntier = \"boot\"\n"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func mustLoadResolve(t *testing.T, body string) (Profile, error) {
	t.Helper()
	m, err := Load(writeConfig(t, body))
	if err != nil {
		return Profile{}, err
	}
	return m.Resolve("a")
}

func TestGrokElevatedResumeAndRules(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("grok", "", popsRoles, "[profiles.main.options]\npermission = \"bypassPermissions\"")
	_, errOut, err := f.launch(m, "coder")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "notice: coder runs grok with elevated permission=bypassPermissions") ||
		!strings.Contains(errOut, "note: tool_rules not applied for grok") {
		t.Fatalf("stderr = %q", errOut)
	}
	if strings.Contains(strings.Join(f.execed, " "), "--settings") || strings.Contains(strings.Join(f.execed, " "), "--allow") {
		t.Fatalf("grok got Claude rules: %q", f.execed)
	}
	if _, _, err := f.launchWithResume(f.config("grok", "", "", ""), "coder", "s1"); err == nil || !strings.Contains(err.Error(), "only for Claude Code") {
		t.Fatalf("grok resume: %v", err)
	}
	if ClientExecutable(Profile{Client: "grok"}) != "grok" {
		t.Fatal("ClientExecutable")
	}
}

// A grok whose --help lacks --rules is refused before anything is written.
func TestPreflightGrok(t *testing.T) {
	f := newLaunchFixture(t)
	f.fake("grok", "#!/bin/sh\necho '"+strings.Replace(fakeHelp, "--rules ", "", 1)+"'\n")
	if _, _, err := f.launch(f.config("grok", "", "", ""), "coder"); err == nil || err.Error() != "grok lacks required launch flags; inspect its version/help" {
		t.Fatalf("grok without --rules: %v", err)
	}
}
