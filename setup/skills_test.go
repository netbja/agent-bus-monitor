package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func skillsWorld(t *testing.T, cmds map[string]fakeCmd, paths map[string]string) (*Installer, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()
	repo, home := t.TempDir(), t.TempDir()
	if cmds == nil {
		cmds = map[string]fakeCmd{}
	}
	f := &fakeRunner{t: t, cmds: cmds, paths: paths}
	var out, errw bytes.Buffer
	env := map[string]string{"HOME": home}
	in := &Installer{R: f, O: NewOutput(&out, &errw, false), Opt: Options{BinDir: home + "/bin", InstallSkills: true}, Repo: repo, Tmp: t.TempDir(),
		Getenv: func(k string) string { return env[k] }}
	return in, &out, &errw, home
}

// PATH: on it and not shadowed; shadowed by an earlier agentbus; not on it.
func TestCheckPath(t *testing.T) {
	in, out, _, home := skillsWorld(t, nil, map[string]string{"agentbus": "/old/agentbus"})
	bin := home + "/bin"
	in.CheckPath("/usr/bin:" + bin)
	want := "    ✓ PATH: " + bin + " is in your active PATH\n" +
		"    ! agentbus on PATH is /old/agentbus, which shadows " + bin + "/agentbus; remove it or put " + bin + " first.\n"
	if out.String() != want {
		t.Fatalf("%q", out.String())
	}
	in2, out2, _, home2 := skillsWorld(t, nil, nil)
	in2.CheckPath("/usr/bin")
	b2 := home2 + "/bin"
	want2 := "    ! " + b2 + " is NOT in your current PATH!\n" +
		"    • Add it to your shell configuration (e.g. ~/.bashrc or ~/.zshrc):\n" +
		"    •   export PATH=\"" + b2 + ":$PATH\"\n"
	if out2.String() != want2 {
		t.Fatalf("%q", out2.String())
	}
}

// External skills: cloned and marked; a folder setup did not make is left;
// an earlier clone is refreshed at the pin; a dry run only says so.
func TestExternalSkills(t *testing.T) {
	ctx := context.Background()
	in, out, _, home := skillsWorld(t, nil, nil)
	sh := home + "/.local/share/agentbus/skills/mattpocock"
	in.R.(*fakeRunner).cmds["git clone --quiet --depth 1 --branch v1.3.1 https://github.com/mattpocock/skills.git "+sh] = fakeCmd{do: func(Cmd) {
		_ = os.MkdirAll(sh+"/.git", 0o755)
	}}
	in.ExternalSkills(ctx)
	if _, err := os.Stat(sh + "/.git/agentbus-setup"); err != nil || out.String() != "    ✓ external skills: cloned v1.3.1 into "+sh+"\n" {
		t.Fatalf("clone: %q %v", out.String(), err)
	}

	out.Reset()
	in.R.(*fakeRunner).cmds["git -C "+sh+" fetch --quiet --depth 1 origin refs/tags/v1.3.1:refs/tags/v1.3.1"] = fakeCmd{}
	in.R.(*fakeRunner).cmds["git -C "+sh+" checkout --quiet v1.3.1"] = fakeCmd{}
	in.ExternalSkills(ctx)
	if out.String() != "    ✓ external skills: v1.3.1 ("+sh+")\n" {
		t.Fatalf("refresh: %q", out.String())
	}

	out.Reset()
	_ = os.Remove(sh + "/.git/agentbus-setup")
	in.ExternalSkills(ctx)
	if out.String() != "    ! "+sh+" was not created by setup: left as it is (agents use the skills it holds)\n" {
		t.Fatalf("foreign: %q", out.String())
	}

	in2, out2, _, home2 := skillsWorld(t, nil, nil)
	in2.Opt.DryRun = true
	in2.ExternalSkills(ctx)
	if out2.String() != "    • [dry-run] would fetch https://github.com/mattpocock/skills.git v1.3.1 into "+home2+"/.local/share/agentbus/skills/mattpocock\n" {
		t.Fatalf("dry: %q", out2.String())
	}
}

// A failed clone: the log tail and setup.sh's warning.
func TestExternalSkillsCloneFails(t *testing.T) {
	in, out, errw, home := skillsWorld(t, nil, nil)
	sh := home + "/.local/share/agentbus/skills/mattpocock"
	in.R.(*fakeRunner).cmds["git clone --quiet --depth 1 --branch v1.3.1 https://github.com/mattpocock/skills.git "+sh] = fakeCmd{err: errors.New("x"), out: "fatal: no route\n"}
	in.ExternalSkills(context.Background())
	if errw.String() != "      fatal: no route\n" || !strings.Contains(out.String(), "could not clone https://github.com/mattpocock/skills.git v1.3.1: agents start without wayfinder, to-tickets, to-spec, ...") {
		t.Fatalf("%q %q", out.String(), errw.String())
	}
}

// Role skills through the repository's script, as setup.sh runs it.
func TestRoleSkills(t *testing.T) {
	in, out, _, _ := skillsWorld(t, nil, nil)
	script := filepath.Join(in.Repo, "scripts", "link-role-skills.sh")
	_ = os.MkdirAll(filepath.Dir(script), 0o755)
	_ = os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755)
	in.R.(*fakeRunner).cmds[script] = fakeCmd{}
	in.RoleSkills(context.Background())
	if out.String() != "    • Linking role skills for AI clients...\n    ✓ Role skills: linked successfully\n" {
		t.Fatalf("%q", out.String())
	}
	in.Opt.DryRun = true
	out.Reset()
	in.RoleSkills(context.Background())
	if out.String() != "    • [dry-run] would run scripts/link-role-skills.sh\n" {
		t.Fatalf("dry: %q", out.String())
	}
}
