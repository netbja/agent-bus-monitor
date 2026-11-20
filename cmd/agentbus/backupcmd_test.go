package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/backup"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/cmdtest"
	"github.com/netbja/agent-bus-monitor/service"
)

func runBackupWorld(w *backupWorld, args ...string) (int, string, string) {
	var out, errw bytes.Buffer
	code := runBackup(context.Background(), args, "", w.D, &out, &errw)
	return code, out.String(), errw.String()
}

func TestBackupCommandWritesAnArchive(t *testing.T) {
	w := newBackupWorld(t)
	p := w.B.Project()
	code, out, errw := runBackupWorld(w, "--project", p)
	if code != 0 {
		t.Fatalf("backup: %d %s %s", code, out, errw)
	}
	list, _ := backup.List(w.Dir(), "h1")
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
	_, out, _ = runBackupWorld(w, "list")
	if !strings.Contains(out, list[0].Name) || !strings.Contains(out, p) {
		t.Fatalf("list: %q", out)
	}
	runBackupWorld(w, "--project", p)
	_, out, _ = runBackupWorld(w, "--project", p)
	if l, _ := backup.List(w.Dir(), "h1"); len(l) != 2 || !strings.Contains(out, "removed "+list[0].Name) {
		t.Fatalf("retention: %d left, %q", len(l), out)
	}
}

func TestBackupCommandDryRunWritesNothing(t *testing.T) {
	w := newBackupWorld(t)
	code, out, errw := runBackupWorld(w, "--project", w.B.Project(), "--dry-run")
	if code != 0 || !strings.Contains(out, "would write") || !strings.Contains(out, w.B.Project()+": 1 key") ||
		!strings.Contains(out, "config/config.toml") {
		t.Fatalf("dry run: %d %q %q", code, out, errw)
	}
	if _, err := os.Stat(w.Dir()); !os.IsNotExist(err) {
		t.Fatalf("the dry run created %s: %v", w.Dir(), err)
	}
}

func TestBackupCommandScopeAndRefusals(t *testing.T) {
	w := newBackupWorld(t)
	// No --project: the broker's projects and every template; here the
	// template of a project with no key is covered too.
	os.WriteFile(filepath.Join(w.Home, ".config", "agentbus", "projects", "tplonly.toml"), []byte(""), 0o600)
	if code, out, errw := runBackupWorld(w, "--dir", filepath.Join(w.Home, "out")); code != 0 {
		t.Fatalf("backup: %d %s %s", code, out, errw)
	}
	list, _ := backup.List(filepath.Join(w.Home, "out"), "h1")
	if len(list) != 1 {
		t.Fatalf("archives %+v", list)
	}
	cov := strings.Join(list[0].Coverage.Projects, ",")
	if !strings.Contains(cov, w.B.Project()) || !strings.Contains(cov, "tplonly") {
		t.Fatalf("coverage %q", cov)
	}
	for _, args := range [][]string{{"--project", "Bad*"}, {"--dir", "relative"}, {"--bogus"}} {
		if code, _, errw := runBackupWorld(w, args...); code != 2 || errw == "" {
			t.Errorf("%v: %d %q", args, code, errw)
		}
	}
	// The ambient project never narrows it: only the command line does.
	w.Env["AGENT_BUS_PROJECT"] = "tplonly"
	if code, _, _ := runBackupWorld(w, "--dir", filepath.Join(w.Home, "out2")); code != 0 {
		t.Fatal("backup failed")
	}
	l2, _ := backup.List(filepath.Join(w.Home, "out2"), "h1")
	if len(l2) != 1 || !strings.Contains(strings.Join(l2[0].Coverage.Projects, ","), w.B.Project()) {
		t.Fatalf("AGENT_BUS_PROJECT narrowed the scope: %+v", l2)
	}
}

// An unreadable history folder fails the run: no archive (reviewer-codex).
func TestBackupCommandFailsOnAnUnreadableHistory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 folder")
	}
	w := newBackupWorld(t)
	sub := filepath.Join(w.Home, ".local", "state", "agentbus", w.B.Project(), "history", "old")
	os.MkdirAll(sub, 0o700)
	os.Chmod(sub, 0o000)
	t.Cleanup(func() { os.Chmod(sub, 0o700) })
	code, out, errw := runBackupWorld(w, "--project", w.B.Project())
	if code != 1 || !strings.Contains(errw, "history") || !strings.Contains(errw, "no archive written") {
		t.Fatalf("backup: %d %q %q", code, out, errw)
	}
	if l, _ := backup.List(w.Dir(), "h1"); len(l) != 0 {
		t.Fatalf("an archive was written: %+v", l)
	}
}

var fakeAgeBin = cmdtest.FakeAgeBin

const cmdRecipient = cmdtest.Recipient

// --secrets: refused before anything is written without age or a
// recipient; with both, secrets.age holds them and the manifest no longer
// lists them left out; restore --identity brings the password file back.
func TestBackupCommandSecrets(t *testing.T) {
	w := newBackupWorld(t)
	p := w.B.Project()
	age := fakeAgeBin(t)
	w.D.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	if code, _, errw := runBackupWorld(w, "--project", p, "--secrets", "--recipient", cmdRecipient); code != 1 || !strings.Contains(errw, "age") {
		t.Fatalf("no age: %d %q", code, errw)
	}
	w.D.LookPath = func(string) (string, error) { return age, nil }
	if code, _, errw := runBackupWorld(w, "--project", p, "--secrets"); code != 1 || !strings.Contains(errw, "recipient") {
		t.Fatalf("no recipient: %d %q", code, errw)
	}
	if code, _, errw := runBackupWorld(w, "--project", p, "--secrets", "--recipient", "AGE-SECRET-KEY-1X"); code != 2 || errw == "" {
		t.Fatalf("a private key as recipient: %d %q", code, errw)
	}
	if _, err := os.Stat(w.Dir()); !os.IsNotExist(err) {
		t.Fatal("a refused --secrets run created the backup folder")
	}
	os.WriteFile(filepath.Join(w.Home, ".config", "agentbus", "config.toml"), []byte("[backup]\nage_recipient = \""+cmdRecipient+"\"\n"), 0o600)
	code, out, errw := runBackupWorld(w, "--project", p, "--secrets")
	if code != 0 || strings.Contains(out, "left out: redis.env") || !strings.Contains(out, "secrets.age") {
		t.Fatalf("secrets: %d %q %q", code, out, errw)
	}
	list, _ := backup.List(w.Dir(), "h1")
	// restore --identity: the password file comes back 0600.
	identity := filepath.Join(w.Home, "key.txt")
	os.WriteFile(identity, []byte("AGE-SECRET-KEY-1"), 0o600)
	pw := filepath.Join(w.Home, ".config", "agentbus", "redis.env")
	os.Remove(pw)
	w.Env["AGENTBUS_SERVICE_MANAGER"] = "none"
	rw := &restoreWorld{backupWorld: w, archive: list[0].Path, starts: map[int]uint64{},
		services: func() (service.Manager, string) { return nil, "AGENTBUS_SERVICE_MANAGER=none" }}
	c, _ := w.D.Dial()
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

// backup.after_command: run once per complete archive, after the rename and
// before the retention, its outcome recorded for the archive's projects; a
// failure warns and never touches the archive; a dry run or a refused run
// runs nothing.
func TestBackupCommandAfterCommand(t *testing.T) {
	w := newBackupWorld(t)
	p := w.B.Project()
	marker := filepath.Join(w.Home, "ran")
	ok := filepath.Join(w.Home, "send-ok")
	os.WriteFile(ok, []byte("#!/bin/sh\nprintf '%s\\n' \"$AGENTBUS_BACKUP_ARCHIVE\" >> "+marker+"\nls \"$AGENTBUS_BACKUP_DIR\" | wc -l >> "+marker+"\n"), 0o700)
	fail := filepath.Join(w.Home, "send-fail")
	os.WriteFile(fail, []byte("#!/bin/sh\necho ran >> "+marker+"\nexit 3\n"), 0o700)
	cfg := filepath.Join(w.Home, ".config", "agentbus", "config.toml")
	setAfter := func(cmd string) {
		os.WriteFile(cfg, []byte("[backup]\nkeep = 1\nafter_command = \""+cmd+"\"\n"), 0o600)
	}
	last := filepath.Join(w.Home, ".local", "state", "agentbus", "backup-last.json")

	setAfter(ok)
	if code, out, errw := runBackupWorld(w, "--project", p, "--dry-run"); code != 0 || !strings.Contains(out, "would run backup.after_command: "+ok) {
		t.Fatalf("dry run: %d %q %q", code, out, errw)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("the dry run ran the command")
	}
	code, out, errw := runBackupWorld(w, "--project", p)
	if code != 0 || !strings.Contains(out, "backup.after_command: ok") {
		t.Fatalf("ok: %d %q %q", code, out, errw)
	}
	list, _ := backup.List(w.Dir(), "h1")
	got := strings.Split(strings.TrimSpace(string(mustRead(t, marker))), "\n")
	// Run after the rename (the archive is at its final name) and before the
	// retention (keep = 1: the folder still held the previous archive... here
	// the first, so one).
	if got[0] != list[0].Path || strings.TrimSpace(got[1]) != "1" {
		t.Fatalf("the command saw %q, archive %s", got, list[0].Path)
	}
	rec, _ := backup.ReadLast(last)
	if rec.After[p].Outcome != "ok" || rec.After[p].Archive != list[0].Name {
		t.Fatalf("record %+v", rec)
	}
	// A failure: exit code 0 (the archive is complete), a warning, the record.
	setAfter(fail)
	code, out, errw = runBackupWorld(w, "--project", p)
	list, _ = backup.List(w.Dir(), "h1")
	if code != 0 || !strings.Contains(errw, "exit 3") || len(list) != 1 {
		t.Fatalf("fail: %d %q %q %d archives", code, out, errw, len(list))
	}
	f, _ := os.Open(list[0].Path)
	if _, err := backup.Verify(f); err != nil {
		t.Fatalf("the archive after a failed command: %v", err)
	}
	f.Close()
	if rec, _ := backup.ReadLast(last); rec.After[p].Outcome != "exit 3" {
		t.Fatalf("record %+v", rec)
	}
	if st, _ := os.Stat(last); st.Mode().Perm() != 0o600 {
		t.Fatalf("record mode %v", st.Mode().Perm())
	}
	// A refused run (an unreadable history) runs nothing.
	os.Remove(marker)
	if os.Geteuid() != 0 {
		sub := filepath.Join(w.Home, ".local", "state", "agentbus", p, "history", "old")
		os.MkdirAll(sub, 0o700)
		os.Chmod(sub, 0o000)
		defer os.Chmod(sub, 0o700)
		if code, _, _ := runBackupWorld(w, "--project", p); code != 1 {
			t.Fatal("the run did not fail")
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("a refused run ran the command")
		}
	}
	// Set in the environment, it is not read: only config.toml.
	os.WriteFile(cfg, []byte("[backup]\n"), 0o600)
	w.Env["AGENT_BUS_BACKUP_AFTER_COMMAND"] = ok
	os.Chmod(filepath.Join(w.Home, ".local", "state", "agentbus", p, "history", "old"), 0o700)
	if code, _, _ := runBackupWorld(w, "--project", p); code != 0 {
		t.Fatal("backup failed")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("the environment's command ran")
	}
}

type backupWorld = cmdtest.BackupWorld

var newBackupWorld = cmdtest.NewBackupWorld
