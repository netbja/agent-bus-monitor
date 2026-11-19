package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// onBase rewrites the world's expected commands from origin's main to base.
func (w *deliverWorld) onBase(base string) {
	for k, v := range w.r.cmds {
		if nk := strings.ReplaceAll(strings.ReplaceAll(k, "/main", "/"+base), "--base main", "--base "+base); nk != k {
			delete(w.r.cmds, k)
			w.r.cmds[nk] = v
		}
	}
}

// The project's template names dev (dev-branch-flow §4.1): deliver fetches
// origin's dev, counts against it and opens the PR into it, though the
// machine's delivery.base is main.
func TestDeliverTakesTheProjectsBase(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	w.deps.ProjectBase = func() (string, bool, error) { return "dev", true, nil }
	w.onBase("dev")
	code, _, errw := w.run("master", "coder/x", "--task", "t1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	for _, c := range []string{"git fetch origin +refs/heads/dev:refs/remotes/origin/dev",
		"gh pr create --base dev --head coder/x --title first change --body-file <file>"} {
		if !slices.Contains(w.r.ran, c) {
			t.Errorf("did not run %q:\n%s", c, strings.Join(w.r.ran, "\n"))
		}
	}
}

// --base is above the project's base.
func TestDeliverBaseFlagOverTheProjects(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	w.deps.ProjectBase = func() (string, bool, error) { return "dev", true, nil }
	code, _, errw := w.run("master", "coder/x", "--task", "t1", "--base", "main")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	if !w.r.did("gh pr create --base main ") {
		t.Errorf("--base main not used:\n%s", strings.Join(w.r.ran, "\n"))
	}
}

// A project without a base of its own keeps the machine's delivery.base.
func TestDeliverWithoutAProjectBaseKeepsTheMachines(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	w.deps.ProjectBase = func() (string, bool, error) { return "", false, nil }
	if code, _, errw := w.run("master", "coder/x", "--task", "t1"); code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	if !w.r.did("gh pr create --base main ") {
		t.Errorf("machine base not used:\n%s", strings.Join(w.r.ran, "\n"))
	}
}

// A template that names a broken base stops deliver before any command:
// nothing is fetched, pushed or opened against a base nobody chose.
func TestDeliverStopsOnABrokenProjectBase(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	w.deps.ProjectBase = func() (string, bool, error) {
		return "", false, errors.New(`/x/demo.toml: [delivery] base "a..b" is not a valid branch name`)
	}
	code, _, errw := w.run("master", "coder/x", "--task", "t1")
	if code == 0 || !strings.Contains(errw, "demo.toml") {
		t.Fatalf("exit %d, stderr %q: want a refusal naming the template", code, errw)
	}
	if len(w.r.ran) != 0 {
		t.Errorf("ran commands after the refusal:\n%s", strings.Join(w.r.ran, "\n"))
	}
	// --base still delivers: the person's flag needs no template.
	w2 := newDeliverWorld(t, "git@github.com:o/r.git")
	w2.deps.ProjectBase = w.deps.ProjectBase
	if code, _, errw := w2.run("master", "coder/x", "--task", "t1", "--base", "main"); code != 0 {
		t.Fatalf("with --base: exit %d: %s", code, errw)
	}
}

// config --project names the project's delivery base and where it comes
// from: the template's [delivery] base over the machine's delivery.base.
func TestConfigProjectDeliveryBase(t *testing.T) {
	f := newConfigFixture(t)
	f.write(t, "services/demo.env", "")
	if err := os.MkdirAll(filepath.Join(f.xdg, "agentbus", "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, errw, code := f.run(t, "demo")
	if code != 0 || !strings.Contains(out, "delivery base main (default)") {
		t.Fatalf("no template: exit %d %s\n%s", code, errw, out)
	}
	f.write(t, "config.toml", "[delivery]\nbase = \"trunk\"\n")
	if out, _, _ := f.run(t, "demo"); !strings.Contains(out, "delivery base trunk (config.toml)") {
		t.Errorf("machine's base:\n%s", out)
	}
	tpl := filepath.Join(f.xdg, "agentbus", "projects", "demo.toml")
	f.write(t, "projects/demo.toml", "name = \"demo\"\n[delivery]\nbase = \"dev\"\n")
	out, _, _ = f.run(t, "demo")
	if !strings.Contains(out, "delivery base dev (template "+tpl+")") {
		t.Errorf("template's base:\n%s", out)
	}
	jout, _, _ := f.run(t, "demo", "--json")
	if !strings.Contains(jout, `"delivery_base": "dev"`) && !strings.Contains(jout, `"delivery_base":"dev"`) {
		t.Errorf("json lacks delivery_base dev:\n%s", jout)
	}
	// A broken template: said as a warning, never a base guessed.
	f.write(t, "projects/demo.toml", "[delivery]\nbase = \"a..b\"\n")
	out, _, code = f.run(t, "demo")
	if code != 0 || !strings.Contains(out, "delivery base unknown") || !strings.Contains(out, `[delivery] base "a..b" is not a valid branch name`) {
		t.Errorf("broken template: exit %d\n%s", code, out)
	}
}
