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
	"github.com/netbja/agent-bus-monitor/internal/cmdtest"
)

func backupWorldPass(w *backupWorld, t *testing.T) (error, string) {
	t.Helper()
	var log bytes.Buffer
	err := scheduledBackupPass(context.Background(), w.D, &log)
	return err, log.String()
}

func TestScheduledBackup(t *testing.T) {
	// off: nothing.
	w := newBackupWorld(t)
	if err, _ := backupWorldPass(w, t); err != nil {
		t.Fatal(err)
	}
	if l, _ := backup.List(w.Dir(), "h1"); len(l) != 0 {
		t.Fatal("schedule off made an archive")
	}
	// daily: one archive of every project of the host, then nothing while
	// every one is covered by an archive under 24 h.
	w = newScheduleWorld(t, "")
	if err, log := backupWorldPass(w, t); err != nil {
		t.Fatalf("pass: %v\n%s", err, log)
	}
	list, _ := backup.List(w.Dir(), "h1")
	if len(list) != 1 || !strings.Contains(strings.Join(list[0].Coverage.Projects, ","), w.B.Project()) {
		t.Fatalf("archives %+v", list)
	}
	rec := w.Last(t)
	if rec.Scheduled == nil || rec.Scheduled.Archive != list[0].Name || rec.Scheduled.Error != "" || len(rec.Scheduled.Projects) == 0 {
		t.Fatalf("scheduled record %+v", rec.Scheduled)
	}
	if err, _ := backupWorldPass(w, t); err != nil {
		t.Fatal(err)
	}
	if l, _ := backup.List(w.Dir(), "h1"); len(l) != 1 {
		t.Fatalf("a covered host made another archive: %d", len(l))
	}
	// A day later: due again.
	w.D.Now = func() time.Time { return time.Date(2026, 11, 4, 13, 0, 0, 0, time.UTC) }
	if err, _ := backupWorldPass(w, t); err != nil {
		t.Fatal(err)
	}
	if l, _ := backup.List(w.Dir(), "h1"); len(l) != 2 {
		t.Fatalf("after 24 h: %d archives", len(l))
	}
}

// A fresh archive of A alone does not cover B: the run is due.
func TestScheduledBackupJudgesEachProject(t *testing.T) {
	w := newScheduleWorld(t, "")
	os.WriteFile(filepath.Join(w.Home, ".config", "agentbus", "projects", "other.toml"), []byte(""), 0o600)
	if code, out, errw := runBackupWorld(w, "--project", w.B.Project()); code != 0 {
		t.Fatalf("by hand: %s %s", out, errw)
	}
	if err, log := backupWorldPass(w, t); err != nil {
		t.Fatalf("pass: %v\n%s", err, log)
	}
	list, _ := backup.List(w.Dir(), "h1")
	cov := backup.NewestCovering(list)
	if len(list) != 2 || cov["other"].Name == "" {
		t.Fatalf("archives %d, coverage %+v", len(list), cov)
	}
	// The by-hand run wrote no scheduled record; the pass did.
	if w.Last(t).Scheduled == nil {
		t.Fatal("no scheduled record")
	}
}

// Another chore holding the host lock: this one skips its turn.
func TestScheduledBackupSkipsWhileLocked(t *testing.T) {
	w := newScheduleWorld(t, "")
	unlock, err := backupLock(filepath.Join(w.Home, ".local", "state", "agentbus"))
	if err != nil {
		t.Fatal(err)
	}
	if err, _ := backupWorldPass(w, t); err != nil {
		t.Fatal(err)
	}
	if l, _ := backup.List(w.Dir(), "h1"); len(l) != 0 {
		t.Fatal("ran while another held the lock")
	}
	unlock()
	if err, _ := backupWorldPass(w, t); err != nil {
		t.Fatal(err)
	}
	if l, _ := backup.List(w.Dir(), "h1"); len(l) != 1 {
		t.Fatal("did not run once the lock was free")
	}
}

// Secrets in an unattended run only with a recipient; with one and no age,
// the archive is written without them and the run records the error.
func TestScheduledBackupSecrets(t *testing.T) {
	w := newScheduleWorld(t, "")
	if err, _ := backupWorldPass(w, t); err != nil {
		t.Fatal(err)
	}
	list, _ := backup.List(w.Dir(), "h1")
	f, _ := os.Open(list[0].Path)
	m, _ := backup.Verify(f)
	f.Close()
	if len(m.LeftOut) == 0 {
		t.Fatal("no recipient: the secrets are not listed left out")
	}

	w = newScheduleWorld(t, "age_recipient = \""+cmdRecipient+"\"\n")
	w.D.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	err, _ := backupWorldPass(w, t)
	if err == nil || !strings.Contains(err.Error(), "age") {
		t.Fatalf("no age: %v", err)
	}
	list, _ = backup.List(w.Dir(), "h1")
	if len(list) != 1 {
		t.Fatal("no age: no archive written")
	}
	rec := w.Last(t).Scheduled
	if rec == nil || rec.Archive != list[0].Name || !strings.Contains(rec.SecretsError, "age") {
		t.Fatalf("record %+v", rec)
	}
	w.D.LookPath = func(string) (string, error) { return fakeAgeBin(t), nil }
	w.D.Now = func() time.Time { return time.Date(2026, 11, 5, 13, 0, 0, 0, time.UTC) }
	if err, log := backupWorldPass(w, t); err != nil {
		t.Fatalf("with age: %v\n%s", err, log)
	}
	if rec := w.Last(t).Scheduled; rec.SecretsError != "" || rec.Error != "" {
		t.Fatalf("record with age %+v", rec)
	}
	list, _ = backup.List(w.Dir(), "h1")
	f, _ = os.Open(list[0].Path)
	m, _ = backup.Verify(f)
	f.Close()
	found := false
	for _, mem := range m.Members {
		found = found || mem.Path == backup.SecretsName
	}
	if !found {
		t.Fatal("the scheduled archive with a recipient and age holds no secrets.age")
	}
}

// agentbus backup warns about a defaulted backup.dir while daily is on.
func TestBackupWarnsAboutADefaultDirWhenDaily(t *testing.T) {
	w := newBackupWorld(t)
	os.WriteFile(filepath.Join(w.Home, ".config", "agentbus", "config.toml"), []byte("[backup]\nschedule = \"daily\"\n"), 0o600)
	if _, _, errw := runBackupWorld(w, "--project", w.B.Project()); !strings.Contains(errw, "backup.dir is the default") {
		t.Fatalf("no warning: %q", errw)
	}
}

// foureyes: the long-running service's pass closes the client it dialled.
func TestScheduledBackupClosesItsClient(t *testing.T) {
	w := newScheduleWorld(t, "")
	var dialled *redis.Client
	w.D.Dial = func() (*redis.Client, error) {
		c, err := bus.Connect("")
		dialled = c
		return c, err
	}
	if err, log := backupWorldPass(w, t); err != nil {
		t.Fatalf("pass: %v\n%s", err, log)
	}
	if dialled == nil || dialled.Ping(context.Background()).Err() == nil {
		t.Fatal("the pass left its client open")
	}
}

var newScheduleWorld = cmdtest.NewScheduleWorld
