package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func updateWorld(t *testing.T) (*Installer, *bytes.Buffer, *bytes.Buffer, *fakeRunner, *[]string) {
	t.Helper()
	in, _, f, _ := pluginWorld(t, "")
	var out, errw bytes.Buffer
	in.O = NewOutput(&out, &errw, false)
	g := "git -C " + in.Repo + " "
	f.cmds = map[string]fakeCmd{
		g + "diff --quiet":             {},
		g + "diff --cached --quiet":    {},
		g + "describe --tags --always": {out: "v1.0.0\n"},
		g + "rev-parse HEAD":           {out: "aaa\n"},
		g + "pull --ff-only --quiet":   {},
	}
	var exec []string
	in.Exec = func(argv0 string, argv, env []string) error {
		exec = append([]string{argv0}, argv...)
		for _, e := range env {
			if strings.HasPrefix(e, "AGENTBUS_SETUP_FROM=") {
				exec = append(exec, e)
			}
		}
		return nil
	}
	return in, &out, &errw, f, &exec
}

func TestUpdateFastForwards(t *testing.T) {
	in, out, _, f, exec := updateWorld(t)
	g := "git -C " + in.Repo + " "
	f.outs = map[string][]string{g + "rev-parse HEAD": {"aaa\n", "bbb\n"}, g + "describe --tags --always": {"v1.0.0\n", "v1.1.0\n"}}
	f.cmds[g+"tag --merged HEAD --no-merged aaa --sort=v:refname"] = fakeCmd{out: "v1.0.1\nv1.1.0\n"}
	f.cmds[g+"diff --name-only aaa HEAD -- skills/"] = fakeCmd{out: "skills/agent-bus/SKILL.md\nskills/tdd/x.md\nskills/agent-bus/y\n"}
	_ = os.MkdirAll(in.Repo+"/docs/releases", 0o755)
	_ = os.WriteFile(in.Repo+"/docs/releases/v1.1.0.md", nil, 0o644)
	in.Opt.InstallHerdr, in.Opt.Yes, in.Opt.StartDocker, in.Opt.InstallSkills = true, true, false, true
	in.Script = "/r/scripts/setup-go.sh"
	if code := in.Update(context.Background()); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := "\n==> Agent Bus Monitor — update\n" +
		"    ✓ updated v1.0.0 -> v1.1.0 (see CHANGELOG.md)\n" +
		"    • v1.1.0: docs/releases/v1.1.0.md\n" +
		"    • skills changed: agent-bus tdd\n" +
		"    • agents to tell: agentbus roles skill-users agent-bus tdd (then a wake=no directive to re-invoke them)\n"
	if out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
	if w := []string{"/r/scripts/setup-go.sh", "install", "--bin-dir", in.Opt.BinDir, "--install-herdr", "--no-docker", "--yes", "AGENTBUS_SETUP_FROM=v1.0.0"}; !reflect.DeepEqual(*exec, w) {
		t.Fatalf("exec %q", *exec)
	}
}

func TestUpdateUpToDateReinstalls(t *testing.T) {
	in, out, _, _, exec := updateWorld(t)
	in.Opt.InstallSkills, in.Opt.StartDocker, in.Opt.Force = false, true, true
	if code := in.Update(context.Background()); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if out.String() != "\n==> Agent Bus Monitor — update\n    ✓ already up to date (v1.0.0); reinstalling\n" {
		t.Fatalf("%q", out.String())
	}
	if w := []string{in.Repo + "/setup.sh", "install", "--bin-dir", in.Opt.BinDir, "--force", "--no-skills", "AGENTBUS_SETUP_FROM=v1.0.0"}; !reflect.DeepEqual(*exec, w) {
		t.Fatalf("exec %q", *exec)
	}
}

func TestUpdateRefuses(t *testing.T) {
	// A dirty tree, staged or not.
	for _, k := range []string{"diff --quiet", "diff --cached --quiet"} {
		in, _, errw, f, exec := updateWorld(t)
		f.cmds["git -C "+in.Repo+" "+k] = fakeCmd{err: errors.New("1")}
		if code := in.Update(context.Background()); code != 1 || errw.String() != "\nerror: the repository has uncommitted changes; commit or stash them first\n" || len(*exec) != 0 {
			t.Fatalf("%d %q %q", code, errw.String(), *exec)
		}
	}
	// A failed pull: its log, then the error.
	in, _, errw, f, exec := updateWorld(t)
	f.cmds["git -C "+in.Repo+" pull --ff-only --quiet"] = fakeCmd{err: errors.New("1"), out: "fatal: Not possible to fast-forward\n"}
	if code := in.Update(context.Background()); code != 1 || len(*exec) != 0 ||
		errw.String() != "      fatal: Not possible to fast-forward\n\nerror: git pull --ff-only failed (a diverged branch is never merged by setup)\n" {
		t.Fatalf("%d %q", code, errw.String())
	}
	// A dry run pulls nothing.
	in, out, _, f, exec := updateWorld(t)
	delete(f.cmds, "git -C "+in.Repo+" pull --ff-only --quiet")
	in.Opt.DryRun = true
	if code := in.Update(context.Background()); code != 0 || len(*exec) != 0 || !strings.HasSuffix(out.String(), "    • [dry-run] would run git pull --ff-only in "+in.Repo+", then install\n") {
		t.Fatalf("%d %q", code, out.String())
	}
}
