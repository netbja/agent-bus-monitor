package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type pluginRig struct {
	calls []string
	execs [][]string
	env   map[string]string
	d     pluginDeps
	out   bytes.Buffer
	errw  bytes.Buffer
}

func newPluginRig(t *testing.T, ctx string) *pluginRig {
	r := &pluginRig{env: map[string]string{
		"AGENTBUS_PROJECTS_DIR": t.TempDir(),
	}}
	if ctx != "" {
		r.env["HERDR_PLUGIN_CONTEXT_JSON"] = ctx
	}
	r.d = pluginDeps{
		Herdr: func(args ...string) ([]byte, error) {
			r.calls = append(r.calls, strings.Join(args, " "))
			return nil, nil
		},
		Env: func(k string) string { return r.env[k] },
		Exec: func(a string, argv []string) error {
			r.execs = append(r.execs, append([]string{a}, argv...))
			return errors.New("fake exec returned")
		},
		Stdin: strings.NewReader("\n"),
	}
	return r
}

func (r *pluginRig) run(entry string) int { return runPlugin([]string{entry}, r.d, &r.out, &r.errw) }

const open = "plugin pane open --plugin netbja.agentbus --entrypoint "

func TestPluginProjectsOpensThePicker(t *testing.T) {
	r := newPluginRig(t, `{"workspace_id":"w2","workspace_label":"demo"}`)
	if code := r.run("projects"); code != 0 || len(r.calls) != 1 || r.calls[0] != open+"picker --workspace w2" {
		t.Fatalf("code %d calls %q", code, r.calls)
	}
}

func TestPluginBusmonOpensThePaneForAProject(t *testing.T) {
	r := newPluginRig(t, `{"workspace_id":"w2","workspace_label":"demo"}`)
	tpl := "name = \"demo\"\nworking_dir = \"/w\"\n\n[[tabs]]\nname = \"busmon\"\ncommand = \"'/r/busmon' --project demo --as 'Bernard J'\"\n"
	os.WriteFile(filepath.Join(r.env["AGENTBUS_PROJECTS_DIR"], "demo.toml"), []byte(tpl), 0o600)
	r.run("busmon")
	want := open + "busmon --workspace w2 --env AGENTBUS_PLUGIN_PROJECT=demo --env AGENTBUS_PLUGIN_AS=Bernard J"
	if len(r.calls) != 1 || r.calls[0] != want {
		t.Fatalf("calls %q want %q", r.calls, want)
	}
	// AGENT_BUS_HUMAN wins; a template without --as adds none.
	r = newPluginRig(t, `{"workspace_id":"w2","workspace_label":"demo"}`)
	os.WriteFile(filepath.Join(r.env["AGENTBUS_PROJECTS_DIR"], "demo.toml"), []byte("working_dir = \"/w\"\n[[tabs]]\nname=\"busmon\"\ncommand=\"/r/busmon --project demo\"\n"), 0o600)
	r.run("busmon")
	if r.calls[0] != open+"busmon --workspace w2 --env AGENTBUS_PLUGIN_PROJECT=demo" {
		t.Fatalf("calls %q", r.calls)
	}
	r.calls = nil
	r.env["AGENT_BUS_HUMAN"] = "Zed"
	r.run("busmon")
	if !strings.HasSuffix(r.calls[0], "--env AGENTBUS_PLUGIN_AS=Zed") {
		t.Fatalf("calls %q", r.calls)
	}
}

func TestPluginBusmonFallsBackToThePicker(t *testing.T) {
	for _, label := range []string{"codex", "Bad*", "demo"} {
		r := newPluginRig(t, `{"workspace_id":"w2","workspace_label":"`+label+`"}`)
		if label == "codex" { // a valid name, but no template
			os.WriteFile(filepath.Join(r.env["AGENTBUS_PROJECTS_DIR"], "demo.toml"), []byte("working_dir = \"/w\"\n"), 0o600)
		}
		if label == "demo" {
			r.env["AGENTBUS_PROJECTS_DIR"] = filepath.Join(t.TempDir(), "missing")
		}
		r.run("busmon")
		if len(r.calls) != 1 || !strings.HasPrefix(r.calls[0], open+"picker --workspace w2 --env AGENTBUS_PLUGIN_REASON=") ||
			!strings.Contains(r.calls[0], label) {
			t.Fatalf("%s: calls %q", label, r.calls)
		}
	}
}

func TestPluginContextTolerated(t *testing.T) {
	for _, ctx := range []string{"", "{not json", `{"workspace_id":"w9","surprise":[1,2]}`, `[]`} {
		r := newPluginRig(t, ctx)
		r.run("projects")
		r.run("busmon")
		if len(r.calls) != 2 {
			t.Fatalf("ctx %q calls %q", ctx, r.calls)
		}
		wantWS := strings.Contains(ctx, "w9")
		for _, c := range r.calls {
			if strings.Contains(c, "--workspace") != wantWS {
				t.Fatalf("ctx %q call %q", ctx, c)
			}
		}
	}
}

func TestPluginBusmonPaneExecsBusmon(t *testing.T) {
	r := newPluginRig(t, "")
	r.env["AGENTBUS_PLUGIN_PROJECT"] = "demo"
	r.env["AGENTBUS_PLUGIN_AS"] = "Bernard J"
	r.run("busmon-pane")
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	bm := filepath.Join(filepath.Dir(exe), "busmon")
	want := []string{bm, bm, "--project", "demo", "--as", "Bernard J"}
	if len(r.execs) != 1 || strings.Join(r.execs[0], "|") != strings.Join(want, "|") {
		t.Fatalf("execs %q want %q", r.execs, want)
	}

	r = newPluginRig(t, "")
	r.env["AGENTBUS_PLUGIN_PROJECT"] = "Bad*"
	if code := r.run("busmon-pane"); code != 1 || len(r.execs) != 0 || !strings.Contains(r.out.String(), "Bad*") {
		t.Fatalf("code %d execs %q out %q", code, r.execs, r.out.String())
	}
}

func TestPluginNeedsNoProject(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "agentbus")
	if out, err := runOut("go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	fake := filepath.Join(t.TempDir(), "herdr")
	os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	cmd := exec.Command(bin, "plugin", "projects")
	cmd.Env = []string{"HERDR_BIN_PATH=" + fake, "PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}
	out, err := cmd.CombinedOutput()
	if err != nil || strings.Contains(string(out), "project") {
		t.Fatalf("err %v out %q", err, out)
	}
}

func runOut(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

func TestPluginBusmonPaneWaitsWhenExecFails(t *testing.T) {
	r := newPluginRig(t, "")
	r.env["AGENTBUS_PLUGIN_PROJECT"] = "demo"
	pr, pw := io.Pipe()
	r.d.Stdin = pr
	done := make(chan int, 1)
	go func() { done <- r.run("busmon-pane") }()
	select {
	case <-done:
		t.Fatal("returned before a line was supplied")
	case <-time.After(150 * time.Millisecond):
	}
	pw.Write([]byte("\n"))
	select {
	case code := <-done:
		if code != 1 || !strings.Contains(r.out.String(), "fake exec returned") {
			t.Fatalf("code %d out %q", code, r.out.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not return after the line")
	}
}

func TestPluginUnparsableTemplateCommandYieldsNoAs(t *testing.T) {
	r := newPluginRig(t, `{"workspace_id":"w2","workspace_label":"demo"}`)
	os.WriteFile(filepath.Join(r.env["AGENTBUS_PROJECTS_DIR"], "demo.toml"),
		[]byte("working_dir = \"/w\"\n[[tabs]]\nname = \"busmon\"\ncommand = \"'/r/busmon --as X\"\n"), 0o600)
	r.run("busmon")
	if len(r.calls) != 1 || r.calls[0] != open+"busmon --workspace w2 --env AGENTBUS_PLUGIN_PROJECT=demo" {
		t.Fatalf("calls %q", r.calls)
	}
}
