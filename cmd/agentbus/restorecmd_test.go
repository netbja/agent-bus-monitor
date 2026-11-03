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
)

// restoreWorld is a backupWorld whose project was archived then deleted.
type restoreWorld struct {
	*backupWorld
	archive    string
	workspaces string // herdr's workspace list
	starts     map[int]uint64
	answer     string
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
	return &restoreWorld{backupWorld: bw, archive: list[0].Path, workspaces: `{"result":{"workspaces":[]}}`, starts: map[int]uint64{}}
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
			return 0, fmt.Errorf("pid %d: %w", pid, fs.ErrNotExist)
		},
		Lock:  func(string) (func(), error) { return func() {}, nil },
		Stdin: strings.NewReader(w.answer),
		TTY:   true,
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
