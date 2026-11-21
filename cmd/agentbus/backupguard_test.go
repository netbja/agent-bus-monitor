package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/backup"
)

// A backup and a restore are the person's acts (broker-hardening step 4: the
// admin user; reviewer-codex): from an agent's environment both refuse before
// any broker is reached. backup list reads local files and stays open.
func TestBackupAndRestoreRefuseAnAgent(t *testing.T) {
	agent := func(k string) string {
		if k == "AGENT_BUS_AGENT" {
			return "coder"
		}
		return ""
	}
	dialed := false
	d := backup.Deps{Getenv: agent, Dial: func() (*redis.Client, error) { dialed = true; return nil, context.Canceled }}
	var out, errw bytes.Buffer
	if code := runBackup(context.Background(), []string{"--project", "demo"}, "", d, &out, &errw); code == 0 || dialed ||
		!strings.Contains(errw.String(), "the person's act, not coder's") {
		t.Fatalf("backup from an agent: exit %d, dialed %v, %q", code, dialed, errw.String())
	}
	errw.Reset()
	if code := runRestore(context.Background(), []string{"/nowhere.tar.zst"}, "", restoreDeps{Getenv: agent}, &out, &errw); code == 0 ||
		!strings.Contains(errw.String(), "the person's act, not coder's") {
		t.Fatalf("restore from an agent: exit %d, %q", code, errw.String())
	}
	errw.Reset()
	if code := runBackup(context.Background(), []string{"list", "--dir", t.TempDir()}, "", d, &out, &errw); strings.Contains(errw.String(), "person's act") || dialed {
		t.Fatalf("backup list from an agent: exit %d, %q", code, errw.String())
	}
}
