package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/backup"
	"github.com/netbja/agent-bus-monitor/health"
)

// The reader end to end: archives made by the command, the record written
// by the runs; the detail always names the folder.
func TestBackupHealthCheck(t *testing.T) {
	w := newBackupWorld(t)
	p := w.b.Project()
	cfg := filepath.Join(w.home, ".config", "agentbus", "config.toml")
	check := func() health.Outcome {
		t.Helper()
		o, err := backupCheck(p, backupCheckDeps{Getenv: w.d.Getenv, Host: "h1",
			Now: func() time.Time { return time.Date(2026, 11, 3, 13, 0, 0, 0, time.UTC) }})(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	if o := check(); o.Status != health.Warn || !strings.Contains(o.Detail, "no archive covers") || !strings.Contains(o.Detail, w.dir()) {
		t.Fatalf("none: %+v", o)
	}
	if code, out, errw := w.run("--project", p); code != 0 {
		t.Fatalf("backup: %s %s", out, errw)
	}
	if o := check(); o.Status != health.OK || !strings.Contains(o.Detail, w.dir()) {
		t.Fatalf("fresh: %+v", o)
	}
	// A failed after_command warns; emptied, it is not read.
	fail := filepath.Join(w.home, "fail")
	os.WriteFile(fail, []byte("#!/bin/sh\nexit 3\n"), 0o700)
	os.WriteFile(cfg, []byte("[backup]\nafter_command = \""+fail+"\"\n"), 0o600)
	w.run("--project", p)
	if o := check(); o.Status != health.Warn || !strings.Contains(o.Detail, "exit 3") {
		t.Fatalf("after failed: %+v", o)
	}
	os.WriteFile(cfg, []byte("[backup]\n"), 0o600)
	if o := check(); o.Status != health.OK {
		t.Fatalf("after emptied: %+v", o)
	}
	// A failed scheduled attempt warns while daily, until a newer archive.
	last := filepath.Join(w.home, ".local", "state", "agentbus", "backup-last.json")
	backup.RecordScheduled(last, backup.ScheduledRun{At: time.Date(2026, 11, 3, 12, 30, 0, 0, time.UTC), Projects: []string{p}, Error: "disk full"})
	os.WriteFile(cfg, []byte("[backup]\nschedule = \"daily\"\ndir = \""+w.dir()+"\"\n"), 0o600)
	if o := check(); o.Status != health.Warn || !strings.Contains(o.Detail, "disk full") {
		t.Fatalf("scheduled failure: %+v", o)
	}
	// An unreadable record is unknown.
	os.WriteFile(last, []byte("{not json"), 0o600)
	if o := check(); o.Status != health.Unknown {
		t.Fatalf("bad record: %+v", o)
	}
}
