package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/netbja/agent-bus-monitor/setup"
)

// audit step 2b: a host-level act (update, setup's reinstall) writes its
// line on the bus of each project it touched, once; an invalid project name
// is skipped without stopping the others.
func TestAuditHostAct(t *testing.T) {
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	ps := []string{"ha" + stamp, "hb" + stamp}
	for _, p := range ps {
		t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), c, p) })
	}
	e := bus.AuditEntry{Action: "update", Actor: "charlie", Target: "v0.2.0", Outcome: bus.AuditOK, Detail: "v0.1.0 -> v0.2.0, installed, snapshot x"}
	auditHostAct(context.Background(), c, e, []string{ps[0], "Not A Name", ps[1]})
	for _, p := range ps {
		b, err := bus.Open(c, p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := b.AuditEntries(context.Background(), 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Action != "update" || got[0].Actor != "charlie" || got[0].Target != "v0.2.0" ||
			got[0].Outcome != bus.AuditOK || got[0].Detail != e.Detail {
			t.Fatalf("%s: %+v", p, got)
		}
	}
}

// setup's install writes a reinstall line only over an existing install
// (agentbus already in the bin dir), never in a dry run: ok on success,
// failed with its exit otherwise.
func TestReinstallAct(t *testing.T) {
	for _, c := range []struct {
		had, dry bool
		code     int
		outcome  string
		detail   string
	}{
		{false, false, 0, "", ""},
		{true, true, 0, "", ""},
		{true, false, 0, bus.AuditOK, "setup install over an existing install"},
		{true, false, 1, bus.AuditFailed, "setup install over an existing install, exit 1"},
	} {
		e, ok := reinstallAct(c.had, c.dry, c.code, "v0.34.0-3-gabc", "charlie")
		if ok != (c.outcome != "") || ok && (e.Action != "reinstall" || e.Actor != "charlie" || e.Target != "v0.34.0-3-gabc" ||
			e.Outcome != c.outcome || e.Detail != c.detail) {
			t.Errorf("%+v: %+v %v", c, e, ok)
		}
	}
}

// installRunner fakes setup's world for runInstall: every tool found, git
// and go answer their versions, a build writes its -o file, and the build
// of fail is refused.
type installRunner struct{ fail string }

func (r installRunner) LookPath(name string) (string, error) { return "/usr/bin/" + name, nil }
func (r installRunner) Run(_ context.Context, c setup.Cmd) ([]byte, error) {
	switch {
	case c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "--version":
		return []byte("git version 2.50.0\n"), nil
	case c.Name == "git" && slices.Contains(c.Args, "describe"):
		return []byte("v9.9.9\n"), nil
	case c.Name == "go" && slices.Contains(c.Args, "GOVERSION"):
		return []byte("go1.99.0\n"), nil
	case c.Name == "go" && len(c.Args) > 0 && c.Args[0] == "build":
		if c.Args[len(c.Args)-1] == r.fail {
			return []byte("compile error\n"), errors.New("exit status 1")
		}
		out := c.Args[slices.Index(c.Args, "-o")+1]
		return nil, os.WriteFile(out, []byte("#!/bin/sh\n"), 0o755)
	}
	return nil, nil
}

// codex P2: over an existing install, a busmon build that fails after
// agentbus was installed still records the reinstall, failed; a first
// install records nothing.
func TestRunInstallRecordsAFailedReinstall(t *testing.T) {
	run := func(had bool) (int, []bus.AuditEntry) {
		repo, bin := t.TempDir(), t.TempDir()
		if had {
			if err := os.WriteFile(filepath.Join(bin, "agentbus"), []byte("old"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		env := map[string]string{"HOME": t.TempDir(), "XDG_CONFIG_HOME": t.TempDir(), "XDG_DATA_HOME": t.TempDir(), "XDG_STATE_HOME": t.TempDir()}
		getenv := func(k string) string { return env[k] }
		var out bytes.Buffer
		o := setup.NewOutput(&out, &out, false)
		opt, code := setup.ParseArgs([]string{"install", "--bin-dir", bin, "--no-docker", "--no-skills", "--yes"}, getenv, o)
		if code >= 0 {
			t.Fatalf("args: %d\n%s", code, out.String())
		}
		var got []bus.AuditEntry
		code = runInstall(context.Background(), installRunner{fail: "./cmd/busmon"}, o, opt, repo, getenv, func(e bus.AuditEntry) { got = append(got, e) })
		return code, got
	}
	code, got := run(true)
	if code == 0 || len(got) != 1 || got[0].Action != "reinstall" || got[0].Outcome != bus.AuditFailed || got[0].Target != "v9.9.9" ||
		got[0].Detail != "setup install over an existing install, exit 1" {
		t.Fatalf("exit %d, lines %+v", code, got)
	}
	if code, got := run(false); code == 0 || len(got) != 0 {
		t.Fatalf("a first install: exit %d, lines %+v", code, got)
	}
}
