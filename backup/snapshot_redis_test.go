package backup

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
)

// privateRedis starts redis-server on a socket in dir, with args, until the
// test ends or stop is called.
func privateRedis(t *testing.T, dir string, args ...string) (sock string, stop func()) {
	t.Helper()
	server, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("no redis-server on PATH")
	}
	sock = filepath.Join(dir, "r.sock")
	cmd := exec.Command(server, append([]string{"--port", "0", "--unixsocket", sock, "--unixsocketperm", "700", "--dir", dir}, args...)...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop = func() {
		if !stopped {
			stopped = true
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}
	t.Cleanup(stop)
	for range 100 {
		if _, err := os.Stat(sock); err == nil {
			return sock, stop
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("redis-server did not start")
	return "", nil
}

// broker-hardening step 5 on a real broker with ACL users (a private
// redis-server, appendonly on as compose runs it): backup's admin takes the
// snapshot, and the manual restore of the operator notes gives back every
// key, TTL keys included, with users.acl's users and default refused.
func TestSnapshotAndManualRestoreOnADisposableRedis(t *testing.T) {
	if os.Getenv("AGENTBUS_TEST_REDIS_URL") == "" {
		t.Skip("Redis integration test: set AGENTBUS_TEST_REDIS_URL (this one starts its own disposable redis-server)")
	}
	ctx := context.Background()
	pw := map[string]string{}
	for _, u := range bus.ACLRoles {
		pw[u] = "pw-" + u
	}
	acl, err := bus.ACLFile(pw)
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "users.acl"), acl, 0o600); err != nil {
		t.Fatal(err)
	}
	sock, stop := privateRedis(t, data, "--appendonly", "yes", "--aclfile", filepath.Join(data, "users.acl"))
	login := func(sock, user, pw string) *redis.Client {
		return redis.NewClient(&redis.Options{Network: "unix", Addr: sock, Username: user, Password: pw})
	}
	admin := login(sock, bus.ACLAdmin, pw[bus.ACLAdmin])
	defer admin.Close()
	// A key under the marker's prefix that is not this run's stays as it is.
	if err := admin.Set(ctx, SnapshotMarkPrefix+"someone", "theirs", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := admin.Set(ctx, "demo:board", "kept", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := admin.Set(ctx, "demo:lease", "short-lived", time.Hour).Err(); err != nil { // the logical dump skips it
		t.Fatal(err)
	}
	stage := t.TempDir()
	// A run whose marker key already holds a value (a collision): no
	// overwrite, no delete, no snapshot.
	clash := NewSnapshotter(admin, "", nil, stage)
	clash.nonce = func() string { return "0123456789abcdef" }
	if err := admin.Set(ctx, SnapshotMarkPrefix+"0123456789abcdef", "theirs", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if path, why := clash.Take(ctx); path != "" || !strings.Contains(why, "already exists") {
		t.Fatalf("a taken key: %q %q", path, why)
	}
	if v, _ := admin.Get(ctx, SnapshotMarkPrefix+"0123456789abcdef").Result(); v != "theirs" {
		t.Fatalf("an existing value overwritten or removed: %q", v)
	}
	_ = admin.Del(ctx, SnapshotMarkPrefix+"0123456789abcdef")
	// foureyes: the scheduled backup connects as the dispatcher with
	// broker.acl on; its ACL refuses the marker (and BGSAVE): no snapshot,
	// the reason says who takes it, and nothing is written.
	disp := login(sock, bus.ACLDispatcher, pw[bus.ACLDispatcher])
	defer disp.Close()
	if path, why := NewSnapshotter(disp, "", nil, stage).Take(ctx); path != "" || !strings.Contains(why, "NOPERM") ||
		!strings.Contains(why, "only admin takes the snapshot: run agentbus backup by hand") {
		t.Fatalf("as the dispatcher: %q %q", path, why)
	}
	if keys, _ := admin.Keys(ctx, SnapshotMarkPrefix+"*").Result(); len(keys) != 1 {
		t.Fatalf("the dispatcher's refused run left keys: %v", keys)
	}
	path, why := NewSnapshotter(admin, "", nil, stage).Take(ctx)
	if why != "" {
		t.Fatalf("snapshot: %s", why)
	}
	if keys, _ := admin.Keys(ctx, SnapshotMarkPrefix+"*").Result(); len(keys) != 1 || keys[0] != SnapshotMarkPrefix+"someone" {
		t.Fatalf("the run's marker left, or another removed: %v", keys)
	}
	if v, _ := admin.Get(ctx, SnapshotMarkPrefix+"someone").Result(); v != "theirs" {
		t.Fatalf("another value under the prefix changed: %q", v)
	}
	stop()

	// The procedure: a fresh data directory keeping users.acl, broker.rdb
	// as dbfilename, no AOF; start with --appendonly no; CONFIG SET
	// appendonly yes; the normal config after.
	restored := t.TempDir()
	if err := os.WriteFile(filepath.Join(restored, "users.acl"), acl, 0o600); err != nil {
		t.Fatal(err)
	}
	src, _ := os.Open(path)
	dst, _ := os.Create(filepath.Join(restored, "dump.rdb"))
	_, _ = io.Copy(dst, src)
	src.Close()
	dst.Close()
	sock2, _ := privateRedis(t, restored, "--appendonly", "no", "--dbfilename", "dump.rdb", "--aclfile", filepath.Join(restored, "users.acl"))
	admin2 := login(sock2, bus.ACLAdmin, pw[bus.ACLAdmin])
	defer admin2.Close()
	if v, err := admin2.Get(ctx, "demo:board").Result(); err != nil || v != "kept" {
		t.Fatalf("demo:board %q %v", v, err)
	}
	if ttl, err := admin2.TTL(ctx, "demo:lease").Result(); err != nil || ttl <= 0 {
		t.Fatalf("the TTL key: %v %v", ttl, err)
	}
	if err := admin2.ConfigSet(ctx, "appendonly", "yes").Err(); err != nil {
		t.Fatal(err)
	}
	for _, u := range bus.ACLRoles {
		c := login(sock2, u, pw[u])
		if err := c.Ping(ctx).Err(); err != nil {
			t.Errorf("%s after the restore: %v", u, err)
		}
		c.Close()
	}
	def := redis.NewClient(&redis.Options{Network: "unix", Addr: sock2})
	defer def.Close()
	if def.Ping(ctx).Err() == nil {
		t.Error("the default user logs in after the restore")
	}
}
