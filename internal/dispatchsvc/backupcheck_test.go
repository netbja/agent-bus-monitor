package dispatchsvc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/backup"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/internal/cmdtest"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
)

// handArchive makes the archive `agentbus backup --project project` makes,
// by the backup package call runBackup itself makes (backup.MakeArchive on
// backup.Gather's files, in backup.dir), never by the scheduled pass under
// test. Its name and time are runBackup's (the host, d.Now in UTC); its notes
// lack the line runBackup adds about Redis's read instant.
func handArchive(w *cmdtest.BackupWorld, project string) (int, string, string) {
	ctx := context.Background()
	var out, errw bytes.Buffer
	fail := func(err error) (int, string, string) {
		fmt.Fprintf(&errw, "backup: %v\n", err)
		return 1, out.String(), errw.String()
	}
	d := w.D
	r := config.Machine(d.Getenv)
	dir, _, err := config.BackupDir(r, d.Getenv)
	if err != nil {
		return fail(err)
	}
	c, err := d.Dial()
	if err != nil {
		return fail(err)
	}
	paths, err := service.PathsFrom(d.Getenv)
	if err != nil {
		return fail(err)
	}
	projects := []string{project}
	files, left, notes, err := backup.Gather(projects, backup.Locations{
		ConfigDir:    filepath.Dir(config.Path(d.Getenv)),
		ProjectsDir:  rolecfg.ProjectsDir(d.Getenv),
		PasswordFile: bus.PasswordFile(d.Getenv),
		EnvFile:      paths.EnvFile,
		StateDir:     paths.StateDir,
	})
	if err != nil {
		return fail(err)
	}
	job := backup.Job{Projects: projects, Dir: dir, Now: d.Now().UTC(), Files: files, Left: left, Notes: notes}
	if _, err := backup.MakeArchive(ctx, c, d, r, paths, job, &out, &errw); err != nil {
		return fail(err)
	}
	return 0, out.String(), errw.String()
}

// The reader end to end: archives made by the command, the record written
// by the runs; the detail always names the folder.
func TestBackupHealthCheck(t *testing.T) {
	w := cmdtest.NewBackupWorld(t)
	p := w.B.Project()
	cfg := filepath.Join(w.Home, ".config", "agentbus", "config.toml")
	check := func() health.Outcome {
		t.Helper()
		o, err := backupCheck(p, backupCheckDeps{Getenv: w.D.Getenv, Host: "h1",
			Now: func() time.Time { return time.Date(2026, 11, 3, 13, 0, 0, 0, time.UTC) }})(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	if o := check(); o.Status != health.Warn || !strings.Contains(o.Detail, "no archive covers") || !strings.Contains(o.Detail, w.Dir()) {
		t.Fatalf("none: %+v", o)
	}
	if code, out, errw := handArchive(w, p); code != 0 {
		t.Fatalf("backup: %s %s", out, errw)
	}
	if o := check(); o.Status != health.OK || !strings.Contains(o.Detail, w.Dir()) {
		t.Fatalf("fresh: %+v", o)
	}
	// A failed after_command warns; emptied, it is not read.
	fail := filepath.Join(w.Home, "fail")
	os.WriteFile(fail, []byte("#!/bin/sh\nexit 3\n"), 0o700)
	os.WriteFile(cfg, []byte("[backup]\nafter_command = \""+fail+"\"\n"), 0o600)
	handArchive(w, p)
	if o := check(); o.Status != health.Warn || !strings.Contains(o.Detail, "exit 3") {
		t.Fatalf("after failed: %+v", o)
	}
	os.WriteFile(cfg, []byte("[backup]\n"), 0o600)
	if o := check(); o.Status != health.OK {
		t.Fatalf("after emptied: %+v", o)
	}
	// A failed scheduled attempt warns while daily, until a newer archive.
	last := filepath.Join(w.Home, ".local", "state", "agentbus", "backup-last.json")
	backup.RecordScheduled(last, backup.ScheduledRun{At: time.Date(2026, 11, 3, 12, 30, 0, 0, time.UTC), Projects: []string{p}, Error: "disk full"})
	os.WriteFile(cfg, []byte("[backup]\nschedule = \"daily\"\ndir = \""+w.Dir()+"\"\n"), 0o600)
	if o := check(); o.Status != health.Warn || !strings.Contains(o.Detail, "disk full") {
		t.Fatalf("scheduled failure: %+v", o)
	}
	// An unreadable record is unknown.
	os.WriteFile(last, []byte("{not json"), 0o600)
	if o := check(); o.Status != health.Unknown {
		t.Fatalf("bad record: %+v", o)
	}
}
