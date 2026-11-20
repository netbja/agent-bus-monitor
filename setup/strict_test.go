package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildOnly: Build compiles the stamped binary into the temp dir and
// installs nothing (agentbus update installs after its snapshot, update spec
// §3.4 step 3); BuildAndInstall is Build then the install, unchanged.
func TestBuildOnly(t *testing.T) {
	in, _, _, repo, bin := buildWorld(t, "v9.9.9", nil)
	in.Rev = "v9.9.9"
	got, err := in.Build(context.Background(), "busmon", "./cmd/busmon")
	if err != nil || got != filepath.Join(in.Tmp, "busmon") {
		t.Fatalf("Build = %q, %v", got, err)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("not built: %v", err)
	}
	for _, d := range []string{repo, bin} {
		if _, err := os.Stat(filepath.Join(d, "busmon")); err == nil {
			t.Fatalf("Build installed into %s", d)
		}
	}
	in2, _, _, _, _ := buildWorld(t, "v9.9.9", errors.New("exit 1"))
	in2.Rev = "v9.9.9"
	if _, err := in2.Build(context.Background(), "agentbus", "./cmd/agentbus"); err == nil {
		t.Fatal("a failed build is no error")
	}
}

// TestExternalSkillsStrict: the update's form returns the failure install
// only warns about; a folder setup did not make stays the person's, named
// and no failure (update spec §3.4 step 5).
func TestExternalSkillsStrict(t *testing.T) {
	in, out, _, home := skillsWorld(t, nil, nil)
	sh := home + "/.local/share/agentbus/skills/mattpocock"
	in.R.(*fakeRunner).cmds["git clone --quiet --depth 1 --branch v1.3.1 https://github.com/mattpocock/skills.git "+sh] = fakeCmd{err: errors.New("x"), out: "fatal: no route\n"}
	if err := in.ExternalSkillsStrict(context.Background()); err == nil || !strings.Contains(err.Error(), "could not clone") {
		t.Fatalf("clone failure: %v", err)
	}
	if strings.Contains(out.String(), "could not clone") {
		t.Fatalf("the strict form warned as install does: %q", out.String())
	}
	_ = os.MkdirAll(sh, 0o755) // a folder with no marker: the person's
	out.Reset()
	if err := in.ExternalSkillsStrict(context.Background()); err != nil || !strings.Contains(out.String(), "was not created by setup") {
		t.Fatalf("foreign folder: %v %q", err, out.String())
	}
}

// TestRoleSkillsStrict: a failed link script is an error for the update.
func TestRoleSkillsStrict(t *testing.T) {
	in, _, _, _ := skillsWorld(t, nil, nil)
	script := filepath.Join(in.Repo, "scripts", "link-role-skills.sh")
	_ = os.MkdirAll(filepath.Dir(script), 0o755)
	_ = os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755)
	in.R.(*fakeRunner).cmds[script] = fakeCmd{err: errors.New("exit 1"), out: "skill not found\n"}
	if err := in.RoleSkillsStrict(context.Background()); err == nil {
		t.Fatal("a failed link is no error")
	}
	in.R.(*fakeRunner).cmds[script] = fakeCmd{}
	if err := in.RoleSkillsStrict(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(script)
	if err := in.RoleSkillsStrict(context.Background()); err == nil {
		t.Fatal("a tree with no link script is no error for the update")
	}
}

// TestStateDir: XDG_STATE_HOME, else ~/.local/state.
func TestStateDir(t *testing.T) {
	env := map[string]string{"HOME": "/h"}
	get := func(k string) string { return env[k] }
	if StateDir(get) != "/h/.local/state" {
		t.Fatal(StateDir(get))
	}
	env["XDG_STATE_HOME"] = "/s"
	if StateDir(get) != "/s" {
		t.Fatal(StateDir(get))
	}
}

// TestSkillsChanged: each skills/<name> once, sorted; other paths ignored.
func TestSkillsChanged(t *testing.T) {
	got := SkillsChanged("skills/tdd/SKILL.md\nskills/agent-bus/SKILL.md\nskills/tdd/x.md\nREADME.md\nskills\n")
	if strings.Join(got, ",") != "agent-bus,tdd" {
		t.Fatalf("%v", got)
	}
}
