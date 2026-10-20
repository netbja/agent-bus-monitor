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

	"github.com/netbja/agent-bus-monitor/projlist"
)

type pluginRig struct {
	calls  []string
	execs  [][]string
	env    map[string]string
	d      pluginDeps
	runErr error
	out    bytes.Buffer
	errw   bytes.Buffer
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
		Run: func(a string, argv []string) (int, error) {
			r.execs = append(r.execs, append([]string{a}, argv...))
			return 0, r.runErr
		},
		Stdin: strings.NewReader("\n"),
	}
	return r
}

func (r *pluginRig) run(entry string) int { return runPlugin([]string{entry}, r.d, &r.out, &r.errw) }

const open = "plugin pane open --plugin netbja.agentbus --entrypoint "

func TestPluginProjectsOpensThePicker(t *testing.T) {
	r := newPluginRig(t, `{"workspace_id":"w2","focused_pane_id":"w2:p1","workspace_label":"demo"}`)
	if code := r.run("projects"); code != 0 || len(r.calls) != 1 || r.calls[0] != open+"picker" {
		t.Fatalf("code %d calls %q", code, r.calls)
	}
}

func TestPluginBusmonOpensThePaneForAProject(t *testing.T) {
	r := newPluginRig(t, `{"workspace_id":"w2","focused_pane_id":"w2:p1","workspace_label":"demo"}`)
	tpl := "name = \"demo\"\nworking_dir = \"/w\"\n\n[[tabs]]\nname = \"busmon\"\ncommand = \"'/r/busmon' --project demo --as 'Bernard J'\"\n"
	os.WriteFile(filepath.Join(r.env["AGENTBUS_PROJECTS_DIR"], "demo.toml"), []byte(tpl), 0o600)
	r.run("busmon")
	want := open + "busmon --target-pane w2:p1 --env AGENTBUS_PLUGIN_PROJECT=demo --env AGENTBUS_PLUGIN_AS=Bernard J"
	if len(r.calls) != 1 || r.calls[0] != want {
		t.Fatalf("calls %q want %q", r.calls, want)
	}
	// AGENT_BUS_HUMAN wins; a template without --as adds none.
	r = newPluginRig(t, `{"workspace_id":"w2","focused_pane_id":"w2:p1","workspace_label":"demo"}`)
	os.WriteFile(filepath.Join(r.env["AGENTBUS_PROJECTS_DIR"], "demo.toml"), []byte("working_dir = \"/w\"\n[[tabs]]\nname=\"busmon\"\ncommand=\"/r/busmon --project demo\"\n"), 0o600)
	r.run("busmon")
	if r.calls[0] != open+"busmon --target-pane w2:p1 --env AGENTBUS_PLUGIN_PROJECT=demo" {
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
		r := newPluginRig(t, `{"workspace_id":"w2","focused_pane_id":"w2:p1","workspace_label":"`+label+`"}`)
		if label == "codex" { // a valid name, but no template
			os.WriteFile(filepath.Join(r.env["AGENTBUS_PROJECTS_DIR"], "demo.toml"), []byte("working_dir = \"/w\"\n"), 0o600)
		}
		if label == "demo" {
			r.env["AGENTBUS_PROJECTS_DIR"] = filepath.Join(t.TempDir(), "missing")
		}
		r.run("busmon")
		if len(r.calls) != 1 || !strings.HasPrefix(r.calls[0], open+"picker --env AGENTBUS_PLUGIN_REASON=") ||
			!strings.Contains(r.calls[0], label) {
			t.Fatalf("%s: calls %q", label, r.calls)
		}
	}
}

func TestPluginContextTolerated(t *testing.T) {
	for _, ctx := range []string{"", "{not json", `{"workspace_id":"w9","focused_pane_id":"w9:p2","surprise":[1,2]}`, `[]`} {
		r := newPluginRig(t, ctx)
		r.run("projects")
		r.run("busmon")
		if len(r.calls) != 2 {
			t.Fatalf("ctx %q calls %q", ctx, r.calls)
		}
		for _, c := range r.calls {
			if strings.Contains(c, "--workspace") {
				t.Fatalf("ctx %q call %q: herdr refuses --workspace for plugin panes", ctx, c)
			}
		}
	}
}

// herdr 0.9.3: "overlay and popup plugin panes target the active pane" (no
// target flag at all) and "split and zoomed plugin panes target an existing
// pane; use target_pane_id".
func TestPluginPaneTargetsMatchPlacement(t *testing.T) {
	r := newPluginRig(t, `{"workspace_id":"w9","workspace_label":"demo","focused_pane_id":"w9:p2"}`)
	if err := os.WriteFile(filepath.Join(r.env["AGENTBUS_PROJECTS_DIR"], "demo.toml"), []byte("name = \"demo\"\nworking_dir = \"/w\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.run("projects")
	r.run("busmon")
	if len(r.calls) != 2 || r.calls[0] != open+"picker" || !strings.HasPrefix(r.calls[1], open+"busmon --target-pane w9:p2 ") {
		t.Fatalf("calls %q", r.calls)
	}
}

func TestPluginBusmonPaneRunsBusmon(t *testing.T) {
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

func TestPluginBusmonPaneWaitsWhenStartFails(t *testing.T) {
	r := newPluginRig(t, "")
	r.runErr = errors.New("fake exec returned")
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
	r := newPluginRig(t, `{"workspace_id":"w2","focused_pane_id":"w2:p1","workspace_label":"demo"}`)
	os.WriteFile(filepath.Join(r.env["AGENTBUS_PROJECTS_DIR"], "demo.toml"),
		[]byte("working_dir = \"/w\"\n[[tabs]]\nname = \"busmon\"\ncommand = \"'/r/busmon --as X\"\n"), 0o600)
	r.run("busmon")
	if len(r.calls) != 1 || r.calls[0] != open+"busmon --target-pane w2:p1 --env AGENTBUS_PLUGIN_PROJECT=demo" {
		t.Fatalf("calls %q", r.calls)
	}
}

// busmon itself exiting non-zero (broker refused, bad --as) must leave the reason readable.
func TestPluginBusmonPaneWaitsWhenBusmonExitsNonZero(t *testing.T) {
	r := newPluginRig(t, "")
	r.env["AGENTBUS_PLUGIN_PROJECT"] = "demo"
	r.d.Run = func(a string, argv []string) (int, error) {
		r.out.WriteString("Redis connection failed\n")
		return 1, nil
	}
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
		if code != 1 || !strings.Contains(r.out.String(), "busmon exited with 1") {
			t.Fatalf("code %d out %q", code, r.out.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not return after the line")
	}
}

func TestPluginBusmonPaneCleanExitDoesNotWait(t *testing.T) {
	r := newPluginRig(t, "")
	r.env["AGENTBUS_PLUGIN_PROJECT"] = "demo"
	pr, _ := io.Pipe() // never written: a wait would hang
	r.d.Stdin = pr
	done := make(chan int, 1)
	go func() { done <- r.run("busmon-pane") }()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("code %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("waited after a clean exit")
	}
}

// realRun runs a real child: exit code propagated, stdout inherited.
func TestRunChildPropagatesExitCode(t *testing.T) {
	sh := filepath.Join(t.TempDir(), "fakebusmon")
	os.WriteFile(sh, []byte("#!/bin/sh\nexit 3\n"), 0o755)
	if code, err := runChild(sh, []string{sh}); err != nil || code != 3 {
		t.Fatalf("code %d err %v", code, err)
	}
	if _, err := runChild(filepath.Join(t.TempDir(), "missing"), []string{"x"}); err == nil {
		t.Fatal("expected a start error")
	}
}

// workspaceEventRig: a template for demo, a broker answer and a recorded hook.
func workspaceEventRig(t *testing.T, ev string, broker projlist.State) (*pluginRig, *[]string) {
	r := newPluginRig(t, "")
	dir := r.env["AGENTBUS_PROJECTS_DIR"]
	if err := os.WriteFile(filepath.Join(dir, "demo.toml"), []byte("name = \"demo\"\nworking_dir = \"/w\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.env["HERDR_PLUGIN_EVENT_JSON"] = ev
	var hooked []string
	r.d.Exists = func(string) projlist.State { return broker }
	r.d.Hook = func(p, ws string, _, _ io.Writer) int { hooked = append(hooked, p+" "+ws); return 0 }
	return r, &hooked
}

const focusedDemo = `{"event":"workspace_focused","data":{"type":"workspace_focused","workspace":{"workspace_id":"w7","label":"demo"}}}`

func TestPluginWorkspaceEventRunsTheHookForAKnownProject(t *testing.T) {
	r, hooked := workspaceEventRig(t, focusedDemo, projlist.Present)
	if code := r.run("workspace-event"); code != 0 || strings.Join(*hooked, ",") != "demo w7" {
		t.Fatalf("code %d hooked %q", code, *hooked)
	}
}

func TestPluginWorkspaceEventLeavesOtherWorkspacesAlone(t *testing.T) {
	for name, ev := range map[string]string{
		"no template":   `{"data":{"workspace":{"workspace_id":"w7","label":"other"}}}`,
		"not a name":    `{"data":{"workspace":{"workspace_id":"w7","label":"My Notes"}}}`,
		"no workspace":  `{"data":{}}`,
		"malformed":     `{`,
		"empty payload": ``,
	} {
		r, hooked := workspaceEventRig(t, ev, projlist.Present)
		if code := r.run("workspace-event"); code != 0 || len(*hooked) != 0 {
			t.Errorf("%s: code %d hooked %q", name, code, *hooked)
		}
	}
}

// The hook never provisions: a project the broker does not confirm is left.
func TestPluginWorkspaceEventNeedsTheBroker(t *testing.T) {
	for _, st := range []projlist.State{projlist.Absent, projlist.Unknown} {
		r, hooked := workspaceEventRig(t, focusedDemo, st)
		if code := r.run("workspace-event"); code != 0 || len(*hooked) != 0 {
			t.Errorf("%s: code %d hooked %q", st, code, *hooked)
		}
	}
}

// Without a label in the event, herdr's workspace list names it.
func TestPluginWorkspaceEventFindsTheLabel(t *testing.T) {
	r, hooked := workspaceEventRig(t, "", projlist.Present)
	r.env["HERDR_WORKSPACE_ID"] = "w7"
	r.d.Herdr = func(args ...string) ([]byte, error) {
		return []byte(`{"result":{"workspaces":[{"label":"demo","workspace_id":"w7"}]}}`), nil
	}
	if code := r.run("workspace-event"); code != 0 || strings.Join(*hooked, ",") != "demo w7" {
		t.Fatalf("code %d hooked %q", code, *hooked)
	}
}
