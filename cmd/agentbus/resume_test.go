package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/service"
)

// resumeWorld is a suspended project: the suspend world after a suspend,
// with fakes for herdr and up.
type resumeWorld struct {
	*suspendWorld
	running bool       // the herdr session runs
	upArgs  [][]string // every up call
	onUp    func()     // what up brings back
	boot    []string   // the template's agents
}

func newResumeWorld(t *testing.T, agents ...string) *resumeWorld {
	t.Helper()
	w := &resumeWorld{suspendWorld: newSuspendWorld(t), running: true, boot: agents}
	for _, a := range agents {
		w.agent(a, "idle")
	}
	// The project's services env file records its herdr session, as up writes it.
	paths, err := service.PathsFrom(func(k string) string { return w.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WriteEnvFile(paths.EnvFile(w.b.Project()), map[string]string{"HERDR_SESSION": "demo"}); err != nil {
		t.Fatal(err)
	}
	return w
}

// suspendNow suspends the project the way agentbus suspend does.
func (w *resumeWorld) suspendNow(args ...string) {
	w.t.Helper()
	if code, out, errw := w.run(args...); code != 0 {
		w.t.Fatalf("suspend = %d\n%s\n%s", code, out, errw)
	}
}

// kill makes an agent's process gone: its snapshot names a pid that is not
// alive (its tier kept: a proof replaces the whole identity).
func (w *resumeWorld) kill(name string, tier ...string) {
	w.t.Helper()
	dead := bus.AgentIdent{PID: 1 << 22, PIDStart: 1, Pane: "p-" + name}
	if len(tier) > 0 {
		dead.Tier = tier[0]
	}
	if _, err := w.b.Status(context.Background(), name, "idle", "", dead); err != nil {
		w.t.Fatal(err)
	}
}

func (w *resumeWorld) resumeDeps() resumeDeps {
	return resumeDeps{
		Getenv:         func(k string) string { return w.env[k] },
		Bus:            func(string) (*bus.Bus, error) { return w.b, nil },
		Services:       func() (service.Manager, string) { return w.mgr, "" },
		SessionRunning: func(string) (bool, error) { return w.running, nil },
		SessionSocket:  func(string) (string, error) { return "/run/herdr.sock", nil },
		Up: func(args []string, _, _ string, _ io.Writer) int {
			w.upArgs = append(w.upArgs, args)
			if w.onUp != nil {
				w.onUp()
			}
			return 0
		},
		BootRoles:   func(string) ([]string, error) { return w.boot, nil },
		ProcStart:   dispatch.ProcStart,
		StateDir:    func(string) string { return w.state },
		Person:      func() string { return "charlie" },
		AgentCaller: func(context.Context) (string, error) { return "", nil },
		ProofWait:   60 * time.Millisecond,
		Poll:        5 * time.Millisecond,
	}
}

func (w *resumeWorld) resume(args ...string) (int, string, string) {
	var out, errw bytes.Buffer
	o := resumeOptions{}
	rest, partial := extractBool(args, "--partial")
	o.partial = partial
	o.message = strings.Join(rest, " ")
	code := runSuspendedResume(context.Background(), w.b.Project(), o, w.resumeDeps(), &out, &errw)
	return code, out.String(), errw.String()
}

func TestResumeBringsTheProjectBack(t *testing.T) {
	w := newResumeWorld(t, "master", "coder")
	w.mgr.active[w.b.Project()+"/missions"] = false // missions was off before the suspend
	w.suspendNow("--reason", "reboot")
	w.kill("coder")
	w.onUp = func() { w.agent("coder", "idle") } // up relaunches coder on its session
	code, out, errw := w.resume("back", "to", "work")
	if code != 0 {
		t.Fatalf("resume = %d\n%s\n%s", code, out, errw)
	}
	// Services from the record: dispatch was active, missions was not.
	if !w.mgr.active[w.b.Project()+"/dispatch"] || w.mgr.active[w.b.Project()+"/missions"] {
		t.Fatalf("services after resume: %v", w.mgr.active)
	}
	want := []string{w.b.Project(), "--resume", "--no-dispatch", "--no-missions"}
	if len(w.upArgs) != 1 || strings.Join(w.upArgs[0], " ") != strings.Join(want, " ") {
		t.Fatalf("up calls = %q, want %q", w.upArgs, want)
	}
	if p := w.pause(); p != nil {
		t.Fatalf("still paused: %+v", p)
	}
	evs, _, _ := w.b.Recent(context.Background(), []string{"cmd"}, 10)
	last := evs[len(evs)-1]
	if last.Target != "master" || !last.Human || !strings.Contains(last.Message, "resumed after a suspend") ||
		!strings.Contains(last.Message, "back to work") || !strings.Contains(last.Message, "coder") {
		t.Fatalf("resume directive = %+v", last)
	}
	if _, err := os.Stat(filepath.Join(w.state, "suspend.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the snapshot was not set aside: %v", err)
	}
	if kept, _ := filepath.Glob(filepath.Join(w.state, "suspend.*.json")); len(kept) != 1 {
		t.Fatalf("kept snapshots = %v", kept)
	}
}

// A boot agent that does not come back keeps the suspend, unless --partial.
func TestResumeKeepsTheSuspendWhileAnAgentIsMissing(t *testing.T) {
	w := newResumeWorld(t, "master", "coder")
	w.suspendNow()
	w.kill("coder")
	code, _, errw := w.resume()
	if code != 1 || !strings.Contains(errw, "coder") || !strings.Contains(errw, "--partial") {
		t.Fatalf("resume with coder missing = %d %q", code, errw)
	}
	if p := w.pause(); p == nil || !p.Suspended() {
		t.Fatalf("pause = %+v, want the suspend kept", p)
	}
	if !w.mgr.active[w.b.Project()+"/dispatch"] {
		t.Fatal("services not started: they run while the suspend is kept")
	}
	if code, out, errw := w.resume("--partial"); code != 0 {
		t.Fatalf("--partial = %d\n%s\n%s", code, out, errw)
	}
	if p := w.pause(); p != nil {
		t.Fatalf("--partial left the pause: %+v", p)
	}
}

// herdr not running: services only, the pause kept, the way out said.
func TestResumeWithoutHerdr(t *testing.T) {
	w := newResumeWorld(t, "master")
	w.suspendNow()
	w.kill("master")
	w.running = false
	code, _, errw := w.resume()
	if code != 1 || len(w.upArgs) != 0 || !strings.Contains(errw, "herdr --session") {
		t.Fatalf("resume without herdr = %d, up %v, %q", code, w.upArgs, errw)
	}
	if p := w.pause(); p == nil || !p.Suspended() {
		t.Fatalf("pause = %+v", p)
	}
}

// A previous timed pause still due comes back; no directive.
func TestResumeRestoresAPreviousPause(t *testing.T) {
	w := newResumeWorld(t, "master")
	until := time.Now().Add(time.Hour)
	if _, err := w.b.SetPause(context.Background(), "dana", "master", "lunch", until); err != nil {
		t.Fatal(err)
	}
	w.suspendNow()
	code, out, errw := w.resume()
	if code != 0 || !strings.Contains(out, "lunch") {
		t.Fatalf("resume = %d\n%s\n%s", code, out, errw)
	}
	if p := w.pause(); p == nil || p.Suspended() || p.Reason != "lunch" {
		t.Fatalf("pause after = %+v", p)
	}
}

// A record with no Services starts every installed kind.
func TestResumeWithoutServicesStartsEveryInstalledKind(t *testing.T) {
	w := newResumeWorld(t, "master")
	if _, _, _, err := w.b.Suspend(context.Background(), bus.SuspendSpec{By: "charlie", Notify: "master"}); err != nil {
		t.Fatal(err)
	}
	for _, k := range service.Kinds {
		w.mgr.active[w.b.Project()+"/"+k] = false
	}
	if code, out, errw := w.resume(); code != 0 {
		t.Fatalf("resume = %d\n%s\n%s", code, out, errw)
	}
	for _, k := range service.Kinds {
		if !w.mgr.active[w.b.Project()+"/"+k] {
			t.Fatalf("%s not started", k)
		}
	}
}

// Pops are named with their command, never relaunched; an agent caller is refused.
func TestResumeNamesPopsAndRefusesAnAgent(t *testing.T) {
	w := newResumeWorld(t, "master")
	who := selfIdent("p-ui-kimi")
	who.Tier, who.Session = "pop", "s-pop"
	if _, err := w.b.Status(context.Background(), "ui-kimi", "idle", "", who); err != nil {
		t.Fatal(err)
	}
	w.suspendNow()
	w.kill("ui-kimi", "pop")
	w.env["AGENT_BUS_AGENT"] = "coder"
	if code, _, errw := w.resume(); code == 0 || !strings.Contains(errw, "person") {
		t.Fatalf("agent caller = %d %q", code, errw)
	}
	delete(w.env, "AGENT_BUS_AGENT")
	code, out, errw := w.resume()
	if code != 0 || !strings.Contains(out, "agent-spawn ui-kimi "+w.b.Project()) {
		t.Fatalf("resume = %d\n%s\n%s", code, out, errw)
	}
}

// A timed pause stays plain resume's: Bus.Resume ends it, nothing else runs.
func TestPlainResumeOfATimedPause(t *testing.T) {
	w := newResumeWorld(t, "master")
	if _, err := w.b.SetPause(context.Background(), "dana", "master", "lunch", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	full := 0
	d := pauseDeps{Resume: w.b.Resume, Full: func(context.Context, []string) int { full++; return 0 }}
	var out, errw bytes.Buffer
	if code := runResume(context.Background(), d, "charlie", nil, &out, &errw); code != 0 || full != 0 {
		t.Fatalf("timed resume = %d, full %d", code, full)
	}
	w.suspendNow()
	if code := runResume(context.Background(), d, "charlie", []string{"hi"}, &out, &errw); code != 0 || full != 1 {
		t.Fatalf("resume of a suspend = %d, full %d", code, full)
	}
}

// The record's Services decides, with no snapshot at hand: a service off
// before the suspend stays off (reviewer-codex's restart intent).
func TestResumeServicesFromTheRecordAlone(t *testing.T) {
	w := newResumeWorld(t, "master")
	w.mgr.active[w.b.Project()+"/missions"] = false
	w.suspendNow()
	if err := os.Remove(filepath.Join(w.state, "suspend.json")); err != nil {
		t.Fatal(err)
	}
	if code, out, errw := w.resume(); code != 0 {
		t.Fatalf("resume = %d\n%s\n%s", code, out, errw)
	}
	if !w.mgr.active[w.b.Project()+"/dispatch"] || w.mgr.active[w.b.Project()+"/missions"] {
		t.Fatalf("services = %v: want dispatch only, as the record says", w.mgr.active)
	}
}
