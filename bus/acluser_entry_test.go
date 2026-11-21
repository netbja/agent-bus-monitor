package bus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

func brokerACLEnv(t *testing.T) string {
	t.Helper()
	for _, k := range config.Keys {
		if k.Name == "broker.acl" {
			return k.Env
		}
	}
	t.Fatal("no broker.acl")
	return ""
}

// With broker.acl on, Connect logs in as the user the process's entry, its
// identity and its ancestry name (step 4b); off, as the default user.
func TestConnectPicksTheEntrysUser(t *testing.T) {
	testredis.Configure(t)
	ctx := context.Background()
	admin, err := Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	os.MkdirAll(filepath.Join(dir, "agentbus"), 0o700)
	for _, u := range []string{ACLBusmon, ACLAgent, ACLMaster} {
		if err := admin.Do(ctx, "ACL", "SETUSER", u, "reset", "on", ">pw-"+u, "+ping", "+acl|whoami").Err(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { admin.Do(context.Background(), "ACL", "DELUSER", u) })
		os.WriteFile(UserPasswordFile(os.Getenv, u), []byte("REDIS_PASSWORD=pw-"+u+"\n"), 0o600)
	}
	t.Cleanup(func() { SetConnectEntry(ACLEntryCLI, nil) })
	whoami := func() (string, error) {
		c, err := Connect("")
		if err != nil {
			return "", err
		}
		defer c.Close()
		return c.Do(ctx, "ACL", "WHOAMI").Text()
	}
	none := func(context.Context) (string, error) { return "", nil }
	under := func(a string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return a, nil }
	}
	failing := func(context.Context) (string, error) { return "", errors.New("unreadable") }

	t.Setenv(brokerACLEnv(t), "off")
	SetConnectEntry(ACLEntryCLI, none)
	if who, err := whoami(); err != nil || who != "default" {
		t.Fatalf("off: %q %v", who, err)
	}
	t.Setenv(brokerACLEnv(t), "on")
	for _, c := range []struct {
		entry    ACLEntry
		identity string
		ancestor func(context.Context) (string, error)
		want     string
	}{
		{ACLEntryCLI, "", none, ACLBusmon},
		{ACLEntryCLI, "coder", under("coder"), ACLAgent},
		{ACLEntryCLI, "", under("coder"), ACLAgent},
		{ACLEntryCLI, "master", none, ACLMaster},
		{ACLEntryCLI, "", failing, ACLAgent}, // ancestry unknown: the least
		{ACLEntryBusmon, "", none, ACLBusmon},
		{ACLEntryBusmon, "", under("coder"), ACLAgent},
	} {
		t.Setenv("AGENT_BUS_AGENT", c.identity)
		SetConnectEntry(c.entry, c.ancestor)
		if who, err := whoami(); err != nil || who != c.want {
			t.Errorf("%v %q: %q %v, want %q", c.entry, c.identity, who, err, c.want)
		}
	}
	t.Setenv("AGENT_BUS_AGENT", "coder")
	SetConnectEntry(ACLEntryAdmin, none)
	if _, err := whoami(); err == nil {
		t.Error("admin from an agent's environment connected")
	}
	t.Setenv("AGENT_BUS_AGENT", "")
	SetConnectEntry(ACLEntryAdmin, failing)
	if _, err := whoami(); err == nil {
		t.Error("admin with an unreadable ancestry connected")
	}
}
