package bus

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

func TestUserPasswordFile(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := UserPasswordFile(env(map[string]string{"XDG_CONFIG_HOME": "/x"}), "agent"); got != "/x/agentbus/redis-agent.env" {
		t.Errorf("%q", got)
	}
	if got := UserPasswordFile(env(map[string]string{"HOME": "/h"}), "master"); got != "/h/.config/agentbus/redis-master.env" {
		t.Errorf("%q", got)
	}
	if got := UserPasswordFile(env(nil), "agent"); got != "" {
		t.Errorf("no home: %q", got)
	}
	if got := UserPasswordFile(env(map[string]string{"HOME": "/h"}), "../x"); got != "" {
		t.Errorf("a bad user: %q", got)
	}
}

// ConnectAsContext logs in as user with its own password file; "" is today's
// Connect; a user with no file is an error naming the fix, never a fallback
// to the default user.
func TestConnectAs(t *testing.T) {
	testredis.Configure(t)
	ctx := context.Background()
	admin, err := Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "agentbus"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := admin.Do(ctx, "ACL", "SETUSER", "acltest-conn", "reset", "on", ">s3cret-pw", "+ping", "+acl|whoami").Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Do(context.Background(), "ACL", "DELUSER", "acltest-conn") })
	if _, err := ConnectAsContext(ctx, "", "acltest-conn"); err == nil || !strings.Contains(err.Error(), "./setup.sh acl on") {
		t.Fatalf("no password file: %v", err)
	}
	if err := os.WriteFile(UserPasswordFile(os.Getenv, "acltest-conn"), []byte("REDIS_PASSWORD=s3cret-pw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := ConnectAsContext(ctx, "", "acltest-conn")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if who, err := c.Do(ctx, "ACL", "WHOAMI").Text(); err != nil || who != "acltest-conn" {
		t.Fatalf("whoami %q %v", who, err)
	}
	d, err := ConnectAsContext(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if who, _ := d.Do(ctx, "ACL", "WHOAMI").Text(); who == "acltest-conn" {
		t.Fatal("\"\" logged in as the user")
	}
}
