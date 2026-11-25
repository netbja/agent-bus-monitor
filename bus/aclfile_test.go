package bus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func aclTestPasswords() map[string]string {
	pw := map[string]string{}
	for _, r := range ACLRoles {
		pw[r] = "pw-of-" + r
	}
	return pw
}

// users.acl (step 4b, part 2): one line per user from ACLRules, its
// password as a SHA-256 hash only, and the default user off last.
func TestACLFile(t *testing.T) {
	pw := aclTestPasswords()
	got, err := ACLFile(pw)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(got), "\n"), "\n")
	if len(lines) != len(ACLRoles)+1 {
		t.Fatalf("%d lines, want one per user and default:\n%s", len(lines), got)
	}
	for i, role := range ACLRoles {
		sum := sha256.Sum256([]byte(pw[role]))
		want := "user " + role + " reset on #" + hex.EncodeToString(sum[:]) + " " + strings.Join(ACLRules(role), " ")
		if lines[i] != want {
			t.Errorf("line %d:\n got %s\nwant %s", i, lines[i], want)
		}
		if strings.Contains(string(got), pw[role]) {
			t.Errorf("the file holds %s's password in clear", role)
		}
	}
	if lines[len(lines)-1] != "user default off" {
		t.Errorf("last line %q", lines[len(lines)-1])
	}
	for _, bad := range []map[string]string{
		func() map[string]string { m := aclTestPasswords(); delete(m, ACLPing); return m }(),
		func() map[string]string { m := aclTestPasswords(); m[ACLAgent] = ""; return m }(),
	} {
		if _, err := ACLFile(bad); err == nil {
			t.Error("a user without a password was rendered")
		}
	}
}

// The file loads in a real redis-server (a private one on a socket, started
// by the test, never the shared test broker): every user logs in with its
// password, default is refused, and the rules hold.
func TestACLFileOnADisposableRedis(t *testing.T) {
	if os.Getenv("AGENTBUS_TEST_REDIS_URL") == "" {
		t.Skip("Redis integration test: set AGENTBUS_TEST_REDIS_URL (this one starts its own disposable redis-server)")
	}
	server, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("no redis-server on PATH")
	}
	pw := aclTestPasswords()
	data, err := ACLFile(pw)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	aclfile := filepath.Join(dir, "users.acl")
	if err := os.WriteFile(aclfile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "r.sock")
	cmd := exec.Command(server, "--port", "0", "--unixsocket", sock, "--unixsocketperm", "700", "--save", "", "--appendonly", "no", "--aclfile", aclfile)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	ctx := context.Background()
	as := func(user, pass string) *redis.Client {
		c := redis.NewClient(&redis.Options{Network: "unix", Addr: sock, Username: user, Password: pass})
		t.Cleanup(func() { c.Close() })
		return c
	}
	for _, role := range ACLRoles {
		if err := as(role, pw[role]).Ping(ctx).Err(); err != nil {
			t.Errorf("%s does not log in: %v", role, err)
		}
		if err := as(role, "wrong").Ping(ctx).Err(); err == nil {
			t.Errorf("%s logs in with a wrong password", role)
		}
	}
	if err := redis.NewClient(&redis.Options{Network: "unix", Addr: sock}).Ping(ctx).Err(); err == nil {
		t.Error("the default user is not off")
	}
	if err := as(ACLAgent, pw[ACLAgent]).Do(ctx, "FLUSHALL").Err(); err == nil {
		t.Error("agent may FLUSHALL")
	}
	if err := as(ACLAgent, pw[ACLAgent]).Set(ctx, "p:pilot", "x", 0).Err(); err == nil {
		t.Error("agent may write pilot")
	}
	if err := as(ACLMaster, pw[ACLMaster]).Set(ctx, "p:pilot", "x", 0).Err(); err != nil {
		t.Errorf("master may not write pilot: %v", err)
	}
	if err := as(ACLPing, pw[ACLPing]).Get(ctx, "p:pilot").Err(); err == nil {
		t.Error("ping may GET")
	}
	// observer-role: reads every key, writes none, runs no script.
	obs := as(ACLObserver, pw[ACLObserver])
	if err := as(ACLMaster, pw[ACLMaster]).XAdd(ctx, &redis.XAddArgs{Stream: "p:status", Values: []string{"from", "coder"}}).Err(); err != nil {
		t.Fatal(err)
	}
	if n, err := obs.XLen(ctx, "p:status").Result(); err != nil || n != 1 {
		t.Errorf("observer may not read a stream: %d %v", n, err)
	}
	if err := obs.Get(ctx, "p:pilot").Err(); err != nil {
		t.Errorf("observer may not GET: %v", err)
	}
	if err := obs.HGetAll(ctx, "p:board").Err(); err != nil {
		t.Errorf("observer may not HGETALL: %v", err)
	}
	for name, err := range map[string]error{
		"XADD":   obs.XAdd(ctx, &redis.XAddArgs{Stream: "p:cmd", Values: []string{"from", "observer"}}).Err(),
		"SET":    obs.Set(ctx, "p:board", "x", 0).Err(),
		"HSET":   obs.HSet(ctx, "p:board", "t", "x").Err(),
		"DEL":    obs.Del(ctx, "p:status").Err(),
		"EVAL":   obs.Eval(ctx, "return 1", nil).Err(),
		"XGROUP": obs.XGroupCreateMkStream(ctx, "p:cmd", "g", "0").Err(),
		"KEYS":   obs.Keys(ctx, "*").Err(),
	} {
		if err == nil {
			t.Errorf("observer may %s", name)
		}
	}
}
