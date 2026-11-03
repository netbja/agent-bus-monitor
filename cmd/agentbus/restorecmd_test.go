package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/backup"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/service"
)

// restoreWorld is a backupWorld whose project was archived then deleted.
type restoreWorld struct {
	*backupWorld
	archive    string
	workspaces string // herdr's workspace list
	starts     map[int]uint64
	answer     string
	services   func() (service.Manager, string)
}

func newRestoreCmdWorld(t *testing.T) *restoreWorld {
	t.Helper()
	bw := newBackupWorld(t)
	if code, out, errw := bw.run("--project", bw.b.Project()); code != 0 {
		t.Fatalf("backup: %s %s", out, errw)
	}
	list, _ := backup.List(bw.dir(), "h1")
	c, _ := bw.d.Dial()
	if _, err := bus.DeleteProject(context.Background(), c, bw.b.Project()); err != nil {
		t.Fatal(err)
	}
	// This host runs no service manager, and says so explicitly.
	bw.env["AGENTBUS_SERVICE_MANAGER"] = "none"
	return &restoreWorld{backupWorld: bw, archive: list[0].Path, workspaces: `{"result":{"workspaces":[]}}`, starts: map[int]uint64{},
		services: func() (service.Manager, string) { return nil, "AGENTBUS_SERVICE_MANAGER=none" }}
}

func (w *restoreWorld) deps() restoreDeps {
	return restoreDeps{
		Getenv: w.d.Getenv,
		Dial:   w.d.Dial,
		Herdr:  func(...string) ([]byte, error) { return []byte(w.workspaces), nil },
		ProcStart: func(pid int) (uint64, error) {
			if s, ok := w.starts[pid]; ok {
				return s, nil
			}
			return 0, fmt.Errorf("pid %d: %w", pid, dispatch.ErrNoProcess) // what dispatch.ProcStart returns for a pid /proc lists no more
		},
		Lock:     func(string) (func(), error) { return func() {}, nil },
		Services: w.services,
		Stdin:    strings.NewReader(w.answer),
		TTY:      true,
		LookPath: func(string) (string, error) { return "", errors.New("no age") },
	}
}

func (w *restoreWorld) restore(args ...string) (int, string) {
	var out, errw bytes.Buffer
	code := runRestore(context.Background(), args, "", w.deps(), &out, &errw)
	return code, out.String() + errw.String()
}

func TestRestoreCommandRoundTrip(t *testing.T) {
	w := newRestoreCmdWorld(t)
	p := w.b.Project()
	code, out := w.restore(w.archive, "--project", p)
	if code != 0 || !strings.Contains(out, "agentbus up "+p) {
		t.Fatalf("restore: %d\n%s", code, out)
	}
	c, _ := w.d.Dial()
	if v := c.HGet(context.Background(), bus.BoardKey(p), "t1").Val(); !strings.Contains(v, "coder") {
		t.Fatalf("board after restore = %q", v)
	}
	// Keys present now: refused without --force, nothing changed.
	if code, out := w.restore(w.archive, "--project", p); code != 1 || !strings.Contains(out, "--force") {
		t.Fatalf("second restore: %d\n%s", code, out)
	}
	// --force asks; "n" keeps everything.
	w.answer = "n\n"
	if code, out := w.restore(w.archive, "--project", p, "--force", "--redis-only"); code != 1 || !strings.Contains(out, "not confirmed") {
		t.Fatalf("force, no: %d\n%s", code, out)
	}
	w.answer = "y\n"
	if code, out := w.restore(w.archive, "--project", p, "--force", "--redis-only"); code != 0 {
		t.Fatalf("force, yes: %d\n%s", code, out)
	}
}

func TestRestoreCommandRefusesALiveProject(t *testing.T) {
	ctx := context.Background()
	for name, arrange := range map[string]func(w *restoreWorld, c func() error){
		"a live agent pid": func(w *restoreWorld, _ func() error) {
			w.b.Status(ctx, "coder", "idle", "", bus.AgentIdent{Pane: "p1", PID: 4242, PIDStart: 9})
			// Only its agents snapshot is on the broker: the archive's keys
			// are absent, the agent alive.
			c, _ := w.d.Dial()
			keys, _ := bus.ProjectKeys(ctx, c, w.b.Project())
			for _, k := range keys {
				if k != bus.AgentsKey(w.b.Project()) {
					c.Del(ctx, k)
				}
			}
			w.starts[4242] = 9
		},
		"the dispatch lease": func(w *restoreWorld, _ func() error) {
			c, _ := w.d.Dial()
			c.Set(ctx, bus.DispatchKey(w.b.Project()), "tok", 0)
		},
		"an open workspace": func(w *restoreWorld, _ func() error) {
			w.workspaces = `{"result":{"workspaces":[{"label":"` + w.b.Project() + `","workspace_id":"w1"}]}}`
		},
	} {
		w := newRestoreCmdWorld(t)
		arrange(w, nil)
		for _, force := range []bool{false, true} {
			args := []string{w.archive, "--project", w.b.Project()}
			if force {
				args = append(args, "--force", "--yes")
			}
			code, out := w.restore(args...)
			if code != 1 || !strings.Contains(out, "live") {
				t.Errorf("%s (force %v): %d\n%s", name, force, code, out)
			}
			c, _ := w.d.Dial()
			if c.Exists(ctx, bus.BoardKey(w.b.Project())).Val() != 0 {
				t.Errorf("%s (force %v): restored", name, force)
			}
		}
	}
	// A registered agent whose process has died is not live: the project
	// is restored (backup-live-check: a dead pid read as "cannot tell").
	dead := newRestoreCmdWorld(t)
	dead.b.Status(ctx, "coder", "idle", "", bus.AgentIdent{Pane: "p1", PID: 4242, PIDStart: 9})
	c, _ := dead.d.Dial()
	keys, _ := bus.ProjectKeys(ctx, c, dead.b.Project())
	for _, k := range keys {
		if k != bus.AgentsKey(dead.b.Project()) {
			c.Del(ctx, k)
		}
	}
	// Its agents snapshot is a present key, so --force (refused for a live
	// project whatever it says): the liveness check is what is under test.
	if code, out := dead.restore(dead.archive, "--project", dead.b.Project(), "--force", "--yes"); code != 0 || c.Exists(ctx, bus.BoardKey(dead.b.Project())).Val() == 0 {
		t.Fatalf("a dead agent's project: %d\n%s", code, out)
	}
	// Any other read error proves nothing: refused, fail closed.
	other := newRestoreCmdWorld(t)
	other.b.Status(ctx, "coder", "idle", "", bus.AgentIdent{Pane: "p1", PID: 4242, PIDStart: 9})
	od := other.deps()
	od.ProcStart = func(int) (uint64, error) { return 0, fs.ErrPermission }
	var oout bytes.Buffer
	if code := runRestore(ctx, []string{other.archive, "--project", other.b.Project()}, "", od, &oout, &oout); code != 1 ||
		!strings.Contains(oout.String(), "cannot tell") {
		t.Fatalf("an unreadable pid: %d\n%s", code, oout.String())
	}
	// herdr unreadable: cannot tell, refused (fail closed).
	w := newRestoreCmdWorld(t)
	d := w.deps()
	d.Herdr = func(...string) ([]byte, error) { return nil, errors.New("connection refused") }
	var out bytes.Buffer
	if code := runRestore(ctx, []string{w.archive, "--project", w.b.Project()}, "", d, &out, &out); code != 1 ||
		!strings.Contains(out.String(), "cannot tell") {
		t.Fatalf("herdr down: %d\n%s", code, out.String())
	}
}

func TestRestoreCommandArgs(t *testing.T) {
	w := newRestoreCmdWorld(t)
	for _, args := range [][]string{nil, {"a", "b"}, {w.archive, "--redis-only", "--files-only"}, {w.archive, "--project", "Bad*"}, {w.archive, "--bogus"}} {
		if code, out := w.restore(args...); code != 2 {
			t.Errorf("%v: %d\n%s", args, code, out)
		}
	}
	if code, out := w.restore(filepath.Join(w.home, "none.tar.zst")); code != 1 {
		t.Errorf("a missing archive: %d\n%s", code, out)
	}
	bad := filepath.Join(w.home, "bad.tar.zst")
	os.WriteFile(bad, []byte("not an archive"), 0o600)
	if code, out := w.restore(bad); code != 1 || !strings.Contains(out, "nothing written") {
		t.Errorf("a bad archive: %d\n%s", code, out)
	}
	// A non-TTY stdin without --yes is no.
	w2 := newRestoreCmdWorld(t)
	w2.restore(w2.archive, "--project", w2.b.Project(), "--redis-only")
	d := w2.deps()
	d.TTY, d.Stdin = false, strings.NewReader("y\n")
	var out bytes.Buffer
	if code := runRestore(context.Background(), []string{w2.archive, "--project", w2.b.Project(), "--force", "--redis-only"}, "", d, &out, &out); code != 1 {
		t.Fatalf("non-TTY force: %d\n%s", code, out.String())
	}
}

// stateManager answers State with active for the kinds listed.
type stateManager struct {
	service.Manager
	active map[string]bool
	err    error
}

func (f stateManager) State(_, kind string) (service.State, error) {
	return service.State{Installed: true, Active: f.active[kind]}, f.err
}

// reviewer-codex: the services' state must be known. An active service
// refuses; a manager that cannot be found or read refuses (fail closed),
// unless AGENTBUS_SERVICE_MANAGER=none says this host runs none.
func TestRestoreCommandServices(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		services func() (service.Manager, string)
		want     string
	}{
		"an active service": {func() (service.Manager, string) {
			return stateManager{active: map[string]bool{service.Dispatch: true}}, ""
		}, "service is active"},
		"a manager that fails": {func() (service.Manager, string) {
			return stateManager{err: errors.New("bus timeout")}, ""
		}, "cannot tell"},
		"no manager found": {func() (service.Manager, string) { return nil, "systemd --user is not available" }, "cannot tell"},
	} {
		w := newRestoreCmdWorld(t)
		w.services = c.services
		delete(w.env, "AGENTBUS_SERVICE_MANAGER")
		code, out := w.restore(w.archive, "--project", w.b.Project())
		if code != 1 || !strings.Contains(out, c.want) {
			t.Errorf("%s: %d\n%s", name, code, out)
		}
		cl, _ := w.d.Dial()
		if cl.Exists(ctx, bus.BoardKey(w.b.Project())).Val() != 0 {
			t.Errorf("%s: restored", name)
		}
	}
	// Inactive services: restored.
	w := newRestoreCmdWorld(t)
	delete(w.env, "AGENTBUS_SERVICE_MANAGER")
	w.services = func() (service.Manager, string) { return stateManager{}, "" }
	if code, out := w.restore(w.archive, "--project", w.b.Project()); code != 0 {
		t.Fatalf("inactive services: %d\n%s", code, out)
	}
}
