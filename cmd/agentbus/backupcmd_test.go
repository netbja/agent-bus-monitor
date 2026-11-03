package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/backup"
	"github.com/netbja/agent-bus-monitor/bus"
)

// backupWorld: the disposable broker with one project holding a board, a
// temporary HOME (config.toml, a template, a password file) and a fixed clock.
type backupWorld struct {
	b    *bus.Bus
	home string
	env  map[string]string
	d    backupDeps
}

func newBackupWorld(t *testing.T) *backupWorld {
	t.Helper()
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { bus.DeleteProject(context.Background(), r, b.Project()) })
	if err := r.HSet(ctx, bus.BoardKey(b.Project()), "t1", `{"owner":"coder","state":"accepted"}`).Err(); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	w := &backupWorld{b: b, home: home, env: map[string]string{"HOME": home}}
	cfg := filepath.Join(home, ".config", "agentbus")
	os.MkdirAll(filepath.Join(cfg, "projects"), 0o700)
	os.WriteFile(filepath.Join(cfg, "config.toml"), []byte("[backup]\nkeep = 2\n"), 0o600)
	os.WriteFile(filepath.Join(cfg, "projects", b.Project()+".toml"), []byte("working_dir = \"/w\"\n"), 0o600)
	os.WriteFile(filepath.Join(cfg, "redis.env"), []byte(`REDIS_PASSWORD="pw-SECRET"`+"\n"), 0o600)
	clock := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	w.d = backupDeps{
		Getenv: func(k string) string { return w.env[k] },
		Now:    func() time.Time { clock = clock.Add(time.Second); return clock },
		Host:   "h1",
		Dial:   func() (*redis.Client, error) { return r, nil },
	}
	return w
}

func (w *backupWorld) run(args ...string) (int, string, string) {
	var out, errw bytes.Buffer
	code := runBackup(context.Background(), args, "", w.d, &out, &errw)
	return code, out.String(), errw.String()
}

func (w *backupWorld) dir() string {
	return filepath.Join(w.home, ".local", "share", "agentbus", "backups")
}

func TestBackupCommandWritesAnArchive(t *testing.T) {
	w := newBackupWorld(t)
	p := w.b.Project()
	code, out, errw := w.run("--project", p)
	if code != 0 {
		t.Fatalf("backup: %d %s %s", code, out, errw)
	}
	list, _ := backup.List(w.dir(), "h1")
	if len(list) != 1 || strings.Join(list[0].Coverage.Projects, ",") != p {
		t.Fatalf("archives %+v", list)
	}
	if !strings.Contains(out, list[0].Path) || !strings.Contains(out, p+": 1 key") || !strings.Contains(out, "left out: redis.env") {
		t.Fatalf("output %q", out)
	}
	f, _ := os.Open(list[0].Path)
	defer f.Close()
	m, err := backup.Verify(f)
	if err != nil || m.RedisVersion == "" || m.Host != "h1" {
		t.Fatalf("manifest %+v, %v", m, err)
	}
	var names []string
	for _, mem := range m.Members {
		names = append(names, mem.Path)
	}
	all := strings.Join(names, ",")
	for _, want := range []string{"redis/" + p + ".jsonl", "config/config.toml", "config/projects/" + p + ".toml"} {
		if !strings.Contains(all, want) {
			t.Errorf("%s missing from %s", want, all)
		}
	}
	// list shows it; keep = 2 from config.toml prunes the third run's oldest.
	_, out, _ = w.run("list")
	if !strings.Contains(out, list[0].Name) || !strings.Contains(out, p) {
		t.Fatalf("list: %q", out)
	}
	w.run("--project", p)
	_, out, _ = w.run("--project", p)
	if l, _ := backup.List(w.dir(), "h1"); len(l) != 2 || !strings.Contains(out, "removed "+list[0].Name) {
		t.Fatalf("retention: %d left, %q", len(l), out)
	}
}

func TestBackupCommandDryRunWritesNothing(t *testing.T) {
	w := newBackupWorld(t)
	code, out, errw := w.run("--project", w.b.Project(), "--dry-run")
	if code != 0 || !strings.Contains(out, "would write") || !strings.Contains(out, w.b.Project()+": 1 key") ||
		!strings.Contains(out, "config/config.toml") {
		t.Fatalf("dry run: %d %q %q", code, out, errw)
	}
	if _, err := os.Stat(w.dir()); !os.IsNotExist(err) {
		t.Fatalf("the dry run created %s: %v", w.dir(), err)
	}
}

func TestBackupCommandScopeAndRefusals(t *testing.T) {
	w := newBackupWorld(t)
	// No --project: the broker's projects and every template; here the
	// template of a project with no key is covered too.
	os.WriteFile(filepath.Join(w.home, ".config", "agentbus", "projects", "tplonly.toml"), []byte(""), 0o600)
	if code, out, errw := w.run("--dir", filepath.Join(w.home, "out")); code != 0 {
		t.Fatalf("backup: %d %s %s", code, out, errw)
	}
	list, _ := backup.List(filepath.Join(w.home, "out"), "h1")
	if len(list) != 1 {
		t.Fatalf("archives %+v", list)
	}
	cov := strings.Join(list[0].Coverage.Projects, ",")
	if !strings.Contains(cov, w.b.Project()) || !strings.Contains(cov, "tplonly") {
		t.Fatalf("coverage %q", cov)
	}
	for _, args := range [][]string{{"--project", "Bad*"}, {"--dir", "relative"}, {"--bogus"}, {"--secrets"}} {
		if code, _, errw := w.run(args...); code != 2 || errw == "" {
			t.Errorf("%v: %d %q", args, code, errw)
		}
	}
	// The ambient project never narrows it: only the command line does.
	w.env["AGENT_BUS_PROJECT"] = "tplonly"
	if code, _, _ := w.run("--dir", filepath.Join(w.home, "out2")); code != 0 {
		t.Fatal("backup failed")
	}
	l2, _ := backup.List(filepath.Join(w.home, "out2"), "h1")
	if len(l2) != 1 || !strings.Contains(strings.Join(l2[0].Coverage.Projects, ","), w.b.Project()) {
		t.Fatalf("AGENT_BUS_PROJECT narrowed the scope: %+v", l2)
	}
}

// An unreadable history folder fails the run: no archive (reviewer-codex).
func TestBackupCommandFailsOnAnUnreadableHistory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 folder")
	}
	w := newBackupWorld(t)
	sub := filepath.Join(w.home, ".local", "state", "agentbus", w.b.Project(), "history", "old")
	os.MkdirAll(sub, 0o700)
	os.Chmod(sub, 0o000)
	t.Cleanup(func() { os.Chmod(sub, 0o700) })
	code, out, errw := w.run("--project", w.b.Project())
	if code != 1 || !strings.Contains(errw, "history") || !strings.Contains(errw, "no archive written") {
		t.Fatalf("backup: %d %q %q", code, out, errw)
	}
	if l, _ := backup.List(w.dir(), "h1"); len(l) != 0 {
		t.Fatalf("an archive was written: %+v", l)
	}
}
