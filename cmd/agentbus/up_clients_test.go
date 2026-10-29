package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// installer-no-client (Bernard): up with no agent CLI for any role of the
// template refuses before it starts a service or opens a tab, with the install
// pointers and the exact next command; with some missing it starts every tab
// as before and warns with the missing ones (master: never drop a tab on a
// lookup alone).

// withClients gives the fixture an agent tab per role (master on Claude Code,
// coder on Codex) and a PATH holding only have.
func withClients(t *testing.T, deps upDeps, templates string, have ...string) upDeps {
	t.Helper()
	boot := deps.Bootstrap
	deps.Bootstrap = func(args []string) error {
		if err := boot(args); err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(templates, "demo.toml"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString("\n[[tabs]]\nname = \"coder\"\ncommand = \"env ROLES_TOML=/r/roles.toml /r/scripts/agent-launch coder demo\"\n")
		return err
	}
	deps.TabExe = func(cmd string) string {
		switch {
		case strings.Contains(cmd, "agent-launch master"):
			return "claude"
		case strings.Contains(cmd, "agent-launch coder"):
			return "codex"
		}
		return ""
	}
	deps.LookPath = func(exe string) (string, error) {
		for _, h := range have {
			if h == exe {
				return "/bin/" + exe, nil
			}
		}
		return "", errors.New("not found")
	}
	return deps
}

func TestUpWithNoAgentCLIRefusesBeforeOpeningAnything(t *testing.T) {
	work, templates, h, _, deps := upFixture(t)
	deps = withClients(t, deps, templates)
	deps.Services = nil
	var out, errw bytes.Buffer
	code := runUp([]string{"demo", work}, deps, &out, &errw)
	if code != 1 {
		t.Fatalf("up = %d, want 1: %s %s", code, out.String(), errw.String())
	}
	for _, c := range h.calls {
		if strings.HasPrefix(c, "workspace create") || strings.HasPrefix(c, "tab create") || strings.HasPrefix(c, "pane run") {
			t.Fatalf("up opened something with no agent CLI: %q", h.calls)
		}
	}
	msg := errw.String()
	for _, w := range []string{
		"no agent CLI on PATH for this team", "master needs claude", "coder needs codex",
		"Claude Code (claude): https://code.claude.com/docs/en/setup", "Codex CLI (codex): https://github.com/openai/codex",
		"busmon and the bus work meanwhile", "then run: agentbus up demo",
	} {
		if !strings.Contains(msg, w) {
			t.Errorf("missing %q in:\n%s", w, msg)
		}
	}
}

func TestUpStartsEveryTabAndNamesTheMissingCLI(t *testing.T) {
	work, templates, h, _, deps := upFixture(t)
	deps = withClients(t, deps, templates, "codex")
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	// master's call: a lookup alone never drops a tab; the missing one is
	// named, and every tab starts as before (its pane says if it fails).
	got := strings.Join(h.calls, "\n")
	if !strings.Contains(got, "--label coder") || !strings.Contains(got, "--label master") {
		t.Fatalf("want every tab opened:\n%s", got)
	}
	if !strings.Contains(out.String(), "warning: master needs claude, not found (Claude Code (claude): https://code.claude.com/docs/en/setup)") {
		t.Fatalf("the missing one is not named: %q", out.String())
	}
}

func TestUpWithEveryCLIOpensEveryTab(t *testing.T) {
	work, templates, h, _, deps := upFixture(t)
	deps = withClients(t, deps, templates, "claude", "codex")
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	got := strings.Join(h.calls, "\n")
	if !strings.Contains(got, "--label coder") || !strings.Contains(got, "--label master") || strings.Contains(out.String(), "not started") {
		t.Fatalf("%s\n%s", got, out.String())
	}
}

// The real lookup: a tab's executable comes from the manifest its command
// names (an adapter's own executable for a non-builtin client).
func TestTabExecutable(t *testing.T) {
	manifest, err := filepath.Abs("../../roles.toml")
	if err != nil {
		t.Fatal(err)
	}
	env := func(string) string { return "" }
	for cmd, want := range map[string]string{
		"env ROLES_TOML=" + manifest + " /r/scripts/agent-launch master demo":         "claude",
		"env ROLES_TOML=" + manifest + " /r/scripts/agent-launch reviewer-codex demo": "codex",
		"/r/busmon --project demo": "",
		"env ROLES_TOML=" + manifest + " /r/scripts/agent-launch nobody demo": "",
	} {
		if got := tabExecutable(cmd, env); got != want {
			t.Errorf("%q: %q, want %q", cmd, got, want)
		}
	}
}

func TestLookAgentCLIFindsAUserInstall(t *testing.T) {
	home := t.TempDir()
	notOnPath := func(string) (string, error) { return "", errors.New("not found") }
	noShell := func(string) (string, error) { return "", errCLIAbsent }
	look := lookAgentCLI(func(k string) string { return map[string]string{"HOME": home}[k] }, notOnPath, noShell)
	if _, err := look("claude"); err == nil {
		t.Fatal("found a CLI that is nowhere")
	}
	bin := filepath.Join(home, ".local", "bin")
	_ = os.MkdirAll(bin, 0o755)
	_ = os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o644)
	if _, err := look("claude"); err == nil {
		t.Fatal("a file that is not executable counted")
	}
	_ = os.Chmod(filepath.Join(bin, "claude"), 0o755)
	if p, err := look("claude"); err != nil || p != filepath.Join(bin, "claude") {
		t.Fatalf("~/.local/bin/claude: %q, %v", p, err)
	}
	onPath := lookAgentCLI(func(string) string { return home }, func(e string) (string, error) { return "/p/" + e, nil }, noShell)
	if p, _ := onPath("codex"); p != "/p/codex" {
		t.Fatalf("PATH first: %q", p)
	}
}

// reviewer-codex: a generated template quotes the ROLES_TOML assignment
// (ShellJoin); with a space in the manifest's path the tab must still resolve,
// or checkClients would count no agent and let the tabs launch.
func TestTabExecutableReadsAGeneratedTemplateWithASpacedPath(t *testing.T) {
	src, err := os.ReadFile("../../roles.toml")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "my checkout")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "roles.toml")
	if err := os.WriteFile(manifest, src, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := rolecfg.Load(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var tpl upTemplate
	body := rolecfg.Template(m, manifest, "demo", dir, filepath.Join(dir, "scripts"), dir, "", false, false)
	if _, err := toml.Decode(body, &tpl); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, tab := range tpl.Tabs {
		if tab.Name == "busmon" {
			continue
		}
		if !strings.Contains(tab.Command, "'") {
			t.Fatalf("the template no longer quotes the spaced path: %q", tab.Command)
		}
		if got := tabExecutable(tab.Command, func(string) string { return "" }); got != "claude" {
			t.Errorf("%s: %q, want claude", tab.Name, got)
		}
		found++
	}
	if found == 0 {
		t.Fatal("no agent tab in the generated template")
	}
}

// foureyes: a CLI outside the fixed dirs (nvm, Kimi's own installer, volta,
// asdf, mise...) is found by the user's login shell, the one the tab runs
// in; only that shell's clean "no" drops a tab, and a shell that cannot
// answer keeps it, with the reason.
func TestLookAgentCLIAsksTheLoginShell(t *testing.T) {
	home := t.TempDir()
	env := func(k string) string { return map[string]string{"HOME": home}[k] }
	notOnPath := func(string) (string, error) { return "", errors.New("not found") }
	viaShell := lookAgentCLI(env, notOnPath, func(exe string) (string, error) { return home + "/.nvm/bin/" + exe, nil })
	if p, err := viaShell("codex"); err != nil || p != home+"/.nvm/bin/codex" {
		t.Fatalf("shell fallback: %q, %v", p, err)
	}
	unsure := lookAgentCLI(env, notOnPath, func(string) (string, error) { return "", errors.New("shell timed out") })
	if _, err := unsure("kimi"); !errors.Is(err, errCLIUnknown) {
		t.Fatalf("a shell that cannot answer: %v, want errCLIUnknown", err)
	}
	absent := lookAgentCLI(env, notOnPath, func(string) (string, error) { return "", errCLIAbsent })
	if _, err := absent("kimi"); err == nil || errors.Is(err, errCLIUnknown) {
		t.Fatalf("the shell's clean no: %v, want a miss", err)
	}
}

// up keeps a tab whose CLI cannot be told present or absent, and says why.
func TestUpKeepsATabItCannotJudge(t *testing.T) {
	work, templates, h, _, deps := upFixture(t)
	deps = withClients(t, deps, templates, "claude")
	deps.LookPath = func(exe string) (string, error) {
		if exe == "claude" {
			return "/bin/claude", nil
		}
		return "", fmt.Errorf("%w: zsh timed out", errCLIUnknown)
	}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if got := strings.Join(h.calls, "\n"); !strings.Contains(got, "--label coder") {
		t.Fatalf("the unjudged tab was dropped:\n%s", got)
	}
	if !strings.Contains(out.String(), "could not tell whether codex is installed") {
		t.Fatalf("no reason given: %q", out.String())
	}
}

// The real probe asks $SHELL as a login, interactive shell, and reads a
// clean miss from its exit status; no $SHELL cannot answer.
func TestShellProbe(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fakesh")
	// A shell that prints noise, then the path when asked for "present".
	_ = os.WriteFile(fake, []byte("#!/bin/sh\necho 'motd noise'\n[ \"$4\" = present ] && { echo /opt/x/present; exit 0; }\nexit 1\n"), 0o755)
	probe := shellProbe(func(k string) string { return map[string]string{"SHELL": fake}[k] }, time.Second)
	if p, err := probe("present"); err != nil || p != "/opt/x/present" {
		t.Fatalf("present: %q, %v", p, err)
	}
	if _, err := probe("absent"); !errors.Is(err, errCLIAbsent) {
		t.Fatalf("absent: %v, want errCLIAbsent", err)
	}
	if _, err := shellProbe(func(string) string { return "" }, time.Second)("x"); err == nil || errors.Is(err, errCLIAbsent) {
		t.Fatalf("no SHELL: %v, want an unknown answer", err)
	}
	slow := filepath.Join(dir, "slowsh")
	_ = os.WriteFile(slow, []byte("#!/bin/sh\nsleep 5\n"), 0o755)
	if _, err := shellProbe(func(k string) string { return slow }, 200*time.Millisecond)("x"); err == nil || errors.Is(err, errCLIAbsent) {
		t.Fatalf("slow shell: %v, want an unknown answer", err)
	}
}

// master: a timeout is a start. The real lookup with a shell too slow to
// answer: no CLI is taken for missing, nothing is refused, every tab opens.
func TestUpStartsEveryTabWhenTheShellTimesOut(t *testing.T) {
	work, templates, h, _, deps := upFixture(t)
	deps = withClients(t, deps, templates)
	slow := filepath.Join(t.TempDir(), "slowsh")
	_ = os.WriteFile(slow, []byte("#!/bin/sh\nsleep 5\n"), 0o755)
	notOnPath := func(string) (string, error) { return "", errors.New("not found") }
	deps.LookPath = lookAgentCLI(func(string) string { return "" }, notOnPath,
		shellProbe(func(k string) string { return map[string]string{"SHELL": slow}[k] }, 200*time.Millisecond))
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d, want 0 (fail open): %s", code, errw.String())
	}
	got := strings.Join(h.calls, "\n")
	if !strings.Contains(got, "--label master") || !strings.Contains(got, "--label coder") {
		t.Fatalf("a tab was not opened:\n%s", got)
	}
	if !strings.Contains(out.String(), "could not tell whether claude is installed") || !strings.Contains(out.String(), "did not answer within") {
		t.Fatalf("the timeout is not said: %q", out.String())
	}
}
