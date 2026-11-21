package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

func TestConnectEntryOf(t *testing.T) {
	for args, want := range map[string]bus.ACLEntry{
		"service run dispatch": bus.ACLEntryService, "holder": bus.ACLEntryService, "holder --env x": bus.ACLEntryService,
		"holder drain demo": bus.ACLEntryCLI, "service restart dispatch": bus.ACLEntryCLI,
		"backup": bus.ACLEntryAdmin, "restore x.tar": bus.ACLEntryAdmin, "setup": bus.ACLEntryAdmin,
		"report coder x": bus.ACLEntryCLI, "plan load x": bus.ACLEntryCLI, "": bus.ACLEntryCLI,
	} {
		if got := connectEntryOf(strings.Fields(args)); got != want {
			t.Errorf("%q: %v, want %v", args, got, want)
		}
	}
}

// The mains' wiring, end to end (foureyes): with broker.acl on and the role
// users on the disposable broker, the built binaries log in by entry and
// ancestry. busmon's user may run nothing here, so a process wrongly given it
// fails: the person's backup must be admin; a command, or busmon, under a
// registered agent's process tree must be agent.
func TestConnectEntryInMain(t *testing.T) {
	testredis.Configure(t)
	ctx := context.Background()
	admin, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var aclEnv, humanEnv string
	for _, k := range config.Keys {
		switch k.Name {
		case "broker.acl":
			aclEnv = k.Env
		case "identity.as":
			humanEnv = k.Env
		}
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "agentbus"), 0o700)
	for user, rules := range map[string][]any{
		bus.ACLAdmin: {"+@all", "~*", "&*"}, bus.ACLAgent: {"+@all", "~*", "&*"}, bus.ACLBusmon: {"-@all"},
	} {
		args := append([]any{"ACL", "SETUSER", user, "reset", "on", ">pw-" + user}, rules...)
		if err := admin.Do(ctx, args...).Err(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { admin.Do(context.Background(), "ACL", "DELUSER", user) })
		os.WriteFile(filepath.Join(dir, "agentbus", "redis-"+user+".env"), []byte("REDIS_PASSWORD=pw-"+user+"\n"), 0o600)
	}
	p := "ce" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, err := bus.Open(admin, p)
	if err != nil {
		t.Fatal(err)
	}
	b.Status(ctx, "master", "idle", "x", bus.AgentIdent{})
	t.Cleanup(func() {
		keys, _ := bus.ProjectKeys(context.Background(), admin, p)
		if len(keys) > 0 {
			admin.Del(context.Background(), keys...)
		}
	})
	bin := filepath.Join(t.TempDir(), "agentbus")
	busmonBin := filepath.Join(t.TempDir(), "busmon")
	for out, pkg := range map[string]string{bin: ".", busmonBin: "../busmon"} {
		if o, err := exec.Command("go", "build", "-o", out, pkg).CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v %s", pkg, err, o)
		}
	}
	run := func(exe string, args ...string) (string, error) {
		cmd := exec.Command(exe, args...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "XDG_CONFIG_HOME=" + dir, "XDG_STATE_HOME=" + dir,
			"REDIS_URL=" + os.Getenv("AGENTBUS_TEST_REDIS_URL"), aclEnv + "=on", humanEnv + "=charlie"}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	// The person's backup: the admin entry (a CLI entry would be busmon, refused).
	if out, err := run(bin, "-p", p, "backup", "--dry-run", "--dir", t.TempDir()); err != nil {
		t.Fatalf("the person's backup --dry-run: %v\n%s", err, out)
	}
	// This test's process as a registered agent: its children are under it.
	start, err := dispatch.ProcStart(os.Getpid())
	if err != nil {
		t.Skipf("no process start here: %v", err)
	}
	if _, err := b.Status(ctx, "coder", "working", "x", bus.AgentIdent{PID: os.Getpid(), PIDStart: start}); err != nil {
		t.Fatal(err)
	}
	if out, err := run(bin, "-p", p, "report", "coder", "under coder's tree"); err != nil {
		t.Fatalf("a command under coder's tree, no identity: %v\n%s", err, out)
	}
	if out, err := run(busmonBin, "--list"); err != nil {
		t.Fatalf("busmon under coder's tree: %v\n%s", err, out)
	}
	if out, err := run(bin, "-p", p, "backup", "--dry-run", "--dir", t.TempDir()); err == nil {
		t.Fatalf("backup under coder's tree was let through:\n%s", out)
	}
}
