package rolecfg

import (
	"os"
	"strings"
	"testing"
)

func TestResumeArgv(t *testing.T) {
	got, err := ResumeArgv("/r/scripts/agent-launch", "/r/roles.toml", "coder", "demo", "", "5f0c-11")
	want := []string{"env", "ROLES_TOML=/r/roles.toml", "/r/scripts/agent-launch", "coder", "demo", "--resume", "5f0c-11"}
	if err != nil || strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q, %v", got, err)
	}
	got, err = ResumeArgv("/r/scripts/agent-launch", "/r/roles.toml", "coder", "demo", "codex-max", "5f0c-11")
	want = []string{"env", "ROLES_TOML=/r/roles.toml", "/r/scripts/agent-launch", "coder", "demo", "--profile", "codex-max", "--resume", "5f0c-11"}
	if err != nil || strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("swapped argv = %q, %v", got, err)
	}
	for name, c := range map[string][6]string{
		"apostrophe":     {"/r/it's/agent-launch", "/r/roles.toml", "coder", "demo", "", "s"},
		"control":        {"/r/scripts/agent-launch", "/r/roles\n.toml", "coder", "demo", "", "s"},
		"bad role":       {"/r/scripts/agent-launch", "/r/roles.toml", "Coder", "demo", "", "s"},
		"bad profile":    {"/r/scripts/agent-launch", "/r/roles.toml", "coder", "demo", "x y", "s"},
		"no session":     {"/r/scripts/agent-launch", "/r/roles.toml", "coder", "demo", "", ""},
		"relative":       {"scripts/agent-launch", "/r/roles.toml", "coder", "demo", "", "s"},
		"over 8 KiB":     {"/r/" + strings.Repeat("a", 8<<10) + "/agent-launch", "/r/roles.toml", "coder", "demo", "", "s"},
		"session spaces": {"/r/scripts/agent-launch", "/r/roles.toml", "coder", "demo", "", " s"},
	} {
		if argv, err := ResumeArgv(c[0], c[1], c[2], c[3], c[4], c[5]); err == nil {
			t.Errorf("%s: accepted %q", name, argv)
		}
	}
}

func TestResumeArgvRefusesOver64Arguments(t *testing.T) {
	if err := checkResumeArgv(make([]string, 65)); err == nil {
		t.Fatal("65 arguments accepted")
	}
	if err := checkResumeArgv([]string{"env", "a"}); err != nil {
		t.Fatal(err)
	}
}

// Every Claude Code launch carries the SessionStart hook that reports its
// resume command to herdr; with tool rules, both live in one --settings.
func TestLaunchSettingsCarryTheResumeHook(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("claude-code", "", "", "")
	if _, e, err := f.launch(m, "coder", "AGENTBUS_BIN=/opt/ab/agentbus", "AGENTBUS_REPO=/opt/ab"); err != nil {
		t.Fatal(err, e)
	}
	hook := `{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "/opt/ab/agentbus roles report-resume /opt/ab/scripts/agent-launch"}]}]}}`
	args := f.execed[1:]
	i := indexOf(args, "--settings")
	if i < 0 || args[i+1] != hook {
		t.Fatalf("argv = %q", args)
	}
	m = f.config("claude-code", "", popsRoles, "")
	if _, e, err := f.launch(m, "coder", "AGENTBUS_BIN=/opt/ab/agentbus", "AGENTBUS_REPO=/opt/ab"); err != nil {
		t.Fatal(err, e)
	}
	args = f.execed[1:]
	i = indexOf(args, "--settings")
	if i < 0 || args[i+1] != strings.TrimSuffix(popsSettings, "}")+", "+strings.TrimPrefix(hook, "{") {
		t.Fatalf("argv with rules = %q", args)
	}
	if indexOf(args[i+1:], "--settings") >= 0 {
		t.Fatal("two --settings")
	}
	m = f.config("codex", "", "", "")
	if _, _, err := f.launch(m, "coder"); err != nil || strings.Contains(strings.Join(f.execed, " "), "report-resume") {
		t.Fatalf("codex got the hook: %q %v", f.execed, err)
	}
}

// A hook path that needs quoting is quoted for the shell Claude runs it in.
func TestResumeHookQuotesItsPaths(t *testing.T) {
	got := resumeHookCommand(newEnv([]string{"AGENTBUS_BIN=/opt/my ab/agentbus", "AGENTBUS_REPO=/opt/my ab"}))
	if got != "'/opt/my ab/agentbus' roles report-resume '/opt/my ab/scripts/agent-launch'" {
		t.Fatalf("hook = %s", got)
	}
}

func TestLaunchProfileAndResumeTogether(t *testing.T) {
	f := newLaunchFixture(t)
	body := "schema_version = 2\n[defaults]\nprofile = \"main\"\n[profiles.main]\nclient = \"claude-code\"\nprovider = \"p\"\nmodel = \"m\"\n" +
		"[profiles.alt]\nclient = \"claude-code\"\nprovider = \"q\"\nmodel = \"alt-model\"\n[roles.coder]\ntier = \"boot\"\nskills = [\"test-skill\"]\n"
	must(t, os.WriteFile(f.manifest, []byte(body), 0o644))
	m, err := Load(f.manifest)
	must(t, err)
	var out, errw strings.Builder
	err = LaunchProfileResume(m, "coder", "demo", "alt", "session-9", f.env, &out, &errw, func(argv0 string, argv, env []string) error {
		f.execed = argv
		return nil
	})
	args := strings.Join(f.execed, " ")
	if err != nil || !strings.Contains(args, "--model alt-model --resume session-9") {
		t.Fatalf("profile + resume: %v %s", err, args)
	}
}

func indexOf(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}
