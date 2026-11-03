package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/backup"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
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
	for _, args := range [][]string{{"--project", "Bad*"}, {"--dir", "relative"}, {"--bogus"}} {
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

// fakeAgeBin is an age stand-in on a temporary path (see backup's fakeAge).
func fakeAgeBin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "age")
	os.WriteFile(bin, []byte(`#!/bin/sh
if [ "$1" = "-d" ]; then
  [ -f "$3" ] || exit 1
  read header
  base64 -d
else
  echo "FAKEAGE $2"
  base64
fi
`), 0o700)
	return bin
}

const cmdRecipient = "age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3290gq"

// --secrets: refused before anything is written without age or a
// recipient; with both, secrets.age holds them and the manifest no longer
// lists them left out; restore --identity brings the password file back.
func TestBackupCommandSecrets(t *testing.T) {
	w := newBackupWorld(t)
	p := w.b.Project()
	age := fakeAgeBin(t)
	w.d.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	if code, _, errw := w.run("--project", p, "--secrets", "--recipient", cmdRecipient); code != 1 || !strings.Contains(errw, "age") {
		t.Fatalf("no age: %d %q", code, errw)
	}
	w.d.LookPath = func(string) (string, error) { return age, nil }
	if code, _, errw := w.run("--project", p, "--secrets"); code != 1 || !strings.Contains(errw, "recipient") {
		t.Fatalf("no recipient: %d %q", code, errw)
	}
	if code, _, errw := w.run("--project", p, "--secrets", "--recipient", "AGE-SECRET-KEY-1X"); code != 2 || errw == "" {
		t.Fatalf("a private key as recipient: %d %q", code, errw)
	}
	if _, err := os.Stat(w.dir()); !os.IsNotExist(err) {
		t.Fatal("a refused --secrets run created the backup folder")
	}
	os.WriteFile(filepath.Join(w.home, ".config", "agentbus", "config.toml"), []byte("[backup]\nage_recipient = \""+cmdRecipient+"\"\n"), 0o600)
	code, out, errw := w.run("--project", p, "--secrets")
	if code != 0 || strings.Contains(out, "left out: redis.env") || !strings.Contains(out, "secrets.age") {
		t.Fatalf("secrets: %d %q %q", code, out, errw)
	}
	list, _ := backup.List(w.dir(), "h1")
	// restore --identity: the password file comes back 0600.
	identity := filepath.Join(w.home, "key.txt")
	os.WriteFile(identity, []byte("AGE-SECRET-KEY-1"), 0o600)
	pw := filepath.Join(w.home, ".config", "agentbus", "redis.env")
	os.Remove(pw)
	w.env["AGENTBUS_SERVICE_MANAGER"] = "none"
	rw := &restoreWorld{backupWorld: w, archive: list[0].Path, workspaces: `{"result":{"workspaces":[]}}`, starts: map[int]uint64{},
		services: func() (service.Manager, string) { return nil, "AGENTBUS_SERVICE_MANAGER=none" }}
	c, _ := w.d.Dial()
	bus.DeleteProject(context.Background(), c, p)
	d := rw.deps()
	d.LookPath = func(string) (string, error) { return age, nil }
	var rout bytes.Buffer
	if code := runRestore(context.Background(), []string{list[0].Path, "--identity", identity}, "", d, &rout, &rout); code != 0 {
		t.Fatalf("restore --identity: %d\n%s", code, rout.String())
	}
	if st, err := os.Stat(pw); err != nil || st.Mode().Perm() != 0o600 || !strings.Contains(string(mustRead(t, pw)), "pw-SECRET") {
		t.Fatalf("password file after restore: %v %v", st, err)
	}
	// --identity without age is refused before anything.
	d.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	if code := runRestore(context.Background(), []string{list[0].Path, "--identity", identity}, "", d, &rout, &rout); code != 1 {
		t.Fatalf("restore without age: %d", code)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
