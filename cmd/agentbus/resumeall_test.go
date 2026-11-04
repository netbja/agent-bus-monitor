package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

func (w *resumeWorld) all(args ...string) (int, string, string) {
	var out, errw bytes.Buffer
	d := w.resumeDeps()
	d.Projects = func(context.Context) ([]string, error) { return []string{w.b.Project()}, nil }
	d.WorkspaceOpen = func(string, string) (bool, error) { return w.wsOpen, nil }
	code := runResumeAll(context.Background(), append([]string{"--all"}, args...), d, &out, &errw)
	return code, out.String(), errw.String()
}

func (w *resumeWorld) lastCmd() bus.Event {
	w.t.Helper()
	evs, _, err := w.b.Recent(context.Background(), []string{"cmd"}, 10)
	if err != nil || len(evs) == 0 {
		w.t.Fatalf("no cmd entry: %v", err)
	}
	return evs[len(evs)-1]
}

// The crash path (§7): no suspend; stopped services start, a dead agent is
// relaunched in the open workspace, master is told what could not be known,
// and a second run has nothing to do.
func TestResumeAllAfterACrash(t *testing.T) {
	w := newResumeWorld(t, "master", "coder")
	w.wsOpen = true
	w.mgr.active[w.b.Project()+"/dispatch"] = false
	w.kill("coder")
	w.onUp = func() { w.agent("coder", "idle") }
	code, out, errw := w.all("--yes")
	if code != 0 {
		t.Fatalf("resume --all = %d\n%s\n%s", code, out, errw)
	}
	if !w.mgr.active[w.b.Project()+"/dispatch"] || len(w.upArgs) != 1 {
		t.Fatalf("services %v, up %v", w.mgr.active, w.upArgs)
	}
	if want := w.b.Project() + " --resume --no-dispatch --no-missions"; strings.Join(w.upArgs[0], " ") != want {
		t.Fatalf("up = %q", w.upArgs[0])
	}
	last := w.lastCmd()
	if last.Target != "master" || !last.Human || !strings.Contains(last.Message, "crash") ||
		!strings.Contains(last.Message, "Relaunched: coder") || !strings.Contains(last.Message, "turns running at the crash are lost") {
		t.Fatalf("directive = %+v", last)
	}
	if p := w.pause(); p != nil {
		t.Fatalf("a pause appeared: %+v", p)
	}
	n := len(w.upArgs)
	code, out, _ = w.all("--yes")
	if code != 0 || !strings.Contains(out, "nothing to resume") || len(w.upArgs) != n {
		t.Fatalf("second run = %d %q, up %v", code, out, w.upArgs)
	}
}

// A crash never opens a workspace the person had closed: the command is
// named, master is not told, and a timed pause is left to run its course.
func TestResumeAllCrashWithTheWorkspaceClosed(t *testing.T) {
	w := newResumeWorld(t, "master", "coder")
	until := time.Now().Add(time.Hour)
	if _, err := w.b.SetPause(context.Background(), "dana", "master", "lunch", until); err != nil {
		t.Fatal(err)
	}
	w.kill("coder")
	before := len(mustRecent(t, w.b))
	code, out, _ := w.all("--yes")
	if code != 1 || len(w.upArgs) != 0 || !strings.Contains(out, "agentbus up "+w.b.Project()+" --resume") {
		t.Fatalf("resume --all = %d %q, up %v", code, out, w.upArgs)
	}
	if after := len(mustRecent(t, w.b)); after != before {
		t.Fatal("master was told though nothing was relaunched")
	}
	if p := w.pause(); p == nil || p.Reason != "lunch" {
		t.Fatalf("pause = %+v, want the timed pause untouched", p)
	}
}

func mustRecent(t *testing.T, b *bus.Bus) []bus.Event {
	t.Helper()
	evs, _, err := b.Recent(context.Background(), []string{"cmd"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

// A suspended project takes §5 under --all; dry-run writes nothing; no
// terminal and no --yes asks for --yes; an agent caller is refused.
func TestResumeAllOfASuspendedProject(t *testing.T) {
	w := newResumeWorld(t, "master")
	w.suspendNow()
	if code, out, _ := w.all("--dry-run"); code != 0 || !strings.Contains(out, "suspended by charlie") {
		t.Fatalf("dry run = %d %q", code, out)
	}
	if code, _, errw := w.all(); code != 2 || !strings.Contains(errw, "--yes") {
		t.Fatalf("no terminal = %d %q", code, errw)
	}
	w.env["AGENT_BUS_AGENT"] = "coder"
	if code, _, errw := w.all("--yes"); code != 1 || !strings.Contains(errw, "person") {
		t.Fatalf("agent caller = %d %q", code, errw)
	}
	delete(w.env, "AGENT_BUS_AGENT")
	if p := w.pause(); p == nil || !p.Suspended() || len(w.upArgs) != 0 {
		t.Fatalf("something was done before the go: pause %+v, up %v", p, w.upArgs)
	}
	if code, out, errw := w.all("--yes"); code != 0 {
		t.Fatalf("resume --all = %d\n%s\n%s", code, out, errw)
	}
	if p := w.pause(); p != nil {
		t.Fatalf("still suspended: %+v", p)
	}
}

// A resume the hook started that leaves the project suspended tells the
// person by a for=person notice: nobody reads the hook's log.
func TestHookResumeTellsThePersonWhenAnAgentIsMissing(t *testing.T) {
	w := newResumeWorld(t, "master", "coder")
	w.suspendNow()
	w.kill("coder")
	w.env[resumeHookEnv] = "1"
	if code, _, _ := w.resume(); code != 1 {
		t.Fatalf("resume = %d", code)
	}
	evs, _, err := w.b.Recent(context.Background(), []string{"notify"}, 10)
	if err != nil || len(evs) == 0 || !strings.Contains(evs[len(evs)-1].Message, "stays suspended") {
		t.Fatalf("notice = %+v %v", evs, err)
	}
}

// hookWorld is the workspace hook's fakes.
type hookWorld struct {
	mgr     *fakeManager
	rec     *bus.PauseRecord
	recErr  error
	auto    bool
	boot    string
	person  string
	busy    bool
	spawned [][2]string
	d       upDeps
}

func newHookWorld(t *testing.T) *hookWorld {
	h := &hookWorld{mgr: newFakeManager(), auto: true, boot: "boot-2", person: "charlie"}
	for _, k := range service.Kinds {
		h.mgr.specs["demo/"+k] = service.Spec{Project: "demo", Kind: k}
	}
	home := t.TempDir()
	paths, err := service.PathsFrom(func(k string) string { return map[string]string{"HOME": home}[k] })
	if err != nil {
		t.Fatal(err)
	}
	h.d = upDeps{
		Services:     func() (service.Manager, string) { return h.mgr, "" },
		Paths:        paths,
		SuspendPause: func(string) (*bus.PauseRecord, error) { return h.rec, h.recErr },
		AutoResume:   func() bool { return h.auto },
		Boot:         func() string { return h.boot },
		Hostname:     func() (string, error) { return "host-a", nil },
		Person:       func() string { return h.person },
		Lock: func(string, bool) (func(), error) {
			if h.busy {
				return nil, errUpBusy
			}
			return func() {}, nil
		},
		SpawnResume: func(p, log string) error { h.spawned = append(h.spawned, [2]string{p, log}); return nil },
	}
	return h
}

func (h *hookWorld) run() (int, string) {
	var out, errw bytes.Buffer
	code := upHook(h.d, "demo", "w1", &out, &errw)
	return code, out.String() + errw.String()
}

func (h *hookWorld) startedAny() bool {
	for _, c := range h.mgr.calls {
		if strings.HasPrefix(c, "start ") {
			return true
		}
	}
	return false
}

func suspendedOn(host, boot string) *bus.PauseRecord {
	return &bus.PauseRecord{Kind: bus.PauseKindSuspend, Host: host, Boot: boot, At: time.Now().UnixMilli(), By: "charlie"}
}

func TestHookOnASuspendedProject(t *testing.T) {
	for _, tc := range []struct {
		name  string
		set   func(*hookWorld)
		spawn bool
		say   string
	}{
		{"auto_resume off", func(h *hookWorld) { h.auto = false }, false, "suspended since"},
		{"same boot", func(h *hookWorld) { h.boot = "boot-1" }, false, "suspended: agentbus resume brings it back"},
		{"recorded boot unknown", func(h *hookWorld) { h.rec.Boot = "" }, false, "suspended: agentbus resume brings it back"},
		{"current boot unknown", func(h *hookWorld) { h.boot = "" }, false, "suspended: agentbus resume brings it back"},
		{"another host", func(h *hookWorld) { h.rec.Host = "host-b" }, false, "suspended: agentbus resume brings it back"},
		{"no name recorded", func(h *hookWorld) { h.person = "" }, false, "no name"},
		{"a resume already runs", func(h *hookWorld) { h.busy = true }, false, "resume already running"},
		{"after a reboot", func(*hookWorld) {}, true, "resuming"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHookWorld(t)
			h.rec = suspendedOn("host-a", "boot-1")
			tc.set(h)
			code, out := h.run()
			if code != 0 || !strings.Contains(out, tc.say) {
				t.Fatalf("hook = %d %q", code, out)
			}
			if h.startedAny() {
				t.Fatalf("the hook started a service on a suspended project: %v", h.mgr.calls)
			}
			if got := len(h.spawned) == 1; got != tc.spawn {
				t.Fatalf("spawned = %v, want %v", h.spawned, tc.spawn)
			}
			if tc.spawn && (h.spawned[0][0] != "demo" || filepath.Base(h.spawned[0][1]) != "suspend.log") {
				t.Fatalf("spawned = %v", h.spawned)
			}
		})
	}
}

// The resume the hook starts is exactly agentbus --project <p> resume --yes:
// never --partial.
func TestHookResumeArgv(t *testing.T) {
	if got := strings.Join(hookResumeArgv("demo"), " "); got != "--project demo resume --yes" {
		t.Fatalf("argv = %q", got)
	}
	if d := withSuspendHook(upDeps{}); d.SpawnResume == nil || d.SuspendPause == nil || d.AutoResume == nil || d.Boot == nil {
		t.Fatal("the real hook deps are not set")
	}
}

// A pause it cannot read, or no pause, keeps today's hook: the services start.
func TestHookStartsServicesWhenNotSuspended(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  *bus.PauseRecord
		err  error
	}{
		{"unreadable", nil, errors.New("broker down")},
		{"no pause", nil, nil},
		{"timed pause", &bus.PauseRecord{Reason: "lunch"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHookWorld(t)
			h.rec, h.recErr = tc.rec, tc.err
			if code, out := h.run(); code != 0 || !h.startedAny() || len(h.spawned) != 0 {
				t.Fatalf("hook = %d %q, calls %v, spawned %v", code, out, h.mgr.calls, h.spawned)
			}
		})
	}
}
